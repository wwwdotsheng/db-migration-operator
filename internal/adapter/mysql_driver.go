package adapter

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

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

const mysqlVersionRange = ">= 5.7, < 9"

var mysqlVersionPattern = regexp.MustCompile(`^\d+\.\d+(\.\d+)?\b`)

func (a *MysqlDriver) CheckVersion(ctx context.Context) (VersionInfo, error) {
	var raw string
	if err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&raw); err != nil {
		return VersionInfo{}, fmt.Errorf("%w: MySQL: %w", ErrServerVersionQuery, err)
	}
	return VersionInfo{Raw: raw}, checkMysqlVersion(raw)
}

func checkMysqlVersion(raw string) error {
	if strings.Contains(strings.ToLower(raw), "mariadb") {
		return fmt.Errorf("%w: MySQL driver cannot use MariaDB server %q", ErrIncompatibleServerVersion, raw)
	}
	return checkVersionRange(raw, mysqlVersionPattern.FindString(raw), mysqlVersionRange)
}

func (a *MysqlDriver) WithMigrationLock(ctx context.Context, run func(MigrationLedger) error) error {
	const lockName = "SHA2(CONCAT('db-migration-operator:', LOWER(DATABASE())), 256)"
	return withMigrationLock(ctx, a.db, "SELECT GET_LOCK("+lockName+", 0)",
		"SELECT RELEASE_LOCK("+lockName+")", false, run)
}

func (a *MysqlDriver) Close(context.Context) error { return a.db.Close() }
