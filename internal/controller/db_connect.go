package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	"github.com/wwwdotsheng/db-migration-operator/internal/version"
)

// prepareDatabase 完成迁移执行前的连接准备：
// 解析 driverConfig（注册表按 driver 分发）→ 读取 Secret 凭据 → 构建 DSN → 版本校验闸门。
// 返回的 dsn 供迁移流程复用。
func (r *SchemaMigrationReconciler) prepareDatabase(ctx context.Context, req ctrl.Request, migration *dbv1.SchemaMigration) (string, error) {
	logger := logf.FromContext(ctx)

	cfg, err := adapter.DefaultConfigManager.Parse(migration.Spec.Driver, migration.Spec.DriverConfig.Raw)
	if err != nil {
		logger.Error(err, "Failed to parse driver config", "driver", migration.Spec.Driver)
		return "", err
	}

	var secret corev1.Secret
	if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: cfg.SecretRef().Name}, &secret); err != nil {
		err = fmt.Errorf("read secret %s: %w", cfg.SecretRef().Name, err)
		logger.Error(err, "Failed to read secret", "secret", cfg.SecretRef().Name)
		return "", err
	}
	password, ok := secret.Data[cfg.SecretRef().Key]
	if !ok {
		err = fmt.Errorf("secret %s missing key %s", cfg.SecretRef().Name, cfg.SecretRef().Key)
		logger.Error(err, "Secret missing key", "secret", cfg.SecretRef().Name, "key", cfg.SecretRef().Key)
		return "", err
	}

	dsn, err := cfg.BuildDSN(string(password))
	if err != nil {
		logger.Error(err, "Failed to build DSN")
		return "", err
	}

	parser, err := version.DefaultParserManager.New(migration.Spec.Driver, dsn)
	if err != nil {
		logger.Error(err, "Failed to create version parser", "driver", migration.Spec.Driver)
		return "", err
	}
	v, err := parser.GetVersion(ctx)
	if err != nil {
		logger.Error(err, "Failed to query database version", "driver", migration.Spec.Driver)
		return "", err
	}
	ok, err = version.DefaultCheckerManager.Check(migration.Spec.Driver, v)
	if err != nil {
		logger.Error(err, "Failed to check version constraint", "driver", migration.Spec.Driver, "version", v)
		return "", err
	}
	if !ok {
		err = fmt.Errorf("database version %s does not satisfy %s constraint", v, migration.Spec.Driver)
		logger.Error(err, "Database version rejected", "driver", migration.Spec.Driver, "version", v)
		return "", err
	}
	return dsn, nil
}
