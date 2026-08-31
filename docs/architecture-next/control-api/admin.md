# Admin 子领域设计

- **边界状态**：已接受
- **细节状态**：草案，等待实现前评审
- **实现状态**：尚未创建 `internal/admin` 代码
- **适用范围**：Control API 内的平台级管理能力

## 1. 目的

`admin` 是 Control API 内负责平台级管理的业务子领域，不是一个用于堆放通用
Handler、公共工具或配置代码的杂项目录。它回答的问题是：**哪个平台操作者可以
执行哪些跨 Tenant 的管理命令，以及这些命令怎样调用真正拥有数据的业务模块。**

Platform Operator 与 Tenant 用户共用同一个 `control-web`、本地账号和服务端
Session。共享前端不表示共享授权：平台命令由 `admin` 授权，Tenant 内资源仍由
`tenant` Membership 以及对应业务模块授权。

## 2. 统一语言

| 术语 | 定义 |
| --- | --- |
| Platform Operator | 获得平台级管理授权的 `UserAccount`，不是 Tenant 角色 |
| Operator Grant | 将平台级能力授予某个 `UserID` 的可撤销记录 |
| Admin Command | 改变平台级状态或编排多个拥有方 Use Case 的命令 |
| Tenant Provisioning | 创建 Tenant 并建立初始 Owner 的平台级流程 |
| Initial Tenant Owner | Tenant 创建时必须建立的第一个有效 `OWNER` Membership |
| Admin Context | 由有效 Session 与有效 Operator Grant 共同建立的可信调用上下文 |

`Platform Operator`、`Tenant Owner` 和 Tenant 内的 `ADMIN` 是三个不同概念。
Platform Operator 即使同时属于某个 Tenant，也必须显式选择当前处于平台上下文
还是 Tenant 上下文。

## 3. 所有权边界

`admin` 拥有：

- Operator Grant 的生命周期和平台级授权规则。
- 平台管理 Command、Query 及其审计语义。
- Tenant Provisioning 等跨模块工作流的应用编排。
- `/v1/admin/*` HTTP API、直接创建平台用户和一次性进程启动引导的业务入口。

`admin` 不拥有：

- 用户名、密码凭证或 Session；这些属于 `identity`。
- Tenant、Membership 或 Invitation；这些属于 `tenant`。
- Agent、Runtime Profile、Deployment 或 Channel Binding 数据。
- PostgreSQL 连接、HTTP Server、日志与 Tracing；这些属于共享 `infra`。

`admin` 禁止导入其他模块的 PostgreSQL Adapter，也禁止直接写入其他模块拥有的
表。跨模块修改必须通过使用方定义的 Application Port 调用拥有方 Use Case。

## 4. 领域模型

### 4.1 Operator Grant Aggregate

建议的最小状态：

```text
OperatorGrant
├── ID
├── UserID
├── Status: ACTIVE | REVOKED
├── GrantedByActorType: USER | SYSTEM_BOOTSTRAP
├── GrantedByUserID
├── GrantedAt
├── RevokedBy
├── RevokedAt
└── Version
```

V1 可以先使用一组固定的 Platform Operator 能力，不必立即设计复杂 RBAC。未来
增加细粒度权限时，应扩展 Operator Grant，而不是把平台权限塞入 Identity Session
或 Tenant Membership。

必须满足：

- 一个 `UserID` 最多有一个有效 Operator Grant。
- 禁用的 UserAccount 不能建立 Admin Context。
- 撤销 Grant 后，后续平台命令必须立即失效。
- 普通平台命令不得撤销最后一个可用 Platform Operator；Break-glass 机制另行决策。

## 5. Application Command 与 Query

### 5.1 V1 Command

| Command | 入口 | 说明 |
| --- | --- | --- |
| `EnsureInitialPlatformOperator` | 进程启动 Adapter | 只在全新数据库中原子建立首个账号、凭证和 Grant |
| `CreatePlatformUserAccount` | Control Web / HTTP | 通过 Identity Port 直接创建全局用户和临时凭证 |
| `GrantPlatformOperator` | Control Web / HTTP | 将平台能力授予已有 UserAccount |
| `RevokePlatformOperator` | Control Web / HTTP | 撤销平台能力，但不得删除账号 |
| `ProvisionTenantWithInitialOwner` | Control Web / HTTP | 创建 Tenant，并建立初始 Owner |

`SuspendTenant`、`RestoreTenant` 和平台级数据查看属于后续能力，不应为了目录完整而
提前创建空接口。

### 5.2 V1 Query

- `GetMyAdminCapabilities`
- `ListPlatformOperators`
- `ListPlatformUserAccounts`
- `ListTenantsForOperations`
- `GetTenantProvisioningStatus`

Admin Query 只能返回平台运维所需的最小信息。Platform Operator 需要查看某个
Tenant 的 Agent 或 Deployment 内容时，必须采用另行设计的审计访问或显式 Tenant
Membership，不能因为拥有 Operator Grant 就直接读取业务表。

## 6. 首个 Platform Operator 引导

