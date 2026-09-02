package version

import "testing"

func TestMysqlChecker(t *testing.T) {
	c := NewMysqlChecker()
	constraint := ">= 8.0.20 < 8.1.0 || >= 8.4.0 < 9.0.0"
	cases := []struct {
		version string
		want    bool
	}{
		{"8.0.13", false}, // 低于支持下限
		{"8.0.36", true},  // 8.0 LTS 区间
		{"8.1.0", false},  // Innovation release 拒绝
		{"8.4.0", true},   // 8.4 LTS
		{"9.0.0", false},  // 超范围
	}
	for _, tc := range cases {
		got, err := c.Check(tc.version, constraint)
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
	constraint := ">= 14.0"
	cases := []struct {
		version string
		want    bool
	}{
		{"13.6", false}, // 低于支持下限
		{"14.0", true},  // 下限
		{"16.4", true},  // 两位版本号（major.minor）→ 解析为 16.4.0
		{"17.0", true},  // 未来版本（major 内）
	}
	for _, tc := range cases {
		got, err := c.Check(tc.version, constraint)
		if err != nil {
			t.Fatalf("Check(%s) error: %v", tc.version, err)
		}
		if got != tc.want {
			t.Errorf("Check(%s) = %v, want %v", tc.version, got, tc.want)
		}
	}
}
