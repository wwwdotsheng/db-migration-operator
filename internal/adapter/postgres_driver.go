package adapter

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

type PostgresDriver struct{ db *sql.DB }

func NewPostgresDriver(dsn string) (*PostgresDriver, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL connection: %w", err)
	}
	return &PostgresDriver{db: db}, nil
}

var _ Driver = (*PostgresDriver)(nil)

func (a *PostgresDriver) GetServerVersion(ctx context.Context) (string, error) {
	var raw string
	err := a.db.QueryRowContext(ctx, "SHOW server_version").Scan(&raw)
	return raw, err
}

func (a *PostgresDriver) WithMigrationLock(ctx context.Context, run func(MigrationLedger) error) error {
	// PostgreSQL advisory locks are scoped to the current database.
	return withMigrationLock(ctx, a.db, "SELECT pg_try_advisory_lock(620794813321)",
		"SELECT pg_advisory_unlock(620794813321)", true, run)
}

func (a *PostgresDriver) Close(context.Context) error { return a.db.Close() }
