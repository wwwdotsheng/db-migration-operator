package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const migrationRetryInterval = 10 * time.Second

type ledgerDecision struct {
	alreadyDone     bool
	reason          string
	message         string
	recoveryVersion int
}

func inspectLedger(records []adapter.MigrationRecord, version int, before *int, checksum string) ledgerDecision {
	for _, row := range records {
		if row.Version == version {
			switch {
			case row.Checksum != "" && row.Checksum != checksum:
				return ledgerDecision{reason: "MigrationIdentityConflict", message: fmt.Sprintf("版本 %d 已登记不同的迁移内容", version)}
			case row.Status != dbv1.PhaseDone:
				return recoveryDecision(row.Version)
			case row.Checksum == "":
				return ledgerDecision{reason: "MigrationIdentityConflict", message: fmt.Sprintf("版本 %d 的旧账本没有 checksum，需要人工核对", version)}
			default:
				return ledgerDecision{alreadyDone: true}
			}
		}
	}
	for _, row := range records {
		if row.Status != dbv1.PhaseDone {
			return recoveryDecision(row.Version)
		}
	}
	if before != nil {
		for _, row := range records {
			if row.Version == *before && row.Status == dbv1.PhaseDone {
				return ledgerDecision{}
			}
		}
		return ledgerDecision{reason: dbv1.ReasonBeforeVersionNotSatisfied, message: fmt.Sprintf("等待前置版本 %d 完成", *before)}
	}
	return ledgerDecision{}
}

func recoveryDecision(version int) ledgerDecision {
	return ledgerDecision{reason: "MigrationRecoveryRequired", recoveryVersion: version, message: fmt.Sprintf(
		"版本 %d 的执行结果待确认，请核对真实数据库结构与迁移账本", version)}
}

func migrationChecksum(spec dbv1.SchemaMigrationSpec) (string, error) {
	// Canonicalize driverConfig object key order before hashing the spec.
	var config any
	decoder := json.NewDecoder(bytes.NewReader(spec.DriverConfig.Raw))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return "", fmt.Errorf("decode driverConfig for checksum: %w", err)
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	spec.DriverConfig.Raw = canonical
	spec.DriverConfig.Object = nil
	data, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	return checksumBytes(data), nil
}

func checksumBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (r *SchemaMigrationReconciler) reconcileLocked(
	ctx context.Context, migration *dbv1.SchemaMigration, driver adapter.Driver, cfg adapter.Config, serverVersion, checksum string, ledger adapter.MigrationLedger,
) (ctrl.Result, error) {
	planned := migration.DeepCopy()
	records, err := ledger.Records(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("read migration ledger: %w", err)
	}

	decision := inspectLedger(records, migration.Spec.Version, migration.Spec.BeforeVersion, checksum)
	if decision.alreadyDone {
		return ctrl.Result{}, r.completeMigration(ctx, migration)
	}
	switch decision.reason {
	case "MigrationIdentityConflict":
		return ctrl.Result{}, r.patchStatus(ctx, migration, dbv1.PhaseFailed, decision.message)
	case "":
	default:
		// Keep the original SQL error and confirmed progress across requeues.
		if decision.reason == "MigrationRecoveryRequired" {
			prior := meta.FindStatusCondition(migration.Status.Conditions, dbv1.ConditionBlocked)
			if prior != nil && prior.Reason == decision.reason && prior.ObservedGeneration == migration.Generation &&
				strings.HasPrefix(prior.Message, fmt.Sprintf("版本 %d ", decision.recoveryVersion)) {
				decision.message = prior.Message
			}
		}
		return r.blockMigration(ctx, migration, decision.reason, decision.message, migrationRetryInterval)
	}

	// The advisory lock covers both the snapshot comparison and DDL. If
	// another migration finished while the Job was running, its snapshot
	// differs and this round waits for a new Job.
	snapshot, err := json.Marshal(records)
	if err != nil {
		return ctrl.Result{}, err
	}
	ready, result, err := r.reconcileShadow(ctx, migration, driver, cfg, serverVersion, checksumBytes(snapshot))
	if err != nil || !ready {
		return result, err
	}

	// Re-read from the API server before starting a write. A cached CR
	// may have been updated or deleted while the Job was running.
	var latest dbv1.SchemaMigration
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(migration), &latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if latest.UID != planned.UID || latest.Generation != planned.Generation || !latest.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if latest.Status.Phase == dbv1.PhaseDone || latest.Status.Phase == dbv1.PhaseFailed {
		return ctrl.Result{}, nil
	}

	*migration = latest
	if err := ledger.Start(ctx, planned.Name, planned.Spec.Version, checksum); err != nil {
		return ctrl.Result{}, fmt.Errorf("persist Applying before executing SQL: %w", err)
	}
	if err := r.patchStatus(ctx, migration, dbv1.PhaseApplying, ""); err != nil {
		return ctrl.Result{}, err
	}
	if n, err := ledger.ExecSQL(ctx, planned.Spec.ApplySql); err != nil {
		return r.blockMigration(ctx, migration, "MigrationRecoveryRequired",
			fmt.Sprintf("版本 %d 已确认 %d 条 SQL 成功，后续结果待核对: %v", planned.Spec.Version, n, err), migrationRetryInterval)
	}
	if err := ledger.MarkDone(ctx, planned.Spec.Version, checksum); err != nil {
		return r.blockMigration(ctx, migration, "MigrationRecoveryRequired",
			fmt.Sprintf("版本 %d 的 SQL 已返回成功，但账本写入结果待核对: %v", planned.Spec.Version, err), migrationRetryInterval)
	}
	return ctrl.Result{}, r.completeMigration(ctx, migration)
}

func (r *SchemaMigrationReconciler) blockMigration(ctx context.Context, migration *dbv1.SchemaMigration, reason, message string, delay time.Duration) (ctrl.Result, error) {
	if err := r.setCondition(ctx, migration, dbv1.ConditionBlocked, metav1.ConditionTrue, reason, message); err != nil {
		return ctrl.Result{}, err
	}
	if migration.Status.Phase != dbv1.PhaseBlocked || migration.Status.Message != message {
		r.Recorder.Event(migration, corev1.EventTypeWarning, reason, message)
	}
	if err := r.patchStatus(ctx, migration, dbv1.PhaseBlocked, message); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: delay}, nil
}

func (r *SchemaMigrationReconciler) completeMigration(ctx context.Context, migration *dbv1.SchemaMigration) error {
	if err := r.setCondition(ctx, migration, dbv1.ConditionBlocked, metav1.ConditionFalse, dbv1.ReasonMigrationApplied, "迁移完成"); err != nil {
		return err
	}
	if err := r.patchStatus(ctx, migration, dbv1.PhaseDone, "migration done"); err != nil {
		return err
	}
	r.Recorder.Event(migration, corev1.EventTypeNormal, dbv1.ReasonMigrationApplied, "迁移完成")
	return nil
}

// Keep temporary API failures retryable. An actual failed preview is terminal.
func isPreviewFailure(err error) bool {
	var failure *ValidationError
	return errors.As(err, &failure)
}
