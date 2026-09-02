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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"

	_ "github.com/go-sql-driver/mysql"
)

// SchemaMigrationReconciler reconciles a SchemaMigration object
type SchemaMigrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=db.migration.dev,resources=schemamigrations/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SchemaMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	var migration dbv1.SchemaMigration
	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !slices.Contains([]dbv1.SchemaMigrationOperation{
		dbv1.SchemaMigrationOperationApply,
		dbv1.SchemaMigrationOperationRollback,
	}, migration.Spec.Operation) {
		err := fmt.Errorf("migration operation %s is not supported", migration.Spec.Operation)
		logger.Error(err, "migration operation is not supported")
		return ctrl.Result{}, err
	}

	// 按 migration.Spec.Driver 获取版本组件（注册已由 internal/version 的 init 完成）

	//mysqlAdapter, err := adapter.NewMysqlAdapter(migration.Spec.DatabaseRef)
	//
	//if err != nil {
	//	logger.Error(err, "failed to create mysql adapter")
	//	return ctrl.Result{}, err
	//}

	statusEnum := []dbv1.SchemaMigrationStatusPhase{
		dbv1.SchemaMigrationStatusPhaseApplying,
		dbv1.SchemaMigrationStatusPhaseFailed,
		dbv1.SchemaMigrationStatusPhaseDone,
		dbv1.SchemaMigrationStatusPhaseRolling,
		dbv1.SchemaMigrationStatusPhaseRolledBack,
		dbv1.SchemaMigrationStatusPhaseRollbackFailed,
	}
	if !slices.Contains(statusEnum, migration.Status.Phase) {
		err := fmt.Errorf("unknow status %s", migration.Status.Phase)
		logger.Error(err, err.Error())
		migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseUnknown
		err = r.Status().Update(ctx, &migration)
		logger.Error(err, err.Error())
		return ctrl.Result{}, err
	}

	switch migration.Spec.Operation {
	case dbv1.SchemaMigrationOperationApply:
		switch migration.Status.Phase {
		case dbv1.SchemaMigrationStatusPhaseUnknown,
			dbv1.SchemaMigrationStatusPhaseApplying:
			dsn, err := r.prepareDatabase(ctx, req, &migration)
			if err != nil {
				return r.setFailed(&migration, err)
			}
			return r.applyMigration(ctx, &migration, dsn)
		case dbv1.SchemaMigrationStatusPhaseDone,
			dbv1.SchemaMigrationStatusPhaseFailed:
			return ctrl.Result{}, nil
		case dbv1.SchemaMigrationStatusPhaseRolledBack:
			return r.setFailed(&migration, fmt.Errorf("版本已回滚，如需重新应用请创建新版本"))
		default:
			return ctrl.Result{}, fmt.Errorf("非法状态: %s", migration.Status.Phase)
		}

	case dbv1.SchemaMigrationOperationRollback:
		switch migration.Status.Phase {
		case dbv1.SchemaMigrationStatusPhaseDone,
			dbv1.SchemaMigrationStatusPhaseRolling:
			dsn, err := r.prepareDatabase(ctx, req, &migration)
			if err != nil {
				return r.setFailed(&migration, err)
			}
			return r.rollbackMigration(ctx, &migration, dsn)
		case dbv1.SchemaMigrationStatusPhaseRolledBack,
			dbv1.SchemaMigrationStatusPhaseRollbackFailed:
			return ctrl.Result{}, nil
		case dbv1.SchemaMigrationStatusPhaseApplying,
			dbv1.SchemaMigrationStatusPhaseFailed:
			return r.setFailed(&migration, fmt.Errorf("版本未成功应用，无需回滚"))
		default:
			return ctrl.Result{}, fmt.Errorf("非法状态: %s", migration.Status.Phase)
		}
	}

	return ctrl.Result{}, nil
}

func (r *SchemaMigrationReconciler) setFailed(migration *dbv1.SchemaMigration, err error) (ctrl.Result, error) {
	migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseFailed
	migration.Status.Message = err.Error()
	if uerr := r.Status().Update(context.Background(), migration); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{}, err
}

func (r *SchemaMigrationReconciler) applyMigration(ctx context.Context, migration *dbv1.SchemaMigration, dsn string) (ctrl.Result, error) {
	return ctrl.Result{}, fmt.Errorf("applyMigration not implemented")
}

func (r *SchemaMigrationReconciler) rollbackMigration(ctx context.Context, migration *dbv1.SchemaMigration, dsn string) (ctrl.Result, error) {
	return ctrl.Result{}, fmt.Errorf("rollbackMigration not implemented")
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchemaMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1.SchemaMigration{}).
		Named("schemamigration").
		Complete(r)
}
