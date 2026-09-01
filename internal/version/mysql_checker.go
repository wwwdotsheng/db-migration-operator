package version

type MysqlChecker struct {
}

var _ Checker = (*MysqlChecker)(nil)

func NewMysqlChecker() *MysqlChecker {
	return &MysqlChecker{}
}

func (c *MysqlChecker) Check(version, versionRange string) (bool, error) {
	return false, nil
}
