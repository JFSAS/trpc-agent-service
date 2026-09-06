# 下一代架构级约束

- **设计状态**：已接受
- **实现状态**：Control API 的 Identity、Admin、Tenant、Agent、Runtime Profile V1 与
  PostgreSQL 基线已实现；Deployment V1 的简化边界已接受，业务实现尚未开始
- **确认日期**：2026-09-04；Profile 主线事实于 2026-09-05 对齐
- **适用范围**：新建的生产代码、协议、数据库迁移、镜像和部署配置

本文只记录当前已经确认、后续实现不得默默绕过的架构级约束。尚未确认的
领域细节列在“待决策事项”中，不作为既成事实。

## 约束用语

- **必须**：实现不可违反；改变它需要显式更新本文或通过 ADR 替代。
- **禁止**：明确不允许出现的依赖或行为。
- **应当**：默认选择；偏离时必须在代码评审中说明原因。

## 1. 系统与生产 Workload

### ARC-000：当前开发期协议可直接调整

当前尚未形成稳定对外版本。字段与凭据模型直接收敛到现有 `/v1` 和 `schema_version=v1`，
不因本次修改新增协议代际、兼容双栈或开发期旧 Reader。开发期旧数据、Digest、Receipt
无需保存，后续实现可同步调整现有 Schema、Fixture 和数据库基线并重建测试数据；不增加
仅为兼容这些旧数据的迁移或历史存储。数据库重建是显式开发操作，不由文档更新隐式执行。

这不取消目标运行模型中已发布 Revision / Manifest 的不可变性、并发控制或幂等规则。
稳定对外发布后再建立版本演进与升级迁移契约；不得把未稳定的当前实现误作历史兼容负担。

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

### ARC-002：配置运行热路径与凭据启动接口分离

`channel-gateway` 的路由选择、Run Admission 和 `agent-worker` 的运行配置读取必须
使用已发布的不可变版本与运行投影，不得同步查询 Control API 的 Draft、最新配置
或绑定状态。Gateway 与 Worker 均禁止直接读取 Control API 拥有的业务表。

Profile 直接录入凭据的当前 V1 引入一个明确例外：新 Attempt 启动时，Worker
经 RuntimeCredentialResolver 调用 Runtime Profile Owner 在现有 Control API 内的
认证批量凭据接口，授权后取得本次 Attempt 的固定凭据集合；该调用不重新选择资源
或读取当前 Draft。它不是新的 Secret 服务，也不得变成逐节点、逐工具或逐 Provider
调用的同步查询。Profile 的消费方法与可选 runtime HTTP Adapter 已实现；默认
bootstrap 不注册内部路由，真实 Run/Attempt 授权、可信 Workload 认证与 Worker 接线仍待完成。

这项决策替代“Control API 不可用完全不影响新 Run 执行”的旧承诺：已投影路由可继续
接收消息，但需要凭据的新 Attempt 会依赖该内部接口的可用性；接口不可用时以稳定、
可重试结果失败，不回退到明文事件或任意缓存凭据。已完成批量解析的运行中 Attempt
在其生命周期内复用已解析集合，不逐次回查 Control API；无凭据闭包的 Attempt 不调用
该接口。配置发布、运行路由与非秘密快照仍通过不可变版本、版本化事件和只读投影协作。

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

### ARC-102：业务协议共享与公开技术库分开

跨 Workload 的业务协议继续只通过以下区域共享：

- `api/`：OpenAPI、AgentSpec Schema 和版本化事件定义。
- `gen/`：由 `api/` 生成的协议代码。

2026-09-04 按 Channel Gateway 设计修正，明确允许一个有限的公开技术库入口：
`platform/im/wecom`，用于可直接 import 的企微外部协议 Client，编译进使用它的 Go Workload，
不形成独立服务。后续公开技术包必须分别说明职责和依赖，不把本条解释为任意共享实现的许可。

公开技术库必须满足：

- 不依赖 `services/`、平台 Domain/Application 或业务数据库；不接收 TenantID、BindingID、
  RunID、Admission、平台租约、Profile 凭据解析器等业务输入。
