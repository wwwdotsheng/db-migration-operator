package version

import (
	"regexp"
	"strings"
)

type MysqlCleaner struct{}

var _ Cleaner = (*MysqlCleaner)(nil)

func (c *MysqlCleaner) Clean(raw string) string {
	// MariaDB 为兼容旧客户端，VERSION() 返回 "5.5.5-10.3.27-MariaDB" 伪装前缀，
	// 真实版本号在第二段；该行为源自 MariaDB，非 MySQL 协议契约。
	if strings.Contains(raw, "MariaDB") {
		parts := strings.Split(raw, "-")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	re := regexp.MustCompile(`\d+\.\d+(\.\d+)?`)
	return re.FindString(raw)
}
