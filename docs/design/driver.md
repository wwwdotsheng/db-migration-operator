# Driver 接口设计

本文记录拟议的五方法接口。版本检查已迁入 Driver。现有驱动仍覆盖多个主版本，Controller 会把原始版本交给 `ShadowSpec`，供 Driver 选择镜像。真实迁移仍通过 `WithMigrationLock` 将 `MigrationLedger` 交给 Controller。

## 接口

Controller 通过一个 `Driver` 接口调用数据库能力。账本读写和锁管理留在 adapter 内部，由 MySQL 和 PostgreSQL 实现共用。

`SchemaMigrationStatus` 由 Controller 独立更新。Driver 接收数据库侧请求，返回执行结果。账本中的 `Applying` 由 Driver 写入，CR 上的 `Applying` 由 Controller 写入，两者分别服务于恢复和进度展示。

```go
type Driver interface {
    CheckVersion(ctx context.Context) (VersionInfo, error)
    ShadowSpec(cfg Config, applySQL []string) (ShadowSpec, error)
    InspectMigration(ctx context.Context, req MigrationRequest) (Inspection, error)
    ApplyMigration(ctx context.Context, req MigrationRequest, previewedSnapshot string) (ApplyResult, error)
    Close(ctx context.Context) error
}
```

- `CheckVersion` 查询目标数据库的实际版本，按当前 Driver 的兼容规则进行校验。
- `ShadowSpec` 使用当前 Driver 固定的预演镜像，生成脚本和运行参数。Controller 负责创建和观察 Job。
- `InspectMigration` 读取账本，判断迁移是否可预演、已完成、等待前置版本、身份冲突或待人工核对。可以预演时返回账本快照标识。
- `ApplyMigration` 持锁复查执行条件。核对通过后写入账本 `Applying`、执行 DDL，再标记 `Done`。返回执行阶段和处理结论，由 Controller 更新 CR 状态。
- `Close` 释放 Driver 持有的数据库资源。

## 驱动注册和版本检查

`internal/adapter` 下的 `AdapterManager` 维护驱动工厂注册表，按 `spec.driver` 的值精确创建 Driver。同一种数据库可以注册多套实现，例如 `mysql-5.7` 和 `mysql-8.0`。查不到驱动名时，由 Controller 报告配置错误。

Controller 创建 Driver 后调用 `CheckVersion`，通过后才继续迁移。各 Driver 定义自己的版本获取、识别和比较规则。接口允许不同的版本格式和比较方式。版本查询失败、版本无法识别和版本不兼容应返回可区分的错误，Controller 据此更新 CR 状态。

```go
type VersionInfo struct {
    Raw string // 服务端返回的原始版本字符串
}
```

`CheckVersion` 返回 `nil` 错误表示兼容。Controller 可以把驱动名和原始版本作为结构化日志字段记录，日志中的异常控制字符应转义。

每套 Driver 的预演镜像应覆盖其声明的兼容范围。需要使用不同预演镜像的数据库版本可以注册为不同的 Driver，由用户通过 `spec.driver` 选择。


## 从预演到执行

1. Controller 调用 `InspectMigration`。发现同版本、同 checksum 已完成时，直接补写 CR 的 `Done`。发现未完成账本时，进入待核对。前置版本未完成则重新排队。
2. Controller 使用 `ShadowSpec` 创建或复用影子 Job，将预演结果与 CR 身份、迁移内容 checksum 和账本快照绑定。等待 Job 时无需持有迁移锁。
3. 预演成功后，Controller 从 API Server 重新读取 CR，核对 UID、generation 和删除状态，再调用 `ApplyMigration`。迁移请求携带名称、版本、前置版本、checksum 和 SQL。
4. Driver 在目标数据库的迁移锁内重新检查账本和迁移身份，再比较预演对应的快照。若快照变化，返回「重新预演」。若已经完成，返回「已完成」。满足全部条件后才执行 DDL。

## 执行结果与恢复

`ApplyResult` 应区分执行阶段与处理结论。阶段可包括获取锁、核对账本、写入 `Applying`、执行 DDL 和写入 `Done`。结论应能区分已完成、可重试、阻塞、需重新预演和待人工核对，底层错误用于诊断。

- 拿锁失败且确认尚未进入执行阶段时，可以重新排队。
- DDL 执行出错且结果不明，或 DDL 成功但账本完成状态未确认时，进入待核对，暂停自动执行。
- 控制器重启后依据数据库账本恢复。账本仍为 `Applying` 时先核对实际结构。账本已经为 `Done` 且身份一致时，只补写 CR 状态。

数据库账本的 `Applying` 必须在执行 DDL 前落盘。若重构后仍需在执行期间更新 CR 的 `Applying`，由 Controller 单独处理展示时机。故障恢复以数据库账本为依据。

在线 DDL 涉及独立的执行 Job 和切表结果核对，首版范围与验收见[在线 DDL 需求](../requirements/online-ddl.md)。
