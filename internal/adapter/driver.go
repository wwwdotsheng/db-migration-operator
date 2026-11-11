package adapter

import "context"

type VersionInfo struct {
	Raw string
}

type Driver interface {
	CheckVersion(ctx context.Context) (VersionInfo, error)
	WithMigrationLock(ctx context.Context, run func(MigrationLedger) error) error
	Close(ctx context.Context) error
}
