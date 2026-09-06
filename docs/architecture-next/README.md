# 下一代架构文档

本目录记录从远端基线重新建设的生产级 Agent SaaS 平台架构，并作为新代码的
规范性依据。根目录 `README.md` 只提供项目入口和实现状态概览。

## 文档状态

| 状态 | 含义 |
| --- | --- |
| 草案 | 尚在讨论，不约束实现 |
| 已接受 | 设计已经确认，新增实现必须遵守 |
| 已实现 | 已有代码和测试证据证明约束落地 |
| 已替代 | 已由后续文档或 ADR 取代 |

设计状态和实现状态必须分开记录，不能因为文档“已接受”就声称功能已经完成。

## 当前文档

- [架构级约束](constraints.md)：当前已经确认的系统边界、源码组织、依赖规则、
  运行发布和多租户约束。
- [Admin 子领域](control-api/admin.md)：Platform Operator、平台级管理命令、
  Tenant 开通与共享 Control Web 边界。
- [Identity 子领域](control-api/identity.md)：本地用户账号、密码凭证、Session 与
  认证上下文。
- [Tenant 子领域](control-api/tenant.md)：Tenant、Membership、初始 Owner 与 V1
  租户授权；Invitation 留待后续纵向切片。
- [Agent 子领域](control-api/agent.md)：Agent、当前 Draft、AgentSpec 校验与不可变
  AgentVersion 发布；运行配置绑定与执行不属于该切片。
- [AgentSpec V1](control-api/agent-spec.md)：节点协议、三层校验、稳定诊断、
  Canonicalization、Digest 与 Schema 演进规则。
- [Runtime Profile 子领域](control-api/runtime-profile.md)：已实现的可复用运行资源、单一
  Draft、11 个管理 API、强延迟幂等发布、私有凭据与 Deployment 边界。
- [RuntimeProfileSpec V1](control-api/runtime-profile-spec.md)：当前四种 Resource Kind、
  Write/Canonical/Read 分离、内部 CredentialID、校验、Digest、Schema 与 Fixture。
- [Runtime Profile 凭据](control-api/runtime-profile-credentials.md)：直接录入、加密存储、
  Draft COW、live 更新与已实现的消费边界；真实 Run/Attempt 与 Worker 接线仍待后续。
- [Deployment V1](control-api/deployment.md)：已接受无 Environment、无用户绑定表的
  简化边界；确定 AgentVersion + ProfileRevision，经同名匹配、校验和编译，生成不可变
  DeploymentRevision + 最小 RuntimeManifest。Schema / Event、Compiler、Application、
  PostgreSQL 原子发布、八个 HTTP 路由、Bootstrap 与多副本固定 Digest 启动门禁已实现；发布当前停在
  `PENDING` Outbox，不表示 Relay 分发或 Worker 执行已完成。
- [ChannelAccount V1 设计与实现](control-api/channel-account.md)：租户自助接入机器人、账户内部
  凭据、完整账户快照、Gateway 认证解析与状态回传；管理/内部 HTTP 与真实 PostgreSQL 已实现。
- [ChannelBinding V1 设计与实现](control-api/channelbinding.md)：精确部署目标、Binding CAS、
  账户 RouteGeneration、账户启停联动、事务 Outbox 及路由发布；管理 API/受限 Producer 已实现。
- [Gateway Control 接入与验收](channel-gateway/control-integration-v1.md)：可信快照/凭据 Adapter、
  动态 Telegram、启动装配与运行观测已实现；真实 Telegram 入站到 RunRequested 已验收。
  正文由 Gateway 工作树维护，本目录只保留入口；不等同 Worker 执行或完整回复。
- [产品易用性 TODO](control-api/product-usability-todo.md)：同名表单骨架、托管 Session Storage、
  模板/独立连接测试、分层状态展示和客户端技术字段处理；目标已确认，能力待设计与实现。
