package adapter

import (
	"context"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
)

type Persistent interface {
	Init(ctx context.Context) error
	GetLatestVersion(ctx context.Context) (int, error)
	GetMigrationStatus(ctx context.Context, version int) (dbv1.SchemaMigrationStatusPhase, bool, error)
	InsertNewVersion(ctx context.Context, name string, version int, status dbv1.SchemaMigrationStatusPhase) error
	ExecSql(ctx context.Context, sql string) error
	UpdateVersion(ctx context.Context, version int, prevStatus, destStatus dbv1.SchemaMigrationStatusPhase) error
	Close(ctx context.Context) error
}
