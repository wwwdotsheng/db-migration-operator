package version

import "testing"

// 这一组测的是【Checker 的语义】（semver 约束怎么解析），
// 而不是【当前配置的范围值】——
// 以后调整 MysqlVersionRange / PostgresVersionRange 不用改这里的断言。
func TestPostgresChecker(t *testing.T) {
	c := NewPostgresChecker()
	cases := []struct {
		name    string
		version string
		rangeS  string
		want    bool
	}{
		{"范围：落在区间内", "15.19", ">= 13, < 18", true},
		{"范围：下界", "13.0", ">= 13, < 18", true},
		{"范围：上界外", "18.0", ">= 13, < 18", false},
		{"范围：下界外", "12.9", ">= 13, < 18", false},
		{"精确等号：命中", "16.4", "= 16.4", true},
		{"精确等号：差一个 patch 就不命中", "16.5", "= 16.4", false},
		{"历史版本也能表达（PG 9.6 → 10 开始是两位数）", "9.6.24", ">= 9.6, < 10", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Check(tc.version, tc.rangeS)
			if err != nil {
				t.Fatalf("Check(%s, %q) error: %v", tc.version, tc.rangeS, err)
			}
			if got != tc.want {
				t.Errorf("Check(%s, %q) = %v, want %v", tc.version, tc.rangeS, got, tc.want)
			}
		})
	}
}

func TestMysqlChecker(t *testing.T) {
	c := NewMysqlChecker()
	cases := []struct {
		name    string
		version string
		rangeS  string
		want    bool
	}{
		{"范围：5.7 在区间内", "5.7.44", ">= 5.7, < 9", true},
		{"范围：8.0 在区间内", "8.0.36", ">= 5.7, < 9", true},
		{"范围：8.4 在区间内", "8.4.11", ">= 5.7, < 9", true},
		{"范围：9.0 超出上界", "9.1.0", ">= 5.7, < 9", false},
		{"范围：5.6 低于下界", "5.6.51", ">= 5.7, < 9", false},
		{"精确等号", "8.0.46", "= 8.0.46", true},
		{"精确等号：差一个 patch 就不命中", "8.0.45", "= 8.0.46", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Check(tc.version, tc.rangeS)
			if err != nil {
				t.Fatalf("Check(%s, %q) error: %v", tc.version, tc.rangeS, err)
			}
			if got != tc.want {
				t.Errorf("Check(%s, %q) = %v, want %v", tc.version, tc.rangeS, got, tc.want)
			}
		})
	}
}

// Normalize 是 Checker 的一半职责：把数据库返回的原始串变成可比较的版本号。
// 这组用例覆盖真实出现过的发行版后缀与 MariaDB 伪装前缀。
func TestMysqlCheckerNormalize(t *testing.T) {
	c := NewMysqlChecker()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"官方格式", "8.0.46", "8.0.46"},
		{"Ubuntu 发行版后缀", "8.0.36-0ubuntu0.22.04.1", "8.0.36"},
		{"Percona 后缀", "8.0.36-29", "8.0.36"},
		{"MariaDB 伪装前缀", "5.5.5-10.3.27-MariaDB-0+deb10u1", "10.3.27"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Normalize(tc.raw); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestPostgresCheckerNormalize(t *testing.T) {
	c := NewPostgresChecker()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"SHOW server_version 干净输出", "16.4", "16.4"},
		{"带发行版信息的长串", "PostgreSQL 16.4 (Ubuntu 16.4-1.pgdg22.04+1) on x86_64", "16.4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Normalize(tc.raw); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestProductionRangesAreValid 只断言【当前配置的约束本身能被解析】，
// 不断言它覆盖哪些版本 —— 覆盖范围是运维决策，不是这个测试的职责。
func TestProductionRangesAreValid(t *testing.T) {
	for name, tc := range map[string]struct {
		checker        Checker
		vrange, sample string
	}{
		"mysql":    {NewMysqlChecker(), MysqlVersionRange, "8.0.46"},
		"postgres": {NewPostgresChecker(), PostgresVersionRange, "15.19"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.checker.Check(tc.sample, tc.vrange); err != nil {
				t.Fatalf("当前配置的约束 %q 无法解析: %v", tc.vrange, err)
			}
		})
	}
}
