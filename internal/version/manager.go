package version

import "fmt"

// 包级单例：进程启动时注册一次，业务代码只 Get 不 Register
// （变量名用 Default 前缀，避免与类型 CheckerManager/ParserManager 同名冲突）
var (
	DefaultCheckerManager = NewCheckerManager()
	DefaultParserManager  = NewParserManager()
)

// init：注册所有内置驱动（编译期注册，失败即 panic = fail-fast）
func init() {
	must(DefaultCheckerManager.Register("mysql", NewMysqlChecker()))
	must(DefaultParserManager.Register("mysql", NewMysqlParser()))
}

func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("版本组件注册失败: %v", err))
	}
}
