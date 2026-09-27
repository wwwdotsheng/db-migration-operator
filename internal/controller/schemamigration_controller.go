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

	"github.com/go-logr/logr"
	adapter "github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	"github.com/wwwdotsheng/db-migration-operator/internal/resolver"
	"github.com/wwwdotsheng/db-migration-operator/internal/version"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	BeforeVersionNotSatisfied = errors.New("before version not satisfied")
)

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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SchemaMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var migration dbv1.SchemaMigration
	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger := logf.FromContext(ctx)
	logger = logger.WithValues(
		"changeType", migration.Spec.ChangeType,
		"version", migration.Spec.Version,
	)

	if !slices.Contains(validChangeType, migration.Spec.ChangeType) {
		msg := fmt.Sprintf("unknown ChangeType type: %q", migration.Spec.ChangeType)
		if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, msg); perr != nil {
			return ctrl.Result{}, perr
		}
		r.Recorder.Event(&migration, corev1.EventTypeWarning, "InvalidChangeType", msg)
		return ctrl.Result{}, nil
	}

	if !slices.Contains(validStatusPhases, migration.Status.Phase) {
		invalidPhase := migration.Status.Phase
		msg := fmt.Sprintf(
			"invalid phase %q, migration terminated, manual intervention required",
			invalidPhase,
		)
		if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, msg); perr != nil {
			return ctrl.Result{}, perr
		}
		r.Recorder.Event(&migration, corev1.EventTypeWarning, "InvalidPhaseStatus", msg)
		return ctrl.Result{}, nil
	}

	switch migration.Status.Phase {
	case dbv1.PhaseInit,
		dbv1.PhaseApplying,
		dbv1.PhaseBlocked:
		dsn, dbCfg, serverVersion, err := r.prepareDatabase(ctx, &migration)
		if err != nil {
			switch {
			case errors.Is(err, adapter.ErrNotParseDriverConfig):
				if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, err.Error()); perr != nil {
					return ctrl.Result{}, perr
				}
				return ctrl.Result{}, nil
			case errors.Is(err, adapter.ErrNotFoundSecret):
				// ★ Blocked 有三个来源，靠 Event + Condition 的 Reason 区分
				if cerr := r.setCondition(ctx, &migration, dbv1.ConditionBlocked,
					metav1.ConditionTrue, dbv1.ReasonSecretNotFound, err.Error()); cerr != nil {
					return ctrl.Result{}, cerr
				}
				if perr := r.patchStatus(ctx, &migration, dbv1.PhaseBlocked, err.Error()); perr != nil {
					return ctrl.Result{}, perr
				}
				r.Recorder.Eventf(&migration, corev1.EventTypeWarning, dbv1.ReasonSecretNotFound,
					"等待 Secret 出现，30 分钟后重试: %v", err)
				return ctrl.Result{RequeueAfter: 30 * time.Minute}, nil
			case errors.Is(err, adapter.ErrUnauthorizedSecret):
				if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, err.Error()); perr != nil {
					return ctrl.Result{}, perr
				}
				return ctrl.Result{}, nil
			default:
				// ★ 未知错误也要让用户看得见 —— 否则 CR 的 phase 一直是空的，
				//   只能翻 Operator 日志才知道为什么。但仍然返回 err，
				//   因为这类错误（连不上库、版本不匹配…）重试可能恢复。
				if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, err.Error()); perr != nil {
					return ctrl.Result{}, perr
				}
				r.Recorder.Event(&migration, corev1.EventTypeWarning, "PrepareDatabaseFailed", err.Error())
				return ctrl.Result{}, err
			}
		}

		// ★ 用 Condition 而不是 Phase 判断「要不要预演」。
		//
		//	为什么：Phase 会被中间态覆盖。典型场景 ——
		//	  Init 时 prepareDatabase 报 ErrNotFoundSecret → Phase 变 Blocked
		//	  下次 reconcile 进来 Phase 已经是 Blocked
		//	  → 如果这里判断 `Phase == Init`，预演就被【永久跳过】直接上真库了
		//
		//	Condition 是单调的（一旦 True 就不会自己变回去），所以不会漏。
		persistent, aerr := adapter.DefaultAdapterManager.New(migration.Spec.Driver, dsn)
		if aerr != nil {
			if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, aerr.Error()); perr != nil {
				return ctrl.Result{}, perr
			}
			r.Recorder.Event(&migration, corev1.EventTypeWarning, "NoAdapterForDriver", aerr.Error())
			return ctrl.Result{}, nil
		}

		if !meta.IsStatusConditionTrue(migration.Status.Conditions, dbv1.ConditionShadowValidated) {
			done, serr := r.shadowValidate(ctx, &migration, persistent, dbCfg, serverVersion)
			if serr != nil {
				if cerr := r.setCondition(ctx, &migration, dbv1.ConditionShadowValidated,
					metav1.ConditionFalse, dbv1.ReasonValidationFailed, serr.Error()); cerr != nil {
					return ctrl.Result{}, cerr
				}
				if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, serr.Error()); perr != nil {
					return ctrl.Result{}, perr
				}
				r.Recorder.Event(&migration, corev1.EventTypeWarning, "ShadowValidationFailed", serr.Error())
				return ctrl.Result{}, nil
			}
			// ★ 声明式：Job 还没跑完就退出，等它变化再回来。
			//   不要阻塞 reconcile —— Owns(&Job{}) 会在 Job 状态变化时唤醒我们。
			if !done {
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}

			if cerr := r.setCondition(ctx, &migration, dbv1.ConditionShadowValidated,
				metav1.ConditionTrue, dbv1.ReasonValidationPassed, "影子预演通过"); cerr != nil {
				return ctrl.Result{}, cerr
			}
			r.Recorder.Event(&migration, corev1.EventTypeNormal, "ShadowValidated", "影子预演通过")

			if uerr := r.patchStatus(ctx, &migration, dbv1.PhaseApplying, ""); uerr != nil {
				return ctrl.Result{}, uerr
			}
		}

		if migration.Status.Phase == dbv1.PhaseBlocked && migration.Spec.BeforeVersion != nil {
			if err := r.judgeBlock(ctx, persistent, *migration.Spec.BeforeVersion); err != nil {
				if errors.Is(err, BeforeVersionNotSatisfied) {
					if cerr := r.setCondition(ctx, &migration, dbv1.ConditionBlocked,
						metav1.ConditionTrue, dbv1.ReasonBeforeVersionNotSatisfied, err.Error()); cerr != nil {
						return ctrl.Result{}, cerr
					}
					r.Recorder.Eventf(&migration, corev1.EventTypeWarning,
						dbv1.ReasonBeforeVersionNotSatisfied, "等待前置版本: %v", err)
					return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
				}
				return ctrl.Result{}, err
			}
		}

		if migration.Status.Phase == dbv1.PhaseBlocked {
			err = r.patchStatus(ctx, &migration, dbv1.PhaseApplying, "")
			if err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(&migration, corev1.EventTypeNormal, "BlockCleared", "block is cleared")
		}

		if err := r.applyMigration(ctx, logger, persistent, &migration); err != nil {
			if errors.Is(err, BeforeVersionNotSatisfied) {
				if cerr := r.setCondition(ctx, &migration, dbv1.ConditionBlocked,
					metav1.ConditionTrue, dbv1.ReasonBeforeVersionNotSatisfied, err.Error()); cerr != nil {
					return ctrl.Result{}, cerr
				}
				if perr := r.patchStatus(ctx, &migration, dbv1.PhaseBlocked, err.Error()); perr != nil {
					return ctrl.Result{}, perr
				}
				r.Recorder.Eventf(&migration, corev1.EventTypeWarning,
					dbv1.ReasonBeforeVersionNotSatisfied, "执行前发现前置版本不满足: %v", err)
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}

			if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, err.Error()); perr != nil {
				return ctrl.Result{}, fmt.Errorf("patch status: %w", perr)
			}

			return ctrl.Result{}, fmt.Errorf("apply migration failed: %w", err)
		}

		// ★ Blocked 已经不再成立，把 condition 转成 False（而不是删掉）——
		//   保留历史能看出「曾经因为什么卡过」。
		if cerr := r.setCondition(ctx, &migration, dbv1.ConditionBlocked,
			metav1.ConditionFalse, dbv1.ReasonMigrationApplied, "阻塞已解除，迁移完成"); cerr != nil {
			return ctrl.Result{}, cerr
		}
		if peer := r.patchStatus(ctx, &migration, dbv1.PhaseDone, "migration done"); peer != nil {
			return ctrl.Result{}, fmt.Errorf("patch status: %w", peer)
		}
		r.Recorder.Event(&migration, corev1.EventTypeNormal, dbv1.ReasonMigrationApplied, "迁移完成")

		return ctrl.Result{}, nil
	case dbv1.PhaseDone,
		dbv1.PhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *SchemaMigrationReconciler) judgeBlock(ctx context.Context, persistent adapter.Persistent, version int) error {
	status, exists, err := persistent.GetVersionStatus(ctx, version)
	if err != nil {
		return err
	}
	if !exists || status != dbv1.PhaseDone {
		return BeforeVersionNotSatisfied
	}
	return nil
}

