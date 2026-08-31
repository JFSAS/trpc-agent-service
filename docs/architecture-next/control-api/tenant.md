# Tenant 子领域设计

- **边界状态**：已接受
- **细节状态**：草案，等待实现前评审
- **实现状态**：只有目录骨架，尚无业务代码
- **适用范围**：Control API 的 Tenant、Membership、Invitation 与租户授权

## 1. 目的

`tenant` 负责 Tenant 生命周期、用户与 Tenant 的成员关系、邀请以及 Tenant Context
的建立。它不负责验证密码，也不负责判断某个用户是否是 Platform Operator。

Tenant 是客户工作空间，不是登录账号。用户先通过 `identity` 认证，再由
Membership 获得某个 Tenant 内的角色和权限。

## 2. 统一语言

| 术语 | 定义 |
| --- | --- |
| Tenant | 平台内彼此隔离的客户工作空间 |
| Membership | UserAccount 与 Tenant 之间带角色和状态的关系 |
| Tenant Owner | 在一个 Tenant 中具有 `OWNER` Membership 的用户 |
| Invitation | 授权指定接收者加入指定 Tenant 的一次性凭证 |
| Tenant Context | 已验证 Identity 与有效 Membership 共同建立的请求上下文 |
| Initial Owner | Tenant 创建时必须建立的第一个有效 Owner |

Platform Operator 不属于 Tenant 角色体系。Operator 创建 Tenant 属于 `admin`
工作流；Tenant 和 Membership 的状态规则仍由 `tenant` 拥有。

## 3. 所有权边界

`tenant` 拥有：

- Tenant 的创建、状态与稳定标识。
- Membership 的角色、状态和唯一性。
- Invitation 的创建、接受、撤销和过期。
- Tenant Context 与 Tenant 内通用授权能力。
- 初始 Owner、最后一个 Owner 和 Owner 转移规则。

`tenant` 不拥有：

- UserAccount、Password Credential 或 Session；它们属于 `identity`。
- Platform Operator Grant 和 Tenant Provisioning 编排；它们属于 `admin`。
- Agent、Runtime Profile、Deployment 或 Channel Binding 的领域规则。

## 4. 领域模型

### 4.1 Tenant Aggregate

```text
Tenant
├── ID
├── Slug
├── Name
├── Status: PROVISIONING | ACTIVE | SUSPENDED
├── CreatedAt
├── UpdatedAt
└── Version
```

Tenant 只有在初始 Owner 已可用后才能进入 `ACTIVE`。平台暂停 Tenant 的入口属于
Admin Command，但允许何种状态迁移以及对已有运行投影的影响由 Tenant 和后续运行
面设计共同决定。

### 4.2 Membership Aggregate

```text
Membership
├── ID
├── TenantID
├── UserID
├── Role: OWNER | ADMIN | DEVELOPER | VIEWER
├── Status: ACTIVE | SUSPENDED
├── CreatedBy
├── CreatedAt
├── UpdatedAt
└── Version
```

Membership 独立于 Tenant Aggregate 持久化，避免成员数量增长导致每次修改整个
Tenant。最低业务约束：

- `(TenantID, UserID)` 唯一。
- Tenant 至少保留一个有效 Owner。
- 普通邀请不能授予 `OWNER`。
- Owner 转移必须是独立 Command，不能用普通角色更新绕过最后 Owner 检查。
- 禁用的 Membership 不能建立 Tenant Context。

### 4.3 Invitation Aggregate

```text
Invitation
├── ID
├── TenantID
├── InviteeIdentifier
├── Role
├── TokenHash
├── Status: PENDING | ACCEPTED | REVOKED | EXPIRED
├── InvitedBy
├── AcceptedBy
├── ExpiresAt
├── AcceptedAt
├── CreatedAt
└── Version
```

Invitation Token 是一次性高强度随机值，数据库只保存 Token Hash。邀请的投递方式
可以先采用人工复制链接，后续再增加邮件 Adapter；投递方式不能改变 Invitation
的生命周期和单次使用语义。

## 5. 账号准入规则

V1 不提供匿名公开注册，采用以下流程：

```text
Platform Operator
  -> admin.ProvisionTenantWithInitialOwner
       -> tenant 创建 Tenant
       -> identity 解析或创建 Initial Owner Account
       -> tenant 建立 Initial Owner Membership

Tenant Owner
  -> tenant.CreateInvitation
       -> 接收者接受 Invitation
       -> identity 解析或创建 UserAccount
       -> tenant 创建 Membership
```

