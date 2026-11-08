package adapter

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

type MysqlDriver struct{ db *sql.DB }

func NewMysqlDriver(dsn string) (*MysqlDriver, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open MySQL connection: %w", err)
	}
	return &MysqlDriver{db: db}, nil
}

var _ Driver = (*MysqlDriver)(nil)

func (a *MysqlDriver) GetServerVersion(ctx context.Context) (string, error) {
	var raw string
	err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&raw)
	return raw, err
}

func (a *MysqlDriver) WithMigrationLock(ctx context.Context, run func(MigrationLedger) error) error {
	const lockName = "SHA2(CONCAT('db-migration-operator:', LOWER(DATABASE())), 256)"
	return withMigrationLock(ctx, a.db, "SELECT GET_LOCK("+lockName+", 0)",
		"SELECT RELEASE_LOCK("+lockName+")", false, run)
}

func (a *MysqlDriver) Close(context.Context) error { return a.db.Close() }
