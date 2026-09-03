# 下一代架构级约束

- **设计状态**：已接受
- **实现状态**：Control API 的 Identity、Admin、Tenant、Agent、Runtime Profile V1 与
  PostgreSQL 基线已实现
- **确认日期**：2026-09-03
- **适用范围**：新建的生产代码、协议、数据库迁移、镜像和部署配置

本文只记录当前已经确认、后续实现不得默默绕过的架构级约束。尚未确认的
领域细节列在“待决策事项”中，不作为既成事实。

## 约束用语

- **必须**：实现不可违反；改变它需要显式更新本文或通过 ADR 替代。
- **禁止**：明确不允许出现的依赖或行为。
- **应当**：默认选择；偏离时必须在代码评审中说明原因。

## 1. 系统与生产 Workload

### ARC-001：按运行职责拆分生产 Workload

平台必须至少区分以下运行职责：

- `control-api`：管理 Tenant、Agent、Runtime Profile、Deployment 和
  Channel Binding，并发布不可变运行配置。
- `channel-gateway`：处理 IM Webhook、身份与绑定解析、Run Admission、
  运行路由投影以及回复投递。
- `agent-worker`：消费 Run、执行 Agent、完成 Run，并产生 `ReplyIntent`。
- `local-im-provider`：提供与外部 IM Adapter 一致的本地 IM 服务端能力，供发布后
  的本地 WebUI 调试使用。
- `control-web`：Tenant 管理员与 Platform Operator 共用的 Control 管理界面，
  只连接 Control API，但必须保持租户上下文与平台管理上下文隔离。
- `local-im-web`：Local IM 调试客户端，只连接 Local IM Provider。

每个 Go Workload 必须拥有独立二进制和独立镜像。禁止在同一个生产镜像中
一次构建全部二进制，再依靠 Compose `entrypoint` 选择运行角色。

### ARC-002：Control API 不得位于运行热路径

`channel-gateway` 和 `agent-worker` 在处理已发布 Agent 的每条消息时，禁止同步
调用 Control API，也禁止读取 Control API 拥有的业务表。

Control API 暂时不可用时，已经发布并已投影的 Agent 必须仍可接收消息和执行。
Control API 与运行面只通过不可变版本、版本化事件和只读投影协作。

### ARC-003：NATS JetStream 承担异步传输

Run 分发和跨 Workload 事件传输使用 NATS JetStream，不再使用 Redis Streams。
NATS 是传输与重投机制，不是业务事实的权威存储。Redis 是否用于缓存、Session
或其他用途属于独立决策。

### ARC-004：Worker 职责必须收敛

Agent Worker 只负责：

1. 消费一个已经固定运行版本的 Run。
2. 获取执行租约或 Fence，避免过期 Worker 提交结果。
3. 使用固定的 `RuntimeManifest` 执行 Agent。
4. 持久化 Run 的终态并产生 `ReplyIntent`。
5. 在持久化结果满足幂等条件后确认消息。

Worker 禁止承担 Control Plane 逻辑、Control Outbox Relay、IM 回复投递或
Channel Binding 管理。

## 2. 仓库、编译与共享代码

### ARC-101：一个仓库、一个根 Go Module

当前架构使用一个 Git 仓库和一个根 `go.mod`。这只统一依赖版本和本地开发，
不表示不同 Workload 可以共享内部实现。

每个生产 Workload 必须位于自己的 `services/<workload>/` 下，并使用服务本地的
`internal/`。一个 Workload 禁止导入另一个 Workload 的 `internal` 包。

### ARC-102：共享区域只允许协议和生成代码

跨 Workload 共享只允许通过以下区域发生：

- `api/`：OpenAPI、AgentSpec Schema 和版本化事件定义。
- `gen/`：由 `api/` 生成的协议代码。

`api/` 和 `gen/` 中禁止放置 Repository、业务 Service、领域实体或手写的全局
`contract/common/types`。协议 DTO 不得直接充当领域实体或数据库 Row。

### ARC-103：每个二进制只有一个 Composition Root

