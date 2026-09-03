# Runtime Profile V1 子领域与实现契约

- **边界状态**：已接受
- **V1 纵向切片状态**：已接受
- **RuntimeProfileSpec 字段状态**：V1 已冻结；四种 Resource Kind 与叶子字段见
  [`runtime-profile-spec.md`](runtime-profile-spec.md)
- **实现状态**：已实现；Domain、Application、PostgreSQL、10 个 HTTP API、OpenAPI 与
  集成测试已经贯通
- **适用范围**：Control API 中可复用运行资源配置的编辑、校验与不可变修订发布

## 1. 目的

runtimeprofile 子领域负责管理 Tenant 范围内可复用的具体运行资源配置。它把一组
Model、Tool、Knowledge、Storage 资源配置编辑成 Draft，完成环境无关校验，并发布为
不可变快照。

V1 的最终发布结果是：

~~~text
一个 ProfileRevision
└── 包含一份经过校验、规范化且不可变的 RuntimeProfileSpec
~~~

Runtime Profile 不是正在运行的 Agent，也不是 tRPC-Agent-Go 配置对象。它不选择
Agent，不决定某份配置当前是否生效，不解析 Secret Value，也不生成
RuntimeManifest。

Runtime Profile、Deployment 和 Channel Binding 的边界、Draft/Revision API、表结构、
发布事务与 RuntimeProfileSpec V1 字段均已接受并由代码与测试实现。这里的“已实现”
只指 Runtime Profile 控制面纵向切片；Deployment、RuntimeManifest、Worker Adapter 与
运行面兼容性仍未实现。上位约束见 [`../constraints.md`](../constraints.md)，字段契约见
[`runtime-profile-spec.md`](runtime-profile-spec.md)，相邻的 Agent 边界见
[`agent.md`](agent.md) 与 [`agent-spec.md`](agent-spec.md)。

## 2. V1 统一语言

| 术语 | 定义 |
| --- | --- |
| Runtime Profile | Tenant 范围内稳定的配置集合身份与展示元数据 |
| ProfileDraft | 一个 Runtime Profile 当前唯一、可修改且允许暂时不完整的工作副本 |
| Draft Revision | ProfileDraft 每次成功保存后递增的乐观并发计数 |
| RuntimeProfileSpec | 描述具体 Model、Tool、Knowledge、Storage 资源及其 SecretRef 的声明式文档 |
| Profile Resource | RuntimeProfileSpec 内的一项具名资源；不是独立 REST 资源或独立聚合 |
| Resource Key | Spec 内的逻辑 Key；在该 Revision 中不可变，不是跨 Revision 身份 |
| Capability | 资源声明能够满足的逻辑能力字符串 |
| Runtime Role Binding | Deployment 拥有的运行角色到 Storage Resource 映射；不是 Agent Slot |
| SecretRef | 指向外部凭据的非秘密引用；不是 Secret Value |
| Canonical RuntimeProfileSpec | 通过发布校验并完成确定性规范化的 RuntimeProfileSpec |
| ProfileRevision | 具有稳定 ID、包含一份 Canonical RuntimeProfileSpec 的不可变发布快照 |
| ProfileRevisionSummary | ProfileRevision 的元数据只读投影；不包含 RuntimeProfileSpec |
| Profile Revision Number | ProfileRevision 在单个 Runtime Profile 内从 1 递增的业务编号 |
| Source Draft Revision | 生成某个 ProfileRevision 的 Draft Revision |
| Spec Digest | Canonical RuntimeProfileSpec 的 SHA-256 内容摘要 |
| Validation Report | 针对确定 Draft Revision 返回的结构化 Error 与 Warning |

“Draft Revision”和“Profile Revision Number”不能简称为同一个含义不明的
“Revision”。前者保护可变 Draft 的并发写入，后者标识不可变发布记录。

正文使用 Runtime Profile，Go 包使用 runtimeprofile，HTTP 资源使用
runtime-profiles。V1 的发布记录统一称为 ProfileRevision，不引入 ProfileVersion、
RuntimeVersion、ModelRevision 或 ToolRevision。

## 3. 所有权边界

