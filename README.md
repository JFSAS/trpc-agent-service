# tRPC Agent Service

面向多租户的生产级 Agent SaaS 平台。仓库正在从远端基线重新建设，采用独立
Workload、服务本地 `internal/`、领域模块和不可变运行快照组织代码。

## 当前状态

目前只完成：

- 下一代架构约束文档。
- Control API 可编译目录骨架。
- Tenant、Agent、Runtime Profile、Deployment、Channel Binding 模块边界。
- 单一 Bootstrap、独立二进制和独立镜像入口。

当前还没有可用的 HTTP API、数据库 Schema、NATS 协议或 Agent 执行能力，不能
把目录、接口或容器存在视为生产功能完成。

## 生产 Workload

| Workload | 职责 | 状态 |
| --- | --- | --- |
| `control-api` | Tenant、AgentSpec、Profile、Deployment、Channel Binding | 目录骨架 |
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
go test ./...
go build ./services/control-api/cmd/control-api
```

Control API 的详细目录说明见
[`services/control-api/README.md`](services/control-api/README.md)。架构约束见
[`docs/architecture-next/constraints.md`](docs/architecture-next/constraints.md)。
