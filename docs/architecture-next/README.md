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
  DeploymentRevision + 最小 RuntimeManifest。具体协议与实施计划仍属待实现设计，
  不计入已实现 API。
- [Channel Gateway 技术栈、代码结构与部署设计](channel-gateway/README.md)：Go Gateway、
  进程内公开 Go 企微库方向、持久接纳/执行所有权与完整部署设计；已有入站切片、连续积压门禁、公开企微 P0 与 Connection/企微入站装配；Delivery Final 与可组合 Runner 已有代码，默认 App 仅新增 Maintenance；Final 生产入口与真实 Control/Worker 仍待交付，本轮最终验收见实施状态 §11。
- [Channel Gateway 实施状态](channel-gateway/implementation-status.md)：保留各切片历史验收，当前 Runtime 范围及最终验收集中于 §11。
- [Delivery Runtime V1](channel-gateway/delivery-runtime-v1.md)：独立维护、有界 Runner/PG RuntimePorts、LocalOwner 和默认只维护的生产边界。
- [Channel Gateway 四 Module 入门说明](channel-gateway/module-introduction.md)：先理解四类事实和调用关系，再读详细规范。
- [Channel Gateway 四个业务 Module](channel-gateway/module-boundaries.md)：解释单一 Gateway
  Workload 内 routing、admission、connection、delivery 的职责追踪、目录分工、深接口、事务 seam 和故障时序。
- [Channel Gateway 设计复审](channel-gateway/design-review.md)：记录本轮已修正问题、仍需冻结
  的 D0 决策、历史 `origin/main` 同步与当前本地 main 分叉，以及实施门禁。
- [公开 Go Connector 设计](channel-gateway/public-go-connector.md)：`platform/im/wecom` 的
  包职责、导入方式、生命周期、ACK 相关性和故障测试；不独立部署。
- [Channel Gateway：首批 IM 行为与 SDK 调研](channel-gateway/im-channel-sdk-semantics.md)：
  Telegram 与企业微信智能机器人接入语义、固定 SDK 源码和实验；接入契约为草案，
  已有 Telegram 私聊实测，不代表 Gateway 生产链路已实现。
- [首个 Platform Operator 引导决策](decisions/0001-initial-platform-operator-bootstrap.md)：
  首次启动的数据库判定、Secret 输入、并发原子性与管理员直接创建用户。
- [部署目录结构](operations/deployment.md)：Compose、NATS、Observability 与
  Helm 部署资产的统一目录和所有权。
- [可观测性与 Telemetry](operations/observability.md)：OpenTelemetry、Metrics、
  Trace、日志、Dashboard、告警和基础设施观测的生产基线。

## 当前后续路线

Deployment 的用户级边界已经接受，业务代码尚未实现。后续按以下顺序交付可验证纵切：

1. Profile 凭据实现与专属文档以重构基线 `bf107766` 为准；本地 main 已分叉，
   不把历史同步视为当前分支整合完成。复用现有
   `CheckUsable`、`ResolveForAttempt` 与可选 runtime HTTP Adapter。真实 Run/Attempt
   授权拥有方、可信 Workload 认证、默认解析路由与 Worker 接线仍待实现。
2. 冻结 Deployment Input、RuntimeManifest、Publication Event、稳定诊断和
   PlatformExecutionContract；无 Environment 前置，也不新增用户映射表。
3. 实现纯同名匹配 Compiler、Storage 角色选择、最小闭包及节点工具分配。
4. 实现授权、ProfileCredentialChecker 元数据检查、并发控制与幂等 Application。
5. 实现 PostgreSQL 原子发布与 Outbox、HTTP/OpenAPI、真实数据库和契约测试；
   文档中的规划路由不得混入当前已实现 API 清单。
6. 按开发波次接入可选 Relay，再独立完成 ChannelBinding 生效版本切换与真实 Control
   route publisher。Gateway 已有运行投影/Telegram 入站代码，已有 Connection/企微入站，仍需与真实 Worker
   固定 Manifest 执行、Delivery/原回复关联衔接；新 Attempt 的批量凭据取值依赖 Control API
   内的 Profile Owner 接口，已解析 Attempt 复用固定集合，不逐调用查控制面。

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