每个 Workload 只有一个 `cmd/<workload>/main.go` 和一个进程级 `bootstrap`。
`main.go` 必须保持轻量，只负责调用 bootstrap、传递退出码和处理最外层错误。

禁止为同一进程创建多个互相竞争的启动入口，禁止让领域模块自行监听端口或
接管进程信号。

### ARC-104：目录、包和文件名必须表达明确职责

目录、Go 包和文件必须使用领域概念、Use Case 或技术职责命名。名称应当让维护者
不打开文件就能判断其主要内容，禁止用模糊名称建立可以持续堆放无关代码的容器。

- Domain 文件按领域概念命名，例如 `tenant.go`、`membership.go` 和
  `operator_grant.go`；禁止使用 `model.go` 汇集多个实体、聚合和规则。
- Application 文件按 Use Case 或明确职责命名，例如 `provision_tenant.go`、
  `manage_members.go`、`login.go`、`ports.go` 和 `errors.go`。当一个
  `service.go` 已包含多个独立 Command 或 Query 时，必须按 Use Case 拆分文件；
  拆分文件不表示创建新的业务模块或进程。
- Inbound Adapter 文件按协议职责或资源命名，例如 `session_handler.go`、
  `operator_handler.go` 和 `middleware.go`；Outbound Adapter 文件按持久化对象或
  外部能力命名，例如 `account_store.go`、`session_store.go` 和
  `deployment_publisher.go`。禁止让单个通用 `handler.go` 或 `store.go` 长期承载
  多组无关接口。
- 同类 Adapter 的包名必须在一个 Workload 内保持一致。Control API 的 HTTP 与
  PostgreSQL Adapter 分别统一使用 `httpadapter` 和 `postgresadapter`，禁止同一
  目录结构混用 `http`/`httpadapter` 或 `postgres`/`postgresadapter`。
- Go 文件名使用小写单词和下划线，测试文件必须与被测职责对应并使用
  `<name>_test.go`。禁止使用 `common.go`、`utils.go`、`helpers.go`、`types.go`、
  `misc.go` 等无法表达所有权的兜底名称。
- `doc.go` 只用于包级文档，禁止用空 `doc.go`、空 `Module` 或空 Wiring 冒充已实现
  能力。尚未开始的模块应保留在架构文档中，进入代码树后必须具有真实纵向切片。

## 3. Control API 内部模块

### ARC-201：业务模块必须保持独立所有权

Control API 至少包含以下独立模块：

- `identity`
- `admin`
- `tenant`
- `agent`
- `runtimeprofile`
- `deployment`
- `channelbinding`

`identity` 拥有本地用户账号、密码凭证和服务端 Session；`admin` 拥有
Platform Operator 授权以及平台级管理命令；`tenant` 拥有 Tenant、Membership
和 Invitation。其他目录拥有各自的 Agent 管理与发布能力。

这些目录表示 Control Context 内的业务模块，不自动等同于独立微服务。模块是否
继续拆成 Workload，必须由独立扩缩容、故障隔离或组织所有权需求驱动。

### ARC-202：业务 Adapter 跟随所属模块

每个业务模块按以下结构组织：

```text
<module>/
├── domain/
├── application/
├── adapter/
│   ├── inbound/
│   └── outbound/
└── wiring.go
```

Agent HTTP Handler 和 Agent PostgreSQL Repository 必须留在 `agent/` 内。出站
Adapter 只有在存在明确的 Application Port 时才创建；Agent V1 不构造运行中的
tRPC-Agent-Go 对象，也不保留空的 `adapter/outbound/trpcagent/`。禁止再次建立包含
所有业务 Handler 或所有业务 Repository 的全局 `adapter/` 目录。

### ARC-203：基础设施连接由服务共享

`services/control-api/internal/infra/` 只提供进程级技术能力：

- HTTP Server、Router 和通用 Middleware 基础设施。
- PostgreSQL 连接池、事务执行器和健康检查。
- NATS 连接、JetStream Client 和连接健康检查。
- 日志、Tracing 和 Metrics 基础设施。

`infra` 禁止包含 Agent SQL、Deployment Subject、Channel Binding 规则、租户角色
判断或任何业务 Repository。共享连接不等于共享业务实现。

