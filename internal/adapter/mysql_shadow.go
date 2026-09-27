package adapter

import (
	"encoding/base64"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

var _ ShadowValidator = (*MysqlAdapter)(nil)

// mysqlShadowImage 按真库主版本选预演镜像。
//
// ★ 必须同时含 mysqld、mysql 客户端、mysqldump。
//
// 注：MySQL 里「schema」就是 database，一个连接只看到一个库，
// 所以没有 PG 那种「导哪个 schema」的选项 —— 要导的就是 driverConfig.database。
//
// 返回相对镜像名，仓库前缀由部署环境拼。
func mysqlShadowImage(serverVersion string) string {
	major := serverVersion
	if i := strings.IndexByte(major, '.'); i > 0 {
		major = major[:i]
	}
	switch major {
	case "5", "8":
		return "mysql:" + major
	default:
		return "mysql:8"
	}
}

// mysqlShadowScript 在预演容器里执行。
//
// 跟 PG 版的三个差异：
//  1. 临时实例靠 mysqld --initialize-insecure，不是 initdb
//  2. 客户端工具是 mysqldump / mysql，不是 pg_dump / psql
//  3. 切换用户靠 gosu / su-exec / setpriv（不同镜像里可用的不一样）
const mysqlShadowScript = `
set -e

# ★ mysqld 拒绝以 root 运行。
#   不同镜像里可用的切用户工具不一样，这里逐个探测：
#     mysql:8        → gosu
#     mariadb        → su-exec
#     其它            → setpriv（util-linux 自带）
if [ "$(id -u)" = "0" ]; then
  if command -v gosu >/dev/null 2>&1; then
    exec gosu mysql "$0" "$@"
  elif command -v su-exec >/dev/null 2>&1; then
    exec su-exec mysql "$0" "$@"
  else
    exec setpriv --reuid=mysql --regid=mysql --clear-groups "$0" "$@"
  fi
fi

DATADIR=/tmp/mysqldata
SOCK=/tmp/mysql.sock
SHADOW_DB=shadow_validation

fail() { echo "SHADOW-VALIDATION-FAILED: $2"; tail -5 "$1" 2>/dev/null; exit 1; }

echo "[1/4] 初始化并启动临时实例"
mkdir -p "$DATADIR"
mysqld --initialize-insecure --datadir="$DATADIR" >/tmp/init.log 2>&1 \
  || fail /tmp/init.log "mysqld --initialize-insecure 失败"
mysqld --datadir="$DATADIR" --socket="$SOCK" --port=13306 \
  --bind-address=127.0.0.1 >/tmp/mysqld.log 2>&1 &
for i in $(seq 1 120); do
  mysqladmin --socket="$SOCK" -u root ping >/dev/null 2>&1 && break
  sleep 0.5
done
mysqladmin --socket="$SOCK" -u root ping >/dev/null 2>&1 \
  || fail /tmp/mysqld.log "临时 mysqld 没能就绪"

echo "[2/4] 从真库导出结构"
# ★ 凭据写进 0600 的临时文件，不用 --password= 命令行参数 ——
#   命令行参数在 /proc/<pid>/cmdline 里可见，而且 mysql 客户端会警告。
#   （MYSQL_PWD 环境变量也不够好：/proc/<pid>/environ 同样可读。）
umask 077
cat > /tmp/my.cnf <<EOF
[client]
password=$REAL_PASSWORD
EOF
mysqldump --defaults-extra-file=/tmp/my.cnf \
  --no-data --skip-add-drop-table --single-transaction \
  --host="$REAL_HOST" --port="$REAL_PORT" --user="$REAL_USER" \
  "$REAL_DB" > /tmp/schema.sql 2>/tmp/dump.log \
  || fail /tmp/dump.log "mysqldump 失败"

echo "[3/4] 导入到临时实例"
mysql --socket="$SOCK" -u root -e "CREATE DATABASE $SHADOW_DB" 2>/tmp/createdb.log \
  || fail /tmp/createdb.log "建影子库失败"
# 导入报警不算致命（DEFINER、权限、GTID 之类），继续走试跑
mysql --socket="$SOCK" -u root "$SHADOW_DB" < /tmp/schema.sql 2>/tmp/import.log || true

echo "[4/4] 逐条试跑"
echo "$APPLY_SQL_B64" | base64 -d > /tmp/apply.sql
mysql --socket="$SOCK" -u root "$SHADOW_DB" < /tmp/apply.sql 2>/tmp/err.log \
  || fail /tmp/err.log "试跑失败"

echo "SHADOW-VALIDATION-PASSED"
`

func (m *MysqlAdapter) ShadowSpec(cfg Config, applySQL []string, serverVersion string) (ShadowSpec, error) {
	my, ok := cfg.(*MysqlConfig)
	if !ok {
		return ShadowSpec{}, fmt.Errorf("MysqlAdapter.ShadowSpec: 期望 *MysqlConfig，收到 %T", cfg)
	}

	// ★ SQL 用 base64 传，避免引号/换行在 shell 里被拆坏
	var buf strings.Builder
	for _, stmt := range applySQL {
		buf.WriteString(stmt)
		buf.WriteString(";\n")
	}
	sqlB64 := base64.StdEncoding.EncodeToString([]byte(buf.String()))

	return ShadowSpec{
		Image:  mysqlShadowImage(serverVersion),
		Script: mysqlShadowScript,
		// ★ mysql:8 官方镜像里 mysql 用户的 uid 是 999
		//   （PostgreSQL 的 alpine 镜像是 70 —— 这也是 driver 特定的）
		RunAsUser: ptr(int64(999)),
		FSGroup:   ptr(int64(999)),
		Env: []corev1.EnvVar{
			{Name: "REAL_HOST", Value: my.Host},
			{Name: "REAL_PORT", Value: fmt.Sprint(my.Port)},
			{Name: "REAL_USER", Value: my.User},
			{Name: "REAL_DB", Value: my.Database},
			{Name: "APPLY_SQL_B64", Value: sqlB64},
			{
				// ★ 密码走 Secret 引用，不进 Pod spec 明文
				Name:      "REAL_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &my.PasswordSecretRef},
			},
		},
	}, nil
}
