package version

import "regexp"

type PostgresChecker struct{}

var _ Checker = (*PostgresChecker)(nil)

func NewPostgresChecker() *PostgresChecker {
	return &PostgresChecker{}
}

var postgresVersionPattern = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// Normalize 从 SHOW server_version 的输出里提取版本号：
//
//	16.4                                                    → 16.4
//	PostgreSQL 16.4 (Ubuntu 16.4-1.pgdg22.04+1) on x86_64   → 16.4
func (c *PostgresChecker) Normalize(raw string) string {
	return postgresVersionPattern.FindString(raw)
}

func (c *PostgresChecker) Check(normalizedVersion, versionRange string) (bool, error) {
	return semverCheck(normalizedVersion, versionRange)
}
