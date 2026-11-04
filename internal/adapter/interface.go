package adapter

import (
	"context"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
)

type Persistent interface {
	Init(ctx context.Context) error
	// GetServerVersion 返回数据库的原始版本字符串（不做格式处理）。
	// 版本查询属于"数据库通道"的能力，因此由 adapter 承担；
	// 规范化与范围判断在 internal/version 的 Checker 里完成。
	GetServerVersion(ctx context.Context) (string, error)
	GetLatestVersion(ctx context.Context) (int, error)
	GetMigrationStatus(ctx context.Context, version int) (dbv1.SchemaMigrationStatusPhase, bool, error)
	InsertNewVersion(ctx context.Context, name string, version int, status dbv1.SchemaMigrationStatusPhase) error
	ExecSql(ctx context.Context, sql []string) (int, error)
	UpdateVersion(ctx context.Context, version int, prevStatus, destStatus dbv1.SchemaMigrationStatusPhase) error
	GetVersionStatus(ctx context.Context, version int) (dbv1.SchemaMigrationStatusPhase, bool, error)
	Close(ctx context.Context) error
}