runtimeprofile 拥有：

- Runtime Profile 的稳定 ID、TenantID、名称和描述。
- 每个 Runtime Profile 当前唯一的 ProfileDraft 及 Draft Revision。
- RuntimeProfileSpec 的结构以及不依赖 Agent、Environment 和网络状态的校验规则。
- Model、Tool、Knowledge、Storage 的具名资源定义。
- SecretRef 的合法位置和语法校验，但不拥有 Secret Value。
- 从确定 Draft Revision 发布不可变 ProfileRevision 的事务规则。
- Profile Revision Number、Source Draft Revision、Spec Digest 和发布审计元数据。

runtimeprofile 不拥有：

- Agent、AgentDraft、AgentVersion 或 Agent Slot。
- Agent Slot 到 Model/Tool/Knowledge Resource 的绑定，以及 Runtime Role 到 Storage
  Resource 的绑定。
- Environment、Secret Value、Secret Manager 或凭据解析。
- DeploymentRevision、RuntimeManifest 或当前生效指针。
- Provider 连通性探测、真实模型调用或 Tool 调用。
- tRPC-Agent-Go 对象构造。
- Run、Attempt、Worker、Gateway、IM 或 ReplyIntent。
- ProfileRevision 发布后的自动部署或自动重新部署。
- Deployment Outbox、NATS 发布或 Gateway 投影。

三个核心输入的职责如下：

~~~text
AgentVersion
└── 声明逻辑 Slot 与 Capability Requirement

ProfileRevision
└── 声明具体 Profile Resource 与 Capability

Environment
└── 提供目标环境以及 SecretRef 的受控解析上下文

               Deployment
               ├── 选择准确的 AgentVersion
               ├── 选择准确的 ProfileRevision
               ├── 建立 Agent Slot -> Model/Tool/Knowledge Binding
               ├── 建立 Runtime Role -> Storage Binding
               ├── 选择 Environment
               ├── 执行跨对象兼容性校验
               └── 生成不可变 RuntimeManifest
~~~

Profile Resource Key 与 Agent Slot 或 Runtime Role 即使同名也不构成隐式绑定。
Model、Tool、Knowledge 使用 Agent Slot Binding；Session、Memory、Artifact 等 Storage
使用 Deployment 拥有的 Runtime Role Binding。两类绑定都必须显式表达。

## 4. 聚合与修订粒度

V1 以一份完整 RuntimeProfileSpec 作为发布粒度，但不把全部历史 Revision
加载进一个无界对象图：

~~~text
RuntimeProfile 聚合当前状态
├── 稳定身份与可变展示元数据
├── 当前唯一 ProfileDraft
└── latest_revision_number

ProfileRevision
└── runtimeprofile 拥有的不可变发布记录
      └── 一整份 RuntimeProfileSpec
            ├── models
            ├── tools
            ├── knowledge
            └── storage
~~~

RuntimeProfile 是写入一致性边界。发布用例只加载当前 Profile、当前 Draft 和下一个
编号所需状态，并在同一事务插入一条 ProfileRevision。它不会在内存中持有或加载
全部历史 Revision；历史元数据通过独立 Query 分页读取 ProfileRevisionSummary，
完整 Spec 只通过单项 ProfileRevision Query 读取。

Model、Tool、Knowledge 和 Storage 是 RuntimeProfileSpec 内部的值对象。V1 不为
它们分别建立独立 Aggregate、Draft、Revision、Repository 或 REST CRUD。修改任意
资源后，用户发布一份新的完整 ProfileRevision。

这样做的理由是：

1. Deployment 需要的是一组彼此一致的运行资源，而不是若干可能处于不同发布时间的
   可变记录。
2. 一次发布具有一个 Source Draft Revision、一个 Digest 和一个审计边界。
3. V1 尚不存在资源由不同团队独立审批、独立回滚或独立复用的事实需求。
4. 可以避免形成 ProfileRevision 指向多套 ModelRevision、ToolRevision、
   KnowledgeRevision 和 StorageRevision 的引用网。

整体发布粒度是已经实现的 V1 契约。只有出现明确的独立生命周期、独立所有者或不可
接受的整体发布成本时，才重新评估单资源聚合；目录预留和“以后可能需要”都不是拆分
依据。

