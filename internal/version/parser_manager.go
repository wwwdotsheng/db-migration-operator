package version

import "fmt"

type ParserFactory func(dsn string) Parser

type ParserManager struct {
	factories map[string]ParserFactory
}

func NewParserManager() *ParserManager {
	return &ParserManager{factories: map[string]ParserFactory{}}
}

func (pm *ParserManager) Register(driver string, factory ParserFactory) error {
	if _, ok := pm.factories[driver]; ok {
		return fmt.Errorf("driver %s already registered", driver)
	}
	pm.factories[driver] = factory
	return nil
}

func (pm *ParserManager) New(driver, dsn string) (Parser, error) {
	factory, ok := pm.factories[driver]
	if !ok {
		return nil, fmt.Errorf("driver %s parser not registered", driver)
	}
	return factory(dsn), nil
}
