package version

// Checker 校验版本号是否满足版本范围约束。
type Checker interface {
	Check(version, versionRange string) (bool, error)
}

// 各驱动支持的版本范围（与 CI 测试矩阵保持一致）。
const (
	MysqlVersionRange    = ">= 8.0.20 < 8.1.0 || >= 8.4.0 < 9.0.0"
	PostgresVersionRange = ">= 14.0"
)
