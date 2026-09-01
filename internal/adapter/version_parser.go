package adapter

import (
	"context"
	"errors"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
)

var (
	DriverVersionParserRepeatErr = errors.New("repeat version parser")
)

// VersionParserManagerInterface 这个是单例
type VersionParserManagerInterface interface {
	Register(driver string, parserInterface VersionParserInterface)
}

type VersionParserInterface interface {
	GetVersion(ctx context.Context) (string, error)
}

type DriverPluginManagerInterface interface {
}

// DriverPluginManager 这个是单例
type DriverPluginManager struct {
	DriverPlugins []DriverPlugin
}

var _ DriverPluginManagerInterface = (*DriverPluginManager)(nil)

func (dpm *DriverPluginManager) Register(plugin DriverPlugin, driver string, supportVersion []string) error {
	return nil
}

// DriverPlugin 之前的Persistent
type DriverPlugin interface {
	SupportedVersions() []string
	Init(ctx context.Context) error
	GetLatestVersion(ctx context.Context) (int, error)
	InsertNewVersion(ctx context.Context, name string, version int, status dbv1.SchemaMigrationStatusPhase) error
	ExecSql(ctx context.Context, sql string) error
	UpdateVersion(ctx context.Context, version int, prevStatus, destStatus dbv1.SchemaMigrationStatusPhase) error
	Close(ctx context.Context) error
}
