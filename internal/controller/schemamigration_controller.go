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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"

	_ "github.com/go-sql-driver/mysql"
)

// SchemaMigrationReconciler reconciles a SchemaMigration object
type SchemaMigrationReconciler struct {
	client.Client
	// APIReader 直读 API server（不经缓存），用于读取 Secret 等敏感对象，
	// 避免为 list/watch secrets 扩大 RBAC 权限。
	APIReader client.Reader
	Scheme    *runtime.Scheme
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

	// 空 Phase 是新建 CR 的初始态，归一化为 Unknown（本地赋值，避免多余更新）
	if migration.Status.Phase == "" {
		migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseUnknown
	}

	statusEnum := []dbv1.SchemaMigrationStatusPhase{
		dbv1.SchemaMigrationStatusPhaseUnknown,
		dbv1.SchemaMigrationStatusPhaseApplying,
		dbv1.SchemaMigrationStatusPhaseFailed,
		dbv1.SchemaMigrationStatusPhaseDone,
		dbv1.SchemaMigrationStatusPhaseRolling,
		dbv1.SchemaMigrationStatusPhaseRolledBack,
		dbv1.SchemaMigrationStatusPhaseRollbackFailed,
	}
	if !slices.Contains(statusEnum, migration.Status.Phase) {
		err := fmt.Errorf("unknow status %s", migration.Status.Phase)
		logger.Error(err, "unknow status")
		migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseUnknown
		if uerr := r.Status().Update(ctx, &migration); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{}, nil
	}

	switch migration.Spec.Operation {
	case dbv1.SchemaMigrationOperationApply:
		switch migration.Status.Phase {
		case dbv1.SchemaMigrationStatusPhaseUnknown,
			dbv1.SchemaMigrationStatusPhaseApplying:
			dsn, err := r.prepareDatabase(ctx, req, &migration)
			if err != nil {
				return r.setFailed(ctx, &migration, err)
			}
			return r.applyMigration(ctx, &migration, dsn)
		case dbv1.SchemaMigrationStatusPhaseDone,
			dbv1.SchemaMigrationStatusPhaseFailed:
			return ctrl.Result{}, nil
		case dbv1.SchemaMigrationStatusPhaseRolledBack:
			return r.setFailed(ctx, &migration, fmt.Errorf("版本已回滚，如需重新应用请创建新版本"))
		default:
			return ctrl.Result{}, fmt.Errorf("非法状态: %s", migration.Status.Phase)
		}

	case dbv1.SchemaMigrationOperationRollback:
		switch migration.Status.Phase {
		case dbv1.SchemaMigrationStatusPhaseDone,
			dbv1.SchemaMigrationStatusPhaseRolling:
			dsn, err := r.prepareDatabase(ctx, req, &migration)
			if err != nil {
				return r.setFailed(ctx, &migration, err)
			}
			return r.rollbackMigration(ctx, &migration, dsn)
		case dbv1.SchemaMigrationStatusPhaseRolledBack,
			dbv1.SchemaMigrationStatusPhaseRollbackFailed:
			return ctrl.Result{}, nil
		case dbv1.SchemaMigrationStatusPhaseApplying,
			dbv1.SchemaMigrationStatusPhaseFailed:
			return r.setFailed(ctx, &migration, fmt.Errorf("版本未成功应用，无需回滚"))
		default:
			return ctrl.Result{}, fmt.Errorf("非法状态: %s", migration.Status.Phase)
		}
	}

	return ctrl.Result{}, nil
}

func (r *SchemaMigrationReconciler) setFailed(ctx context.Context, migration *dbv1.SchemaMigration, err error) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	logger.Error(err, "Migration failed",
		"version", migration.Spec.Version,
		"operation", migration.Spec.Operation,
		"phase", migration.Status.Phase)

	migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseFailed
	migration.Status.Message = err.Error()
	uctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if uerr := r.Status().Update(uctx, migration); uerr != nil {
		logger.Error(uerr, "Failed to update failed status")
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{}, err
}

