# 文档索引

本仓库的架构文档分为两类：

- [`architecture-next/`](architecture-next/README.md)：当前重构使用的规范性文档，
  用于约束后续代码、协议、数据库和部署设计。
- 根目录 [`README.md`](../README.md)：项目入口、当前状态和生产 Workload 概览。

## 当前阶段

Control API 的 Identity、Admin、Tenant、Agent 和 Runtime Profile V1 已有实现。
当前下一纵切是 Deployment V1 设计收敛：AgentVersion + ProfileRevision 经同类别
同名匹配、校验和编译，生成不可变 DeploymentRevision 与最小 RuntimeManifest。
V1 不引入 Environment 业务对象或用户映射表；设计边界已接受，业务实现尚未开始。

Channel Gateway 的既有重构代码基线为 `bf107766`，不是已经与分叉后的本地 main 同步。
该基线中的 Profile 内直接填写、私有
加密存储、公开 configured/status、COW/live/CAS、`CheckUsable`、`ResolveForAttempt`
与可选内部 runtime HTTP Adapter 已实现。真实 Deployment Checker、
Run/Attempt 授权拥有方、可信 Workload 认证、默认解析路由与 Worker 接线仍待完成。

后续复用已完成的 Profile 前置能力，从 Deployment 协议和静态执行契约继续，实现
Compiler、Application、PostgreSQL/HTTP/Outbox 与真实测试；可选 Relay 和
ChannelBinding 与真实 Control publisher / Worker 运行接线另分阶段。Gateway 第一切片已有
Routing、Telegram 持久入站、PG Outbox/NATS 与 CGR-27 连续积压门禁。实际工作树还已有
公开企微 P0、Connection/Supervisor、企微入站和 0005，Gateway 按可选账户配置直接装配
公开库。既有 Delivery Final/原回复关联/Sender 保留；当前新增 RuntimePorts、Maintenance/
Runner、LocalOwner、0007 与 Decode 错误分类，默认 App 只维护，最终验收见实施状态 §11。
生产 Final Consumer/发送 Runner、真实 Control publisher / Worker 与完整物理 Bot 管理仍未完成。
详见[实施状态](architecture-next/channel-gateway/implementation-status.md)。

当前规范性入口：

- [下一代架构文档](architecture-next/README.md)
- [架构级约束](architecture-next/constraints.md)
- [Deployment V1 设计与实施路线](architecture-next/control-api/deployment.md)

文档中的“已接受”表示设计决策已经确认；它不表示对应代码已经实现或通过
端到端验证。实现状态必须单独记录。

## 重构调研与设计草案

- [Channel Gateway 技术栈与部署设计](architecture-next/channel-gateway/README.md)：纯 Go、公开企微协议库、模块职责与完整部署方向；与第一切片当前状态分开。

- [Channel Gateway 实施状态](architecture-next/channel-gateway/implementation-status.md)：入站切片、持续积压门禁、公开企微 P0、分项/最终验证状态和完整目标缺口。

- [Channel Gateway 四 Module 入门说明](architecture-next/channel-gateway/module-introduction.md)：一条消息的职责流、各 Module 的拥有事实/例子、公开库与 Adapter、开发分工和部署阶段。

- [Channel Gateway 四个业务 Module](architecture-next/channel-gateway/module-boundaries.md)：解释 routing、admission、connection、delivery 为何在同一 Workload 内分开，以及消息追踪、代码/人员分工、接口、同事务校验与失败归属。

- [Channel Gateway 设计复审](architecture-next/channel-gateway/design-review.md)：列出已修订矛盾、仍阻断实现的 D0 决策，以及历史 `origin/main` 同步、当前本地 main 分叉与仍待完成的运行接线。

- [Channel Gateway：Telegram 与企业微信智能机器人](architecture-next/channel-gateway/im-channel-sdk-semantics.md)：机器人行为、SDK 接纳边界与真实实验记录；历史机器人实验不等于当前 Gateway 第一切片或完整链路已验收。

- [公开 Go Connector 设计](architecture-next/channel-gateway/public-go-connector.md)：企微库直接导入，Telegram SDK 直接引用；无独立 Connector 部署单元。
