package version

import "fmt"

// DefaultCheckerManager 是版本检查器的进程级注册表。
// 驱动在 init 阶段注册，运行期只读（Get）。
var DefaultCheckerManager = NewCheckerManager()

// DefaultParserManager 是版本获取器的进程级注册表。
// 驱动在 init 阶段注册，运行期只读（Get）。
var DefaultParserManager = NewParserManager()

// init 注册内置驱动；注册失败视为启动错误，直接终止。
func init() {
	must(DefaultCheckerManager.Register("mysql", NewMysqlChecker()))
	must(DefaultParserManager.Register("mysql", NewMysqlParser()))
}

// must 将注册错误提升为启动失败。
func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("版本组件注册失败: %v", err))
	}
}
