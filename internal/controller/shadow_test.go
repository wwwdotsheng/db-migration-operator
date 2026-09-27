package controller

import "testing"

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
