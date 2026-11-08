package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMigrationChecksumAndJobIdentity(t *testing.T) {
	spec := validSpec(dbv1.ChangeTypeExpand)
	spec.DriverConfig.Raw = []byte(`{"host":"mysql","port":3306}`)
	first, err := migrationChecksum(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.DriverConfig.Raw = []byte(`{"port":3306,"host":"mysql"}`)
	reordered, err := migrationChecksum(spec)
	if err != nil || reordered != first {
		t.Fatalf("same config produced a different digest: %v", err)
	}
	spec.ApplySql[0] = "ALTER TABLE items ADD COLUMN changed INT"
	modified, err := migrationChecksum(spec)
	if err != nil || modified == first {
		t.Fatalf("modified SQL did not change the digest: %v", err)
	}

	m := &dbv1.SchemaMigration{ObjectMeta: metav1.ObjectMeta{UID: "original", Generation: 1}}
	name := shadowJobName(m, "snapshot-one")
	if name == shadowJobName(m, "snapshot-two") {
		t.Fatal("different ledger snapshots reused the Job")
	}
	m.Generation++
	if name == shadowJobName(m, "snapshot-one") {
		t.Fatal("different generations reused the Job")
	}
	m.Generation--
	m.UID = "recreated"
	if name == shadowJobName(m, "snapshot-one") {
		t.Fatal("different owners reused the Job")
	}
}

func TestMigrationLedgerGates(t *testing.T) {
	before := 7
	for _, tc := range []struct {
		name   string
		rows   []adapter.MigrationRecord
		before *int
		reason string
		done   bool
	}{
		{name: "same content done", rows: []adapter.MigrationRecord{{Version: 8, Status: dbv1.PhaseDone, Checksum: "same"}}, done: true},
		{name: "same version different SQL", rows: []adapter.MigrationRecord{{Version: 8, Status: dbv1.PhaseDone, Checksum: "different"}}, reason: "MigrationIdentityConflict"},
		{name: "legacy version cannot prove content", rows: []adapter.MigrationRecord{{Version: 8, Status: dbv1.PhaseDone}}, reason: "MigrationIdentityConflict"},
		{name: "legacy predecessor usable", rows: []adapter.MigrationRecord{{Version: 7, Status: dbv1.PhaseDone}}, before: &before},
		{name: "missing predecessor", before: &before, reason: dbv1.ReasonBeforeVersionNotSatisfied},
		{name: "interrupted version", rows: []adapter.MigrationRecord{{Version: 8, Status: dbv1.PhaseApplying, Checksum: "same"}}, reason: "MigrationRecoveryRequired"},
		{name: "different version blocked by unresolved migration", rows: []adapter.MigrationRecord{{Version: 7, Status: dbv1.PhaseApplying}}, reason: "MigrationRecoveryRequired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inspectLedger(tc.rows, 8, tc.before, "same")
			if got.reason != tc.reason || got.alreadyDone != tc.done {
				t.Fatalf("unexpected decision: %+v", got)
			}
		})
	}
}

type interruptedLedger struct {
	rows       []adapter.MigrationRecord
	executions int
}

func (l *interruptedLedger) Records(context.Context) ([]adapter.MigrationRecord, error) {
	return l.rows, nil
}
func (l *interruptedLedger) Start(_ context.Context, _ string, version int, checksum string) error {
	l.rows = append(l.rows, adapter.MigrationRecord{Version: version, Status: dbv1.PhaseApplying, Checksum: checksum})
	return nil
}
func (l *interruptedLedger) ExecSQL(context.Context, []string) (int, error) {
	l.executions++
	return 1, nil
}
func (l *interruptedLedger) MarkDone(context.Context, int, string) error {
	return errors.New("injected ledger outage after DDL")
}

func TestMigrationDoesNotReexecuteAfterLedgerWriteFailure(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := dbv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	m := &dbv1.SchemaMigration{ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "default", UID: "uid", Generation: 1}, Spec: validSpec(dbv1.ChangeTypeExpand)}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: shadowJobName(m, checksumBytes([]byte("null"))), Namespace: m.Namespace,
		OwnerReferences: []metav1.OwnerReference{{APIVersion: dbv1.GroupVersion.String(), Kind: "SchemaMigration", Name: m.Name, UID: m.UID, Controller: new(true)}}},
		Status: batchv1.JobStatus{Succeeded: 1}}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(m).WithObjects(m, job).Build()
	r := &SchemaMigrationReconciler{Client: k8s, APIReader: k8s, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}
	driver, err := adapter.NewMysqlDriver("user@tcp(localhost:3306)/test")
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close(ctx)
	sum, err := migrationChecksum(m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	ledger := &interruptedLedger{}
	for round := 0; round < 2; round++ {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
			t.Fatal(err)
		}
		result, err := r.reconcileLocked(ctx, m, driver, &adapter.MysqlConfig{}, "8.0.36", sum, ledger)
		if err != nil || result.RequeueAfter == 0 {
			t.Fatalf("round %d result=%v err=%v", round, result, err)
		}
	}
	if ledger.executions != 1 || len(ledger.rows) != 1 || ledger.rows[0].Status != dbv1.PhaseApplying {
		t.Fatalf("unsafe repeat: %+v", ledger)
	}
	if m.Status.Phase != dbv1.PhaseBlocked {
		t.Fatalf("unexpected CR phase %q", m.Status.Phase)
	}
	condition := meta.FindStatusCondition(m.Status.Conditions, dbv1.ConditionBlocked)
	if condition == nil || condition.Reason != "MigrationRecoveryRequired" || !strings.Contains(condition.Message, "injected ledger outage") {
		t.Fatalf("missing recovery condition: %v", condition)
	}
}

func TestMigrationRejectsEditedBoundCR(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := dbv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	migration := &dbv1.SchemaMigration{
		ObjectMeta: metav1.ObjectMeta{Name: "edited", Namespace: "default", Generation: 2},
		Spec:       validSpec(dbv1.ChangeTypeExpand),
	}
	original, err := migrationChecksum(migration.Spec)
	if err != nil {
		t.Fatal(err)
	}
	migration.Status.SpecChecksum = original
	migration.Spec.Version++
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(migration).WithObjects(migration).Build()
	r := &SchemaMigrationReconciler{Client: k8s}
	key := client.ObjectKeyFromObject(migration)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Get(ctx, key, migration); err != nil {
		t.Fatal(err)
	}
	if migration.Status.Phase != dbv1.PhaseFailed || migration.Status.SpecChecksum != original {
		t.Fatalf("unexpected phase or binding: %+v", migration.Status)
	}
}
