package adapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type MysqlDriver struct {
	db *sql.DB
}

func NewMysqlDriver(dsn string) (*MysqlDriver, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL connection: %w", err)
	}
	return &MysqlDriver{
		db: db,
	}, nil
}

var _ Driver = (*MysqlDriver)(nil)

// GetServerVersion 返回 MySQL 的原始版本串（例如 "8.0.36-0ubuntu0.22.04.1"）。
// 不做清洗：发行版后缀、MariaDB 伪装前缀由 internal/version 的 Checker 处理。
func (adapter *MysqlDriver) GetServerVersion(ctx context.Context) (string, error) {
	var raw string
	if err := adapter.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&raw); err != nil {
		return "", fmt.Errorf("query version: %w", err)
	}
	return raw, nil
}

func (adapter *MysqlDriver) Init(ctx context.Context) error {
	if _, err := adapter.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations
(
    id              int auto_increment primary key comment 'ID',
    name            varchar(50) not null comment '版本名称',
    version         int         not null unique comment '版本编号',
    status          varchar(30) not null comment '执行状态'
);`); err != nil {
		return fmt.Errorf("create schema_migrations table failed: %v", err)
	}
	return nil
}

func (adapter *MysqlDriver) GetMigrationStatus(ctx context.Context, version int) (dbv1.SchemaMigrationStatusPhase, bool, error) {
	var resStatus dbv1.SchemaMigrationStatusPhase
	err := adapter.db.QueryRowContext(ctx,
		`SELECT status FROM schema_migrations WHERE version = ?`, version).Scan(&resStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get migration status error: %v", err)
	}
	return resStatus, true, nil
}

func (adapter *MysqlDriver) GetLatestVersion(ctx context.Context) (int, error) {
	var v int
	// ★ COALESCE 不能省：没有已完成版本时 MAX() 返回一行 NULL 而不是 NoRows，
	//   而 Scan 到 int 会因为 "converting NULL to int is unsupported" 报错。
	//   （原来只处理了 ErrNoRows，所以空表时会失败。）
	err := adapter.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations WHERE status = ?;`,
		dbv1.PhaseDone).
		Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("get latest version sql error：%v", err)
	}
	return v, nil
}

func (adapter *MysqlDriver) InsertNewVersion(ctx context.Context, name string, version int, status dbv1.SchemaMigrationStatusPhase) error {
	if _, err := adapter.db.ExecContext(ctx,
		`INSERT INTO schema_migrations (name, version, status) VALUE (?, ?, ?)`,
		name,
		version,
		status,
		//dbv1.SchemaMigrationStatusPhaseApplying,
	); err != nil {
		return fmt.Errorf("insert new schema_migrations failed: %v", err)
	}
	return nil
}

func (adapter *MysqlDriver) ExecSql(ctx context.Context, queryArr []string) (int, error) {
	logger := logf.FromContext(ctx)
	runLines := 0
	for i, query := range queryArr {
		if _, err := adapter.db.ExecContext(ctx, query); err != nil {
			if mysqlErr, ok := errors.AsType[*mysql.MySQLError](err); ok && isIdempotentErrno(mysqlErr.Number) {
				logger.Info("DDL skipped as idempotent",
					"index", i+1,
					"ddl", query,
					"errno", mysqlErr.Number,
					"reason", mysqlErr.Message)
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

func isIdempotentErrno(errno uint16) bool {
	switch errno {
	case 1060, 1061, 1091:
		return true
	}
	return false
}

func (adapter *MysqlDriver) UpdateVersion(ctx context.Context, version int, prevStatus, destStatus dbv1.SchemaMigrationStatusPhase) error {
	res, err := adapter.db.ExecContext(ctx,
		`UPDATE schema_migrations SET status = ? WHERE status = ? AND version = ?;`,
		destStatus,
		prevStatus,
		version,
	)
	if err != nil {
		return fmt.Errorf("update version from schema_migrations table error：%v", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var current dbv1.SchemaMigrationStatusPhase
		_ = adapter.db.QueryRowContext(ctx, "SELECT status FROM schema_migrations WHERE version=?", version).Scan(&current)
		return fmt.Errorf("%w: version=%d expected=%q current=%q", ErrVersionConflict, version, prevStatus, current)
	}
	return nil
}

func (adapter *MysqlDriver) GetVersionStatus(ctx context.Context, version int) (dbv1.SchemaMigrationStatusPhase, bool, error) {
	var resStatus dbv1.SchemaMigrationStatusPhase
	err := adapter.db.QueryRowContext(ctx, `SELECT status FROM schema_migrations WHERE version = ?;`, version).
		Scan(&resStatus)
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

func (adapter *MysqlDriver) Close(ctx context.Context) error {
	if err := adapter.db.Close(); err != nil {
		return fmt.Errorf("close db error: %v", err)
	}
	return nil
}
