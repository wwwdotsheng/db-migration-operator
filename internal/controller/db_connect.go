package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	"github.com/wwwdotsheng/db-migration-operator/internal/version"
)

// prepareDatabase 完成迁移执行前的连接准备：
// 解析 driverConfig → 读取 Secret 凭据 → 构建 DSN → 版本校验闸门。
// 返回的 dsn 供迁移流程复用。
func (r *SchemaMigrationReconciler) prepareDatabase(ctx context.Context, req ctrl.Request, migration *dbv1.SchemaMigration) (string, error) {
	var cfg *adapter.MysqlConfig
	var err error
	switch migration.Spec.Driver {
	case "mysql":
		cfg, err = adapter.ParseMysqlConfig(migration.Spec.DriverConfig.Raw)
	default:
		return "", fmt.Errorf("unsupported driver %q", migration.Spec.Driver)
	}
	if err != nil {
		return "", err
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: cfg.PasswordSecretRef.Name}, &secret); err != nil {
		return "", fmt.Errorf("read secret %s: %w", cfg.PasswordSecretRef.Name, err)
	}
	password, ok := secret.Data[cfg.PasswordSecretRef.Key]
	if !ok {
		return "", fmt.Errorf("secret %s missing key %s", cfg.PasswordSecretRef.Name, cfg.PasswordSecretRef.Key)
	}

	dsn, err := cfg.BuildDSN(string(password))
	if err != nil {
		return "", err
	}

	parser, err := version.DefaultParserManager.New(migration.Spec.Driver, dsn)
	if err != nil {
		return "", err
	}
	v, err := parser.GetVersion(ctx)
	if err != nil {
		return "", err
	}
	ok, err = version.DefaultCheckerManager.Check(migration.Spec.Driver, v)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("database version %s does not satisfy %s constraint", v, migration.Spec.Driver)
	}
	return dsn, nil
}
