package version

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Checker 负责把数据库 adapter 返回的原始版本字符串规范化，并判断是否落在支持范围内。
//
// ★ 它同时承担「解析」与「判断」两件事：
//
//	adapter 只负责连库拿到原始字符串（I/O）；
//	规范化（去掉发行版后缀、MariaDB 伪装前缀）和范围判断都在这里完成，
//	因此 Checker 可以脱离数据库做单元测试。
type Checker interface {
	// Normalize 把数据库返回的原始版本串规范化成可比较的版本号。
	// 例如 "8.0.36-0ubuntu0.22.04.1" → "8.0.36"、
	// "5.5.5-10.3.27-MariaDB" → "10.3.27"。
	Normalize(raw string) string
	// Check 判断规范化后的版本号是否满足范围约束。
	Check(normalizedVersion, versionRange string) (bool, error)
}

// 各驱动支持的版本范围（声明即契约：只放行 CI/环境验证过的版本）。
const (
	// MysqlVersionRange 是支持并验证过的 MySQL 版本范围。
	//
	// ★ 用范围而不是 "= x.y.z"：
	//   精确等号意味着 patch 版本一变就跑不了，而补丁升级在生产里很常见。
	//   范围的下界是"我们的 SQL 语法和幂等错误码在这里成立"的最低版本。
	MysqlVersionRange = ">= 5.7, < 9"
	// PostgresVersionRange 是支持并验证过的 PostgreSQL 版本范围。
	//
	// ★ 上界 18：pg_dump 的版本必须 ≥ 服务器版本，
	//   而预演镜像按主版本选（postgres:<major>-alpine），目前覆盖 13-17。
	//   超出这个范围要先去 postgresShadowImage 里补 tag。
	PostgresVersionRange = ">= 13, < 18"
)

// semverCheck 是两个 driver 共用的范围判断逻辑。
// 留在包内而不是各自实现，避免 MySQL / PostgreSQL 两份一模一样的代码。
func semverCheck(version, versionRange string) (bool, error) {
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
