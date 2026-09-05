# tRPC Agent Service

面向多租户的生产级 Agent SaaS 平台。仓库正在从远端基线重新建设，采用独立
Workload、服务本地 `internal/`、领域模块和不可变运行快照组织代码。

## 当前状态

Control API 的首个可运行版本已经覆盖六个核心子领域：

- Identity：登录、Session 校验、`/me`、登出和强制首次修改密码。
- Admin：Platform Operator 授权、全局用户管理、Tenant 开通和首个 Operator 引导。
- Tenant：Tenant 查询、Owner 添加或移除已有平台用户、Membership 授权。
- Agent：Agent 管理、单一 Draft、AgentSpec 校验和不可变 Version 发布。
- Runtime Profile：可复用运行资源配置、单一 Draft、RuntimeProfileSpec 校验和不可变
  ProfileRevision 发布、Profile 私有加密凭据、Draft COW 与显式 live 更新。
- Deployment：确定 AgentVersion + ProfileRevision 的同名匹配与纯 Compiler、
  DeploymentRevision + RuntimeManifest 原子发布、八个管理 HTTP 操作与
  同事务 `PENDING` PostgreSQL Outbox。
- PostgreSQL `0001_baseline.sql`、Gin 进程组装、OpenAPI 和真实 PostgreSQL 集成测试。

Runtime Profile 当前 V1 实现四个资源 Kind、11 个管理 HTTP 操作、独立 Write/Read DTO、
强延迟发布幂等和 Tenant 隔离。Deployment Control Publication 已实现关闭的
Schema / Event 契约、Application、PostgreSQL 原子发布、Gin/Bootstrap 接线和真实
PostgreSQL 集成测试。Profile 消费方法与可选内部取值 Adapter 已有代码，
但默认 bootstrap 不注册内部取值路由。Relay / JetStream 分发、ChannelBinding、
Gateway、Worker 真实执行和 Local IM Provider 仍属于后续纵向切片；当前 Outbox
保持 `PENDING`，Control 发布成功不表示已进入运行链路。
Control API 启动必需外部 `CONTROL_PROFILE_CREDENTIAL_KEY`；配置要求及本地命令见
[Compose 启动说明](deploy/compose/README.md)。

## Deployment V1（Control Publication 已实现）

设计入口见 [`Deployment V1`](docs/architecture-next/control-api/deployment.md)：

```text
AgentVersion + ProfileRevision
        ↓ 同类别同名匹配、校验与编译
DeploymentRevision + RuntimeManifest
```

V1 不引入 Environment 管理对象或用户 Slot → Resource 映射表。测试与生产使用不同
ProfileRevision；平台执行限制由静态配置契约提供。Manifest 只固定实际需要的资源、
Storage 运行角色和节点工具分配；Worker 使用该不可变快照，不跟随最新 Profile。

凭据交互采用用户直接在 Profile 填写、Profile 内部加密保存的方式。
Deployment 已通过 `ProfileCredentialChecker` 检查实际进入 Manifest 的凭据元数据与
使用权，Manifest、Outbox、Event、Receipt 和公开响应均不含真实值。八个
Deployment 管理路由已在 Control API 注册；发布只到达持久化的 `PENDING`
Outbox。项目尚未稳定发布，不维护开发期 ref-only 兼容，也不建设独立
Secret 产品。

后续路线是可选 Relay / JetStream 分发 → ChannelBinding 生效版本切换与
Gateway 运行投影 → Worker 固定 Manifest 执行。Worker 在新 Attempt 启动时的
凭据解析和节点级工具强制仍是 Runtime 纵切，不应从 Control Publication 实现
推断出当前已可执行 Run。

## 生产 Workload

| Workload | 职责 | 状态 |
| --- | --- | --- |
| `control-api` | Identity、Admin、Tenant、Agent、Runtime Profile 及 Deployment | Identity/Admin/Tenant/Agent/Runtime Profile/Deployment Publication V1 |
| `channel-gateway` | IM Webhook、Run Admission、运行投影和回复投递 | 待设计 |
| `agent-worker` | 消费 Run、执行 Agent、完成 Run、产生 ReplyIntent | 待设计 |
| `local-im-provider` | 与外部 IM Adapter 统一的本地调试服务 | 待设计 |
| `control-web` | Control 管理界面，仅连接 Control API | 待重构 |
| `local-im-web` | Local IM 调试客户端，仅连接 Local IM Provider | 待重构 |

## 仓库结构

```text
├── services/                 # 独立构建的生产 Workload
│   └── control-api/
├── api/                      # OpenAPI、Schema、版本化事件源文件
├── gen/                      # 仅存放协议生成代码
├── docs/architecture-next/   # 当前规范性架构文档
├── tests/                    # 跨包集成与端到端测试
└── go.mod                    # 单一根 Go Module
```

## 当前验证命令

```bash
just test
just vet
just vuln
just build
```

Control API 的详细目录说明见
[`services/control-api/README.md`](services/control-api/README.md)。架构约束见
[`docs/architecture-next/constraints.md`](docs/architecture-next/constraints.md)。
Runtime Profile V1 已实现契约见
[`Runtime Profile V1`](docs/architecture-next/control-api/runtime-profile.md) 和
[`RuntimeProfileSpec V1`](docs/architecture-next/control-api/runtime-profile-spec.md)。
