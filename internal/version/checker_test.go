package version

import "testing"

func TestMysqlChecker(t *testing.T) {
	c := NewMysqlChecker()
	cases := []struct {
		version string
		want    bool
	}{
		{"8.0.46", true},  // 精确版本（当前部署，已验证）
		{"8.0.45", false}, // 低一个 patch
		{"8.0.47", false}, // 高一个 patch
		{"8.1.0", false},  // Innovation
		{"8.4.0", false},  // 未验证的 LTS
	}
	for _, tc := range cases {
		got, err := c.Check(tc.version, MysqlVersionRange)
		if err != nil {
			t.Fatalf("Check(%s) error: %v", tc.version, err)
		}
		if got != tc.want {
			t.Errorf("Check(%s) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestPostgresChecker(t *testing.T) {
	c := NewPostgresChecker()
	cases := []struct {
		version string
		want    bool
	}{
		{"16.4", true},  // 精确版本
		{"16.3", false}, // 低一个 minor
		{"16.5", false}, // 高一个 minor
		{"14.0", false}, // 未验证的 major
	}
	for _, tc := range cases {
		got, err := c.Check(tc.version, PostgresVersionRange)
		if err != nil {
			t.Fatalf("Check(%s) error: %v", tc.version, err)
		}
		if got != tc.want {
			t.Errorf("Check(%s) = %v, want %v", tc.version, got, tc.want)
		}
	}
}
