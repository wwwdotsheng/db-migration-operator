package version

import "fmt"

type ParserManager struct {
	m map[string]Parser
}

func NewParserManager() *ParserManager {
	return &ParserManager{m: map[string]Parser{}}
}

func (pm *ParserManager) Register(driver string, p Parser) error {
	if _, ok := pm.m[driver]; ok {
		return fmt.Errorf("driver %s already registered", driver)
	}
	pm.m[driver] = p
	return nil
}
