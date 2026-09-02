package version

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

type PostgresParser struct {
	dsn     string
	cleaner Cleaner
}

var _ Parser = (*PostgresParser)(nil)

func NewPostgresParser(dsn string) Parser {
	return &PostgresParser{dsn: dsn, cleaner: &PostgresCleaner{}}
}

func (p *PostgresParser) GetVersion(ctx context.Context) (string, error) {
	db, err := sql.Open("postgres", p.dsn)
	if err != nil {
		return "", fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()

	var raw string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&raw); err != nil {
		return "", fmt.Errorf("query server_version: %w", err)
	}
	return p.cleaner.Clean(raw), nil
}
