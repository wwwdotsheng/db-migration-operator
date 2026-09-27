package controller

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestImageRepository(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"空值 → 不加前缀（用镜像原始名）", "", ""},
		{"带尾斜杠 → 原样", "harbor.internal/dockerhub/", "harbor.internal/dockerhub/"},
		{"不带尾斜杠 → 自动补上", "harbor.internal/dockerhub", "harbor.internal/dockerhub/"},
		{"只有一层", "harbor.internal", "harbor.internal/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("IMAGE_REPOSITORY", c.env)
			if got := imageRepository(); got != c.want {
				t.Errorf("imageRepository() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveImage(t *testing.T) {
	cases := []struct {
		name  string
		env   string
		image string
		want  string
	}{
		{"无前缀", "", "postgres:15-alpine", "postgres:15-alpine"},
		{"有前缀", "harbor.internal/dockerhub", "postgres:15-alpine", "harbor.internal/dockerhub/postgres:15-alpine"},
		{"有前缀带斜杠", "harbor.internal/dockerhub/", "postgres:15-alpine", "harbor.internal/dockerhub/postgres:15-alpine"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("IMAGE_REPOSITORY", c.env)
			if got := resolveImage(c.image); got != c.want {
				t.Errorf("resolveImage(%q) = %q, want %q", c.image, got, c.want)
			}
		})
	}
}

func TestSummarizeFailure(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantIn    []string
		wantNotIn []string
	}{
		{
			name: "优先取脚本标的失败行 + 紧随的数据库报错",
			raw: `[1/4] initdb + 起临时实例
[2/4] 从真库导出 schema
[3/4] 导入到临时实例
[4/4] 逐条试跑
SHADOW-VALIDATION-FAILED: 试跑失败
psql:/tmp/apply.sql:1: ERROR:  relation "orders" does not exist`,
			wantIn:    []string{"SHADOW-VALIDATION-FAILED: 试跑失败", `relation "orders" does not exist`, "kubectl logs job/"},
			wantNotIn: []string{"[1/4]", "[2/4]", "\n"},
		},
		{
			name:   "没有标记行时取最后一行非空",
			raw:    "line one\n\n\nline last\n\n",
			wantIn: []string{"line last", "kubectl logs job/"},
		},
		{
			name:   "输出为空时的兜底",
			raw:    "",
			wantIn: []string{"输出为空", "kubectl logs job/"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarizeFailure(c.raw, "shadow-abc123")
			for _, w := range c.wantIn {
				if !strings.Contains(got, w) {
					t.Errorf("结果里应包含 %q，实际: %q", w, got)
				}
			}
			for _, w := range c.wantNotIn {
				if strings.Contains(got, w) {
					t.Errorf("结果里不该出现 %q，实际: %q", w, got)
				}
			}
		})
	}
}

func TestClipKeepsUTF8Intact(t *testing.T) {
	// 每个中文 3 字节，截断点大概率落在一个字符中间
	s := strings.Repeat("中", 200)
	got := clip(s, 100)
	if !utf8.ValidString(got) {
		t.Fatalf("截断后不是合法 UTF-8: %q", got)
	}
	if len(got) > 100+len("…") {
		t.Errorf("截断后长度 %d 超过了上限", len(got))
	}
}
