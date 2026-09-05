# 文档索引

本仓库的架构文档分为两类：

- [`architecture-next/`](architecture-next/README.md)：当前重构使用的规范性文档，
  用于约束后续代码、协议、数据库和部署设计。
- 根目录 [`README.md`](../README.md)：项目入口、当前状态和生产 Workload 概览。

## 当前阶段

Control API 的 Identity、Admin、Tenant、Agent、Runtime Profile 和 Deployment
Publication V1 已有实现。Deployment 将确定的 AgentVersion + ProfileRevision
经同类别同名匹配、校验和编译，生成不可变 DeploymentRevision 与最小
RuntimeManifest。V1 不引入 Environment 业务对象或用户映射表。

凭据交互采用 Profile 内直接填写，内部加密保存、公开只返回 configured/status；
Deployment 已通过 Profile Owner 的 `ProfileCredentialChecker` 检查必要凭据元数据；
无独立 Secret 产品，值不进入 Manifest、Outbox、Event、Receipt 或公开响应。
Worker 新 Attempt 经 Profile Owner 的内部认证接口批量解析是后续 Runtime 接线；
不增加协议代际或开发期 ref-only 兼容。

Deployment 的 Schema / Event、纯 Compiler、Application、PostgreSQL 原子发布、
八个 HTTP 路由、Bootstrap 和真实 PostgreSQL Integration 已落地。当前发布
停在同事务 `PENDING` Outbox；可选 Relay / JetStream 分发和
ChannelBinding / Gateway / Worker 运行链路另分阶段。

当前规范性入口：

- [下一代架构文档](architecture-next/README.md)
- [架构级约束](architecture-next/constraints.md)
- [Deployment V1 设计与实施路线](architecture-next/control-api/deployment.md)

文档中的“已接受”表示设计决策已经确认；“已实现”必须以当前代码和
测试为依据。Control Publication、Distribution 和 Runtime Execution 是三个独立完成层级。