Control API 自己拥有本地用户名、密码凭证和服务端 Session。认证包含可持久化
业务状态与用例，必须由 `identity` 业务模块拥有；禁止建立共享的
`internal/infra/auth` 包。数据库只能保存带独立 Salt 和可版本化参数的密码哈希，
禁止保存明文或可逆密码。

认证与租户授权必须分开：认证建立平台用户会话；Tenant 角色、资源
所有权和发布权限由 Tenant Membership 及对应 application/domain 规则决定。

### ARC-204：模块 Wiring 与进程 Bootstrap 分工

每个模块的 `wiring.go` 负责组装本模块的 Adapter、Application 和 Domain，向
bootstrap 暴露小型 `NewModule` 接口。

模块 Wiring：

- 可以接收 bootstrap 创建的显式依赖。
- 可以构造本模块 Repository、Use Case 和 Handler。
- 禁止读取环境变量。
- 禁止创建新的全局数据库或 NATS 连接。
- 禁止启动 HTTP Server、处理 OS Signal 或决定进程退出。

进程 bootstrap 负责读取配置、创建共享基础设施、组装各模块、注册 HTTP 路由、
启动后台任务和管理关闭顺序。禁止使用隐藏依赖的全局 Service Locator。

### ARC-205：依赖必须向内

允许的依赖方向是：

```text
cmd -> bootstrap -> module wiring -> adapter -> application -> domain
                         adapter -----------------------> infra
```

- Domain 禁止依赖 Gin、SQL Driver、NATS、tRPC-Agent-Go SDK 或配置框架。
- Application 可以依赖本模块 Domain，并在使用方一侧定义所需 Port。
- Adapter 实现 Application Port，并完成 HTTP DTO、协议 DTO、数据库 Row 与领域
  类型之间的转换。
- Bootstrap 和 Wiring 是外层组装代码，可以依赖具体实现。

### ARC-206：跨模块协作使用 Application Port

一个业务模块需要另一个模块的数据时，必须在使用方 Application 中定义最小的
查询或命令 Port，由拥有方提供实现，再由 bootstrap 显式注入。

禁止：

- 导入另一个模块的 PostgreSQL Adapter。
- 直接读写另一个模块拥有的表。
- Repository 调用另一个 Repository 来编排业务流程。
- 为同进程调用无理由增加 HTTP 或 NATS 跳转。

跨模块修改必须调用拥有方 Use Case 或发布领域事件；跨模块读可以使用只读
Snapshot 或专用 Read Model。

### ARC-207：平台管理命令必须属于 Admin 子领域

Platform Operator 发起的平台级 Command 和 Query 必须进入 `admin/application`。
HTTP 与一次性运维 CLI 只是 `admin` 的 Inbound Adapter；`cmd/control-api` 和
进程 `bootstrap` 禁止包含 Tenant 开通、初始 Owner 创建或 Operator 授权规则。

`admin` 可以通过 Application Port 编排 `identity` 与 `tenant` 的拥有方 Use Case，
但禁止直接写入账号、凭证、Tenant、Membership 或 Invitation 表。Platform
Operator 权限是平台级授权，不自动构成任何 Tenant Membership，也不得隐式获得
Tenant 内 Agent、Profile、Deployment 或 Channel Binding 的访问权。

### ARC-208：V1 账号准入采用 Admin 创建用户和 Owner 添加已有用户

V1 禁止公开注册。每个 Tenant 必须由受信任的 Platform Operator 通过 `admin`
命令创建，并原子建立初始 Tenant Owner。Platform Operator 可以创建全局账号；
Tenant Owner 只能把已有 ACTIVE 账号添加为普通 `MEMBER`。V1 不实现 Invitation
和 Owner 转移，普通成员删除也不得移除 `OWNER`。

Platform Operator、Tenant Owner 与普通 Tenant Member 共用 `identity` 的本地账号
和服务端 Session。身份认证只建立平台 User Identity；平台权限由 `admin` 校验，
Tenant 权限由 `tenant` 的 Membership 校验。

