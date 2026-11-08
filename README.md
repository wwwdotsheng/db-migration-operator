# db-migration-operator

用 Kubernetes CRD 声明式管理数据库 schema 迁移。

毕业设计「数据库演进自动化运维平台」的核心组件 —— 这个仓库是它的 Operator 部分。

![状态机](docs/schemamigration-state-machine-light.png)

---

## 它解决什么问题

线上改表出事故，基本是两类：

**乱序** —— 版本 7 还没跑完就跑版本 8，DDL 在错误的结构上执行。传统做法靠人记顺序，或者把顺序写死进脚本。

**半执行** —— 一个迁移里有 5 条 ALTER，第 3 条挂了。库停在中间状态：既不是迁移前，也不是迁移后。

---

## 做法

### 顺序靠声明，不靠人记

CR 里用 `beforeVersion` 声明前置版本。控制器每轮 reconcile 先查账本里前置版本的状态，不是 `Done` 就停在 `Blocked` 轮询等待，**不硬跑**。前置版本转 `Done` 后自动继续，并发 `BlockCleared` 事件。

```yaml
spec:
  version: 8
  beforeVersion: 7      # 7 没 Done 就一直等
```

### 用账本区分重复提交与不确定结果

同一目标库按版本保存迁移内容的 SHA-256。CR 首次协调时也会记录内容摘要，后续发现内容被修改时停止执行。相同版本、相同内容且已完成时，控制器补写 CR 的 Done。相同版本但内容不同则报冲突。已有账本会按需增加 checksum 列，旧记录保留 NULL。旧 Done 可作为前置版本，直接重提该版本时需要人工核对。

控制器用数据库会话锁串行执行同一目标库的迁移。影子 Job 运行时释放锁，准备执行真实 SQL 时重新读取账本。如果期间有其他迁移完成，会基于当前账本重新预演。此机制协调使用本 Operator 的迁移，手工或其他工具执行的 DDL 需要另行协调。

真实 SQL 开始前先写 Applying，全部成功后再更新账本 Done 和 CR Done。遇到失败或中断时，Applying 记录会阻止继续执行，并将 CR 标为 Blocked，Condition 原因为 MigrationRecoveryRequired。数据库返回重复列、重复索引等错误时，控制器保留错误供人工核对。

### 凭据不落盘，且不放大 RBAC

库密码放 Secret。控制器读取时走 `APIReader` 绕开 manager 的 cache —— 否则为了缓存 Secret，得把 `secrets` 的 RBAC 权限放大到全命名空间 `list`/`watch`。现在只需要 `get`。

---

## 状态与恢复

CR 的 phase 表示 Kubernetes 侧进度。数据库账本记录真正开始执行的版本。CR 可能停在 Blocked 而账本仍是 Applying。排障时同时检查 CR、影子 Job、账本和真实库结构。

升级前先停止旧控制器，更新 SchemaMigration CRD，再启动新版本。旧控制器不会遵守新的互斥和 checksum 规则。数据库账号需要创建和升级账本的权限。迁移 SQL 应由可信操作者提交，限定在目标库的普通 DDL。手工 DDL、显式事务控制、切换数据库和修改账本不属于这套执行协议。

发现 MigrationRecoveryRequired 时，先停止该目标库的其他变更，保存账本和 schema，核对每条 SQL 的真实效果。若全部已生效，数据库管理员可在维护窗口以版本和原 checksum 为条件人工确认账本 Done。若部分生效，先人工修复到预期结构再确认。只有确认所有 SQL 均未生效、且停掉相关控制器后，才可移除未完成记录并重新提交。不要为旧 Done 记录猜测或补写未核对的 checksum。

## 快速开始

需要一个能连通的 Kubernetes 集群（本地 Kind 即可）。

```bash
# 1. 准备数据库凭据
kubectl create secret generic mysql-credentials \
  --from-literal=password='<your-db-password>'

# 2. 装 CRD 并启动控制器
make install
make run

# 3. 提交一个迁移
kubectl apply -f config/samples/db_v1_schemamigration.yaml

# 4. 看状态
kubectl get schemamigration schemamigration-sample -o jsonpath='{.status.phase}'
```

`config/samples/tests/` 下还有四个场景样例：幂等探针、失败边界、瞬时锁、多列清理。

---

## CRD

