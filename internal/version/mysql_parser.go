package version

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

type MysqlParser struct {
	dsn     string
	cleaner Cleaner
}

var _ Parser = (*MysqlParser)(nil)

func NewMysqlParser(dsn string) Parser {
	return &MysqlParser{dsn: dsn, cleaner: &MysqlCleaner{}}
}

func (p *MysqlParser) GetVersion(ctx context.Context) (string, error) {
	db, err := sql.Open("mysql", p.dsn)
	if err != nil {
		return "", fmt.Errorf("open mysql: %w", err)
	}
	defer db.Close()

	var raw string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&raw); err != nil {
		return "", fmt.Errorf("query version: %w", err)
	}
	return p.cleaner.Clean(raw), nil
}