func (r *SchemaMigrationReconciler) applyMigration(ctx context.Context, logger logr.Logger, persistent adapter.Persistent, migration *dbv1.SchemaMigration) error {
	if err := persistent.Init(ctx); err != nil {
		return fmt.Errorf("failed init mysql adapter: %w", err)
	}

	status, found, err := persistent.GetMigrationStatus(ctx, migration.Spec.Version)
	if err != nil {
		return fmt.Errorf("failed get migration status version=%d: %w", migration.Spec.Version, err)
	}

	switch {
	case found && status == dbv1.PhaseDone:
		if perr := r.patchStatus(ctx, migration, dbv1.PhaseDone, "migration done"); perr != nil {
			return perr
		}
		return nil
	case found && status == dbv1.PhaseFailed:
		if err := persistent.UpdateVersion(ctx, migration.Spec.Version, dbv1.PhaseFailed, dbv1.PhaseApplying); err != nil {
			if errors.Is(err, adapter.ErrVersionConflict) {
				return fmt.Errorf("version conflict, will we-read status: %w", err)
			}
			return fmt.Errorf("failed update version status: %w", err)
		}
	case !found:
		if err := persistent.InsertNewVersion(ctx, migration.Name, migration.Spec.Version, dbv1.PhaseApplying); err != nil {
			return fmt.Errorf(
				"failed insert migration version=%d status=%q: %w",
				migration.Spec.Version, dbv1.PhaseApplying, err,
			)
		}
	}

	if migration.Spec.BeforeVersion != nil {
		status, exists, err := persistent.GetVersionStatus(ctx, *migration.Spec.BeforeVersion)
		if err != nil {
			return fmt.Errorf(
				"failed get before version=%d status: %w",
				*migration.Spec.BeforeVersion, err,
			)
		}

		if (!exists) || (status != dbv1.PhaseDone) {
			return BeforeVersionNotSatisfied
		}
	}

	runLines, err := persistent.ExecSql(ctx, migration.Spec.ApplySql)
	if err != nil {
		uerr := fmt.Errorf("failed to apply migration: applied sql lines %d err: %w", runLines, err)
		logger.Error(err, uerr.Error())
		return err
	}

	if err := persistent.UpdateVersion(ctx, migration.Spec.Version, dbv1.PhaseApplying, dbv1.PhaseDone); err != nil {
		return fmt.Errorf("failed to update version: %w", err)
	}

	err = r.patchStatus(ctx, migration, dbv1.PhaseDone, "migration done")
	if err != nil {
		return err
	}

	return nil
}