## 5. V1 纵向切片

### 5.1 Command

- **CreateRuntimeProfile**：在 Tenant 内创建 Runtime Profile，并原子建立
  revision=1、spec={} 的初始 ProfileDraft。
- **UpdateRuntimeProfile**：修改名称和描述，不修改任何已发布 ProfileRevision。
- **SaveProfileDraft**：使用 Expected Draft Revision 保存一份允许继续编辑的文档。
- **ValidateProfileDraft**：校验一个确定的 Draft Revision，返回结构化报告，不产生
  发布记录。
- **PublishProfileRevision**：使用 Expected Draft Revision 发布不可变
  ProfileRevision；成功后将该值记录为 Source Draft Revision。

初始 Draft 不注入默认 Provider、默认模型、默认 Endpoint 或其他隐藏配置。UI 模板
若需要提供初始内容，应通过一次显式 SaveProfileDraft 保存。

### 5.2 Query

- **GetRuntimeProfile**
- **ListRuntimeProfiles**
- **GetProfileDraft**
- **GetProfileRevision**：返回单个完整 ProfileRevision，包括已重新验证完整性的
  Canonical RuntimeProfileSpec。
- **ListProfileRevisions**：分页返回 ProfileRevisionSummary，不包含 Spec，也不执行
  完整 Spec Canonicalization。

V1 不提供 Delete、Archive、Clone、Diff、Rollback、Approve、Activate、
ConnectionTest 或 latest 快捷接口。

## 6. HTTP API 与授权

V1 已实现以下 10 个端点：

| 方法 | 路径 | 用例 |
| --- | --- | --- |
| POST | /v1/tenants/{tenant_id}/runtime-profiles | CreateRuntimeProfile |
| GET | /v1/tenants/{tenant_id}/runtime-profiles | ListRuntimeProfiles |
| GET | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id} | GetRuntimeProfile |
| PATCH | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id} | UpdateRuntimeProfile |
| GET | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft | GetProfileDraft |
| PUT | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft | SaveProfileDraft |
| POST | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft/validate | ValidateProfileDraft |
| POST | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions | PublishProfileRevision |
| GET | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions | ListProfileRevisions |
| GET | /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions/{revision_number} | GetProfileRevision |

OpenAPI components 是 Control API 共享命名空间，使用 RuntimeProfile、
RuntimeProfileDraft、RuntimeProfileRevision、RuntimeProfileRevisionSummary 和
RuntimeProfileValidationReport；本模块内部领域术语仍是 ProfileDraft、
ProfileRevision 与 ProfileRevisionSummary。发布集合使用 revisions，不使用
versions。

最小请求体与响应语义：

| 操作 | 请求体 | 成功响应 |
| --- | --- | --- |
| CreateRuntimeProfile | {name, description?} | {profile, draft: RuntimeProfileDraft} |
| UpdateRuntimeProfile | {name?, description?}，至少一个字段 | RuntimeProfile |
| SaveProfileDraft | {expected_revision, spec} | 保存后的 RuntimeProfileDraft |
| ValidateProfileDraft | {expected_revision} | RuntimeProfileValidationReport |
| PublishProfileRevision | {expected_revision} | {revision: RuntimeProfileRevision} |
| ListProfileRevisions | 无 | {revisions: RuntimeProfileRevisionSummary[], total, offset, limit} |
| GetProfileRevision | 无 | RuntimeProfileRevision |

`RuntimeProfileRevision` 是包含 Canonical RuntimeProfileSpec 的完整表示。
`RuntimeProfileRevisionSummary` 只包含 `id`、`tenant_id`、`profile_id`、
`revision_number`、`source_draft_revision`、`schema_version`、`spec_digest`、
`published_by` 和 `published_at`；它明确不包含 `spec`。Summary 是不可变 Revision 的
查询投影，不是第二个聚合、第二份发布记录或可单独修改的资源。

