package version

import "fmt"

type Cleaner interface {
	Clean(raw string) string
}

type CleanerManager struct {
	m map[string]Cleaner
}

func NewCleanerManager() *CleanerManager {
	return &CleanerManager{m: map[string]Cleaner{}}
}

func (cm *CleanerManager) Register(driver string, c Cleaner) error {
	if _, ok := cm.m[driver]; ok {
		return fmt.Errorf("driver %s cleaner already registered", driver)
	}
	cm.m[driver] = c
	return nil
}

func (cm *CleanerManager) Get(driver string) (Cleaner, error) {
	c, ok := cm.m[driver]
	if !ok {
		return nil, fmt.Errorf("driver %s cleaner not registered", driver)
	}
	return c, nil
}