### ARC-209：首个 Platform Operator 必须使用一次性数据库引导协议

显式启用 `auto` 时，Control API 进程 bootstrap 必须在数据库迁移后、启动 HTTP
Server 前调用 `admin/application.EnsureInitialOperator`。它只能在没有任何
UserAccount 和有效 OperatorGrant 的全新数据库中创建首个 UserAccount、Password
Credential 和 OperatorGrant。

数据库状态是引导是否完成的唯一事实来源。禁止把文件系统安装锁作为权威状态，
也禁止通过公开 Setup HTTP API 或日志输出密码完成引导。V1 从
`CONTROL_BOOTSTRAP_PASSWORD` 读取初始密码，只在进程内存中把它传给 Identity
Password Hasher，不把明文写入 HTTP 响应或数据库字段。

引导必须在同一个 PostgreSQL 事务中取得固定 advisory lock，锁内重新检查状态，
原子写入账号、凭证和 Grant。首个 Grant 的 Actor 必须记录为
`SYSTEM_BOOTSTRAP`。并发副本取得锁后若发现有效 Operator，必须幂等返回 NOOP。

`auto` 模式下已有账号但没有有效 Operator 时，进程必须返回稳定的恢复错误并阻止
业务 HTTP Server 启动；禁止自动提升已有账号或重新打开初始引导。BootstrapState、
审计 Outbox 和 Break-glass 恢复属于后续切片。详细协议见
[`ADR-0001`](decisions/0001-initial-platform-operator-bootstrap.md)。

### ARC-210：Platform Operator 可以直接创建平台用户

有效 Platform Operator 可以通过 `admin/application.Service.CreateUser`
直接创建全局 UserAccount。Admin 必须通过使用方定义的 Identity Application Port
完成账号和临时密码凭证创建，禁止直接写 Identity 表。

该命令不得自动授予 OperatorGrant 或 Tenant Membership。新增账号首次登录必须先
轮换临时密码；Operator 授权和 Tenant 准入分别通过各自拥有方 Use Case 完成。

### ARC-211：Agent V1 终止于不可变 AgentVersion

`agent` 拥有 Tenant 范围内稳定的 Agent、每个 Agent 当前唯一的 AgentDraft、
AgentSpec 领域校验和不可变 AgentVersion 发布。发布结果是一个 AgentVersion，
其中包含一份经过校验、规范化并计算 Digest 的 Canonical AgentSpec；AgentSpec 与
AgentVersion 不是两个彼此独立的发布资源。

V1 使用 Expected Draft Revision 实现乐观并发，并以
`(TenantID, AgentID, SourceDraftRevision)` 保证发布幂等。Draft 发布后仍可继续编辑，
已经发布的 Version 禁止更新。V1 不引入并行 Draft、审批流或 Draft/Published/Deployed
混合状态机。

AgentSpec 只能表达逻辑行为与 Model/Tool/Knowledge Slot 需求，不绑定具体
ProfileRevision、Environment 或 Secret。Runtime Profile 绑定和 RuntimeManifest 生成属于
Deployment 发布阶段；实际 tRPC-Agent-Go 对象只能由 Worker 根据固定
RuntimeManifest 组装。详细设计见 [`control-api/agent.md`](control-api/agent.md)。

### ARC-212：Runtime Profile V1 终止于不可变 ProfileRevision

`runtimeprofile` 拥有 Tenant 范围内稳定的 Runtime Profile、当前唯一的 ProfileDraft、
RuntimeProfileSpec 校验和不可变 ProfileRevision 发布。V1 已实现 10 个 Tenant-scoped
HTTP API，覆盖 Profile 元数据、Draft 保存与校验以及 Revision 发布与读取。

RuntimeProfileSpec V1 是关闭协议，只接受四种 Resource Kind：Model 的
`openai_compatible`、Tool 的 `mcp_streamable_http`、Knowledge 的
`qdrant_openai` 和 Storage 的 `postgres_state`。Kind 的严格字段、SecretRef 位置、
数量与字符串上限由版本化 JSON Schema、Go Domain 类型和 Fixture 共同约束；禁止以
开放配置 Blob 或 SDK Option 扩展 V1。