Publish 的 expected_revision 在成功创建发布记录后成为 source_draft_revision。幂等重试
返回相同的 {revision: RuntimeProfileRevision}，不重新计算或附带一个可能随 Validator 修订而
变化的 Validation Report。发布校验失败时，422 响应使用
{error, validation: RuntimeProfileValidationReport}。

V1 授权沿用 Agent 子领域的最小规则：

- ACTIVE Tenant 的 OWNER 与 MEMBER 可以读写、校验和发布。
- 只有 Platform Operator 身份、但没有该 Tenant Membership 的用户不能访问。
- Restricted Session 必须先完成密码轮换。
- 路径中的 tenant_id 只是资源选择器，不是授权证据。
- Handler 从可信 Session 建立 User Identity，Application 再通过 Tenant
  Membership Port 判定访问权。
- 所有 Query、Command 和 Repository 条件都必须显式携带 TenantID。

细粒度 RBAC、只读成员、发布审批和 Tenant Policy 不在 V1。

### 6.1 HTTP 状态语义

| 操作 | 成功状态 | 说明 |
| --- | ---: | --- |
| 创建 Runtime Profile | 201 | Profile 与初始 Draft 原子创建 |
| 获取、列表和修改元数据 | 200 | 正常完成 |
| 保存 Draft | 200 | CAS 成功并返回递增后的 Draft Revision |
| 校验 Draft | 200 | 校验操作已完成；响应中的 valid 可以为 false |
| 首次发布 Expected Draft Revision | 201 | 创建新的 ProfileRevision，并记录 Source Draft Revision |
| 重试已发布的 Source Draft Revision | 200 | 返回已存在的同一 ProfileRevision |

稳定应用错误：

| HTTP | Error Code | 语义 |
| ---: | --- | --- |
| 400 | INVALID_REQUEST | 外层请求 JSON、路径、分页、字段类型或必填字段非法 |
| 400 | INVALID_RUNTIME_PROFILE | Profile 名称或描述非法 |
| 401 | UNAUTHENTICATED | 没有有效 Session |
| 403 | PASSWORD_CHANGE_REQUIRED | Restricted Session |
| 403 | TENANT_FORBIDDEN | 目标 ACTIVE Tenant 中不存在 OWNER/MEMBER Membership |
| 404 | RUNTIME_PROFILE_NOT_FOUND | Runtime Profile 不存在 |
| 404 | RUNTIME_PROFILE_REVISION_NOT_FOUND | 指定 ProfileRevision 不存在 |
| 409 | RUNTIME_PROFILE_DRAFT_REVISION_CONFLICT | Expected Draft Revision 已过期 |
| 422 | RUNTIME_PROFILE_SPEC_INVALID | Draft 不能安全保存，或 Spec 不能发布 |
| 500 | INTERNAL_ERROR | 未分类服务端错误，不暴露实现细节 |

未知请求字段应返回 400。外层请求无法解析、缺少 expected_revision/spec 或字段类型
错误时返回普通 INVALID_REQUEST，不返回 Validation Report。外层请求已解析且 spec 已
提供后，Spec 顶层不是 Object、重复 Key、明文 Credential 或超过文档上限等 L0 内容
问题返回 422 RUNTIME_PROFILE_SPEC_INVALID，并携带 RuntimeProfileValidationReport。
列表分页沿用 offset/limit，默认 20、最大 100。

## 7. 生命周期

~~~text
CreateRuntimeProfile
    |
    v
RuntimeProfile + ProfileDraft(revision=1, spec={})
    |
    +-- SaveDraft(expected=1) --> ProfileDraft(revision=2)
    |                                  |
    |                                  +-- ValidateDraft --> Report
    |                                  |
    |                                  +-- Publish(expected=2)
    |                                          |
    |                                          v
    |                                  ProfileRevision(number=1,
    |                                    source_draft_revision=2)
    |
    +-- Draft 继续编辑 --> revision=3 --> ProfileRevision(number=2)
~~~

ProfileDraft 不会“变成”ProfileRevision。发布只是从一个确定的 Draft Revision
创建不可变快照，发布后 Draft 仍可继续编辑。

