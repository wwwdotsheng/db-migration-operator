package adapter

import "fmt"

// Factory 按 DSN 造一个 Persistent。
type Factory func(dsn string) (Persistent, error)

// Manager 按 driver 名分派到对应的 adapter 实现。
//
// 跟 ConfigManager / CheckerManager 同一套注册模式：
// 加新数据库 = 实现 Persistent + 注册一行，controller 零改动。
//
// ★ 为什么不用「switch driver」代替：
//
//	switch 的默认分支容易写成"静默回落到 mysql"——
//	结果是「用 mysql 驱动去连 postgres」这种诡异错误。
//	注册表查不到就明确报 ErrNoAdapterForDriver，失败得早且清楚。
type Manager struct {
	factories map[string]Factory
}

func NewManager() *Manager {
	return &Manager{factories: map[string]Factory{}}
}

func (m *Manager) Register(driver string, f Factory) error {
	if _, ok := m.factories[driver]; ok {
		return fmt.Errorf("adapter for driver %q already registered", driver)
	}
	m.factories[driver] = f
	return nil
}

// New 按 driver 造 adapter。driver 未注册时返回 ErrNoAdapterForDriver。
func (m *Manager) New(driver, dsn string) (Persistent, error) {
	f, ok := m.factories[driver]
	if !ok {
		return nil, fmt.Errorf("%w: driver %q", ErrNoAdapterForDriver, driver)
	}
	return f(dsn)
}

// Registered 返回已注册的 driver，用于启动时的能力自检。
func (m *Manager) Registered() []string {
	out := make([]string, 0, len(m.factories))
	for d := range m.factories {
		out = append(out, d)
	}
	return out
}

var DefaultAdapterManager = NewManager()

func init() {
	must(DefaultAdapterManager.Register("mysql", func(dsn string) (Persistent, error) {
		return NewMysqlAdapter(dsn)
	}))
	must(DefaultAdapterManager.Register("postgres", func(dsn string) (Persistent, error) {
		return NewPostgresAdapter(dsn)
	}))
}