发布幂等键是 `(TenantID, ProfileID, SourceDraftRevision)`。已经发布 Source Draft
Revision N 后，即使当前 Draft 已前进到 N+1，对 N 的延迟重试仍必须返回原
ProfileRevision；只有 N 尚未发布且不再是当前 Draft 时才返回冲突。任何向调用方、
Deployment Compiler 或其他子领域暴露完整 ProfileRevision Spec 的读取，
都必须重新 Canonicalize，并核对 Schema Version 与 Spec Digest；持久化层返回的
`spec_jsonb` 不能直接视为可信 Canonical Spec。只返回 Revision 元数据的 Summary
读取必须使用不包含 `spec_jsonb` 的专用投影，不得加载完整 Spec，也不得执行完整
Spec Canonicalization。

Runtime Profile V1 不创建 DeploymentRevision 或 RuntimeManifest，不发布 NATS/Outbox
事件，不访问 Worker，也不构造 tRPC-Agent-Go 对象。详细契约见
[`control-api/runtime-profile.md`](control-api/runtime-profile.md) 与
[`control-api/runtime-profile-spec.md`](control-api/runtime-profile-spec.md)。

## 4. Runtime Profile、Deployment 与 Channel Binding

### ARC-301：三个模块必须分开

- Runtime Profile 管理可复用、Tenant 范围内的 Model、Tool、Knowledge、Storage
  资源配置、这些资源引用的 `SecretRef` 及其不可变修订。
- Deployment 选择 Environment、AgentVersion 和 ProfileRevision，并生成不可变
  `RuntimeManifest`。
- Channel Binding 管理外部 Bot/Channel 与 DeploymentRevision 的绑定，并发布
  Gateway 所需的运行路由投影。

三个模块禁止共享一个可任意读写的配置 Blob。它们通过稳定 ID、不可变 Revision
和 Application Port 协作。Runtime Profile V1 已实现契约见
[`control-api/runtime-profile.md`](control-api/runtime-profile.md) 与
[`control-api/runtime-profile-spec.md`](control-api/runtime-profile-spec.md)。

### ARC-302：运行时只消费不可变快照

AgentDraft 和 ProfileDraft 禁止进入运行链路。Deployment 必须选择明确且不可变的
AgentVersion 与 ProfileRevision，并生成不可变的 DeploymentRevision 和 RuntimeManifest。

Gateway 只持有运行所需的最小投影，不得投影 AgentDraft、编辑器状态或原始
Profile 管理数据。Gateway 在接纳 Run 时必须固定 RuntimeManifest 的 ID、Revision
和 Digest；Worker 不负责选择 Agent 或解析当前生效配置。

## 5. PostgreSQL Outbox 与 NATS Relay

### ARC-401：业务写入与 Outbox 必须原子提交

发布 DeploymentRevision、RuntimeManifest 或 Channel Binding 变更时，业务记录
和 Outbox 记录必须在同一个 PostgreSQL 事务中提交。

Application 禁止在业务事务中直接调用 NATS 后再提交数据库，也禁止提交数据库
后以一次无持久记录的尽力调用发送 NATS。

### ARC-402：Relay 负责传输，不拥有业务决策

NATS Relay 从 PostgreSQL Outbox 领取记录，使用稳定 Event ID 幂等发布到
JetStream，并记录成功、重试和失败状态。

- 事件名称、版本、Payload 映射和 Subject 语义跟随拥有该事件的业务模块。
- NATS 连接、重连和 JetStream Client 由 `infra/nats` 提供。
- Consumer 必须按 Event ID 幂等处理重复投递。
- Outbox 清理必须晚于消费者可恢复窗口和审计保留要求。

Relay 在 V1 中可以作为同一个 Control API bootstrap 管理的后台任务；未来是否
拆成独立 Workload 不得改变 Application 或 Domain 接口。

## 6. 多租户与 Secret 约束

### ARC-501：Tenant Context 必须来自可信身份

