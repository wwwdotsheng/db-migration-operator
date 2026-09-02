package adapter

import (
	"strings"
	"testing"
)

func TestParseMysqlConfig(t *testing.T) {
	valid := []byte(`{
		"host": "mysql-test",
		"port": 30306,
		"user": "root",
		"database": "testdb",
		"tls": "required",
		"passwordSecretRef": {"name": "mysql-credentials", "key": "password"}
	}`)
	cfg, err := ParseMysqlConfig(valid)
	if err != nil {
		t.Fatalf("ParseMysqlConfig(valid) error: %v", err)
	}
	if cfg.Host != "mysql-test" || cfg.Port != 30306 {
		t.Errorf("ParseMysqlConfig 字段解析错误: %+v", cfg)
	}

	cases := []struct {
		name string
		raw  string
	}{
		{"非法 JSON", `{host:}`},
		{"缺 host", `{"port":30306,"user":"root","database":"testdb","passwordSecretRef":{"name":"n","key":"k"}}`},
		{"缺 passwordSecretRef", `{"host":"h","port":30306,"user":"root","database":"testdb"}`},
	}
	for _, tc := range cases {
		if _, err := ParseMysqlConfig([]byte(tc.raw)); err == nil {
			t.Errorf("%s: 期望报错，实际成功", tc.name)
		}
	}
}

func TestBuildDSN(t *testing.T) {
	cfg := &MysqlConfig{Host: "mysql-test", Port: 30306, User: "root", Database: "testdb", TLS: "required"}
	dsn, err := cfg.BuildDSN("p@ss:w/ord")
	if err != nil {
		t.Fatalf("BuildDSN error: %v", err)
	}
	for _, want := range []string{"root", "mysql-test:30306", "testdb", "tls=true"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN 缺少 %q: %s", want, dsn)
		}
	}

	cfg.TLS = "invalid"
	if _, err := cfg.BuildDSN("x"); err == nil {
		t.Error("非法 tls 模式应报错")
	}
}
