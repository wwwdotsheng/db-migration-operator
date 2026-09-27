package adapter

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// Config 是 driver 配置的统一访问面：controller 只依赖接口，
// 不依赖具体 driver 类型（MysqlConfig/PostgresConfig/...）。
// 加新 driver = 实现接口 + init 注册一行，controller 零改动。
type Config interface {
	BuildDSN(password string) (string, error)
	SecretRef() corev1.SecretKeySelector
}

// ConfigFactory 把 driverConfig（RawExtension）解析为 Config。
type ConfigFactory func(raw []byte) (Config, error)

type ConfigManager struct {
	factories map[string]ConfigFactory
}

func NewConfigManager() *ConfigManager {
	return &ConfigManager{factories: map[string]ConfigFactory{}}
}

func (cm *ConfigManager) Register(driver string, factory ConfigFactory) error {
	if _, ok := cm.factories[driver]; ok {
		return fmt.Errorf("driver %s config parser already registered", driver)
	}
	cm.factories[driver] = factory
	return nil
}

func (cm *ConfigManager) Parse(driver string, raw []byte) (Config, error) {
	factory, ok := cm.factories[driver]
	if !ok {
		return nil, fmt.Errorf("driver %s config parser not registered", driver)
	}
	return factory(raw)
}

var DefaultConfigManager = NewConfigManager()

func init() {
	must(DefaultConfigManager.Register("mysql", func(raw []byte) (Config, error) {
		return ParseMysqlConfig(raw)
	}))
	must(DefaultConfigManager.Register("postgres", func(raw []byte) (Config, error) {
		return ParsePostgresConfig(raw)
	}))
}

func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("配置组件注册失败: %v", err))
	}
}