Runtime Profile 不使用 DRAFT/PUBLISHED/ACTIVE/DEPLOYED 混合状态机。“当前生效配置”
属于 Deployment。V1 每个 Runtime Profile 只有一个当前 Draft，不实现并行 Draft、
分支、合并、审批或多人实时协作。

## 8. V1 不变量

- Runtime Profile、Draft、ProfileRevision 和所有 Repository 条件必须显式携带
  TenantID。
- Runtime Profile 与初始 ProfileDraft 必须原子提交。
- 每个 Runtime Profile 只有一个当前 ProfileDraft。
- Draft 保存必须携带 Expected Draft Revision；成功后 Revision 加一。
- Draft 可以语义不完整，但必须是可安全存储的 JSON Object 并满足大小限制。
- 允许发布资源集合为空或只包含部分类别的合法 ProfileRevision；这不证明它能满足
  任意 AgentVersion，Deployment 仍必须完成资源与绑定校验。
- 发布请求必须携带 Expected Draft Revision，并在事务内重新检查当前 Draft；发布
  成功后把它记录为 Source Draft Revision。
- 有 Error 的 Validation Report 禁止发布；Warning 不阻止发布。
- 同一 (TenantID, ProfileID, SourceDraftRevision) 重复发布必须幂等返回同一个
  ProfileRevision。
- ProfileRevision 具有稳定 ID；Profile Revision Number 在单个 Runtime Profile 内从 1
  单调递增，主要用于人类展示和 HTTP 路径。
- ProfileRevision 发布后禁止更新和删除。
- Spec Digest 基于 Canonical RuntimeProfileSpec 计算。
- Spec Digest 不建立唯一约束；恢复旧内容后允许发布新的业务 Revision。
- Resource Key 只在某一类资源和一份 ProfileRevision 内有效。
- ProfileDraft 和任何“最新可变 Profile”禁止进入运行链路。
- Deployment 只能引用明确的 ProfileRevision ID，并固定 Schema Version 与 Digest。
- RuntimeProfileSpec、诊断、日志和审计数据禁止包含 Secret Value。
- Profile 发布不读取 Secret Value，不执行网络请求，不探测 Provider。
- Profile 发布不创建 DeploymentRevision 或 RuntimeManifest，不触发 NATS。
- 发布新 ProfileRevision 不改变任何已有 Deployment。

## 9. 校验边界

架构区分四个校验层级。当前 Runtime Profile V1 已实现 L0、L1、L2；L3 属于尚未
实现的 Deployment 发布切片：

1. **L0：传输与 Draft 内容安全校验**
   - JSON 可解析且顶层是 Object。
   - 拒绝重复 Key、非法 UTF-8 和超过上限的文档。
   - 递归拒绝已知明文 Credential 字段。
   - 允许缺少发布所需字段。
2. **L1：RuntimeProfileSpec Schema 校验**
   - Schema Version、必填集合、字段类型和大小限制。
   - Resource Key、Capability、URL 和 SecretRef 格式。
   - Resource Kind 使用严格判别联合。
   - 所有对象拒绝未知字段，不接受开放式配置 Blob。
3. **L2：Runtime Profile 领域语义校验**
   - Capability 必须符合具体 Resource Kind 的固定或受控允许范围。
   - 只有某个已冻结 Kind 明确定义类型化资源引用时，才校验引用存在性和目标类别；
     V1 不预建通用资源依赖图、Cycle Detector 或闭包编译器。
   - 同一 SecretRef 可以被多个资源复用；Profile 发布不判断其环境授权。
   - 不允许 Agent Slot、Environment、Deployment 或 RuntimeManifest 字段。
   - 不访问 Agent、Environment、Secret Backend、Worker 或 Provider 网络。
4. **L3：未来 Deployment 兼容性校验**
   - Agent 的 Model/Tool/Knowledge Slot 是否全部显式绑定。
   - Deployment 所需 Session/Memory/Artifact 等 Runtime Role 是否显式绑定到 Storage。
   - 经 Resource Kind 规则校验的 Capability 是否满足 AgentVersion Requirement。
   - Environment 是否允许并能解析 SecretRef。
   - 非秘密 Endpoint 是否通过目标 Environment 的静态 Endpoint/Egress Policy。
   - 所有指向版本化受管配置的逻辑引用能否解析为不可变、可审计的具体 Revision。
   - Generation 参数是否被目标 Model 支持。
   - Deployment Compiler 是否明确支持所选 ProfileRevision Schema Version，并能把实际
     绑定的 Resource Kind 编译为 RuntimeManifest Adapter Kind/Version。
   - 目标 Worker 是否明确支持生成的 RuntimeManifest Schema Version，以及其中的运行
     Adapter Kind/Version。
   - 是否可以生成完整且不可变的 RuntimeManifest。

