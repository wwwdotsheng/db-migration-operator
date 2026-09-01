package version

import "testing"

func TestMysqlCleaner(t *testing.T) {
	c := &MysqlCleaner{}
	cases := []struct {
		raw  string
		want string
	}{
		{"8.0.46", "8.0.46"},                           // 官方格式（测试库真实输出）
		{"8.0.36-0ubuntu0.22.04.1", "8.0.36"},          // Ubuntu 发行版后缀
		{"8.0.36-29", "8.0.36"},                        // Percona 后缀
		{"5.5.5-10.3.27-MariaDB-0+deb10u1", "10.3.27"}, // MariaDB 兼容前缀
	}
	for _, tc := range cases {
		if got := c.Clean(tc.raw); got != tc.want {
			t.Errorf("MysqlCleaner.Clean(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestPostgresCleaner(t *testing.T) {
	c := &PostgresCleaner{}
	cases := []struct {
		raw  string
		want string
	}{
		{"16.4", "16.4"}, // SHOW server_version 干净输出
		{"PostgreSQL 16.4 (Ubuntu 16.4-1.pgdg22.04+1) on x86_64", "16.4"},
	}
	for _, tc := range cases {
		if got := c.Clean(tc.raw); got != tc.want {
			t.Errorf("PostgresCleaner.Clean(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
