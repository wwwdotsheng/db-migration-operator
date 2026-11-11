/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	adapter "github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	"github.com/wwwdotsheng/db-migration-operator/internal/resolver"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// secretRetryInterval 是「等 Secret 出现」的重试间隔。
//
// ★ 为什么轮询而不是 watch Secret：
//
//	为了最小权限，Resolver 走 APIReader（只给 secrets 的 get），
//	没有 list/watch —— 那样 watch 不了 Secret。
//	→ 只能轮询。30 分钟太长（建好 Secret 要干等），2 分钟是折中。
const secretRetryInterval = 2 * time.Minute

var validChangeType = []dbv1.SchemaMigrationChangeType{
	dbv1.ChangeTypeExpand,
	dbv1.ChangeTypeContract,
	dbv1.ChangeTypeMixed,
}

var validStatusPhases = []dbv1.SchemaMigrationStatusPhase{
	dbv1.PhaseInit,
	dbv1.PhaseApplying,
	dbv1.PhaseBlocked,
	dbv1.PhaseFailed,
	dbv1.PhaseDone,
}

// SchemaMigrationReconciler reconciles a SchemaMigration object
type SchemaMigrationReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Resolver  *resolver.SecretResolver
}

// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create;delete

// Reconcile 按顺序准备目标库、检查依赖、预演并应用迁移。
func (r *SchemaMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var migration dbv1.SchemaMigration
	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if reason, message := validateMigration(&migration); reason != "" {
		if err := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, message); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&migration, corev1.EventTypeWarning, reason, message)
		return ctrl.Result{}, nil
	}

	if !migration.DeletionTimestamp.IsZero() || migration.Status.Phase == dbv1.PhaseDone || migration.Status.Phase == dbv1.PhaseFailed {
		return ctrl.Result{}, nil
	}

	checksum, err := migrationChecksum(migration.Spec)
	if err != nil {
		return ctrl.Result{}, r.patchStatus(ctx, &migration, dbv1.PhaseFailed, err.Error())
	}
	if migration.Status.SpecChecksum != "" && migration.Status.SpecChecksum != checksum {
		return ctrl.Result{}, r.patchStatus(ctx, &migration, dbv1.PhaseFailed, "迁移内容已修改，请使用新的 CR 和版本")
	}
	if migration.Status.SpecChecksum == "" {
		base := migration.DeepCopy()
		migration.Status.SpecChecksum = checksum
		if err := r.Status().Patch(ctx, &migration, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("bind migration content: %w", err)
		}
		if migration.Status.SpecChecksum != checksum {
			return ctrl.Result{}, errors.New("SchemaMigration CRD must be upgraded to persist status.specChecksum")
		}
	}

	driver, cfg, serverVersion, err := r.prepareDatabase(ctx, &migration)
	if err != nil {
		return r.handleDatabasePreparationError(ctx, &migration, err)
	}
	defer driver.Close(ctx)

	var result ctrl.Result
	err = driver.WithMigrationLock(ctx, func(ledger adapter.MigrationLedger) error {
		var reconcileErr error
		result, reconcileErr = r.reconcileLocked(ctx, &migration, driver, cfg, serverVersion, checksum, ledger)
		return reconcileErr
	})
	if errors.Is(err, adapter.ErrMigrationLockBusy) {
		return r.blockMigration(ctx, &migration, "MigrationLockBusy", "同一目标数据库正在处理迁移", migrationRetryInterval)
	}
	return result, err
}

func validateMigration(migration *dbv1.SchemaMigration) (reason, message string) {
	if !slices.Contains(validChangeType, migration.Spec.ChangeType) {
		return "InvalidChangeType", fmt.Sprintf("unknown ChangeType type: %q", migration.Spec.ChangeType)
	}
	if !slices.Contains(validStatusPhases, migration.Status.Phase) {
		return "InvalidPhaseStatus", fmt.Sprintf("invalid phase %q, migration terminated, manual intervention required", migration.Status.Phase)
	}
	return "", ""
}

func (r *SchemaMigrationReconciler) handleDatabasePreparationError(ctx context.Context, migration *dbv1.SchemaMigration, err error) (ctrl.Result, error) {
	switch {
	case errors.Is(err, adapter.ErrNotParseDriverConfig):
		if perr := r.patchStatus(ctx, migration, dbv1.PhaseFailed, err.Error()); perr != nil {
			return ctrl.Result{}, perr
		}
		return ctrl.Result{}, nil
	case errors.Is(err, adapter.ErrNotFoundSecret):
		if cerr := r.setCondition(ctx, migration, dbv1.ConditionBlocked,
			metav1.ConditionTrue, dbv1.ReasonSecretNotFound, err.Error()); cerr != nil {
			return ctrl.Result{}, cerr
		}
		if perr := r.patchStatus(ctx, migration, dbv1.PhaseBlocked, err.Error()); perr != nil {
			return ctrl.Result{}, perr
		}
		r.Recorder.Eventf(migration, corev1.EventTypeWarning, dbv1.ReasonSecretNotFound,
			"等待 Secret 出现，%s 后重试: %v", secretRetryInterval, err)
		return ctrl.Result{RequeueAfter: secretRetryInterval}, nil
	case errors.Is(err, adapter.ErrUnauthorizedSecret):
		if perr := r.patchStatus(ctx, migration, dbv1.PhaseFailed, err.Error()); perr != nil {
			return ctrl.Result{}, perr
		}
		return ctrl.Result{}, nil
	case errors.Is(err, adapter.ErrNoAdapterForDriver):
		if perr := r.patchStatus(ctx, migration, dbv1.PhaseFailed, err.Error()); perr != nil {
			return ctrl.Result{}, perr
		}
		r.Recorder.Event(migration, corev1.EventTypeWarning, "NoAdapterForDriver", err.Error())
		return ctrl.Result{}, nil
	default:
		// 保留错误用于诊断。写入 Failed 后，下一轮会按终态结束。
		if perr := r.patchStatus(ctx, migration, dbv1.PhaseFailed, err.Error()); perr != nil {
			return ctrl.Result{}, perr
		}
		r.Recorder.Event(migration, corev1.EventTypeWarning, "PrepareDatabaseFailed", err.Error())
		return ctrl.Result{}, err
	}
}

