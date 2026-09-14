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

	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
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
		if migration.Status.Phase == dbv1.PhaseInit {
			if uerr := r.patchStatus(ctx, &migration, dbv1.PhaseApplying, ""); uerr != nil {
				return ctrl.Result{}, uerr
			}
		}

		dsn, err := r.prepareDatabase(ctx, req, &migration)
		if err != nil {
			logger.Error(err, "failed to prepare database")
			return ctrl.Result{}, err
		}

		persistent, err := adapter.NewMysqlAdapter(dsn)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create mysql adapter: %w", err)
		}

		if migration.Status.Phase == dbv1.PhaseBlocked {
			err := r.judgeBlock(ctx, persistent, *migration.Spec.BeforeVersion)
			if err != nil {
				if errors.Is(err, BeforeVersionNotSatisfied) {
					return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
				}
				return ctrl.Result{}, err
			}
			err = r.patchStatus(ctx, &migration, dbv1.PhaseApplying, "")
			if err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(&migration, corev1.EventTypeNormal, "BlockCleared", "...")
		}

		if err := r.applyMigration(ctx, logger, persistent, &migration); err != nil {
			if errors.Is(err, BeforeVersionNotSatisfied) {
				err := r.patchStatus(ctx, &migration, dbv1.PhaseBlocked, err.Error())
				if err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}

			if perr := r.patchStatus(ctx, &migration, dbv1.PhaseFailed, err.Error()); perr != nil {
				return ctrl.Result{}, fmt.Errorf("patch status: %w", perr)
			}

			return ctrl.Result{}, fmt.Errorf("apply migration failed: %w", err)
		}

		if peer := r.patchStatus(ctx, &migration, dbv1.PhaseDone, "migration done"); peer != nil {
			return ctrl.Result{}, fmt.Errorf("patch status: %w", peer)
		}

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

func (r *SchemaMigrationReconciler) patchStatus(ctx context.Context, migration *dbv1.SchemaMigration, phase dbv1.SchemaMigrationStatusPhase, message string) error {
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
		Named("schemamigration").
		Complete(r)
}