- 只拥有外部协议 DTO、连接/帧处理、请求相关性、协议方法与错误；不拥有业务 Repository、
  PG/NATS、租户路由、持久 Inbox/Outbox、业务限流或投递账本。
- 生命周期由调用方通过 context 显式启动和结束；构造/`init()` 不自动联网、不读取全局环境，
  不监听管理端口或接管进程信号。库可持有调用方实例范围内的有界协议内存状态。
- 沿用根 Go Module，不增加子 `go.mod`、Dockerfile、数据库迁移或部署单元。
  `platform` / `platform/im` 只是目录命名空间，不建立可堆放业务的同名大包。
- 业务模块的 Adapter 使用库；Domain/Application 继续依赖自己的 Port，不依赖 SDK。
  Telegram 直接引用已选第三方 Go SDK，不为对称性再建透明转发的公开 wrapper。

`api/`、`gen/`、`platform/` 均禁止放置共享 Repository、业务 Service、领域实体或全局
`contract/common/types`。外部 Provider DTO 和跨 Workload 协议 DTO 都不得直接充当领域实体
或数据库 Row。公开技术库的具体 API 与实现仍须按对应设计通过契约测试。

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
ProfileRevision 或 Secret。Deployment 发布阶段按类别和名称精确匹配 ProfileRevision
资源，完成兼容性校验与 RuntimeManifest 编译；V1 不要求用户填写 Slot 到 Resource
映射表。实际 tRPC-Agent-Go 对象只能由 Worker 根据固定 RuntimeManifest 组装。
Storage 不是 AgentSpec V1 Slot，不得新增 storage_slot 来套用工具绑定规则。
详细设计见 [`control-api/agent.md`](control-api/agent.md)。

### ARC-212：Runtime Profile V1 拥有资源发布与私有凭据边界

`runtimeprofile` 拥有 Tenant 范围内稳定的 Runtime Profile、当前唯一的 ProfileDraft、
RuntimeProfileSpec 校验、不可变 ProfileRevision 发布和 Profile 私有凭据。V1 已实现 11 个
Tenant-scoped HTTP 操作，覆盖元数据、Draft 保存与校验、Revision 发布与读取及显式
live 凭据更新。公开 Write、内部 Canonical Spec 和公开 Read 使用独立 DTO：用户直接
录入 write-only 凭据，内部 Spec 只包含服务器生成的 CredentialID，公开读取不返回
Spec、内部 ID、值或密文。当前加密值与 Receipt 由 Profile 私有表保存。

RuntimeProfileSpec V1 是关闭协议，只接受四种 Resource Kind：Model 的
`openai_compatible`、Tool 的 `mcp_streamable_http`、Knowledge 的
`qdrant_openai` 和 Storage 的 `postgres_state`；MCP Capability 固定为 `web.search`。
Kind 的严格字段、内部凭据关联位置、数量与字符串上限由版本化 JSON Schema、
Go Domain 类型和 Fixture 共同约束；禁止以
开放配置 Blob 或 SDK Option 扩展 V1。

发布幂等键是 `(TenantID, ProfileID, SourceDraftRevision)`。已经发布 Source Draft
Revision N 后，即使当前 Draft 已前进到 N+1，对 N 的延迟重试仍必须返回原
ProfileRevision；只有 N 尚未发布且不再是当前 Draft 时才返回冲突。发布响应使用固定
脱敏 DTO，不附动态 credential_states。任何完整 ProfileRevision 的读取，包括生成公开
详情的内部来源以及向 Deployment Compiler 或其他子领域暴露完整 Spec 的读取，
都必须重新 Canonicalize，并核对 Schema Version 与 Spec Digest；持久化层返回的
`spec_jsonb` 不能直接视为可信 Canonical Spec。只返回 Revision 元数据的 Summary
读取必须使用不包含 `spec_jsonb` 的专用投影，不得加载完整 Spec，也不得执行完整
Spec Canonicalization。

普通 Draft 凭据 replace 使用 COW 新建关联；clear 只解除 Draft 关联。显式 live 更新
使用独立 Credential CAS，不修改已发布配置与 Digest；live clear 后该 ID 不原地恢复。
凭据写入、清除、含关联资源删除和 live 更新要求 Tenant OWNER，并在事务及 Receipt
重放时复核。Profile 发布仍是静态校验，不读取 AgentVersion、live 状态或 Provider。