Expected Draft Revision 的比较属于 Application/Repository 的并发条件，不属于
RuntimeProfileSpec Validation Report。冲突使用普通错误信封返回 409。

Provider 登录、Tool Endpoint 健康和模型真实调用属于未来显式 Operational
Preflight，不应成为 ProfileRevision 发布事务的一部分。瞬时网络状态不能决定一个
不可变配置快照能否被发布。

## 10. 发布事务与幂等

PublishProfileRevision 已按以下语义实现：

~~~text
1. 校验身份与 Tenant Membership
2. 按 TenantID + ProfileID + SourceDraftRevision 查询已发布记录
3. 如果已经发布，直接返回已有 ProfileRevision
4. 否则读取当前 ProfileDraft 并检查 Expected Draft Revision
5. 在事务外执行确定性的 L1/L2、Canonicalization 和 Digest
6. 开启 PostgreSQL 事务，锁定 RuntimeProfile 与 ProfileDraft
7. 再次查询相同 Source Draft Revision 是否已经发布
8. 再次检查 TenantID 与当前 Draft Revision
9. 分配下一个 Profile Revision Number
10. 插入 ProfileRevision
11. 更新 RuntimeProfile.latest_revision_number
12. 提交事务
~~~

幂等语义必须覆盖“Draft 已继续前进后的旧请求重试”：

~~~text
source draft revision N 已发布
且当前 Draft 已前进到 N+1
=> 重试发布 N 仍返回原 ProfileRevision，而不是 409
~~~

只有 Source Draft Revision N 尚未发布、并且当前 Draft Revision 已不再等于 N 时，
才返回冲突。

这是 Runtime Profile V1 已实现的强延迟重试语义，比当前 Agent V1 实现更强；不能
假定 Agent 发布已经具有相同行为。Agent 是否对齐仍是独立修正，不属于本切片。

数据库唯一约束：

~~~text
UNIQUE (tenant_id, profile_id, revision_number)
UNIQUE (tenant_id, profile_id, source_draft_revision)
~~~

禁止以 Spec Digest 作为发布幂等唯一键。

## 11. 跨模块协作

runtimeprofile 需要验证 Tenant 访问时，在自己的 Application 层定义最小使用方 Port，
例如：

~~~text
TenantAccess.IsActiveMember(TenantID, UserID) -> bool
~~~

bootstrap 将 tenant 模块提供的只读能力适配并注入。runtimeprofile 禁止：

- 导入 tenant 的 PostgreSQL Adapter。
- 直接读取 Tenant 或 Membership 表。
- 导入 agent 或 deployment 的 Repository。
- 为同进程调用增加 HTTP 或 NATS 跳转。
- 在 Repository 之间编排跨模块业务。

未来 deployment 读取 ProfileRevision 时，应由 deployment/application 定义最小的
ProfileRevisionReader Port。只需要候选 Revision 元数据的查询使用 Summary；
Deployment Compiler 需要完整 Spec 时，由 runtimeprofile 拥有方 Query 返回已经重新
Canonicalize，并核对 Schema Version 与 Spec Digest 的完整 ProfileRevision。
bootstrap 负责适配拥有方 Query。runtimeprofile 不提前创建 DeploymentService，也不
反向依赖 deployment。

Deployment 生成 RuntimeManifest 时，只选择 Agent Slot 或 Runtime Role 显式绑定的资源。
只有已接受的具体 Resource Kind 明确定义依赖时，才附加该 Kind 所需的依赖；不能把
整个 ProfileRevision
和所有 SecretRef 原样交给 Worker。

## 12. 持久化实现

