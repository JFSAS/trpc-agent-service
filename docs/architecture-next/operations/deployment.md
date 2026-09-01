# 部署目录结构

- **设计状态**：已接受
- **实现状态**：尚未建立部署目录；本文只确认目录及所有权
- **确认日期**：2026-08-31
- **适用范围**：本地 Compose、NATS 基础设施、可观测性配置和 Kubernetes Helm 部署

本文定义仓库级部署资产的唯一组织方式。它只描述部署与运维资产的所有权，不改变
`ARC-001` 中每个生产 Workload 独立二进制、独立镜像的约束。

## 1. 已接受的目录

```text
deploy/
├── compose/
│   ├── compose.yaml
│   ├── compose.local.yaml
│   └── compose.observability.yaml
├── nats/
│   ├── streams.yaml
│   └── permissions.yaml
├── observability/
│   └── ...
└── helm/
    └── agent-platform/
```

该结构是后续新增部署文件的规范性入口。禁止同时建立第二套平级 `docker/`、`k8s/`、
`charts/` 或按个人习惯分散的部署目录。

## 2. 目录职责

### 2.1 `deploy/compose/`

三个文件采用“稳定基线 + 显式 overlay”的组合方式：

- `compose.yaml` 定义单机自托管环境的完整基线拓扑，包含 PostgreSQL、NATS 和已经
  纳入发行的生产 Workload。它只引用由 CI 固定的 `image@sha256:<digest>` 或不可变
  Release Tag，不声明 `build:`，并统一定义内部网络、命名 Volume、健康检查和 Secret
  挂载。它不挂载源码，也不向宿主机暴露 PostgreSQL、NATS 等内部服务端口。
- `compose.local.yaml` 是本地开发 overlay，只增加 `build:`、回环端口、调试变量和
  本地 Secret 文件。它不能改变 Workload 职责、事件语义或运行拓扑。
- `compose.observability.yaml` 是可选的可观测性 overlay，增加 Collector、Prometheus、
  Tempo、Loki、Grafana 等组件；未启用时不影响业务服务运行。

固定组合顺序为：单机自托管只加载基线；本地开发加载基线后再加载 local overlay；
启用可观测性时始终把 observability overlay 放在最后。根 `justfile` 封装这些组合命令，
调用方不自行维护 `docker compose -f ...` 参数。

Compose 用于本地开发、集成测试和单机验收；它不承担跨节点调度和高可用承诺。

### 2.2 `deploy/nats/`

- `streams.yaml` 声明 JetStream Stream、Subject、Retention、Storage 和复制要求。
- `permissions.yaml` 声明各 Workload 的 Publish、Subscribe 与管理权限边界。
- 文件只保存声明式配置，不保存 NATS Credential、Token 或运行时状态。
- 不兼容的 Stream 变更必须由显式迁移处理，禁止启动时静默删除并重建。

NATS Server 不直接读取这些 YAML。它们由唯一的、版本化的 NATS
bootstrap/reconciliation 命令读取、校验并幂等应用。Compose 与 Helm 调用同一实现，
不得各自维护初始化逻辑；reconciliation 遇到不兼容变更时必须失败并要求显式迁移。

Durable Consumer 的业务语义仍由消费方拥有；部署文件只提供基础设施声明，不能成为
新的全局事件业务包。

### 2.3 `deploy/observability/`

该目录保存 OpenTelemetry Collector、Prometheus、Tempo、Loki、Grafana Alloy、
Grafana、Alertmanager、NATS Surveyor 和 postgres_exporter 的版本化配置、Dashboard
与告警规则。完整约束见 [可观测性与 Telemetry 设计](observability.md)。

### 2.4 `deploy/helm/agent-platform/`

该目录是 Kubernetes 生产部署入口，负责把已经存在的独立镜像组装为 Deployment、
Service、Job、Ingress、Secret 引用、NetworkPolicy、HPA 和其他平台资源。

Helm 不重新定义业务配置、事件协议或数据库 Schema。Compose 与 Helm 必须使用相同
镜像、端口语义、健康检查、环境变量和 Secret 契约。

V1 数据库迁移由 Control API 进程启动流程执行：Migration SQL 嵌入该服务二进制，
进程监听端口前完成迁移，并使用 PostgreSQL advisory lock 串行化多副本迁移。Compose
与 Helm 不得同时创建数据库 Migration Job。未来若改用独立 Job，必须通过架构决策
一次性移除进程启动迁移，避免两个执行者竞争迁移所有权。

## 3. 服务本地资产

以下文件继续跟随所属 Workload，不迁入 `deploy/`：

```text
services/<workload>/Dockerfile
services/<workload>/migrations/
services/<workload>/cmd/<workload>/
services/<workload>/internal/bootstrap/
```

`deploy/` 负责组装已经定义好的服务，不拥有服务内部的编译入口、Migration SQL、
Domain、Application、Repository 或业务 Adapter。

每个生产 Workload 仍然只构建自己的一个二进制和镜像。禁止建立一个全局 Dockerfile
一次构建全部二进制，再通过 Compose `entrypoint` 或 Helm `command` 选择业务角色。

## 4. 运行时文件与 Secret

以下内容禁止提交到 `deploy/`：

- `.env` 实际值和任何生产 Secret。
- PostgreSQL、NATS、Prometheus、Tempo、Loki 或 Grafana 的运行数据。
- Helm 渲染后的临时 Manifest。
- 本地生成的证书、Credential、Bootstrap Password 和 Token。
- Compose 容器日志、备份包和调试 Dump。

仓库只保存安全的 `.env.example`、Secret 名称、SecretRef 和配置 Schema。运行时数据与
Secret 必须位于 Git 忽略目录、Docker Volume、Kubernetes Secret 或外部 Secret
Manager 中。

## 5. 操作入口与一致性

根 `justfile` 是开发者操作入口，负责调用 Compose、Helm、测试和验证命令。禁止为了
每个部署动作继续增加仓库根目录 Shell 脚本。

Compose 与 Helm 必须满足：

1. 发布镜像使用 `image@sha256:<digest>`，或由 CI 解析为 digest 的不可变 Release Tag；
   不使用不可追踪的 `latest` 作为发布依据。
2. 服务名、端口、Probe、环境变量和 Secret 名称保持一致。
3. PostgreSQL、NATS 和观测后端默认不直接暴露公网。
4. 本地覆盖不能改变生产业务语义。
5. 部署文件存在不代表服务已实现或生产验收已经完成。

## 6. 当前实现状态

当前只实现 Control API 的本地 Compose 启动：

- `deploy/compose/compose.yaml` 只编排 Control API 及其必需的 PostgreSQL。
- `deploy/compose/compose.local.yaml` 只增加 Control API 本地构建和回环端口。
- 本地 Compose 默认使用 `auto` 完成一次性 Platform Operator bootstrap，并直接通过
  `CONTROL_BOOTSTRAP_USERNAME` 和 `CONTROL_BOOTSTRAP_PASSWORD` 提供简单的本地凭证；
  已有 Operator 时 bootstrap 保持幂等。
- 当前 Compose 不加入 NATS、Gateway、Worker、Local IM 或可观测性组件。

`compose.observability.yaml`、NATS 声明和 Helm Chart 继续保留为目标结构，等相应
Workload 进入实现阶段后再分别补充。当前切片必须通过 Compose 配置校验、镜像构建、
容器启动、PostgreSQL Migration 和 `/healthz` 验证。
