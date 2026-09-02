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

func (cm *CheckerManager) Get(driver string) (Checker, error) {
	entry, ok := cm.m[driver]
	if !ok {
		return nil, fmt.Errorf("driver %s checker not registered", driver)
	}
	return entry.Checker, nil
}

func (cm *CheckerManager) Check(driver, version string) (bool, error) {
	entry, ok := cm.m[driver]
	if !ok {
		return false, fmt.Errorf("driver %s checker not registered", driver)
	}
	return entry.Checker.Check(version, entry.VersionRange)
}
