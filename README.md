# tRPC Agent Service

面向多租户的生产级 Agent SaaS 平台。仓库正在从远端基线重新建设，采用独立
Workload、服务本地 `internal/`、领域模块和不可变运行快照组织代码。

## 当前状态

Control API 的首个可运行版本已经覆盖五个核心子领域：

- Identity：登录、Session 校验、`/me`、登出和强制首次修改密码。
- Admin：Platform Operator 授权、全局用户管理、Tenant 开通和首个 Operator 引导。
- Tenant：Tenant 查询、Owner 添加或移除已有平台用户、Membership 授权。
- Agent：Agent 管理、单一 Draft、AgentSpec 校验和不可变 Version 发布。
- Runtime Profile：可复用运行资源配置、单一 Draft、RuntimeProfileSpec 校验和不可变
  ProfileRevision 发布、Profile 私有加密凭据、Draft COW 与显式 live 更新。
- PostgreSQL `0001_baseline.sql`、Gin 进程组装、OpenAPI 和真实 PostgreSQL 集成测试。

Runtime Profile 当前 V1 实现四个资源 Kind、11 个管理 HTTP 操作、独立 Write/Read DTO、
强延迟发布幂等和 Tenant 隔离。Profile 消费方法与可选内部取值 Adapter 已有代码，
但默认 bootstrap 不注册内部路由；真实 Deployment、RuntimeManifest、Run/Attempt
授权拥有方、Worker Adapter 和 Local IM Provider 仍属于后续纵向切片。Gateway 第一切片
已含 Routing、Telegram/企微持久入站、Connection 与 PG Outbox/NATS；当前新增 Delivery
Maintenance/可组合 Runner 与 0007，默认只维护，不启用生产回复。完整回复链路尚未完成，
具体能力与本轮验证状态见[Gateway 实施状态](docs/architecture-next/channel-gateway/implementation-status.md)。
Control API 启动必需外部 `CONTROL_PROFILE_CREDENTIAL_KEY`；配置要求及本地命令见
[Compose 启动说明](deploy/compose/README.md)。

## 下一纵切：Deployment V1（设计已调整，尚未实现）

设计入口见 [`Deployment V1`](docs/architecture-next/control-api/deployment.md)：

```text
AgentVersion + ProfileRevision
        ↓ 同类别同名匹配、校验与编译
DeploymentRevision + RuntimeManifest
```

V1 不引入 Environment 管理对象或用户 Slot → Resource 映射表。测试与生产使用不同
ProfileRevision；平台执行限制由静态配置契约提供。Manifest 只固定实际需要的资源、
Storage 运行角色和节点工具分配；Worker 使用该不可变快照，不跟随最新 Profile。

Profile 当前 V1 已实现用户直接填写凭据、Write/Canonical/Read 分离、私有加密存储、
COW/live/CAS，公开读取只返回非秘密配置与允许的凭据状态。Deployment 仅校验 Profile
内部凭据的配置状态与授权，Manifest 只保留内部引用；后续 Worker 在新 Attempt 启动时经
Profile Owner 的内部认证接口批量取值，已解析 Attempt 复用固定集合。该入口引入
Control API 对新 Attempt 的可用性依赖；它不读取 Draft 或跟随最新配置。Profile 的消费
方法和可选内部 HTTP Adapter 已有实现，默认路由与真实运行接线仍待完成。当前单一 V1
不维护开发期 ref-only 兼容分支，也不建设独立 Secret 产品。

Profile 凭据协议与 Owner 接口已就绪；Deployment 开发顺序是协议 → 纯 Compiler →
Application → PostgreSQL/HTTP/Outbox → 可选 Relay；
ChannelBinding 生效版本切换、真实 Control publisher / Worker 与 Gateway 完整收发仍待后续
纵切；Gateway 另有 Final/Sender 模块和默认独立 Maintenance，发送 Runner 仅显式组合。Control 当前已实现范围
仍以上述列表和实际 OpenAPI 为准；Deployment 路由尚未实现。

## 生产 Workload

| Workload | 职责 | 状态 |
| --- | --- | --- |
| `control-api` | Identity、Admin、Tenant、Agent、Runtime Profile 及后续控制面领域 | Identity/Admin/Tenant/Agent/Runtime Profile V1 |
| `channel-gateway` | IM Webhook、Run Admission、运行投影和回复投递 | Routing/Admission/Connection/Delivery 已有代码；默认新增 Maintenance，可组合 Runner 已有，本轮最终验收已通过；生产 Final 消费/发送与真实 Worker 接线待实现，见[实施状态](docs/architecture-next/channel-gateway/implementation-status.md) |
| `agent-worker` | 消费 Run、执行 Agent、完成 Run、产生 ReplyIntent | 待设计 |
| `local-im-provider` | 与外部 IM Adapter 统一的本地调试服务 | 待设计 |
| `control-web` | Control 管理界面，仅连接 Control API | 待重构 |
| `local-im-web` | Local IM 调试客户端，仅连接 Local IM Provider | 待重构 |

## 仓库结构

```text
├── services/                 # 独立构建的生产 Workload
│   ├── control-api/
│   └── channel-gateway/      # Routing / Admission / Connection / Delivery Final；生产 Final 接线待完成
├── api/                      # OpenAPI、Schema、版本化事件源文件
├── gen/                      # 仅存放协议生成代码
├── platform/im/wecom/        # 公开 Go 企微 P0 协议库；Gateway 已进程内直接装配
├── docs/architecture-next/   # 当前规范性架构文档
├── tests/                    # 跨包集成与端到端测试
└── go.mod                    # 单一根 Go Module
```

公开技术库 `platform/im/wecom` 已有纯 Go 企微 P0 协议 Client 与本地真实 WebSocket
测试；Gateway 已进程内直接引用，不新增独立 Connector 服务。Telegram 入站已直接
引用第三方 Go SDK。
公开包接口与职责见 [Go Connector 设计](docs/architecture-next/channel-gateway/public-go-connector.md)。
当前可按账户配置启动 Connection 与企微入站。Delivery Final、Sender lookup、原 ReplyOrigin
已另有源码与本地纵切；默认 Final 生产消费/调度、真实 Worker 回复链路仍待交付。
精确当前验证以实施状态最新章节为准。

## 当前验证命令

```bash
just test
just vet
just vuln
just build

# Gateway 第一切片源码验证入口，不表示完整 Agent E2E 已通过
just gateway-build
just gateway-test
```

Control API 的详细目录说明见
[`services/control-api/README.md`](services/control-api/README.md)。架构约束见
[`docs/architecture-next/constraints.md`](docs/architecture-next/constraints.md)。
Runtime Profile V1 已实现契约见
[`Runtime Profile V1`](docs/architecture-next/control-api/runtime-profile.md) 和
[`RuntimeProfileSpec V1`](docs/architecture-next/control-api/runtime-profile-spec.md)。
