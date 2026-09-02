package version

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

type MysqlChecker struct {
}

var _ Checker = (*MysqlChecker)(nil)

func NewMysqlChecker() *MysqlChecker {
	return &MysqlChecker{}
}

func (c *MysqlChecker) Check(version, versionRange string) (bool, error) {
	v, err := semver.NewVersion(version)
	if err != nil {
		return false, fmt.Errorf("parse version %q: %w", version, err)
	}
	constraint, err := semver.NewConstraint(versionRange)
	if err != nil {
		return false, fmt.Errorf("parse constraint %q: %w", versionRange, err)
	}
	return constraint.Check(v), nil
}