Control API 的 Tenant Context 必须由已验证的有效本地用户会话与有效
Tenant Membership 共同建立。Gateway 的 Tenant Context 必须由已验证的
Channel Binding 建立。禁止信任请求 Body、Query 或事件 Payload 中未经
认证和授权的 `tenant_id`。

所有 Aggregate、Repository 查询、唯一约束、Outbox 事件、运行投影、日志和审计
记录都必须包含并校验 Tenant 范围。仅在表中增加 `tenant_id` 字段不构成隔离完成。

### ARC-502：只传播 SecretRef

AgentSpec、Runtime Profile、RuntimeManifest、事件、日志和 Trace 在需要引用凭据时，
只能传播 `SecretRef` 或脱敏元数据，禁止传播明文模型密钥、IM Token、数据库密码
或其他凭证。非秘密资源配置不受本条限制。

## 7. Local IM 与 Web

### ARC-601：Local IM 是生产支持能力

Local IM Provider 是发布后本地调试的正式服务，不得命名为 Demo、Simulator，
也不得放入 `tools/`。它必须通过与 Telegram、企业微信等外部 Channel 一致的
Adapter 接口接入 Gateway。

Control Web 和 Local IM Web 纳入当前重构，并作为两个独立前端工程维护。两者
必须拥有独立的源码目录、依赖清单、构建配置和部署入口，不共享应用路由、状态
容器或发布生命周期。

两条前端链路必须保持独立：

```text
Control Web -> Control API
Local IM Web -> Local IM Provider -> Channel Gateway -> Agent Worker
```

禁止让 Local IM Web 为了本地调试绕过 Local IM Provider 或 Channel Gateway
直接调用 Worker；禁止让 Control Web 进入消息执行热路径。

### ARC-602：Control Web 共用工程但隔离授权上下文

Platform Operator 与 Tenant 用户使用同一个 `control-web` 工程、构建产物和登录
会话。平台管理路由应位于独立的 `/admin` 路由空间，Tenant 管理路由必须显式携带
Tenant 标识。前端菜单或路由隐藏只能改善交互，不能代替 Control API 的服务端
授权。

Platform Operator 同时具有 Tenant Membership 时，前端必须让用户显式进入平台
或 Tenant 上下文；禁止根据 Operator 身份隐式切换 Tenant、自动提升 Tenant 权限
或复用平台命令绕过 Tenant Application 规则。

## 8. 待决策事项

以下内容尚未因本文而自动确定：

- V1 之后是否引入并行 Draft、发布审批或 Tenant Policy。
- Deployment Compiler 对已冻结 Profile Schema/Resource Kind 的 RuntimeManifest 映射，
  Environment 中 SecretRef 的解析与授权规则，以及 Worker 的 RuntimeManifest
  Schema/Adapter Kind 兼容规则。
- Channel Gateway 与 Worker 的数据库表所有权和完成 Run 的精确事务边界。
- Platform Operator 全部丢失后的 Break-glass 恢复、密码恢复、MFA、Session
  过期策略与 PostgreSQL RLS。
- 共享库与独立库的物理隔离策略。
- Control Outbox Relay 何时需要拆成独立 Workload。
- 运行监控、计费和审计投影进入 Control Web 的时间与查询模型。

这些事项必须在对应实现开始前单独讨论。禁止通过临时字段、全局 `contract`
或跨模块 SQL 调用提前固化未确认结论。

## 9. 新代码检查清单

新增代码在合并前至少回答：

1. 业务所有权属于哪个模块？
2. Domain 是否引入了框架或基础设施依赖？
3. Port 是否定义在真正的使用方？
4. 是否直接访问了其他模块或 Workload 的实现/数据表？
5. 可变配置是否在进入运行链路前冻结为不可变 Revision？
6. 数据库写入与事件发布是否使用 Transactional Outbox？
7. Tenant Context 是否来自可信来源并贯穿所有数据访问？
8. 是否只跨 Workload 共享版本化协议，而没有共享业务实现？
9. 新后台任务是否仍由唯一 bootstrap 管理生命周期？
10. 对应测试验证的是实际行为，还是只有目录、接口或容器存在？
