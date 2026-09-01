package version

import "fmt"

type CheckerManager struct {
	m map[string]Checker
}

func NewCheckerManager() *CheckerManager {
	return &CheckerManager{m: map[string]Checker{}}
}

func (cm *CheckerManager) Register(driver string, check Checker) error {
	if _, exists := cm.m[driver]; exists {
		return fmt.Errorf("driver %s checker already exists", driver)
	}
	cm.m[driver] = check
	return nil
}
