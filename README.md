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
  ProfileRevision 发布。
- PostgreSQL `0001_baseline.sql`、Gin 进程组装、OpenAPI 和真实 PostgreSQL 集成测试。

Runtime Profile V1 已冻结并实现四个资源 Kind、10 个 HTTP API、强延迟发布幂等和
Tenant 隔离。它只完成控制面的配置编辑、校验与不可变发布；Deployment、
RuntimeManifest、Worker Adapter、Gateway 和 Local IM Provider 仍属于后续纵向切片。

## 生产 Workload

| Workload | 职责 | 状态 |
| --- | --- | --- |
| `control-api` | Identity、Admin、Tenant、Agent、Runtime Profile 及后续控制面领域 | Identity/Admin/Tenant/Agent/Runtime Profile V1 |
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