Runtime Profile V1 不创建 DeploymentRevision 或 RuntimeManifest，不发布 NATS/Outbox
事件，也不构造 tRPC-Agent-Go 对象。Profile 已提供 CheckUsable、ResolveForAttempt 和
可选内部 runtimehttp Adapter；默认 bootstrap 未注入执行授权拥有方与工作负载认证，
不注册内部取值路由。真实 Deployment、Run/Attempt 授权与 Worker 接线仍待后续任务。
详细契约见
[`control-api/runtime-profile.md`](control-api/runtime-profile.md) 与
[`control-api/runtime-profile-spec.md`](control-api/runtime-profile-spec.md)。

## 4. Runtime Profile、Deployment 与 Channel Binding

### ARC-301：三个模块必须分开

- Runtime Profile 管理可复用、Tenant 范围内的 Model、Tool、Knowledge、Storage
  资源配置、内部凭据关联与不可变修订，并拥有当前凭据的加密值、状态和写入事务。
- Deployment 选择 AgentVersion 和 ProfileRevision，按资源类别和名称精确匹配、校验，
  将解析结果固定到不可变 `RuntimeManifest`；用户不需要额外的 Slot → Resource 映射表。
- Channel Binding 管理外部 Bot/Channel 与 DeploymentRevision 的绑定，并发布
  Gateway 所需的运行路由投影。

V1 不引入 Environment 实体、表、管理 API、`environment_id` 或隐藏的 `default`，
也不引入 Overlay、环境继承或多层配置合并。测试与生产使用不同 RuntimeProfile。
凭据由 Profile 在 Tenant/Profile 作用域内管理，固定 CredentialID、类别、资源名、
用途和目的范围；用户不手填 SecretRef 或内部 ID。Profile 发布只校验 Canonical 配置与
关联形状，不解密、不探测 Provider。Deployment 经 Profile 的 CheckUsable port 检查
所选发布关联是否可用；Worker 取值还需真实执行拥有方验证当前 Attempt/lease/Manifest
授权。后两条真实调用链尚待接线。Capability 只验证已按类别和名称匹配的资源，
不用于搜索候选资源，也不进行模糊名称匹配。

三个模块禁止共享一个可任意读写的配置 Blob。它们通过稳定 ID、不可变 Revision
和 Application Port 协作。Runtime Profile V1 已实现契约见
[`control-api/runtime-profile.md`](control-api/runtime-profile.md) 与
[`control-api/runtime-profile-spec.md`](control-api/runtime-profile-spec.md)。
Deployment 的已接受简化边界与待实现协议见
[`control-api/deployment.md`](control-api/deployment.md)。

V1 不引入 Environment 业务对象、生命周期、管理 API、必传 environment_id、
隐藏默认 Environment、Overlay、环境继承或深层合并。用户通过选择测试和生产等
不同 ProfileRevision 区分配置。平台执行后端、网络范围和资源上限由平台部署配置
提供；这不消除 Worker 的 CodeExecutor、工作区和进程资源等运行适配职责。

### ARC-302：运行时只消费不可变快照

AgentDraft 和 ProfileDraft 禁止进入运行链路。Deployment 必须选择明确且不可变的
AgentVersion 与 ProfileRevision，并生成不可变的 DeploymentRevision 和 RuntimeManifest。

Gateway 只持有运行所需的最小投影，不得投影 AgentDraft、编辑器状态或原始
Profile 管理数据。Gateway 在接纳 Run 时必须固定 RuntimeManifest 的 ID、Revision
和 Digest；Worker 不负责选择 Agent 或解析当前生效配置，不跟随最新 ProfileRevision，
也不得在执行时重新匹配名称。

RuntimeManifest 固定 AgentVersion、ProfileRevision 和编译结果，只包含 Agent 实际
需要的资源、选定运行角色与必要依赖，不复制整份 Profile；编译结果必须保留节点级
工具分配。Manifest 中的已解析关系是编译产物，不是新增用户输入映射表。

### ARC-303：同名匹配与 Storage 运行角色分离