func (r *SchemaMigrationReconciler) reconcileShadow(
	ctx context.Context, migration *dbv1.SchemaMigration, persistent adapter.Driver,
	cfg adapter.Config, serverVersion, snapshot string,
) (bool, ctrl.Result, error) {
	done, err := r.shadowValidate(ctx, migration, persistent, cfg, serverVersion, snapshot)
	if err != nil && !isPreviewFailure(err) {
		return false, ctrl.Result{}, err
	}
	if err != nil {
		if patchErr := r.setCondition(ctx, migration, dbv1.ConditionShadowValidated,
			metav1.ConditionFalse, dbv1.ReasonValidationFailed, err.Error()); patchErr != nil {
			return false, ctrl.Result{}, patchErr
		}
		if patchErr := r.patchStatus(ctx, migration, dbv1.PhaseFailed, err.Error()); patchErr != nil {
			return false, ctrl.Result{}, patchErr
		}
		r.Recorder.Event(migration, corev1.EventTypeWarning, "ShadowValidationFailed", err.Error())
		return false, ctrl.Result{}, nil
	}
	if !done {
		if err := r.setCondition(ctx, migration, dbv1.ConditionShadowValidated,
			metav1.ConditionFalse, "ValidationPending", "等待当前账本快照的影子预演"); err != nil {
			return false, ctrl.Result{}, err
		}
		// Job 状态变化会唤醒控制器，定时重排作为兜底。
		return false, ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	if err := r.setCondition(ctx, migration, dbv1.ConditionShadowValidated,
		metav1.ConditionTrue, dbv1.ReasonValidationPassed, "影子预演通过"); err != nil {
		return false, ctrl.Result{}, err
	}
	r.Recorder.Event(migration, corev1.EventTypeNormal, "ShadowValidated", "影子预演通过")
	return true, ctrl.Result{}, nil
}

// prepareDatabase 完成迁移执行前的连接准备：
// 解析 driverConfig（注册表按 driver 分发）→ 读取 Secret 凭据 → 构建 DSN
// → 创建 adapter（数据库通道）→ 取服务器版本 → 版本校验闸门。
//
// ★ 版本查询由 adapter 承担（它本来就持有数据库连接），因此返回的 persistent
// 由调用方复用，并在本轮 Reconcile 结束时 Close。
func (r *SchemaMigrationReconciler) prepareDatabase(
	ctx context.Context, migration *dbv1.SchemaMigration,
) (adapter.Driver, adapter.Config, string, error) {
	cfg, err := adapter.DefaultConfigManager.Parse(migration.Spec.Driver, migration.Spec.DriverConfig.Raw)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: driver %q: %w", adapter.ErrNotParseDriverConfig, migration.Spec.Driver, err)
	}

	password, err := r.Resolver.ResolvePassword(ctx, migration.Namespace, cfg.SecretRef())
	if err != nil {
		return nil, nil, "", err
	}

	dsn, err := cfg.BuildDSN(password)
	if err != nil {
		return nil, nil, "", err
	}

	persistent, err := adapter.DefaultAdapterManager.New(migration.Spec.Driver, dsn)
	if err != nil {
		return nil, nil, "", err
	}

	versionInfo, err := persistent.CheckVersion(ctx)
	if err != nil {
		_ = persistent.Close(ctx)
		return nil, nil, "", fmt.Errorf("driver %q: %w", migration.Spec.Driver, err)
	}
	return persistent, cfg, versionInfo.Raw, nil
}

// setCondition 更新一个 condition 并写回 status。
// 用 meta.SetStatusCondition：它会处理 LastTransitionTime 和去重
// （同一个 condition 内容没变就不会重复写，避免无意义的状态更新）。
func (r *SchemaMigrationReconciler) setCondition(
	ctx context.Context, migration *dbv1.SchemaMigration,
	condType string, status metav1.ConditionStatus, reason, message string,
) error {
	base := migration.DeepCopy()
	changed := meta.SetStatusCondition(&migration.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: migration.Generation,
	})
	if !changed {
		return nil
	}
	if perr := r.Status().Patch(ctx, migration, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); perr != nil {
		return fmt.Errorf("failed to patch condition: %w", perr)
	}
	return nil
}

func (r *SchemaMigrationReconciler) patchStatus(ctx context.Context, migration *dbv1.SchemaMigration, phase dbv1.SchemaMigrationStatusPhase, message string) error {
	if migration.Status.Phase == phase && migration.Status.Message == message && migration.Status.ObservedGeneration == migration.Generation {
		return nil
	}
	base := migration.DeepCopy()
	migration.Status.Phase = phase
	migration.Status.ObservedGeneration = migration.Generation
	migration.Status.Message = message
	if perr := r.Status().Patch(ctx, migration, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); perr != nil {
		return fmt.Errorf("failed to patch status: %w", perr)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchemaMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1.SchemaMigration{}).
		Owns(&batchv1.Job{}).
		Named("schemamigration").
		Complete(r)
}
