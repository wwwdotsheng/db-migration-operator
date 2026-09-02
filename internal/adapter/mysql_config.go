package adapter

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	corev1 "k8s.io/api/core/v1"
)

type MysqlConfig struct {
	Host              string                   `json:"host"`
	Port              int                      `json:"port"`
	User              string                   `json:"user"`
	Database          string                   `json:"database"`
	TLS               string                   `json:"tls,omitempty"`
	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`
}

func ParseMysqlConfig(raw []byte) (*MysqlConfig, error) {
	var cfg MysqlConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse mysql config: %w", err)
	}
	if cfg.Host == "" || cfg.Port == 0 || cfg.User == "" || cfg.Database == "" {
		return nil, fmt.Errorf("mysql config 缺少必填字段（host/port/user/database）")
	}
	if cfg.PasswordSecretRef.Name == "" || cfg.PasswordSecretRef.Key == "" {
		return nil, fmt.Errorf("mysql config 缺少 passwordSecretRef")
	}
	return &cfg, nil
}

func (c *MysqlConfig) BuildDSN(password string) (string, error) {
	cfg := mysql.NewConfig()
	cfg.User = c.User
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = c.Database
	cfg.Timeout = 5 * time.Second

	switch c.TLS {
	case "", "required":
		cfg.TLSConfig = "true"
	case "skip-verify":
		cfg.TLSConfig = "skip-verify"
	default:
		return "", fmt.Errorf("unsupported tls mode %q（required/skip-verify）", c.TLS)
	}
	return cfg.FormatDSN(), nil
}
