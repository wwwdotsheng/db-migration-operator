package version

// Checker 校验版本号是否满足版本范围约束。
type Checker interface {
	Check(version, versionRange string) (bool, error)
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