```yaml
apiVersion: db.migration.dev/v1
kind: SchemaMigration
metadata:
  name: add-remark-column
spec:
  name: test1                    # 账本里的迁移名
  changeType: Expand             # Expand | Contract | Mixed
  driver: mysql                  # 目前完整支持 mysql
  driverConfig:
    host: mysql.example.com
    port: 3306
    user: root
    database: testdb
    tls: skip-verify
    passwordSecretRef:
      name: mysql-credentials    # 密码从哪个 Secret 取
      key: password
  version: 8                     # 本次迁移的版本号，>= 1
  beforeVersion: 7               # 可选，前置版本
  applySql:
    - "alter table test_data add `remark` varchar(100) default null"
```

| 字段 | 说明 |
|------|------|
| `version` | 单调递增的版本号，最小 1 |
| `beforeVersion` | 前置依赖。账本里它的状态不是 `Done` 就停在 `Blocked` |
| `applySql` | 至少一条。按顺序执行，全部成功才算迁移完成 |
| `driverConfig` | 结构由 driver 决定，不写死进 CRD schema |
| `changeType` | 标记迁移类型，配合 Expand-Contract 模式使用 |

---

## 架构

```
cmd/main.go                        manager 入口
api/v1/                            SchemaMigration CRD 类型
internal/controller/               reconcile 状态机
internal/version/                  版本串规范化 + 版本范围判断（纯逻辑，不碰数据库）
internal/adapter/                  driverConfig 解析 / DSN 构建 / 取服务器版本 / 账本读写 / 数据库会话锁 / DDL 执行
internal/resolver/                 Secret 读取（走 APIReader，绕开缓存）
```

**加一种数据库不用改控制器。** 版本检测：adapter 负责取原始版本串，version 包负责规范化与范围判断，两者都按 driver 名注册：

```go
DefaultCheckerManager.Register("mysql",    NewMysqlChecker(),    MysqlVersionRange)
DefaultCheckerManager.Register("postgres", NewPostgresChecker(), PostgresVersionRange)
// adapter 的注册同理：DefaultAdapterManager.Register("mysql", NewMysqlDriver)
```

---

## 设计取舍

**为什么 `driverConfig` 用 `runtime.RawExtension` 而不是写进 CRD schema？**
不同数据库的连接参数结构差别大（MySQL 有 TLS 模式、PostgreSQL 有 sslmode 和 search_path）。写进 schema 就要为每种 driver 加一堆可选字段，加一种库改一次 CRD。现在由 driver 自己的 ConfigFactory 解析，CRD schema 保持稳定。

**为什么 `Blocked` 要轮询而不是 watch？**
被依赖的版本状态记在数据库账本表里，不在 Kubernetes 里，没法 watch。轮询间隔 10 秒 —— 迁移是低频操作，这个开销可以接受。

**哪些情况会重新协调？**
缺少 Secret、依赖版本未完成、同库锁被占用、影子 Job 未结束时，控制器会等待后再次检查。真实 SQL 开始后的失败需要先核对账本和实际结构。当前连接准备失败会记录 Failed，修正配置后可以重新提交 CR。

---

## 已知限制

**`Failed` 是终态，改对 spec 也不会自动重试。** CR 进了 `Failed` 之后 `reconcile` 直接返回，即使把 spec 改对也起不来，修正原因后可新建 CR。这是有意的（不让自动重试掩盖真问题），但缺一个「条件变化后自动拉回」的机制。

**PostgreSQL 验证范围。** 已在隔离的 PostgreSQL 16 测试库验证数据库会话互斥、正常迁移，以及影子结构导入失败时不执行真实 DDL。其他版本和主从拓扑尚未纳入本次验收。

**旧账本缺少内容身份。** 旧记录无法自动确认原始 SQL，升级后首次处理前应检查是否有遗留 Applying 或 Failed 记录。

**没做多副本 leader election 的故障演练。** 只在单机 Kind 和自建 K8s 上验证过单副本运行。

---

## 开发

```bash
make test        # gofmt + vet + envtest 上的单元测试
make test-e2e    # 需要本地 Kind 集群
make lint        # golangci-lint（含 logcheck 自定义插件）
make manifests   # 重新生成 CRD / RBAC
make build       # 编译 manager 二进制
make docker-build docker-push IMG=<your-registry>/db-migration-operator:tag
```

测试分三层：

- `internal/version/*_test.go` — 版本检测的表驱动测试，纯函数，不依赖集群
- `internal/adapter/mysql_config_test.go` — DSN 构建，覆盖 TLS 模式和特殊字符转义
- `internal/controller/*_test.go` — envtest 起真 apiserver，覆盖 CRD 校验、非法 `changeType` 的失败边界、终态幂等
- `test/e2e/` — Kind 集群上的端到端（需要 Docker）

---

## License

Apache License 2.0
