package adapter

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// identPattern 限制会被拼进 SQL / 命令行参数的标识符：只允许 [a-z0-9_]。
// ★ 必须校验：schema 名会被拼进 pg_dump 的 -n 参数，而命令行参数拼错了
// 轻则报错、重则操作到别的 schema。标识符不能用占位符参数化。
var identPattern = regexp.MustCompile(`^[a-z0-9_]{1,63}$`)

// PostgresConfig 是 driver=postgres 的 driverConfig。
type PostgresConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Database string `json:"database"`

	// Schema 可选。留空表示【导出全部非系统 schema】。
	//
	// ★ 为什么不默认 public：
	//   漏掉一个 schema 会导致「预演通过、真库失败」—— 这是最糟的失败方向；
	//   多导一点只是慢。所以默认取全集，需要时再缩小。
	Schema string `json:"schema,omitempty"`

	// SSLMode 可选：disable（默认）/ require / verify-ca / verify-full
	SSLMode string `json:"sslMode,omitempty"`

	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`
}

var _ Config = (*PostgresConfig)(nil)

func ParsePostgresConfig(raw []byte) (*PostgresConfig, error) {
	var cfg PostgresConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	if cfg.Host == "" || cfg.Port == 0 || cfg.User == "" || cfg.Database == "" {
		return nil, fmt.Errorf("postgres config 缺少必填字段（host/port/user/database）")
	}
	if cfg.PasswordSecretRef.Name == "" || cfg.PasswordSecretRef.Key == "" {
		return nil, fmt.Errorf("postgres config 缺少 passwordSecretRef")
	}
	// ★ 校验 schema：它会被拼进命令行参数
	if cfg.Schema != "" && !identPattern.MatchString(cfg.Schema) {
		return nil, fmt.Errorf("schema 名不合法 %q（只允许 [a-z0-9_]，长度 ≤ 63）", cfg.Schema)
	}
	switch cfg.SSLMode {
	case "", "disable", "require", "verify-ca", "verify-full":
	default:
		return nil, fmt.Errorf("不支持的 sslMode %q（disable/require/verify-ca/verify-full）", cfg.SSLMode)
	}
	return &cfg, nil
}

func (c *PostgresConfig) SecretRef() corev1.SecretKeySelector {
	return c.PasswordSecretRef
}

// BuildDSN 构建 lib/pq 风格的连接串。
//
// ★ 用 net/url 而不是 fmt.Sprintf：
//
//	密码里可能有 @ : / ? # 等字符，手拼会拆坏 URL。
func (c *PostgresConfig) BuildDSN(password string) (string, error) {
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, password),
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:   "/" + c.Database,
	}
	q := url.Values{}
	q.Set("sslmode", sslMode)
	q.Set("connect_timeout", "5") // ★ 连不上时快点失败，别拖到 Job 超时
	u.RawQuery = q.Encode()
	return u.String(), nil
}
