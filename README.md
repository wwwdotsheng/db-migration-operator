# db-migration-operator

这是我的毕业设计项目。我用 Go 和 Kubebuilder 写了一个 Kubernetes Operator，通过 `SchemaMigration` 资源管理单实例 MySQL 和 PostgreSQL 的数据库结构变更。

这个项目主要解决两个问题。多个迁移要按依赖顺序执行。如果执行中途出错或控制器重启，下一轮协调要能判断该继续、补写状态，还是停下来等人工核对。

## 一次迁移怎么执行

1. 提交 `SchemaMigration`，声明目标库、迁移版本、可选的前置版本 `beforeVersion` 和要执行的 SQL。数据库密码从同命名空间的 Secret 读取。
2. Controller 在迁移锁内检查目标库的 `schema_migrations` 账本。前置版本未完成就等待。同版本、同 checksum 已完成时，只补写 CR 的完成状态。
3. Controller 创建影子 Job。Job 导出目标库的表结构，在临时数据库里导入结构并试跑迁移。等待 Job 的这段时间会释放迁移锁。
4. 影子 Job 成功后，Controller 重新加锁并检查账本快照。如果账本已经变化，就基于新快照重新预演。核对通过后，先向账本写入 `Applying`，再在目标库按顺序执行 SQL。
5. SQL 全部成功后，先把账本标为 `Done`，再更新 CR。如果执行结果不确定，CR 会进入 `Blocked`，原因是 `MigrationRecoveryRequired`。处理时要一起核对 CR、Job、数据库账本和实际表结构。

## 本地运行

准备 Go 1.26、Make、kubectl、可访问的 Kubernetes 集群，以及控制器和影子 Job 都能连通的测试数据库。下面用 MySQL 示例演示，测试库应先有 `test_data` 表。

先修改 [示例 CR](config/samples/db_v1_schemamigration.yaml) 中的数据库连接参数和迁移 SQL，再用当前 kubectl 命名空间创建 Secret。

```bash
kubectl create secret generic mysql-credentials --from-literal=password='<测试库密码>'
make install
make run
```

保持控制器运行，在另一个终端提交迁移并查看状态。

```bash
kubectl apply -f config/samples/db_v1_schemamigration.yaml
kubectl describe schemamigration schemamigration-sample
kubectl get jobs
```

## 端到端测试

在具备 Go、Docker、Kind、kubectl 和 Make 的 Linux 开发机上运行：

```bash
CERT_MANAGER_INSTALL_SKIP=true make test-e2e
```

测试使用独立的 `bin/e2e.kubeconfig`，用本机 Go 构建 Operator 的测试镜像，并将测试依赖镜像加载到 Kind。测试通过后删除专用集群。可通过 `KIND_CLUSTER` 指定测试集群名，通过 `KIND_NODE_IMAGE` 指定节点镜像，例如使用开发机已有的 `kindest/node:v1.32.2`。

迁移成功用例使用 PostgreSQL 16，在独立命名空间中创建临时数据库，提交新增字段的 CR，并核对影子 Job、真实列定义、迁移账本和 CR 完成状态。失败时输出 CR、Job、事件和影子容器日志，随后清理测试命名空间。

## 代码入口

- [api/v1](api/v1) 定义 CR 的字段、状态和 Condition。
- [internal/controller](internal/controller) 负责 Reconcile、影子 Job 编排和 CR 状态更新。
- [internal/adapter](internal/adapter) 负责数据库连接、版本查询、预演规格、迁移锁、账本和 SQL 执行。

## 当前范围和下一步

影子 Job 验证的是结构和 SQL 执行。真实库的锁等待、数据规模和业务请求延迟需要在单独的实验中验证。`changeType` 已有 `Expand`、`Contract` 和 `Mixed` 三种标记，在线收缩与应用发布编排仍处于设计阶段。

下一步计划把 Controller 使用的数据库操作收敛到一个 Driver 接口。五个方法、锁内复查和重启恢复方案见 [设计文档](docs/design/README.md)。
