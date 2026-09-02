package version

// Checker 校验版本号是否满足版本范围约束。
type Checker interface {
	Check(version, versionRange string) (bool, error)
}

// 各驱动支持的版本范围（声明即契约：只放行 CI/环境验证过的版本）。
const (
	MysqlVersionRange    = "= 8.0.46" // 当前 K8s 部署版本，已验证
	PostgresVersionRange = "= 16.4"   // 待部署 PG 后改为实际验证版本
)
