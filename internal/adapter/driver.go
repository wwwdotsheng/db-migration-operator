package adapter

import "context"

type Driver interface {
	GetServerVersion(ctx context.Context) (string, error)
	WithMigrationLock(ctx context.Context, run func(MigrationLedger) error) error
	Close(ctx context.Context) error
}
