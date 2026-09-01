package version

import "fmt"

var DefaultCheckerManager = NewCheckerManager()

var DefaultParserManager = NewParserManager()

var DefaultCleanerManager = NewCleanerManager()

func init() {
	must(DefaultCheckerManager.Register("mysql", NewMysqlChecker()))
	must(DefaultParserManager.Register("mysql", NewMysqlParser))
	must(DefaultCleanerManager.Register("mysql", &MysqlCleaner{}))
	must(DefaultCleanerManager.Register("postgres", &PostgresCleaner{}))
}

func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("版本组件注册失败: %v", err))
	}
}
