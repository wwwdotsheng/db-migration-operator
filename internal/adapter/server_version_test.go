package adapter

import (
	"errors"
	"testing"
)

func TestServerVersionCompatibility(t *testing.T) {
	cases := []struct {
		name  string
		check func(string) error
		raw   string
		want  error
	}{
		{"mysql lower bound", checkMysqlVersion, "5.7.0", nil},
		{"mysql below range", checkMysqlVersion, "5.6.51", ErrIncompatibleServerVersion},
		{"mysql upper bound", checkMysqlVersion, "9.0.0", ErrIncompatibleServerVersion},
		{"mysql 8.4", checkMysqlVersion, "8.4.11", nil},
		{"mysql ubuntu", checkMysqlVersion, "8.0.36-0ubuntu0.22.04.1", nil},
		{"mysql percona", checkMysqlVersion, "8.0.36-29", nil},
		{"mysql rejects mariadb", checkMysqlVersion, "5.5.5-10.3.27-MariaDB-0+deb10u1", ErrIncompatibleServerVersion},
		{"mysql malformed", checkMysqlVersion, "unknown build 8.0.36", ErrUnrecognizedServerVersion},
		{"postgres lower bound", checkPostgresVersion, "13.0", nil},
		{"postgres below range", checkPostgresVersion, "12.9", ErrIncompatibleServerVersion},
		{"postgres upper bound", checkPostgresVersion, "18.0", ErrIncompatibleServerVersion},
		{"postgres ubuntu", checkPostgresVersion, "16.4 (Ubuntu 16.4-1.pgdg22.04+1)", nil},
		{"postgres full version", checkPostgresVersion, "PostgreSQL 16.4 (Ubuntu 16.4-1.pgdg22.04+1) on x86_64", nil},
		{"postgres malformed", checkPostgresVersion, "unknown build 16.4", ErrUnrecognizedServerVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.check(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("check(%q) = %v, want %v", tc.raw, err, tc.want)
			}
		})
	}
}

func TestPostgresShadowImageWithRawServerVersion(t *testing.T) {
	raw := "PostgreSQL 16.4 (Ubuntu 16.4-1.pgdg22.04+1) on x86_64"
	if image := postgresShadowImage(raw); image != "postgres:16-alpine" {
		t.Fatalf("postgresShadowImage(%q) = %q, want postgres:16-alpine", raw, image)
	}
}
