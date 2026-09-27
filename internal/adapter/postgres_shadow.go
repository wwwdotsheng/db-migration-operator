package adapter

import (
	"encoding/base64"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

var _ ShadowValidator = (*PostgresAdapter)(nil)

// PostgresShadowImage 跑 PG 预演的镜像。
//
// ★ 必须同时含 postgres server 和 pg_dump/psql —— 预演要在同一个 Pod 里
//
//	起一个临时实例、dump 真库 schema、导入、再试跑。
//	官方 alpine 镜像两者都有。
const PostgresShadowImage = "postgres:15-alpine"

// postgresShadowScript 在预演容器里执行。
//
// 约定：
//   - 失败时把原因打到 stderr 并退出非 0 —— 不写 /dev/termination-log，
//     因为容器以非 root 跑，那个文件写不了（实测）。
//     配合 Pod 的 terminationMessagePolicy: FallbackToLogsOnError，
//     K8s 会把 stderr 尾部自动放进 Pod status 的 message。
//   - 成功时打印 SHADOW-VALIDATION-PASSED。
const postgresShadowScript = `
set -e

# ★ 必须以非 root 跑 —— initdb 拒绝 root。
#   即使 Pod 的 securityContext 已经指定了用户，这里也兜一层：
#   本地 docker run 调试时不带 securityContext 也能跑。
if [ "$(id -u)" = "0" ]; then
  exec gosu postgres "$0" "$@"
fi

export PGDATA=/tmp/pgdata
export PGHOST=/tmp
export PGUSER=postgres

fail() { echo "SHADOW-VALIDATION-FAILED: $2"; tail -5 "$1" 2>/dev/null; exit 1; }

echo "[1/4] initdb + 起临时实例"
initdb -D "$PGDATA" -U postgres >/tmp/initdb.log 2>&1 || fail /tmp/initdb.log "initdb 失败"
# ★ -l 指定日志文件，否则 pg_ctl 的失败原因无迹可寻
pg_ctl -D "$PGDATA" -l /tmp/pg.log \
  -o "-c listen_addresses=localhost -c unix_socket_directories=/tmp" -w start >/dev/null 2>&1 \
  || fail /tmp/pg.log "pg_ctl start 失败"
until pg_isready -q; do sleep 0.2; done

echo "[2/4] 从真库导出 schema"
# ★ 凭据写进 0600 的 .pgpass，不用 PGPASSWORD 环境变量 ——
#   /proc/<pid>/environ 对同 uid 的进程是可读的。
#   .pgpass 格式：host:port:db:user:password
umask 077
echo "$REAL_HOST:$REAL_PORT:$REAL_DB:$REAL_USER:$REAL_PASSWORD" > /tmp/.pgpass
export PGPASSFILE=/tmp/.pgpass
REAL_DSN="postgresql://$REAL_USER@$REAL_HOST:$REAL_PORT/$REAL_DB?sslmode=disable"
# ★ REAL_SCHEMA 为空 = 导出全部非系统 schema（默认，避免漏 schema）
if [ -n "$REAL_SCHEMA" ]; then
  pg_dump -n "$REAL_SCHEMA" --schema-only --no-owner --no-privileges "$REAL_DSN" > /tmp/schema.sql 2>/tmp/dump.log \
    || fail /tmp/dump.log "pg_dump 失败"
else
  pg_dump --schema-only --no-owner --no-privileges "$REAL_DSN" > /tmp/schema.sql 2>/tmp/dump.log \
    || fail /tmp/dump.log "pg_dump 失败"
fi

echo "[3/4] 导入到临时实例"
# 导入报警不算致命（扩展、权限之类），继续走试跑
psql -q -d postgres -f /tmp/schema.sql 2>/tmp/import.log || true

echo "[4/4] 逐条试跑"
echo "$APPLY_SQL_B64" | base64 -d > /tmp/apply.sql
psql -q -v ON_ERROR_STOP=1 -d postgres -f /tmp/apply.sql 2>/tmp/err.log \
  || fail /tmp/err.log "试跑失败"

echo "SHADOW-VALIDATION-PASSED"
`

func (a *PostgresAdapter) ShadowSpec(cfg Config, applySQL []string) (ShadowSpec, error) {
	pg, ok := cfg.(*PostgresConfig)
	if !ok {
		return ShadowSpec{}, fmt.Errorf("PostgresAdapter.ShadowSpec: 期望 *PostgresConfig，收到 %T", cfg)
	}

	// ★ SQL 用 base64 传，避免引号/换行在 shell 里被拆坏
	var buf strings.Builder
	for _, stmt := range applySQL {
		buf.WriteString(stmt)
		buf.WriteString(";\n")
	}
	sqlB64 := base64.StdEncoding.EncodeToString([]byte(buf.String()))

	return ShadowSpec{
		Image:  PostgresShadowImage,
		Script: postgresShadowScript,
		// ★ postgres:15-alpine 里 postgres 用户的 uid 是 70 —— 必须显式指定，
		//   因为这个镜像没有 USER 指令（默认 root），而 initdb 拒绝 root。
		//   脚本里还有一层 gosu 兜底，两边都写是为了「声明式可见 + 本地能调试」。
		RunAsUser: ptr(int64(70)),
		FSGroup:   ptr(int64(70)),
		Env: []corev1.EnvVar{
			{Name: "REAL_HOST", Value: pg.Host},
			{Name: "REAL_PORT", Value: fmt.Sprint(pg.Port)},
			{Name: "REAL_USER", Value: pg.User},
			{Name: "REAL_DB", Value: pg.Database},
			// ★ 空表示导出全部非系统 schema（默认，更安全：漏 schema 比多导危险）
			{Name: "REAL_SCHEMA", Value: pg.Schema},
			{Name: "APPLY_SQL_B64", Value: sqlB64},
			{
				// ★ 密码走 Secret 引用，不进 Pod spec 明文
				Name:      "REAL_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &pg.PasswordSecretRef},
			},
		},
	}, nil
}

// ptr 返回 v 的指针。
func ptr[T any](v T) *T { return &v }
