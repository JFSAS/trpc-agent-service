# ADR-0001：首个 Platform Operator 数据库引导协议

- **状态**：已接受
- **日期**：2026-08-31
- **适用范围**：Control API 首次启动、Identity 账号创建和 Admin Operator Grant

## 1. 背景

普通平台管理命令必须先验证有效的 Identity Session 和 Operator Grant，但全新部署
中两者都不存在。若 `BootstrapPlatformOperator` 只会向已有 `UserID` 授予 Grant，
而 `CreateUserAccount` 又只能由已经授权的 Admin 调用，系统就无法建立第一个可登录
的 Platform Operator。

参考项目 Sub2API 在主 HTTP Server 启动前检查首次安装状态，只在空用户库中根据
启动配置创建首个管理员；已有管理员时跳过，已有普通用户但没有管理员时不会自动
提权。它还允许管理员通过管理 API 直接创建普通用户。

本项目迁移这些行为语义，但不复制以下实现细节：

- Operator 权限继续使用独立 `OperatorGrant`，不塞入 UserAccount 的角色字段。
- 数据库状态是唯一事实来源，不使用 `config.yaml`、`.installed` 或容器文件系统
  判断引导是否完成。
- 密码只从 Secret 文件读取，不通过普通环境变量、配置文件或日志传播。
- 并发副本通过数据库锁和原子事务竞争，不使用无锁的 `COUNT` 后直接插入。
- 现有账号但没有有效 Operator 时阻塞启动并进入显式恢复流程，不静默启动一个
  无人能够管理的平台，也不自动提升任意现有账号。

## 2. 决策

### 2.1 启动位置和模块所有权

Control API 的进程 `bootstrap` 必须在完成 PostgreSQL Migration 后、监听 HTTP
端口前，调用 `admin/application.EnsureInitialPlatformOperator`。

```text
process bootstrap
  -> admin startup inbound adapter
  -> EnsureInitialPlatformOperator
       -> InitialOperatorBootstrapUnitOfWork
            -> identity RegisterInitialOperatorAccount
            -> admin OperatorGrant Repository
            -> admin BootstrapState Repository
            -> Audit/Outbox Repository
  -> commit
  -> start HTTP server
```

`admin/application` 拥有引导条件、结果分类和流程编排；`identity/application` 仍然
拥有用户名规范化、密码策略、密码哈希和 UserAccount 创建。Admin 禁止直接写入
Identity 表，进程 bootstrap 也禁止包含账号或授权规则。

### 2.2 启动输入

自动引导使用以下进程配置：

```text
CONTROL_BOOTSTRAP_MODE=auto | disabled
CONTROL_BOOTSTRAP_USERNAME=<initial-operator-username>
CONTROL_BOOTSTRAP_PASSWORD_FILE=/run/secrets/control-bootstrap-password
```

- `auto` 只允许在全新数据库状态下创建首个 Operator。
- `disabled` 或缺少有效输入时，全新部署返回明确的启动错误。
- `CONTROL_BOOTSTRAP_PASSWORD_FILE` 是一次性 Secret 输入，不是安装状态标记。
- Password 文件内容只在内存中进入 Identity Password Hasher；数据库只保存哈希。
- 日志、Trace、审计、错误、配置快照和事件禁止包含 Password 或文件内容。
- 引导成功后，后续启动忽略这些输入；部署方应移除该 Secret。

初始密码属于临时凭证。首个 Operator 第一次登录后，只能执行修改密码和登出，
完成密码轮换后才能建立完整 Admin Context。

### 2.3 数据库状态矩阵

状态检查必须在取得锁后重新执行：

| 数据库状态 | 结果 |
| --- | --- |
| 已存在有效 Operator | `NOOP_ALREADY_INITIALIZED`，禁止修改账号、密码或 Grant |
| 无任何 UserAccount、OperatorGrant 和完成记录，且 auto 输入有效 | 原子创建首个账号、凭证、Grant、完成记录和审计事实 |
| 全新状态但 mode=disabled 或 Secret 输入缺失 | `BOOTSTRAP_INPUT_REQUIRED`，HTTP Server 不启动 |
| 现有账号但没有有效 Operator | `RECOVERY_REQUIRED`，禁止自动提升账号 |
| 已有完成记录但没有有效 Operator | `INTEGRITY_FAILURE`，进入显式 Break-glass 恢复 |
| 存在残留 Grant、未知状态或数据不一致 | `INTEGRITY_FAILURE` |