Deployment 发布时，对 AgentVersion 内 AgentSpec.requirements 的 models、tools、knowledge
分别在 ProfileRevision 同类别中按名称精确匹配。不存在同名资源、同名但 Capability
不满足要求时必须返回稳定诊断；禁止相似名称猜测、按 Capability 任意选择候选或
跨类别匹配。V1 不引入异名映射。

Storage 采用独立的 Deployment 消费约定：storage.session 是必需运行角色资源，
storage.memory 是可选运行角色资源，选中后还须满足对应 storage.session 或
storage.memory Capability。其他 Storage 不自动启用；不按唯一候选猜测，不修改
RuntimeProfileSpec Schema，不增加 Agent Slot。Session/Memory 数据服务不得隐式
转化成模型工具或其他执行入口。

### ARC-304：静态执行契约与按需工具暴露

Deployment 使用由平台进程配置加载、固定版本与 Digest 的 PlatformExecutionContract
进行静态兼容性校验，涵盖已接入的 Adapter Kind/Version、执行范围和资源上限。
纯 Compiler 接收该固定快照，禁止实时枚举 Worker 节点、探测 Provider 或访问网络来
决定编译结果。Manifest 固定契约版本与 Digest，并保存执行所需的确定限制配置。
Provider 在线状态、凭据真实可用性等属于独立在线诊断，不是发布事务的必需步骤。

Worker 支持某工具不代表 Agent 默认拥有该工具。最终工具集合必须同时满足 Agent
声明与节点选择、Profile 同名资源、平台允许的实现和配置。Worker Adapter 必须显式
控制 tRPC Executor、Skills、Memory 等扩展可能派生的额外工具和执行入口；若派生
入口不能关闭或无法保持与 Manifest 一致，该 Adapter 与静态契约不兼容。

当前 Runtime Profile 仅实现 mcp_streamable_http + web.search 工具资源的配置协议，
不代表 Worker 已接入执行。内建工具、命令执行资源及其节点隔离协议留待后续扩展。
V1 不新建动态工具注册中心、调度平台、策略微服务或 Sandbox Manager。

不可变快照固定资源配置与凭据关联，不固定 live 凭据值。后续 Worker 按固定 Manifest
的最小凭据集合和当前有效 Attempt 授权，经 Profile 内部接口一次性解析；值只进入
该 Attempt 的内存初始化批次，不写回 Manifest 或通过业务事件传播。

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

### ARC-502：Profile 内部凭据与不可变配置分离

AgentSpec 不携带真实凭据、SecretRef 或 CredentialID。当前 V1 已实现用户在 Runtime
Profile 写入 DTO 中直接输入 API Key、Token 或 DSN；该写入请求须脱敏且不得直接成为
Canonical Spec、公开响应、日志、Trace 或 Outbox Payload。Profile Owner 在 PostgreSQL
内加密保存真实值，独立运行注入加密密钥；写入 DTO、受信 Canonical Spec 与公开读取
DTO 分离。公开 Profile 读取组合非秘密 config 与当前 credential_states；不可变
Revision 配置与后续 Deployment 公开配置隐藏内部凭据 ID，不回显值或密文。动态状态
不进入不可变 Revision Content、RuntimeManifest 或发布 Receipt 的固定响应。用户无需维护
SecretRef 或独立 Secret 产品。

内部凭据身份与授权至少包含 `(TenantID, ProfileID, CredentialID, Purpose)`，并固定
非秘密目标与 audience；名称、ID 或调用方传入的 tenant_id 本身不是授权。DSN 的
host、port、database 和 TLS 等非秘密连接配置进入不可变配置，不允许仅轮换凭据值就
改换目标。改变目标或 audience 必须生成新 CredentialID 并发布新的配置 Revision。

Deployment Application 经 ProfileCredentialChecker.CheckUsable 读取最小执行闭包的
凭据 configured/status、所属关系、用途和授权结果，不解密、不取得真实值。纯 Compiler
只消费固定配置并输出凭据 uses 闭包；动态 Checker 结果仅用于 Application Report 与
发布准入，不进入 Compiler 输入、Canonical Content 或 Digest。RuntimeManifest 保留
必要内部凭据身份、用途和固定目标，不携带真实值或密文；公开读取对内部身份做投影。
Outbox、事件和运行投影可传播受控的内部引用，但禁止传播真实凭据或密文。

