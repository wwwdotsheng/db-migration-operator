package version

import (
	"regexp"
	"strings"
)

type MysqlChecker struct{}

var _ Checker = (*MysqlChecker)(nil)

func NewMysqlChecker() *MysqlChecker {
	return &MysqlChecker{}
}

var mysqlVersionPattern = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// Normalize 处理 MySQL 版本串里的发行版信息：
//
//	8.0.36-0ubuntu0.22.04.1         → 8.0.36   （Ubuntu 后缀）
//	8.0.36-29                       → 8.0.36   （Percona 后缀）
//	5.5.5-10.3.27-MariaDB-0+deb10u1 → 10.3.27  （MariaDB 伪装前缀）
func (c *MysqlChecker) Normalize(raw string) string {
	// MariaDB 为兼容旧客户端，VERSION() 返回 "5.5.5-10.3.27-MariaDB" 伪装前缀，
	// 真实版本号在第二段；该行为源自 MariaDB，非 MySQL 协议契约。
	if strings.Contains(raw, "MariaDB") {
		parts := strings.Split(raw, "-")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return mysqlVersionPattern.FindString(raw)
}

func (c *MysqlChecker) Check(normalizedVersion, versionRange string) (bool, error) {
	return semverCheck(normalizedVersion, versionRange)
}