func (r *SchemaMigrationReconciler) applyMigration(ctx context.Context, migration *dbv1.SchemaMigration, dsn string) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// 1. 设置 Applying（已是 Applying 则跳过，避免重复更新）
	if migration.Status.Phase != dbv1.SchemaMigrationStatusPhaseApplying {
		migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseApplying
		migration.Status.Message = "迁移执行中"
		if err := r.Status().Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}
	}


	// 2. 连接 + Init（建 schema_migrations 表）
	persistent, err := adapter.NewMysqlAdapter(dsn)
	if err != nil {
		return r.setFailed(ctx, migration, err)
	}
	defer persistent.Close(ctx)
	if err := persistent.Init(ctx); err != nil {
		return r.setFailed(ctx, migration, err)
	}

	// 3. 幂等检查（账本状态驱动）
	status, found, err := persistent.GetMigrationStatus(ctx, migration.Spec.Version)
	if err != nil {
		return r.setFailed(ctx, migration, err)
	}
	switch {
	case found && status == dbv1.SchemaMigrationStatusPhaseDone:
		// 已应用：补 Done 状态后结束
		migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseDone
		migration.Status.Message = "迁移已完成"
		if err := r.Status().Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	case found && status == dbv1.SchemaMigrationStatusPhaseRolledBack:
		return r.setFailed(ctx, migration, fmt.Errorf("版本 %d 已回滚，如需重新应用请创建新版本", migration.Spec.Version))
	case found && status == dbv1.SchemaMigrationStatusPhaseFailed:
		// 上次失败：重置为 Applying 续跑
		if err := persistent.UpdateVersion(ctx, migration.Spec.Version,
			dbv1.SchemaMigrationStatusPhaseFailed, dbv1.SchemaMigrationStatusPhaseApplying); err != nil {
			return r.setFailed(ctx, migration, err)
		}
	case !found:
		// 新迁移：插入一条 applying 记录
		if err := persistent.InsertNewVersion(ctx, migration.Name, migration.Spec.Version,
			dbv1.SchemaMigrationStatusPhaseApplying); err != nil {
			return r.setFailed(ctx, migration, err)
		}
	}

	// 4. 逐条执行 ApplySql
	for i, stmt := range migration.Spec.ApplySql {
		if err := persistent.ExecSql(ctx, stmt); err != nil {
			logger.Error(err, "Failed to execute ApplySql",
				"version", migration.Spec.Version, "index", i+1)
			// 补偿回滚（RollbackSql 有就执行，失败不覆盖主错误）
			var rollbackErrs []string
			for j, rb := range migration.Spec.RollbackSql {
				if rerr := persistent.ExecSql(ctx, rb); rerr != nil {
					logger.Error(rerr, "Failed to execute RollbackSql",
						"version", migration.Spec.Version, "index", j+1)
					rollbackErrs = append(rollbackErrs, fmt.Sprintf("补偿回滚失败: %v", rerr))
				}
			}
			if uerr := persistent.UpdateVersion(ctx, migration.Spec.Version,
				dbv1.SchemaMigrationStatusPhaseApplying, dbv1.SchemaMigrationStatusPhaseFailed); uerr != nil {
				logger.Error(uerr, "Failed to update ledger to failed", "version", migration.Spec.Version)
			}
			if len(rollbackErrs) > 0 {
				return r.setFailed(ctx, migration, fmt.Errorf("第 %d 条执行失败: %v；%s", i+1, err, rollbackErrs[0]))
			}
			return r.setFailed(ctx, migration, fmt.Errorf("第 %d 条执行失败: %v", i+1, err))
		}
	}

	// 5. 成功：账本 CAS applying → done + CR 状态 Done
	if err := persistent.UpdateVersion(ctx, migration.Spec.Version,
		dbv1.SchemaMigrationStatusPhaseApplying, dbv1.SchemaMigrationStatusPhaseDone); err != nil {
		return r.setFailed(ctx, migration, err)
	}
	migration.Status.Phase = dbv1.SchemaMigrationStatusPhaseDone
	migration.Status.Message = "迁移完成"
	migration.Status.AppliedAt = &metav1.Time{Time: time.Now()}
	if err := r.Status().Update(ctx, migration); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
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