~~~text
runtime_profiles
├── tenant_id
├── id
├── name
├── description
├── latest_revision_number
├── created_by
├── created_at
└── updated_at

runtime_profile_drafts
├── tenant_id
├── profile_id
├── spec_revision
├── spec_jsonb
├── updated_by
└── updated_at

runtime_profile_revisions
├── tenant_id
├── id
├── profile_id
├── revision_number
├── source_draft_revision
├── schema_version
├── spec_jsonb
├── spec_digest
├── published_by
└── published_at
~~~

数据库实现至少满足：

~~~text
runtime_profiles              PRIMARY KEY (tenant_id, id)
runtime_profile_drafts        PRIMARY KEY (tenant_id, profile_id)
runtime_profile_revisions     PRIMARY KEY (tenant_id, id)

drafts/revisions (tenant_id, profile_id)
    -> runtime_profiles (tenant_id, id)
~~~

- Runtime Profile 与初始 Draft 在同一事务创建。
- Draft 使用 spec_revision 做 Compare-And-Swap。
- latest_revision_number 必须为 NULL 或大于 0。
- revision_number 和 source_draft_revision 都大于 0。
- spec_jsonb 的顶层类型必须是 Object。
- spec_digest 符合 sha256:<64 lowercase hex>。
- 两个发布唯一约束与第 10 节一致。
- runtime_profile_revisions 使用数据库 Trigger 拒绝 UPDATE 和 DELETE。
- 插入 ProfileRevision 与更新 latest_revision_number 在同一事务中。
- 查询字段、Tenant 边界、Revision 和审计字段使用显式列；资源内容保留为完整
  JSONB 文档。

PostgreSQL JSONB 不保留 Canonical JSON 的对象 Key 顺序。任何读取 `spec_jsonb` 并向
调用方、Deployment Compiler 或其他子领域暴露完整 ProfileRevision Spec 的路径，
都必须在 Application 边界重新 Canonicalize，并核对 Schema Version 与 Spec Digest；
不能直接把 JSONB 返回字节视为可信 Canonical Spec。

ListProfileRevisions 使用元数据专用查询，显式选择 Revision Summary 所需列，不选择、
聚合或反序列化 `spec_jsonb`。因此 Summary 读取不得加载完整 Spec，也不得执行完整
Spec Canonicalization。Summary 中的 `schema_version` 与 `spec_digest` 是已发布记录的
元数据，不表示当前查询在缺少 Spec 的情况下重新完成了内容完整性校验。

V1 不创建 model_resources、tool_resources、knowledge_resources、storage_resources、
secret_refs 或 profile_resource_bindings 表。

## 13. 代码结构

~~~text
services/control-api/internal/runtimeprofile/
├── domain/
│   ├── profile.go
│   ├── draft.go
│   ├── revision.go
│   ├── spec.go
│   ├── diagnostic.go
│   ├── validation.go
│   ├── validation_helpers.go
│   ├── schema_validation.go
│   ├── semantic_validation.go
│   └── canonicalization.go
├── application/
│   ├── ports.go
│   ├── create_profile.go
│   ├── update_profile.go
│   ├── save_draft.go
│   ├── validate_draft.go
│   ├── publish_revision.go
│   ├── revision_integrity.go
│   └── queries.go
├── adapter/
│   ├── inbound/http/
│   │   ├── routes.go
│   │   ├── profile_handler.go
│   │   ├── draft_handler.go
│   │   ├── revision_handler.go
│   │   ├── request.go
│   │   └── response.go
│   └── outbound/postgres/
│       ├── store.go
│       ├── profile_repository.go
│       ├── draft_repository.go
│       ├── revision_repository.go
│       └── mapper.go
├── id.go
└── wiring.go
~~~

业务 Adapter 包名继续统一为 httpadapter 和 postgresadapter。文件按领域概念、用例或
持久化职责命名，不建立 service.go、model.go、common.go、utils.go 或新的全局
contract 包。

上述目录已经由真实纵向切片实现；runtimeprofile 不保留空 `doc.go` 或空 Module。

## 14. V1 实现证据

当前实现具有以下证据：