Worker 的 RuntimeCredentialResolver.ResolveForAttempt 通过 ARC-002 规定的 Profile
Owner 内部认证批量接口取值，不跨域读取 SQL。Profile Owner 验证执行身份，并核对
可信 Tenant、Profile、Manifest、Attempt、用途和实际凭据闭包，拒绝仅凭任意 ID 取值。
首版执行授权通过 Run/Attempt 拥有方的在线可信 Query 核验 active、当前 lease epoch/fence
及 Worker 归属；未过期签名不代替当前状态检查。该查询也是新 Attempt 的显式依赖，不跨域
读表、不新增服务；查询后撤销的在途竞争由接收和执行侧 fence 处理。
该接口与普通 Profile 读取分离；值只沿授权加密连接返回 Worker，不进入事件或日志。
同一 Attempt 完整验收批量响应后，仅在本进程内存中复用固定集合，后续不再 Resolve；
连接重试使用已解析集合。响应结果不确定、进程崩溃或租约失效均结束当前 Attempt，
以新 AttemptID 恢复并重新授权取值；不承诺同 Attempt 跨进程恢复，不建立历史密文
或版本固定表。新 Attempt 可以取得轮换后的值。

普通 Draft 的 keep/replace/clear 只改变草稿关联：keep 保留 ID，replace 写时复制生成
新 ID，clear 脱离当前草稿，不修改历史执行。Profile 已提供显式“更新已使用凭据”
动作，由 OWNER 授权并使用独立 Credential CAS，对稳定 ID 执行 replace/clear；调用方
通过 ProfileRevision 与资源字段定位，服务器解析内部 ID，不接受任意 ID 更新。该动作
明确影响已有 Manifest 的新 Attempt。普通配置 CAS 与凭据 CAS 分开；同 ID 轮换不修改
ProfileRevision、DeploymentRevision 或 Manifest Digest，不承诺秘密值的历史重放。
live clear 对该 ID 作终止性撤销，后续解析返回稳定不可用结果；普通 replace 不复活
已撤销 ID，重新录入通过 Draft 新 ID 和新配置发布完成。已解析的运行中 Attempt 不因
后台 clear 立即擦除已交给客户端的值，紧急停止仍需独立执行终止或 Provider 撤销机制。

当前 V1 的直接录入、加密存储、COW/live/CAS 与 Profile 消费方法已实现；
`CheckUsable` 的真实 Deployment Adapter、Run/Attempt 授权拥有方、可信 Workload 认证
与 Worker 批量解析接线仍待完成。默认 bootstrap 不注册内部解析路由，必须同时注入可信
工作负载认证与 `ExecutionAuthorizationVerifier` 才启用。Profile 只持久化加密当前值；
外部 `CONTROL_PROFILE_CREDENTIAL_KEY` 由部署配置注入，代码不生成默认 Key。
按 ARC-000 不维护开发期 ref-only 兼容分支，不扩展成 Secret 平台或 OAuth 池。

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
- Deployment 的 Input/Manifest/Event 精确字段、稳定诊断码、授权矩阵、运行角色
  限制值、静态 PlatformExecutionContract 兼容矩阵，以及真实 ProfileCredentialChecker
  Adapter；真实 Run/Attempt 当前授权拥有方、可信 Workload 认证与 Worker 批量解析接线。
  无 Environment、同名匹配、Profile 内部凭据、节点按需工具暴露等边界已接受；
  Profile Write/Canonical/Read、Credential CAS、CheckUsable、ResolveForAttempt 和可选
  内部 runtime HTTP Adapter 已实现，不再列为尚待设计的前置能力。加密主 Key 轮换
  仍需单独设计，当前实现不支持自动轮换。
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
8. 业务协议是否只经 api/gen 共享，公开技术库是否仅限 ARC-102 明确职责且未共享业务实现？
9. 新后台任务是否仍由唯一 bootstrap 管理生命周期？
10. 对应测试验证的是实际行为，还是只有目录、接口或容器存在？
