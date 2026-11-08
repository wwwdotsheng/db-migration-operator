package adapter

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Run against disposable databases. The adapter creates or upgrades its ledger.
func TestMigrationLockIntegration(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		open Factory
	}{
		{"mysql", "DB_OPERATOR_TEST_MYSQL_DSN", func(dsn string) (Driver, error) { return NewMysqlDriver(dsn) }},
		{"postgres", "DB_OPERATOR_TEST_POSTGRES_DSN", func(dsn string) (Driver, error) { return NewPostgresDriver(dsn) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if dsn == "" {
				t.Skipf("set %s to a disposable test database", tc.env)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			first, err := tc.open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close(ctx)
			second, err := tc.open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close(ctx)
			expected := errors.New("injected callback failure")
			err = first.WithMigrationLock(ctx, func(ledger MigrationLedger) error {
				if _, err := ledger.Records(ctx); err != nil {
					return err
				}
				err := second.WithMigrationLock(ctx, func(MigrationLedger) error {
					t.Error("second session entered a locked database")
					return nil
				})
				if !errors.Is(err, ErrMigrationLockBusy) {
					return errors.Join(errors.New("expected busy lock"), err)
				}
				return expected
			})
			if !errors.Is(err, expected) {
				t.Fatalf("first session: %v", err)
			}
			if err := second.WithMigrationLock(ctx, func(MigrationLedger) error { return nil }); err != nil {
				t.Fatalf("lock leaked: %v", err)
			}
		})
	}
}