- 10 个 HTTP 路由、Application 用例、Domain 规则和 PostgreSQL Adapter 已贯通。
- Tenant Membership 授权、跨 Tenant 隔离和 Restricted Session 经过测试。
- Profile 与初始 Draft 原子创建经过故障回滚测试。
- Draft Compare-And-Swap 和冲突行为经过数据库集成测试。
- 同一 Source Draft Revision 的发布幂等经过并发与延迟重试测试。
- Profile Revision Number 单调递增。
- ProfileRevision 的数据库 UPDATE 和 DELETE 都被 Trigger 拒绝。
- 发布插入成功但更新 latest 失败时整体事务回滚。
- RuntimeProfileSpec JSON Schema、有效 Fixture 和无效 Fixture 已版本化。
- 明文 Credential、未知字段、Kind 规则和非法 SecretRef 的负向测试通过。
- Canonicalization、Golden Document 和稳定 Digest 已固化。
- 单项读取、发布幂等返回和其他暴露完整 Spec 的路径在 JSONB 读取后重新
  Canonicalize，并核对 Schema Version 与 Spec Digest。
- Revision 列表返回不含 Spec 的 ProfileRevisionSummary，查询不加载 `spec_jsonb`，
  也不执行完整 Spec Canonicalization。
- Validate 与 Publish 返回稳定、可定位资源的诊断。
- OpenAPI 与真实注册路由、Handler 行为和状态码一致。
- 没有引入 NATS、Outbox、Worker、tRPC-Agent-Go 或 Agent Slot Binding 依赖。

最终可观察结果是：客户端能够把一份可编辑 ProfileDraft 校验并发布成一个包含
Canonical RuntimeProfileSpec 的不可变 ProfileRevision。将它与 AgentVersion 绑定并
生成 RuntimeManifest 属于 Deployment V1。

## 15. 明确不在 V1

- Secret Value 保存、读取、轮换、Vault/KMS 或 Secret 管理 API。
- Provider Credential 在线验证、Model/Tool/Knowledge 连通性测试。
- ModelRevision、ToolRevision、KnowledgeRevision 或 StorageRevision。
- Profile 继承、组合、模板、环境 Overlay 或深层 Merge。
- Profile 删除、归档、激活、审批和复杂 RBAC。
- Agent Slot Binding、Runtime Role Binding 与 Agent/Profile 兼容性判断。
- Environment 管理、DeploymentRevision 或 RuntimeManifest 生成。
- tRPC-Agent-Go 对象构造、Worker、Run、Gateway、IM 或回复投递。
- Storage 实例创建、迁移、备份和数据生命周期。
- Knowledge 文档摄取、切块、索引构建和索引生命周期。
- Tool Grant、危险操作审批、预算、DLP 和调用审计策略。
- 任意 Go Option、插件、可执行脚本或 Tenant 上传代码。
- NATS 事件、PostgreSQL Outbox 或自动重新部署。

## 16. 已冻结契约与后续决策

RuntimeProfileSpec V1 已冻结：Model 只接受 `openai_compatible`，Tool 只接受
`mcp_streamable_http`，Knowledge 只接受 `qdrant_openai`，Storage 只接受
`postgres_state`；叶子字段、Capability、SecretRef 和上限以版本化 JSON Schema 为准。
禁止用 `map[string]any`、任意 config/parameters 字段或直接透传 tRPC-Agent-Go Option
绕过该关闭协议。

以下仍属于后续 Deployment、Environment 或 Worker 工作，不得反向扩张 Runtime
Profile V1 的实现状态：

1. Deployment 的 Agent Slot 与 Runtime Role Binding 结构。
2. Environment 中 SecretRef 的解析、授权和 Secret Value Version 审计。
3. Deployment Compiler 对四种 Profile Resource Kind 的 RuntimeManifest Adapter
   Kind/Version 映射。
4. RuntimeManifest Schema Version 与 Worker Adapter 的兼容性门禁。
5. Provider、Tool、Knowledge 与 Storage 的在线 Preflight。
6. 新 Resource Kind 是发布新 RuntimeProfileSpec Schema Version，还是先引入独立、
   版本化的 Kind Registry。
