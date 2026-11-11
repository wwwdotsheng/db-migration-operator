# 设计文档

Controller 管理 CR 的状态和影子 Job，Driver 提供数据库相关操作。数据库服务端版本检查与迁移账本中的前置版本检查分别处理。

- [Driver 接口](driver.md) 记录五方法重构、驱动注册、版本检查、预演快照、迁移锁和账本恢复。
- [Driver 版本兼容需求](../requirements/driver-version-compatibility.md) 记录驱动选择和版本检查的验收标准。