// prepareDatabase 完成迁移执行前的连接准备：
// 解析 driverConfig（注册表按 driver 分发）→ 读取 Secret 凭据 → 构建 DSN → 版本校验闸门。
// 返回的 dsn 供迁移流程复用。
func (r *SchemaMigrationReconciler) prepareDatabase(ctx context.Context, migration *dbv1.SchemaMigration) (dsn string, cfg adapter.Config, serverVersion string, err error) {
	cfg, err = adapter.DefaultConfigManager.Parse(migration.Spec.Driver, migration.Spec.DriverConfig.Raw)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: driver %q: %w", adapter.ErrNotParseDriverConfig, migration.Spec.Driver, err)
	}

	password, err := r.Resolver.ResolvePassword(ctx, migration.Namespace, cfg.SecretRef())
	if err != nil {
		return "", nil, "", err
	}

	dsn, err = cfg.BuildDSN(password)
	if err != nil {
		return "", nil, "", err
	}

	parser, err := version.DefaultParserManager.New(migration.Spec.Driver, dsn)
	if err != nil {
		return "", nil, "", err
	}

	v, err := parser.GetVersion(ctx)
	if err != nil {
		return "", nil, "", err
	}

	ok, err := version.DefaultCheckerManager.Check(migration.Spec.Driver, v)
	if err != nil {
		return "", nil, "", err
	}
	if !ok {
		return "", nil, "", fmt.Errorf("database version %s does not satisfy %s constraint", v, migration.Spec.Driver)
	}
	return dsn, cfg, v, nil
}

// setCondition 更新一个 condition 并写回 status。
// 用 meta.SetStatusCondition：它会处理 LastTransitionTime 和去重
// （同一个 condition 内容没变就不会重复写，避免无意义的状态更新）。
func (r *SchemaMigrationReconciler) setCondition(
	ctx context.Context, migration *dbv1.SchemaMigration,
	condType string, status metav1.ConditionStatus, reason, message string,
) error {
	base := migration.DeepCopy()
	meta.SetStatusCondition(&migration.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: migration.Generation,
	})
	if perr := r.Status().Patch(ctx, migration, client.MergeFrom(base)); perr != nil {
		return fmt.Errorf("failed to patch condition: %w", perr)
	}
	return nil
}

func (r *SchemaMigrationReconciler) patchStatus(ctx context.Context, migration *dbv1.SchemaMigration, phase dbv1.SchemaMigrationStatusPhase, message string) error {
	if migration.Status.Phase == phase && migration.Status.Message == message {
		return nil
	}
	base := migration.DeepCopy()
	migration.Status.Phase = phase
	migration.Status.Message = message
	if perr := r.Status().Patch(ctx, migration, client.MergeFrom(base)); perr != nil {
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
