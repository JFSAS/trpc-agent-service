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
- [首个 Platform Operator 引导决策](decisions/0001-initial-platform-operator-bootstrap.md)：
  首次启动的数据库判定、Secret 输入、并发原子性与管理员直接创建用户。
- [部署目录结构](operations/deployment.md)：Compose、NATS、Observability 与
  Helm 部署资产的统一目录和所有权。
- [可观测性与 Telemetry](operations/observability.md)：OpenTelemetry、Metrics、
  Trace、日志、Dashboard、告警和基础设施观测的生产基线。

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