首个 Operator 成功建立后只是普通的 `UserAccount + OperatorGrant`，没有永久超级
身份。后续 Operator 通过正常 Admin Command 授予或撤销。

### 2.4 并发和原子性

V1 使用同一个 PostgreSQL 时，引导流程必须：

1. 开启事务并取得固定键的 PostgreSQL 事务级 advisory lock。
2. 在锁内重新读取 UserAccount、OperatorGrant 和 BootstrapState。
3. 使用 Identity Port 创建 ACTIVE UserAccount 和临时 Password Credential。
4. 使用 Admin Repository 创建 OperatorGrant，授予者记录为 `SYSTEM_BOOTSTRAP`。
5. 写入 `initial-platform-operator-v1` 完成记录和同事务审计事实或 Outbox。
6. 一次提交全部状态；任一步失败时全部回滚。

`InitialOperatorBootstrapUnitOfWork` 是 `admin/application` 的使用方 Port。具体
PostgreSQL Adapter 可以协调事务绑定的 Identity 和 Admin 实现，但 Application
接口禁止暴露 `*sql.Tx`、`pgx.Tx` 或跨模块 SQL。

多个 Control API 副本同时启动时，只有锁的持有者执行创建；其他副本取得锁后看到
有效 Operator 并返回 `NOOP_ALREADY_INITIALIZED`。未来拆分物理数据库时，必须在
拆分前用持久化状态机替换该 Adapter，同时保持 Application Command 的行为契约。

### 2.5 Actor 与审计

OperatorGrant 的授予者必须使用显式 Actor：

```text
ActorRef
├── Type: USER | SYSTEM_BOOTSTRAP
└── UserID: 仅 Type=USER 时存在
```

首个 Grant 使用 `SYSTEM_BOOTSTRAP`，避免伪造一个不存在的 `GrantedBy UserID` 或
错误地记录为自我授权。审计事实至少包含稳定 Command ID、目标 UserID、输入摘要、
结果、时间和进程实例，不包含 Password。

## 3. Platform Operator 直接创建用户

已建立 Admin Context 的 Platform Operator 可以调用
`admin/application.CreatePlatformUserAccount`，由 Admin 使用方定义的
`RegisterManagedUserAccount` Port 委托 Identity 创建全局 UserAccount 和临时密码
凭证。

该命令只创建 Identity 账号：

- 不自动创建 OperatorGrant。
- 不自动创建 Tenant Membership。
- 需要 Operator 权限时，另行执行 `GrantPlatformOperator`。
- 需要加入 Tenant 时，仍执行 Tenant Invitation、Initial Owner 或其他拥有方用例。
- 临时密码第一次登录后必须轮换，且禁止出现在响应、日志和审计中。

这样保留 Sub2API “管理员可以直接添加用户”的管理能力，同时保持 Identity、Admin
和 Tenant 的所有权分离。

## 4. 恢复边界

自动引导只解决全新数据库的首个 Operator，不承担 Operator 全部丢失后的恢复。
`RECOVERY_REQUIRED` 和 `INTEGRITY_FAILURE` 必须返回稳定错误码并阻止业务 HTTP
Server 启动。Break-glass 命令的操作者认证、双人审批和审计保留另行决策；它不能
复用或重新打开初始引导条件。

## 5. 必须验证的场景

1. 全新数据库和有效 Secret 只创建一个账号、凭证和 Grant。
2. 正常重启返回 NOOP，输入变化不会重置用户名或密码。
3. 多副本并发启动只产生一个首个 Operator。
4. Secret 缺失时无业务状态写入，HTTP Server 保持未启动。
5. Identity 创建后、Grant 创建前注入故障会回滚全部状态。
6. 已有普通账号但没有 Operator 时返回 `RECOVERY_REQUIRED`。
7. 日志、配置、Trace、审计和事件均不包含初始密码。
8. Admin 直接创建用户不会附带 OperatorGrant 或 Tenant Membership。
