package adapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// PostgresAdapter 是 Persistent 的 PostgreSQL 实现。
//
// 跟 MysqlAdapter 的区别（三处）：
//  1. 建表语法：serial 而不是 auto_increment；PG 不支持列内联 comment
//  2. 占位符：$1 $2 $3 而不是 ?
//  3. 幂等错误码：SQLSTATE 字符串而不是 errno
type PostgresAdapter struct {
	db *sql.DB
}

func NewPostgresAdapter(dsn string) (*PostgresAdapter, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL connection: %w", err)
	}
	return &PostgresAdapter{db: db}, nil
}

var _ Persistent = (*PostgresAdapter)(nil)

func (a *PostgresAdapter) Init(ctx context.Context) error {
	// ★ serial 而不是 auto_increment
	//   ★ PG 也不支持列内联 comment（要单独 COMMENT ON），所以这里没写注释
	if _, err := a.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations
(
    id      serial primary key,
    name    varchar(50) not null,
    version int         not null unique,
    status  varchar(30) not null
);`); err != nil {
		return fmt.Errorf("create schema_migrations table failed: %v", err)
	}
	return nil
}

func (a *PostgresAdapter) GetMigrationStatus(ctx context.Context, version int) (dbv1.SchemaMigrationStatusPhase, bool, error) {
	var resStatus dbv1.SchemaMigrationStatusPhase
	err := a.db.QueryRowContext(ctx,
		`SELECT status FROM schema_migrations WHERE version = $1`, version).Scan(&resStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get migration status error: %v", err)
	}
	return resStatus, true, nil
}

func (a *PostgresAdapter) GetLatestVersion(ctx context.Context) (int, error) {
	var v int
	// ★ COALESCE 不能省：没有已完成版本时 MAX() 返回 NULL，
	//   而 Scan 到 int 会因为 "converting NULL to int is unsupported" 报错。
	//   （MySQL 那边同样的问题，那边用的是裸 MAX()。）
	err := a.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations WHERE status = $1;`,
		dbv1.PhaseDone).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("get latest version sql error: %v", err)
	}
	return v, nil
}

func (a *PostgresAdapter) InsertNewVersion(ctx context.Context, name string, version int, status dbv1.SchemaMigrationStatusPhase) error {
	if _, err := a.db.ExecContext(ctx,
		`INSERT INTO schema_migrations (name, version, status) VALUES ($1, $2, $3)`,
		name, version, status,
	); err != nil {
		return fmt.Errorf("insert new schema_migrations failed: %v", err)
	}
	return nil
}

func (a *PostgresAdapter) ExecSql(ctx context.Context, queryArr []string) (int, error) {
	logger := logf.FromContext(ctx)
	runLines := 0
	for i, query := range queryArr {
		if _, err := a.db.ExecContext(ctx, query); err != nil {
			if pgErr, ok := errors.AsType[*pq.Error](err); ok && isIdempotentSQLState(pgErr.Code) {
				logger.Info("DDL skipped as idempotent",
					"index", i+1,
					"ddl", query,
					"sqlstate", string(pgErr.Code),
					"reason", pgErr.Message)
				runLines++
				continue
			}
			logger.Error(err, "Exec SQL failed",
				"index", i+1,
				"ddl", query,
				"appliedLines", runLines)
			return runLines, fmt.Errorf("exec sql %d failed: %w (applied %d lines)", i+1, err, runLines)
		}
		runLines++
	}
	return runLines, nil
}

// isIdempotentSQLState 判断一个 SQLSTATE 是否代表「目标已经达成」。
//
// 这套语义跟 MySQL 那边（1060/1061/1091）一一对应：
//
//	42701 duplicate_column     ← 1060 重复列
//	42P07 duplicate_table      ← 1050 表已存在
//	42710 duplicate_object     ← 1061 重复索引
//	42703 undefined_column     ← 1091 要删的东西不存在
//	42P01 undefined_table      ← 1091 同上（表）
func isIdempotentSQLState(code pq.ErrorCode) bool {
	switch code {
	case "42701", "42P07", "42710", "42703", "42P01":
		return true
	}
	return false
}

func (a *PostgresAdapter) UpdateVersion(ctx context.Context, version int, prevStatus, destStatus dbv1.SchemaMigrationStatusPhase) error {
	res, err := a.db.ExecContext(ctx,
		`UPDATE schema_migrations SET status = $1 WHERE status = $2 AND version = $3;`,
		destStatus, prevStatus, version,
	)
	if err != nil {
		return fmt.Errorf("update version from schema_migrations table error: %v", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var current dbv1.SchemaMigrationStatusPhase
		_ = a.db.QueryRowContext(ctx,
			`SELECT status FROM schema_migrations WHERE version = $1`, version).Scan(&current)
		return fmt.Errorf("%w: version=%d expected=%q current=%q", ErrVersionConflict, version, prevStatus, current)
	}
	return nil
}

func (a *PostgresAdapter) GetVersionStatus(ctx context.Context, version int) (dbv1.SchemaMigrationStatusPhase, bool, error) {
	var resStatus dbv1.SchemaMigrationStatusPhase
	err := a.db.QueryRowContext(ctx,
		`SELECT status FROM schema_migrations WHERE version = $1;`, version).Scan(&resStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return resStatus, false, fmt.Errorf("get version status error: %v", err)
	}
	if resStatus == "" {
		return resStatus, false, fmt.Errorf("get version status failed, no rows affected, version: %d", version)
	}
	return resStatus, true, nil
}

func (a *PostgresAdapter) Close(ctx context.Context) error {
	if err := a.db.Close(); err != nil {
		return fmt.Errorf("close db error: %v", err)
	}
	return nil
}
