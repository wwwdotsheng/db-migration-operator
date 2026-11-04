package version

import "fmt"

// CheckerEntry 绑定驱动对应的检查器与版本范围。
type CheckerEntry struct {
	Checker      Checker
	VersionRange string
}

type CheckerManager struct {
	m map[string]CheckerEntry
}

func NewCheckerManager() *CheckerManager {
	return &CheckerManager{m: map[string]CheckerEntry{}}
}

func (cm *CheckerManager) Register(driver string, check Checker, versionRange string) error {
	if _, exists := cm.m[driver]; exists {
		return fmt.Errorf("driver %s checker already exists", driver)
	}
	cm.m[driver] = CheckerEntry{Checker: check, VersionRange: versionRange}
	return nil
}

// Check 先规范化、再判断，并返回规范化后的版本号。
//
// 返回 normalized 是为了让调用方把"数据库实际版本"直接写进错误信息和日志
// （例如 database version 8.0.36 does not satisfy ...）。
func (cm *CheckerManager) Check(driver, rawVersion string) (normalized string, ok bool, err error) {
	entry, exists := cm.m[driver]
	if !exists {
		return "", false, fmt.Errorf("driver %s checker not registered", driver)
	}
	normalized = entry.Checker.Normalize(rawVersion)
	ok, err = entry.Checker.Check(normalized, entry.VersionRange)
	return normalized, ok, err
}

// DefaultCheckerManager 是包级单例，供 controller 直接使用。
var DefaultCheckerManager = NewCheckerManager()

func init() {
	must(DefaultCheckerManager.Register("mysql", NewMysqlChecker(), MysqlVersionRange))
	must(DefaultCheckerManager.Register("postgres", NewPostgresChecker(), PostgresVersionRange))
}

func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("版本组件注册失败: %v", err))
	}
}
