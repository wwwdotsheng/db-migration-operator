package version

import (
	"regexp"
	"strings"
)

type MysqlCleaner struct{}

var _ Cleaner = (*MysqlCleaner)(nil)

func (c *MysqlCleaner) Clean(raw string) string {
	if strings.Contains(raw, "MariaDB") {
		parts := strings.Split(raw, "-")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	re := regexp.MustCompile(`\d+\.\d+(\.\d+)?`)
	return re.FindString(raw)
}
