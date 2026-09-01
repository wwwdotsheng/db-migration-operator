package adapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	_ "github.com/go-sql-driver/mysql"
)

type MysqlAdapter struct {
	db *sql.DB
}

// NewMysqlAdapter TODO 问题在于这个NewMysqlAdapter的DSN不是固定的呢？假如有多个CRD部署的话，那么这个DSN属于什么呢？每一个DSN去New一个吗？好像也可以？
func NewMysqlAdapter(dsn string) (*MysqlAdapter, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL connection: %w", err)
	}
	return &MysqlAdapter{
		db: db,
	}, nil
}

var _ Persistent = (*MysqlAdapter)(nil)

func (adapter *MysqlAdapter) Init(ctx context.Context) error {
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

func (adapter *MysqlAdapter) GetDriverVersion() {
}

func (adapter *MysqlAdapter) GetLatestVersion(ctx context.Context) (int, error) {
	var v int
	err := adapter.db.QueryRowContext(ctx,
		`SELECT MAX(version) AS "version" FROM schema_migrations WHERE status = ?;`,
		dbv1.SchemaMigrationStatusPhaseDone).
		Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get latest version sql error：%v", err)
	}
	return v, nil
}

func (adapter *MysqlAdapter) InsertNewVersion(ctx context.Context, name string, version int, status dbv1.SchemaMigrationStatusPhase) error {
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

func (adapter *MysqlAdapter) ExecSql(ctx context.Context, query string) error {
	if _, err := adapter.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("exec sql error: %v", err)
	}
	return nil
}

func (adapter *MysqlAdapter) UpdateVersion(ctx context.Context, version int, prevStatus, destStatus dbv1.SchemaMigrationStatusPhase) error {
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
		return fmt.Errorf("update version from schema_migrations table failed, no rows affected, prevStatus: %s, destStatus: %s", prevStatus, destStatus)
	}
	return nil
}

func (adapter *MysqlAdapter) Close(ctx context.Context) error {
	if err := adapter.db.Close(); err != nil {
		return fmt.Errorf("close db error: %v", err)
	}
	return nil
}