首次启动不要求预先存在 UserAccount。进程 bootstrap 在 Migration 完成后、HTTP
Server 监听前，通过 Admin 的进程启动 Adapter 调用：

```text
process bootstrap
  -> admin startup inbound adapter
  -> EnsureInitialPlatformOperator
       -> Identity Port: RegisterInitialOperatorAccount
       -> OperatorGrant Repository: SYSTEM_BOOTSTRAP
       -> BootstrapState + Audit/Outbox
```

该流程只在全新数据库和显式 `auto` 配置下执行。它使用 PostgreSQL 事务级 advisory
lock 处理多副本竞争，并将账号、密码哈希、Grant、完成记录和审计事实原子提交。
数据库中已经存在有效 Operator 时幂等跳过；已经存在账号但没有 Operator 时进入
显式恢复状态，禁止自动提权或重置密码。

初始密码从 `CONTROL_BOOTSTRAP_PASSWORD_FILE` 读取，禁止进入配置、日志、Trace、
审计和事件。第一次登录只能修改密码或登出，完成轮换后才建立完整 Admin Context。
完整状态矩阵见
[`ADR-0001`](../decisions/0001-initial-platform-operator-bootstrap.md)。

## 7. Platform Operator 直接创建用户

Platform Operator 可以通过 `CreatePlatformUserAccount` 创建全局 UserAccount：

```text
POST /v1/admin/users
  -> Admin Context
  -> CreatePlatformUserAccount
       -> Identity Port: RegisterManagedUserAccount
       -> Admin Audit/Outbox
```

该命令创建 ACTIVE 账号和必须在首次登录轮换的临时密码凭证。它不自动创建
OperatorGrant 或 Tenant Membership。授予平台权限继续使用
`GrantPlatformOperator`；加入 Tenant 继续使用 Tenant 拥有的准入用例。

## 8. Tenant Provisioning 调用链

```text
Control Web /admin/tenants
  -> admin HTTP Handler
  -> Admin Context 校验
  -> ProvisionTenantWithInitialOwner
       -> identity Application Port：解析或创建 Owner UserAccount
       -> tenant Application Port：创建 Tenant
       -> tenant Application Port：建立 Initial Owner Membership
       -> Admin Audit Port：记录操作者、输入摘要和结果
```

Tenant 与初始 Owner 必须形成一个完整的业务结果：失败时不能留下没有可用 Owner
的 Active Tenant。由于 V1 使用同一个 PostgreSQL，具体实现应通过显式 Unit of
Work 协调拥有方 Use Case；共享事务不改变表和规则的模块所有权。

初始 Owner 的密码设置或一次性激活方式仍需在实现前单独确认。Platform Operator
不得读取或长期持有 Tenant Owner 的明文密码。

## 9. Inbound Adapter 与路由

### 9.1 HTTP

建议的首批接口：

```text
GET    /v1/admin/capabilities
GET    /v1/admin/operators
POST   /v1/admin/operators
DELETE /v1/admin/operators/{user_id}
GET    /v1/admin/users
POST   /v1/admin/users
GET    /v1/admin/tenants
POST   /v1/admin/tenants
```

所有 `/v1/admin/*` 接口必须同时验证 Identity Session 和 Operator Grant。前端是否
显示菜单不能替代服务端授权。

### 9.2 进程启动 Adapter

`admin/adapter/inbound/startup` 向进程 bootstrap 暴露
`EnsureInitialPlatformOperator`。它只负责把已经校验的启动配置转换为 Command；
状态矩阵、并发、幂等和审计规则仍由 `admin/application` 拥有。`cmd/control-api`
保持轻量，不解析账号规则，也不提供可重复运行的提权 CLI。

## 10. 目标代码结构

```text
services/control-api/internal/admin/
├── domain/
│   ├── operator_grant.go
│   └── errors.go
├── application/
│   ├── ensure_initial_operator.go
│   ├── create_platform_user.go
│   ├── grant_operator.go
│   ├── revoke_operator.go
│   ├── provision_tenant.go
│   ├── queries.go
│   └── ports.go
├── adapter/
│   ├── inbound/
│   │   ├── http/
│   │   └── startup/
│   └── outbound/postgres/
└── wiring.go
```

## 11. Control Web 边界

同一个 `control-web` 建议使用明确路由空间：

```text
/login                   # Identity 登录
/admin/*                 # Platform Operator 控制台
/tenants/{tenant_id}/*   # Tenant 管理控制台
```

登录后前端可以分别请求 Admin Capability 与 Tenant Membership 来构建导航。它不能
把某个前端状态中的 `isAdmin=true` 当作授权证据，也不能在平台与 Tenant 上下文间
静默切换。

## 12. 待确认细节

- Platform Operator 全部丢失后的 Break-glass 认证、审批与恢复机制。
- Operator Grant 是否需要细粒度 Permission。
- 新 Tenant 初始 Owner 采用已有账号、一次性激活还是其他安全设置流程。
- Tenant Suspend 对 Gateway 已投影运行配置的精确影响。
- 平台人员受审计访问 Tenant 业务数据的审批与留痕机制。