- [首个 Platform Operator 引导决策](decisions/0001-initial-platform-operator-bootstrap.md)：
  首次启动的数据库判定、Secret 输入、并发原子性与管理员直接创建用户。
- [部署目录结构](operations/deployment.md)：Compose、NATS、Observability 与
  Helm 部署资产的统一目录和所有权。
- [可观测性与 Telemetry](operations/observability.md)：OpenTelemetry、Metrics、
  Trace、日志、Dashboard、告警和基础设施观测的生产基线。

## 当前实现与后续路线

Deployment 的 Control Publication 已经落地并覆盖以下阶段：

1. 已实现关闭的 Deployment Input、RuntimeManifest、公开 Manifest View、
   `RuntimeManifestPublished.v1` Event、稳定诊断和 PlatformExecutionContract。
2. 已实现纯同名匹配 Compiler、Storage 角色选择、最小闭包和节点级工具分配。
3. 已实现授权、真实 `ProfileCredentialChecker`、CAS、Receipt-first 幂等
   Application 及四个 Command / 四个 Query。
4. 已实现 PostgreSQL 原子发布、不可变触发器、`PENDING` Outbox、八个
   HTTP 路由、OpenAPI、Bootstrap 和真实 PostgreSQL 集成测试。
5. Control Publication 闭环已验证；它的终点是持久化 `PENDING` Outbox，而不是消息已分发
   或 Runtime 已执行。
6. 已实现多副本 PlatformExecutionContract 固定 expected Digest：发布配置提供同一个
   `CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST`，Bootstrap 在 DB / HTTP 前比对，
   不匹配即启动失败；只读 CLI 支持按最终 Host 配置预计算，Compose 强制要求预期值。

渠道切片已按以下依赖落地；Worker 为后续阶段，不把“Binding 表完成”当作完整执行闭环：

1. 已实现 ChannelAccount：租户账户、模块私有凭据、版本/CAS与静态管理纵切。
2. 已实现 ChannelBinding：精确目标、账户有效路由与幂等事务Outbox；与账户启停一起验证原子性。
3. 已实现 Control→Gateway 扩展：完整账户快照、认证凭据读取、Gateway动态接入与运行观测。
4. Distribution：Control路由Relay已通过真实PG/JetStream回归；Gateway固定可信路由三元组。
   Manifest正文读取/执行属于Worker，路由日志恢复由Gateway验证。
5. Worker：固定Manifest、新Attempt凭据授权与真实执行，仍为独立Runtime切片。

上面1～4的Control代码与真实PG/mTLS/NATS回归已接通，公开11个操作列入OpenAPI；
Gateway对齐代码由独立任务维护。联合真实Telegram收信已通过，见[Channel联合验收](control-api/channel-acceptance.md)；
模型/Storage/Worker 执行与完整回复仍不属于本次收信验收。实现保留于两份独立工作树，
不以本页推断已经合入 main 或当前服务在线；Channel Web 与产品易用性 TODO 另行交付。
已完成的多副本Digest门禁仍是平台发布配置，不引入Environment、Secret产品或用户映射表。

发布、持久 Outbox、事件已分发、运行面实际执行分别记录状态；Control 发布成功不
代表已进入运行链路。详细阶段与验收案例以 Deployment 文档为准。

## 后续文档结构

下列目录按需创建，不预先生成空文档：

```text
docs/architecture-next/
├── README.md
├── constraints.md
├── decisions/          # 满足 ADR 条件的架构决策
├── control-api/        # Control API 领域、用例、HTTP 与持久化设计
├── channel-gateway/    # IM 接入、Run Admission 与回复投递
├── agent-worker/       # Run 消费、Agent 执行与完成语义
├── protocols/          # OpenAPI、事件和兼容性规则
└── operations/         # 构建、部署、迁移和可观测性
```

只有在已有实质讨论内容时才创建对应文件，禁止预先创建空文档。尚未确认的结论
必须明确标记为“草案”；架构约束变化时，应先更新 `constraints.md`，再修改实现或
新增 ADR。
