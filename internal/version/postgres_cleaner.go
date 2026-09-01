package version

import "regexp"

type PostgresCleaner struct{}

var _ Cleaner = (*PostgresCleaner)(nil)

func (c *PostgresCleaner) Clean(raw string) string {
	re := regexp.MustCompile(`\d+\.\d+(\.\d+)?`)
	return re.FindString(raw)
}