如果 Initial Owner 尚无 UserAccount，具体采用一次性激活、预创建账号或其他设置
密码流程仍需确认；无论实现方式如何，Platform Operator 都不能长期持有 Owner 的
明文密码，Active Tenant 也不能处于没有有效 Owner 的状态。

## 6. Invitation 接受事务

```text
AcceptInvitation
  1. 根据 Token Hash 查找 Invitation
  2. 校验 PENDING、未过期、未撤销
  3. 解析已登录 UserAccount，或通过 Identity Port 完成受邀账号创建
  4. 校验 InviteeIdentifier 与实际接收者匹配
  5. 创建唯一 Membership
  6. 将 Invitation 标记为 ACCEPTED
  7. 写入审计记录
```

步骤 3 至 6 必须作为一个原子业务结果提交。重复提交同一个 Token 必须返回稳定
结果或明确的已接受状态，不能创建第二条 Membership。

## 7. Application Command 与 Query

### 7.1 Command

- `CreateTenant`，只供 Admin Application Port 调用。
- `GrantInitialOwner`，只用于 Tenant Provisioning。
- `CreateInvitation`
- `AcceptInvitation`
- `RevokeInvitation`
- `ChangeMemberRole`
- `SuspendMembership`
- `TransferOwnership`

### 7.2 Query

- `ListMyTenants`
- `GetTenant`
- `ListMembers`
- `ListInvitations`
- `AuthorizeTenantAction`

## 8. HTTP API

建议的 Tenant 用户接口：

```text
GET    /v1/me/tenants
GET    /v1/tenants/{tenant_id}
GET    /v1/tenants/{tenant_id}/members
PATCH  /v1/tenants/{tenant_id}/members/{user_id}
GET    /v1/tenants/{tenant_id}/invitations
POST   /v1/tenants/{tenant_id}/invitations
DELETE /v1/tenants/{tenant_id}/invitations/{invitation_id}
POST   /v1/invitations/inspect
POST   /v1/invitations/accept
```

邀请 Token 应通过请求 Body 提交，禁止写入服务端访问日志。未认证的新用户接受邀请
时，Identity 账号设置与 Tenant Invitation 接受需要明确的短生命周期流程，不能
退化成公开注册接口。

## 9. Tenant Context

```text
Identity Session
  -> UserID
  -> TenantID（来自路由选择）
  -> 查询有效 Membership
  -> TenantContext{TenantID, UserID, MembershipID, Role}
  -> 目标业务 Use Case
```

路由中的 `tenant_id` 只是选择器，不是授权证据。Tenant Context 必须由服务端创建，
禁止信任客户端传入的 Role、MembershipID 或任意 `isOwner` 标志。

Platform Operator 进入 `/admin` 时使用 Admin Context；只有其确实拥有 Membership
并显式进入 `/tenants/{tenant_id}` 时才能获得 Tenant Context。

## 10. Application Port

Tenant Application 可能需要：

```text
TenantRepository
MembershipRepository
InvitationRepository
AccountResolver       # 由 Identity Application 提供
UnitOfWork
TokenGenerator
Clock
AuditSink
```

Agent、Runtime Profile、Deployment 和 Channel Binding 模块若需要校验 Tenant
访问，应在使用方 Application 定义最小 Authorizer Port，由 Tenant Application
实现并由 bootstrap 注入。它们禁止直接读取 `tenant_memberships`。

## 11. 持久化所有权

建议由 Tenant PostgreSQL Adapter 独占：

```text
tenants
tenant_memberships
tenant_invitations
```

最低唯一约束：

```text
UNIQUE(tenant_slug)
UNIQUE(tenant_id, user_id)
UNIQUE(invitation_token_hash)
```

还需要针对同一 Tenant 和 Invitee 的有效邀请建立条件唯一约束或等价事务检查。

## 12. 目标代码结构

```text
services/control-api/internal/tenant/
├── domain/
├── application/
├── adapter/
│   ├── inbound/http/
│   └── outbound/postgres/
└── wiring.go
```

## 13. 待确认细节

- Initial Owner 的安全激活和密码设置方式。
- InviteeIdentifier 使用 Username、Email 还是独立受邀地址类型。
- Invitation 默认有效期、重发与投递 Adapter。
- Tenant Slug 改名、保留名称和删除/归档策略。
- 各 Tenant Role 到具体 Agent、Profile、Deployment 权限的映射。
- Owner 转移是否需要二次认证或被转移方确认。
