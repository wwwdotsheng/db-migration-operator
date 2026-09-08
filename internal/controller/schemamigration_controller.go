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
	"fmt"
	"slices"
	"time"

	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
)

// SchemaMigrationReconciler reconciles a SchemaMigration object
type SchemaMigrationReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SchemaMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var migration dbv1.SchemaMigration
	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	logger := logf.FromContext(ctx,
		"name", req.Name,
		"namespace", req.Namespace,
		"operation", migration.Spec.ChangeType,
		"version", migration.Spec.Version,
	)

	if !slices.Contains([]dbv1.SchemaMigrationChangeType{
		dbv1.ChangeTypeExpand,
		dbv1.ChangeTypeContract,
		dbv1.ChangeTypeMixed,
	}, migration.Spec.ChangeType) {
		err := fmt.Errorf("未知的ChangeType类型：%q", migration.Spec.ChangeType)
		logger.Error(err, "invalid schema migration operation, skip reconcile")
		msg := err.Error()
		if migration.Status.Message != msg {
			migration.Status.Message = msg
			if uerr := r.Status().Update(ctx, &migration); uerr != nil {
				return ctrl.Result{}, uerr
			}
		}
		r.Recorder.Event(&migration, corev1.EventTypeWarning, "InvalidChangeType", msg)
		return ctrl.Result{}, nil
	}

	if !slices.Contains([]dbv1.SchemaMigrationStatusPhase{
		dbv1.PhaseInit,
		dbv1.PhaseApplying,
		dbv1.PhaseBlocked,
		dbv1.PhaseFailed,
		dbv1.PhaseDone,
	}, migration.Status.Phase) {
		invalidPhase := migration.Status.Phase
		msg := fmt.Sprintf("invalid phase %q, migration terminated, manual intervention required", invalidPhase)
		migration.Status.Phase = dbv1.PhaseFailed
		migration.Status.Message = msg
		logger.Error(fmt.Errorf("invalid phase %q", invalidPhase), "invalid schema migration status, terminated")
		r.Recorder.Event(&migration, corev1.EventTypeWarning, "InvalidPhase", msg)
		if uerr := r.Status().Update(ctx, &migration); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{}, nil
	}

	switch migration.Status.Phase {
	case dbv1.PhaseInit,
		dbv1.PhaseApplying,
		dbv1.PhaseBlocked:
		dsn, err := r.prepareDatabase(ctx, req, &migration)
		if err != nil {
			return r.setFailed(ctx, &migration, err)
		}
		return r.applyMigration(ctx, &migration, dsn)
	case dbv1.PhaseDone,
		dbv1.PhaseFailed:
		return ctrl.Result{}, nil
	default:
		return r.setFailed(ctx, &migration, fmt.Errorf("非法状态：%s", migration.Status.Phase))
	}
}

func (r *SchemaMigrationReconciler) setFailed(ctx context.Context, migration *dbv1.SchemaMigration, err error) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	logger.Error(err, "Migration failed",
		"version", migration.Spec.Version,
		"changeType", migration.Spec.ChangeType,
		"phase", migration.Status.Phase,
	)

	msg := err.Error()
	if migration.Status.Phase != dbv1.PhaseFailed ||
		migration.Status.Message != msg {
		migration.Status.Phase = dbv1.PhaseFailed
		migration.Status.Message = msg
		r.Recorder.Event(migration, corev1.EventTypeWarning, "MigrationFailed", msg)

		uctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if uerr := r.Status().Update(uctx, migration); uerr != nil {
			logger.Error(uerr, "Failed to update failed status")
			return ctrl.Result{}, uerr
		}
	}
	return ctrl.Result{}, err
}

func (r *SchemaMigrationReconciler) applyMigration(ctx context.Context, migration *dbv1.SchemaMigration, dsn string) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	persistent, err := adapter.NewMysqlAdapter(dsn)
	if err != nil {
		err = fmt.Errorf("failed to create mysql adapter: %w", err)
		logger.Error(err, err.Error())
		return ctrl.Result{}, err
	}

	err = persistent.Init(ctx)
	if err != nil {
		err = fmt.Errorf("failed to init mysql adapter: %w", err)
		logger.Error(err, err.Error())
		return ctrl.Result{}, err
	}

	status, found, err := persistent.GetMigrationStatus(ctx, migration.Spec.Version)
	if err != nil {
		err = fmt.Errorf("failed to get migration status: %w", err)
		logger.Error(err, err.Error())
		return ctrl.Result{}, err
	}

	switch {
	case found && status == dbv1.PhaseDone:
		if migration.Status.Phase != dbv1.PhaseDone {
			migration.Status.Phase = dbv1.PhaseDone
			if err := r.Status().Update(ctx, migration); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
			}
		}
		return ctrl.Result{}, nil
	case found && status == dbv1.PhaseFailed:
		if err := persistent.UpdateVersion(ctx, migration.Spec.Version, dbv1.PhaseFailed, dbv1.PhaseApplying); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update version: %w", err)
		}
	case !found:
		if err := persistent.InsertNewVersion(ctx, migration.Name, migration.Spec.Version, dbv1.PhaseApplying); err != nil {
			return ctrl.Result{}, err
		}
	}

	if migration.Spec.BeforeVersion != nil {
		status, exists, err := persistent.GetVersionStatus(ctx, *migration.Spec.BeforeVersion)
		if err != nil {
			logger.Error(err, "Failed to get latest version")
			return ctrl.Result{}, err
		}

		if (!exists) || (status != dbv1.PhaseDone) {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}

	runLines, err := persistent.ExecSql(ctx, migration.Spec.ApplySql)
	if err != nil {
		logger.Error(err, fmt.Sprintf("Failed to apply migration: applied sql lines %d", runLines))
		if err := persistent.UpdateVersion(ctx, migration.Spec.Version, dbv1.PhaseApplying, dbv1.PhaseFailed); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update version: %w", err)
		}
		migration.Status.Phase = dbv1.PhaseFailed
		if err := r.Status().Update(ctx, migration); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if err := persistent.UpdateVersion(ctx, migration.Spec.Version, dbv1.PhaseApplying, dbv1.PhaseDone); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update version: %w", err)
	}

	migration.Status.Phase = dbv1.PhaseDone
	if err := r.Status().Update(ctx, migration); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchemaMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1.SchemaMigration{}).
		Named("schemamigration").
		Complete(r)
}
