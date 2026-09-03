# RuntimeProfileSpec V1 Schema 与 Validation 契约

- **设计状态**：已接受
- **所属子领域**：Control API / Runtime Profile
- **协议位置**：`api/schemas/runtimeprofile/v1/`
- **适用对象**：ProfileDraft 与不可变 ProfileRevision 中的 RuntimeProfileSpec
- **不适用对象**：AgentSpec、Environment、DeploymentRevision、RuntimeManifest、
  Worker 运行实例和 Secret Value
- **实现状态**：V1 JSON Schema、Fixture、Go Validator、Canonicalization、Digest 与
  Runtime Profile HTTP 发布链路已实现；Deployment Compiler、RuntimeManifest 和 Worker
  Adapter 仍未实现

## 1. 目的

RuntimeProfileSpec 是 runtimeprofile 子领域拥有的声明式业务协议，用于描述一组可以
复用的具体运行资源。它回答“有哪些可供部署选择的资源”，而不回答“某个 Agent 的
Slot 绑定到哪个资源”。

V1 实现满足：

1. 一份合法 RuntimeProfileSpec 可以确定性规范化并计算稳定 Digest。
2. Draft 可以暂时不完整，只有通过发布校验的 Canonical RuntimeProfileSpec 才能
   进入 ProfileRevision。
3. Control API 可以在不访问 Provider、不解析 Secret 和不构造 tRPC-Agent-Go 对象的
   情况下完成发布校验。
4. ProfileRevision 可以被多个 Deployment 复用，但不会与某个 Agent Slot 名称耦合。
5. 未知字段、明文凭据、万能配置 Blob 和框架内部任意 Option 不能进入协议。
6. 后续 Deployment 必须从 AgentVersion、ProfileRevision、Environment 和显式 Binding
   生成最小 RuntimeManifest；该编译链不属于当前实现。

## 2. 已接受并实现的 V1 边界

| 项目 | 当前状态 |
| --- | --- |
| Runtime Profile、Deployment 与 Channel Binding 分开 | 已接受；边界已落地 |
| Agent Slot 到 Profile Resource 的 Binding 属于 Deployment | 已接受；Deployment 未实现 |
| 凭据只能以 SecretRef 引用，不得包含 Secret Value | 已接受；Schema 与 L0 已实现 |
| 顶层采用版本化 RuntimeProfileSpec 文档 | 已接受；Schema Version 为 `v1` |
| 使用 models、tools、knowledge、storage 四类具名资源集合 | 已接受并实现 |
| 整份 Spec 发布为一个不可变 ProfileRevision | 已接受并实现 |
| 严格 Schema、拒绝未知字段和开放式配置 Blob | 已接受并实现 |
| Resource Key 与 SecretRef 使用关闭的基础语法 | 已接受并实现 |
| 四类 Resource Kind 与叶子字段 | 已冻结并实现；见第 9～13 节 |
| Capability 规则 | 每个 Kind 固定或使用关闭 Allowlist；不引入全局 Registry |
| Environment 中 SecretRef Resolver | 后续 Environment/Deployment 工作 |
| RuntimeManifest Adapter 映射与 Worker 兼容 | 后续 Deployment/Worker 工作 |

版本化 JSON Schema 是字段和 Kind 的公开协议源；Go Domain 类型、Validator 与 Fixture
实现同一关闭边界。聚合、10 个 API 和发布事务见
[runtime-profile.md](runtime-profile.md)；上位约束见
[../constraints.md](../constraints.md)；逻辑资源需求与 Slot 见
[agent-spec.md](agent-spec.md)。

## 3. V1 非目标

RuntimeProfileSpec V1 不定义：

- Agent 的 Prompt、Instruction、节点结构、Generation 参数或逻辑 Slot。
- Agent Slot 到 Profile Resource 的绑定。
- Environment、Region Override、环境 Overlay 或字段深层 Merge。
- Secret Value、密文、API Key、Token、Password、DSN 或 Authorization Header。
- Secret 创建、读取、轮换、Vault/KMS 或云 Secret Manager API。
- DeploymentRevision、RuntimeManifest 或当前生效 Deployment。
- Worker 并发、Run 队列、执行状态、重试、超时和资源配额。
- Provider 在线探测、Tool 健康检查、真实模型调用或凭据验证。
- Knowledge 文档内容、摄取任务、切块、Embedding 和索引生命周期。
- Storage 实例创建、数据库迁移、备份、数据保留和业务数据。
- Tool Grant、审批、预算、DLP 或调用审计策略。
- Tenant 上传的 Go 代码、脚本、插件、回调函数或进程启动命令。
- Profile 继承、模板、组合、导入覆盖或跨 Profile 当前引用。
- 框架内部所有 tRPC-Agent-Go Option 的 JSON 映射。

## 4. 文档模型

V1 顶层信封是：

~~~json
{
  "schema_version": "v1",
  "models": {},
  "tools": {},
  "knowledge": {},
  "storage": {}
}
~~~

逻辑结构：

~~~text
RuntimeProfileSpec
├── schema_version
├── models       map<ResourceKey, ModelResource>
├── tools        map<ResourceKey, ToolResource>
├── knowledge    map<ResourceKey, KnowledgeResource>
└── storage      map<ResourceKey, StorageResource>
~~~

四个资源集合都必须显式出现，可以为空对象。这样可以避免字段缺失、null 和空对象
具有三套不同语义。允许发布空集合或只包含部分资源类别的 ProfileRevision；这只证明
文档自身合法，不证明它能满足任何 AgentVersion，资源完整性由 Deployment 校验。

顶层和每个资源判别联合都必须使用 additionalProperties=false。具体 Kind 只能增加
字段固定、类型明确、上限明确的对象，不能携带开放式 Map 或直接透传 SDK Option。

## 5. Resource 与 Agent Slot 的分离

AgentVersion 中的逻辑需求示例：

~~~text
models.primary
tools.search
knowledge.docs
~~~

ProfileRevision 中的具体资源示例：

~~~text
models.production_chat
tools.web_search
knowledge.product_docs
storage.conversation_state
~~~

后续 Deployment 建立显式映射：

~~~text
Agent Slot                         Profile Resource
models.primary                  -> models.production_chat
tools.search                    -> tools.web_search
knowledge.docs                  -> knowledge.product_docs
~~~

规则：

1. Resource Key 与 Slot Name 属于不同命名空间。
2. 名称相同不能形成隐式绑定。
3. Runtime Profile 发布不读取 AgentVersion。
4. Runtime Profile 发布不判断某个 Agent 是否可部署。
5. Deployment 固定 ProfileRevision ID 后，再验证绑定和 Capability。
6. RuntimeManifest 只包含显式绑定的资源；若一个已接受的具体 Kind 定义了类型化
   依赖，再包含该 Kind 必需的依赖，不能复制整份 Profile。
7. Storage 不是 AgentSpec V1 Slot；Deployment 必须通过独立的 Runtime Role Binding
   把 Session、Memory、Artifact 等运行角色映射到 Storage Resource，不能为 AgentSpec
   增加 storage_slot。

## 6. 标识符与引用语法

### 6.1 Resource Key

Resource Key 使用以下格式：

~~~regex
^[a-z][a-z0-9_-]{0,63}$
~~~

合法示例：

~~~text
production_chat
web-search
product_docs
conversation_state
~~~

非法示例：

~~~text
ProductionChat
1st-model
model/provider
model primary
~~~

Resource Key 只在一份 ProfileRevision 的一个资源类别中唯一。models.primary 与
tools.primary 可以同时存在，因为引用始终携带资源类别。

### 6.2 Capability

V1 不接受 Tenant 自定义的自由 Capability 字符串。关闭集合为：

| Resource Kind | Capability 规则 |
| --- | --- |
| `openai_compatible` | `capabilities` 是 `chat`、`tool_call` 的非空子集，最多 2 项且不得重复；必须包含 `chat` |
| `mcp_streamable_http` | `capability` 固定为 `web.search` |
| `qdrant_openai` | Domain 类型固定提供 `knowledge.search`，Spec 不接收 capability 字段 |
| `postgres_state` | Domain 类型固定提供 `storage.session` 与 `storage.memory`，Spec 不接收 capability 字段 |

V1 不实现全局 Capability Registry、Alias、层级继承或语义蕴含。

Capability 也不能成为 Tenant 自由夸大资源能力的标签。每个被接受的 Resource Kind
必须定义以下二者之一：

- 固定 Capability 集合，由 Kind 自动推导；或
- 受控 Capability Allowlist，Tenant 只能声明其中的子集。

没有这项规则的 Resource Kind 不能冻结进 V1。Deployment 执行：

~~~text
Agent Requirement
⊆ 通过 Resource Kind 规则校验的 Profile Resource Capability
~~~

该集合检查只证明声明与平台 Kind 规则兼容，不证明 Provider 当前在线，也不替代真实
运行结果。

### 6.3 Profile 内部引用

V1 不预先建立通用 ResourceRef、任意深度依赖图、Cycle Detector 或 Closure Compiler。
默认情况下，各 Profile Resource 相互独立，并由 Deployment 直接选择。

只有某个已经冻结的具体 Resource Kind 确实需要依赖另一资源时，才能在该 Kind 的严格
Schema 中加入类型明确的字段，例如只指向 storage 集合的 storage_ref。该字段必须
同时定义目标类别、是否必填、引用上限和缺失时的稳定诊断；其他 Kind 不能因此获得
任意引用能力。

无论具体 Kind 如何设计，都禁止：

- 跨 Runtime Profile 引用资源。
- 引用另一个 Runtime Profile 的 latest 或可变 Draft。
- 使用 URL 或数据库主键绕过 ProfileRevision 边界。
- 用未带目标类别语义的通用 ref 字段。

当前四种 Kind 都没有跨 Resource 引用字段，因此 V1 没有引用深度、环检测或依赖闭包
规则。

### 6.4 受管引用的不可变固定规则

“受管引用”不能只表示一个会随时间改变的 `latest`、别名或当前配置名称。凡是平台内
具有独立生命周期、可以发布新版本的受管对象，必须满足下列二选一规则：

1. RuntimeProfileSpec 直接保存该对象不可变的 Revision ID；或
2. RuntimeProfileSpec 保存有明确“逻辑引用”语义的名称，并由 Deployment 在发布时
   解析、校验和固定到不可变 Revision ID，再写入 RuntimeManifest。

Deployment 无法固定具体 Revision 时必须拒绝发布。rolling、latest 或自动跟随新版本
只能作为未来显式模式设计，不能由普通受管名称隐式获得。该规则适用于 Model、Tool、
Knowledge 和 Storage，不需要为此预建通用 ResourceRef 或资源依赖图。

外部 Provider 的原始模型名、Endpoint 等可以作为非秘密字符串字面量冻结，但这只
冻结平台保存的配置，不保证外部 Provider 的实现、路由或服务行为不变。V1
`postgres_state` 只冻结 `kind` 与 `dsn_ref`，不冻结 Session 或 Memory 业务数据；若
未来增加 Backend Identity、Namespace 或其他非秘密连接字段，必须把它们作为版本化
Schema 字段显式引入。

## 7. SecretRef

### 7.1 语义

SecretRef 是一个非秘密值对象：

~~~text
SecretRef = Tenant 范围内由目标 Environment 解析的、不包含 Secret Value 的逻辑名称
~~~

解析上下文至少包含：

~~~text
TenantID + Environment + SecretRef
~~~

因此不同 Tenant 使用同一名称，也不能解析到同一 Secret。

V1 使用与 Resource Key 相同的简单逻辑名称语法：

~~~regex
^[a-z][a-z0-9_-]{0,63}$
~~~

示例值：

~~~text
model-provider-primary
tool-search-credential
~~~

该语法只冻结 RuntimeProfileSpec 中的逻辑引用名称。Environment 如何把名称解析为
Secret、如何授权以及是否固定 Secret Value Version，仍由后续协议定义；URI、Secret
Manager 路径、云厂商 ARN 或数据库 ID 不属于 V1 SecretRef 字符串。

### 7.2 变化语义

- Spec Digest 包含 SecretRef 名称，不包含 Secret Value。
- 在同一 SecretRef 下轮换 Secret Value，不产生新的 ProfileRevision。
- 从一个 SecretRef 改为另一个 SecretRef 属于 Spec 变化，必须保存新的 Draft
  Revision 并发布新的 ProfileRevision。
- 同一 SecretRef 可以被一份 ProfileRevision 中的多个资源复用；字符串重复本身不是
  错误。若某个 Kind 对不同凭据角色有额外约束，由该 Kind 单独定义。
- Runtime Profile 发布只校验引用的格式和允许位置。
- Secret 是否存在、是否属于该 Tenant、是否允许在目标 Environment 使用以及是否可
  解析，都由 Deployment 兼容性校验负责。
- RuntimeManifest 未来只能携带经过最小化和授权的 SecretRef；由哪个运行时组件负责
  解析，仍由 Environment/Secret Resolver 协议确认。

ProfileRevision 只冻结 SecretRef，不冻结该引用背后的 Secret Value，因此它只保证
非秘密配置和引用身份可复现。Deployment/Run 是否固定实际 Secret Version、如何审计
轮换前后的执行，仍需在 Environment 与运行协议中单独决定，不能被本文默认为已经解决。

### 7.3 明文凭据边界

RuntimeProfileSpec 中禁止出现：

~~~text
api_key
password
token
authorization
credential
client_secret
access_key
secret_key
dsn
cookie
private_key
secret_value
~~~

Schema 不提供这些值字段；L0 还要递归检测已知敏感 Key。服务端不能可靠识别用户误填
在普通自由字符串中的所有 Secret，因此还必须：

- 尽量减少自由文本字段。
- 不接受任意 Map。
- HTTP、Application、审计和错误日志不记录完整 Spec。
- 诊断只返回 JSON Pointer 和安全文案，不回显原始值。
- Fixture 覆盖嵌套对象、大小写变体和常见凭据字段。

## 8. Resource 判别联合

四类资源都是严格的、由 `kind` 判别的关闭联合。V1 每类只接受一种 Kind：

~~~text
ModelResource     = oneOf(openai_compatible)
ToolResource      = oneOf(mcp_streamable_http)
KnowledgeResource = oneOf(qdrant_openai)
StorageResource   = oneOf(postgres_state)
~~~

统一规则：

1. `kind` 是 RuntimeProfileSpec Schema Version 内的稳定协议值。
2. 顶层、Resource 和嵌套值对象全部 `additionalProperties=false`。
3. 所有字段、类型、字符串和集合上限都由 Schema 固定，不使用 SDK 隐式默认。
4. Secret 只通过 Kind 明确允许的 SecretRef 字段表达。
5. Capability 固定或受关闭 Allowlist 约束，不表示 Provider 已连通。
6. 不接收 `map[string]any`、任意 JSON、Go Option 或 SDK 配置对象。
7. JSON Schema、Go 类型、Domain Validator 和 Fixture 共同约束相同字段。

这些 Kind 只是 Control API 已实现的配置协议。它们到 RuntimeManifest Adapter
Kind/Version 的映射以及 Worker 是否能执行尚未实现，必须由后续 Deployment/Worker
兼容性门禁显式确定；Worker 不直接读取 RuntimeProfileSpec。

## 9. Model Resource：`openai_compatible`

V1 Model 只接受 `kind=openai_compatible`：

| 字段 | 类型 | 必填 | V1 约束 |
| --- | --- | --- | --- |
| kind | string | 是 | 固定为 `openai_compatible` |
| model | string | 是 | 长度 1～256；保留 Provider 原始名称 |
| base_url | string | 是 | 长度 1～2048；HTTP/HTTPS、Host 非空，禁止 UserInfo、Query 和 Fragment |
| api_key_ref | SecretRef | 是 | `^[a-z][a-z0-9_-]{0,63}$` |
| capabilities | string[] | 是 | `chat`、`tool_call` 的非空子集，1～2 项、不得重复，并且必须包含 `chat` |

Model Resource 不包含 Prompt、Instruction、temperature、max_output_tokens、Agent 的
`model_slot`、任意 Header、明文 API Key、Environment Overlay 或 tRPC-Agent-Go
Option。发布只验证静态字段与 Capability，不探测 Endpoint、不调用模型，也不证明
Worker 已具有对应运行 Adapter。

## 10. Tool Resource：`mcp_streamable_http`

V1 Tool 只接受 `kind=mcp_streamable_http`：

| 字段 | 类型 | 必填 | V1 约束 |
| --- | --- | --- | --- |
| kind | string | 是 | 固定为 `mcp_streamable_http` |
| server_url | string | 是 | 长度 1～2048；HTTP/HTTPS、Host 非空，禁止 UserInfo、Query 和 Fragment |
| toolset_name | string | 是 | `^[a-z][a-z0-9_]{0,63}$` |
| tool_name | string | 是 | `^[a-z][a-z0-9_]{0,63}$` |
| auth | object | 是 | 关闭联合：`{"kind":"none"}` 或 `{"kind":"bearer","secret_ref":"..."}` |
| capability | string | 是 | 固定为 `web.search` |

V1 不接受平台内建 Tool、任意 HTTP Tool、本地 Command Tool、Tenant 上传代码、任意
Header/Query Map、自由请求模板或进程启动参数。Tool Grant、用户确认、审批、预算、
DLP、调用审计、在线健康和真实调用都不属于 RuntimeProfileSpec。

## 11. Knowledge Resource：`qdrant_openai`

V1 Knowledge 直接声明一个已存在的 Qdrant Collection 及其 OpenAI-compatible
Embedding 配置，只接受 `kind=qdrant_openai`：

| 字段 | 类型 | 必填 | V1 约束 |
| --- | --- | --- | --- |
| kind | string | 是 | 固定为 `qdrant_openai` |
| host | string | 是 | 长度 1～253 |
| port | integer | 是 | 1～65535 |
| tls | boolean | 是 | 必须显式提供 |
| collection | string | 是 | 长度 1～128 |
| qdrant_api_key_ref | SecretRef | 否 | 提供时符合 SecretRef 语法 |
| embedding | object | 是 | 关闭的嵌套对象，字段见下表 |

`embedding` 字段：

| 字段 | 类型 | 必填 | V1 约束 |
| --- | --- | --- | --- |
| model | string | 是 | 长度 1～256 |
| base_url | string | 是 | 长度 1～2048；HTTP/HTTPS、Host 非空，禁止 UserInfo、Query 和 Fragment |
| api_key_ref | SecretRef | 是 | 符合 SecretRef 语法 |
| dimensions | integer | 是 | 1～65536 |

该 Kind 的 Domain 类型固定提供 `knowledge.search` Capability。它不拥有原始文档、
上传、抓取、切块、Embedding 任务、索引创建或重建、查询结果和在线健康，也不接受
任意数据库查询、SQL、脚本或 SDK Option。

## 12. Storage Resource：`postgres_state`

V1 Storage 只接受 `kind=postgres_state`：

| 字段 | 类型 | 必填 | V1 约束 |
| --- | --- | --- | --- |
| kind | string | 是 | 固定为 `postgres_state` |
| dsn_ref | SecretRef | 是 | 符合 SecretRef 语法；完整 DSN 不进入 Spec |

该 Kind 的 Domain 类型固定提供 `storage.session` 和 `storage.memory` Capability。V1
没有 Artifact Storage Kind，也不创建数据库、执行迁移、管理备份或数据生命周期。
Storage 不是 AgentSpec Slot；后续 Deployment 必须通过 Runtime Role Binding 显式选择
它并决定如何进入 RuntimeManifest。

## 13. 顶层 Schema 与冻结上限

| 字段 | 类型 | 必填 | V1 约束 |
| --- | --- | --- | --- |
| schema_version | string | 是 | 固定为 `v1` |
| models | object | 是 | `map<ResourceKey, openai_compatible>`；最多 16 项 |
| tools | object | 是 | `map<ResourceKey, mcp_streamable_http>`；最多 64 项 |
| knowledge | object | 是 | `map<ResourceKey, qdrant_openai>`；最多 32 项 |
| storage | object | 是 | `map<ResourceKey, postgres_state>`；最多 16 项 |

四个集合都可以为空，但必须显式出现。RuntimeProfileSpec 原始 JSON 最大
524288 字节（512 KiB）。Resource Key 和 SecretRef 使用
`^[a-z][a-z0-9_-]{0,63}$`；Tool 名称使用 `^[a-z][a-z0-9_]{0,63}$`。URL 最长
2048，Model 名最长 256，Host 最长 253，Collection 最长 128。

V1 Schema 不定义默认 Resource、Provider、Endpoint、Capability 或认证方式。客户端
必须显式提交所有必填字段；服务端只对 Capability 集合排序并进行 JSON
Canonicalization，不补充运行配置。当前 Kind 没有跨 Resource 引用，因此没有引用深度
或闭包数量限制。

## 14. 禁止开放式配置与越界字段

禁止的是未版本化、未由具体 Kind Schema 限定的 config、settings、parameters、
options、metadata 等开放 Map，而不是这些英文单词本身。某个具体 Kind 只有在满足：

- additionalProperties=false；
- 子字段名称、类型和上限固定；
- 运行语义清楚；
- 不直接透传 SDK；
- 经过 Schema、Domain 和 Fixture 验证；

时，才可以使用一个语义明确的嵌套对象。

以下逃生口始终禁止：

~~~text
map<string, any>
arbitrary JSON
provider_options / sdk_options / runtime_options
arbitrary headers / query parameters
~~~

以下越界字段不属于 RuntimeProfileSpec：

~~~text
environment
environment_overrides
agent_id
agent_version_id
model_slot
tool_slot
knowledge_slot
active
deployed
runtime_manifest
secret_value
~~~

禁止结构：

~~~json
{
  "kind": "anything",
  "config": {
    "arbitrary": "value"
  }
}
~~~

禁止只在 Application 层补救开放 Schema。JSON Schema、Go 类型、Domain Validator
和 Fixture 必须共同限制同一组字段；后续 Deployment Mapper 只能消费已经通过这些
边界的类型化资源。

## 15. 可修改性边界

| 数据 | Draft 中可修改 | 发布后可修改 | 是否进入 Spec Digest |
| --- | --- | --- | --- |
| schema_version | 只能选择服务端支持版本 | 否 | 是 |
| models、tools、knowledge、storage | 是 | 否 | 是 |
| Resource Key | 通过删除旧 Key、增加新 Key 修改 | 否 | 是 |
| kind 与 Kind 叶子字段 | 是 | 否 | 是 |
| Capability | 是 | 否 | 是 |
| SecretRef 名称 | 是 | 否 | 是 |
| Secret Value | 不属于 Spec | 不属于 Spec | 否 |
| Runtime Profile name、description | 通过元数据用例修改 | 可以 | 否 |
| Draft Revision | 否，服务端递增 | 不适用 | 否 |
| Profile Revision Number | 否，服务端分配 | 否 | 否 |
| Spec Digest | 否，服务端计算 | 否 | 结果本身 |
| Agent Slot Binding | 不属于 Spec | 通过新 DeploymentRevision 修改 | 否 |
| Environment | 不属于 Spec | 通过新 DeploymentRevision 修改 | 否 |

修改已发布 ProfileRevision 的正确流程是：将其 Spec 显式复制到当前 Draft、保存新的
Draft Revision、完成修改并发布新的 ProfileRevision。旧 Revision 的 spec_jsonb
永远不能更新。

“复制旧 Revision 到 Draft”不是 V1 必需的独立 API；客户端可以通过现有读写接口
完成。是否增加显式 Restore/Clone 用例留到真实交互需求出现后。

## 16. Validation 分层

架构区分四个确定性层级。当前 runtimeprofile 子领域已实现 L0、L1、L2；L3 属于
尚未实现的 Deployment 发布切片。

### 16.1 L0：传输与 Draft 内容安全校验

SaveProfileDraft 在持久化前执行，目的只是安全保存内容，不要求 Draft 已经可以发布。

检查：

1. 请求体可以按 UTF-8 JSON 解析。
2. 顶层值必须是 Object。
3. JSON Object 中不允许重复 Key。
4. 原始文档不得超过已冻结的请求上限。
5. 递归拒绝已知 Credential Key。
6. HTTP、Application、审计和错误日志不得记录完整 Spec 请求体。

L0 成功只代表文档内容可以安全保存。空对象、缺少 schema_version 或资源尚未配置的
Draft 仍可保存。Expected Draft Revision 的 Compare-And-Swap 属于 Application/Store
并发条件：冲突返回普通 409 错误，不进入 Validation Report；任何失败都不递增 Draft
Revision。

### 16.2 L1：JSON Schema 校验

ValidateProfileDraft 和 PublishProfileRevision 都执行 L1：

- schema_version 必须受支持。
- 顶层四个资源集合必须存在且类型正确。
- 所有对象 additionalProperties=false。
- Resource Key、Capability 和 SecretRef 满足各自格式与数量限制。
- Capability 数组通过 uniqueItems 或等价规则拒绝重复。
- kind 必须选择服务端支持的严格判别联合。
- 必填字段存在，字符串、数组、URL 和数值在上限内。
- SecretRef 只能出现在各 Kind 明确允许的位置。
- URL 禁止 UserInfo；Kind 不允许任意 Header 或 Query Secret。
- 未知字段失败，不静默丢弃。

Draft 保存不要求通过 L1；发布必须通过。

### 16.3 L2：Runtime Profile 领域语义

L2 不访问 Agent、Environment、Secret Backend、Provider 或 Worker：

1. Resource Capability 必须符合该 Kind 的固定集合或受控 Allowlist。
2. 当前四种 Kind 没有跨 Resource 类型化引用；V1 不执行资源引用解析。
3. 同一 SecretRef 可以被多个资源复用。
4. 不允许跨 Profile 或指向可变 Draft 的引用。
5. 不允许 Agent Slot、Environment、Deployment 或 RuntimeManifest 字段。
6. 不执行网络请求，不解析 Secret Value，不构造运行对象。
7. V1 不预建通用资源引用图、环检测或依赖闭包规则。

### 16.4 L3：未来 Deployment 兼容性

L3 不属于 Runtime Profile 发布，将由 Deployment 发布执行：

- AgentVersion 的全部 Model/Tool/Knowledge Slot 是否具有显式 Binding。
- Deployment 所需 Session/Memory/Artifact 等 Runtime Role 是否显式绑定到 Storage。
- Binding 的目标 Resource 是否存在于所选 ProfileRevision。
- 经 Kind 规则校验的 Resource Capability 是否满足 Agent Requirement。
- Agent Generation 参数是否被具体 Model 支持。
- 所需 SecretRef 是否能在目标 Environment 中解析且允许使用。
- 非秘密 Endpoint 是否通过目标 Environment 的静态 Endpoint/Egress Policy。
- 所有指向版本化受管配置的逻辑引用能否固定为不可变、可审计的具体 Revision。
- Deployment Compiler 是否明确支持所选 ProfileRevision Schema Version，并能把实际
  绑定的 Resource Kind 编译为 RuntimeManifest Adapter Kind/Version。
- 目标 Worker 是否明确支持生成的 RuntimeManifest Schema Version，以及其中的运行
  Adapter Kind/Version。
- 若具体 Kind 定义了类型化依赖，这些依赖是否完整。
- 是否能生成最小、完整、不可变的 RuntimeManifest。

### Operational Preflight：运行诊断，不属于上述 Validation 层级

Provider 登录、Tool Endpoint 健康、Knowledge 查询和真实模型请求属于显式运行诊断
或 Deployment Preflight。它们具有瞬时性，不能成为 ProfileRevision 发布事务的一
部分。

## 17. Validation Report

所有可预期的文档问题都返回结构化诊断，不把裸 Go Error 文本作为 API 协议。

~~~json
{
  "valid": false,
  "schema_version": "v1",
  "draft_revision": 4,
  "diagnostics": [
    {
      "code": "RUNTIME_PROFILE_SPEC_SENSITIVE_FIELD",
      "severity": "error",
      "pointer": "/models/primary/api_key",
      "resource_kind": "model",
      "resource_key": "primary",
      "message": "RuntimeProfileSpec 不能包含明文凭据字段。"
    }
  ]
}
~~~

字段：

| 字段 | 说明 |
| --- | --- |
| valid | 没有 Error 时为 true；Warning 不影响 |
| schema_version | 可识别的请求 Schema Version；缺失或无法识别时返回空字符串 |
| draft_revision | 本次验证对应的 Draft Revision |
| diagnostics | 排序稳定的 Error 与 Warning |
| code | 稳定机器码，不随中文文案变化 |
| severity | error 或 warning |
| pointer | RFC 6901 JSON Pointer |
| resource_kind | model、tool、knowledge、storage 或 null |
| resource_key | 问题属于具体 Resource 时为 Key，否则为 null |
| message | 面向用户的安全说明，不作为客户端逻辑判断依据 |

所有字段都必须出现在响应中；resource_kind 与 resource_key 是 required + nullable。
当前 OpenAPI 中 schema_version 是普通 string，不限定为 enum: [v1]，因此缺少或非法
版本的诊断响应仍符合响应协议。

诊断按 pointer、severity、code、resource_kind、resource_key 排序。同一输入和同一
Validator Version 必须返回相同顺序。每个输入问题只能由一个校验层拥有；同一 JSON
Pointer 不得因 Schema 与 Domain 重复检查而返回两条等价诊断。

## 18. 稳定诊断码

### 18.1 Draft 内容安全

| Code | Severity | 含义 |
| --- | --- | --- |
| RUNTIME_PROFILE_SPEC_INVALID_JSON | error | 不是合法 JSON |
| RUNTIME_PROFILE_SPEC_DUPLICATE_KEY | error | JSON Object 包含重复 Key |
| RUNTIME_PROFILE_SPEC_DOCUMENT_REQUIRED | error | 顶层不是 Object |
| RUNTIME_PROFILE_SPEC_DOCUMENT_TOO_LARGE | error | 超过文档大小限制 |
| RUNTIME_PROFILE_SPEC_SENSITIVE_FIELD | error | 出现禁止的 Credential 字段 |

### 18.2 Schema

| Code | Severity | 含义 |
| --- | --- | --- |
| RUNTIME_PROFILE_SPEC_UNSUPPORTED_VERSION | error | Schema Version 不受支持 |
| RUNTIME_PROFILE_SPEC_REQUIRED_FIELD | error | 缺少必填字段 |
| RUNTIME_PROFILE_SPEC_UNKNOWN_FIELD | error | 出现 V1 未定义或越界的字段 |
| RUNTIME_PROFILE_SPEC_INVALID_TYPE | error | 字段类型错误 |
| RUNTIME_PROFILE_SPEC_INVALID_IDENTIFIER | error | Resource Key 或 Tool 名称非法 |
| RUNTIME_PROFILE_SPEC_DUPLICATE_CAPABILITY | error | Capability 重复 |
| RUNTIME_PROFILE_SPEC_SECRET_REF_INVALID | error | SecretRef 格式非法 |
| RUNTIME_PROFILE_SPEC_LIMIT_EXCEEDED | error | 长度、数量或数值超过限制 |
| RUNTIME_PROFILE_SPEC_UNSUPPORTED_KIND | error | Resource Kind 不受支持 |
| RUNTIME_PROFILE_SPEC_INVALID_URL | error | URL 非法、非 HTTP(S)、缺少 Host，或包含 UserInfo、Query、Fragment |

additionalProperties=false 发现的普通未知字段和 agent_id、environment 等已知越界
字段都统一映射为 RUNTIME_PROFILE_SPEC_UNKNOWN_FIELD，同一 Pointer 不再额外返回
“forbidden boundary”诊断。SecretRef 只有出现在 Kind 允许位置且值格式非法时才映射为
RUNTIME_PROFILE_SPEC_SECRET_REF_INVALID；出现在不允许位置时仍是 UNKNOWN_FIELD。

### 18.3 领域语义

| Code | Severity | 含义 |
| --- | --- | --- |
| RUNTIME_PROFILE_SPEC_CAPABILITY_KIND_MISMATCH | error | Capability 不在 Kind 的固定集合或 Allowlist |

若以后某个具体 Kind 引入类型化资源引用，应随该 Kind 的 Schema 增加职责明确的稳定
诊断，而不是预先建立通用 REF_NOT_FOUND、REF_CYCLE 或 UNUSED_RESOURCE。

## 19. Canonicalization 与 Digest

发布按固定顺序执行：

~~~text
Parse without duplicate keys
        ↓
L1 JSON Schema Validation
        ↓
L2 Domain Validation
        ↓
Semantic Normalization
        ↓
RFC 8785 JSON Canonicalization Scheme
        ↓
SHA-256
        ↓
spec_digest = sha256:<lowercase-hex>
~~~

V1 语义规范化规则：

- models、tools、knowledge、storage 是 Object，Key 顺序由 JCS 规范化。
- Capability 是集合，发布前按字典序排序。
- Schema 已禁止重复元素；Canonicalizer 不静默去重。
- V1 只有 Model `capabilities` 是集合型数组；当前没有具有执行或优先级语义的数组。
- 不自动 Trim 或改写模型名、Endpoint、Resource Key 或其他用户值。
- 不注入 SDK、Provider 或环境默认值。
- Runtime Profile 元数据、Draft Revision、Profile Revision Number、时间戳和发布者
  不进入 Spec Digest。
- SecretRef 名称进入 Digest，Secret Value 永远不进入。
- Canonicalizer 与 Validator 必须由 Golden Fixture 固定。

相同内容允许从不同 Source Draft Revision 发布为不同业务 Revision，因此 Digest 不
建立唯一约束。

任何向调用方、Deployment Compiler 或其他子领域暴露完整 ProfileRevision Spec
的读取，都必须重新执行 Canonicalization，并核对 Schema Version 与 Spec Digest。
Revision 列表只返回不含 Spec 的 ProfileRevisionSummary；该元数据投影不得加载
`spec_jsonb`，也不得执行完整 Spec Canonicalization。

## 20. Save、Validate 与 Publish 行为

### 20.1 SaveProfileDraft

- 执行 L0。
- 使用 Expected Draft Revision 做 Compare-And-Swap。
- 成功后 spec_revision 加一。
- 可以保存尚未通过 L1/L2 的文档。
- 不计算正式 Spec Digest，不创建 ProfileRevision。
- 不访问 Provider、Secret Backend、Agent 或 Deployment。

### 20.2 ValidateProfileDraft

- 校验一个确定的 Draft Revision。
- 执行 L1 和 L2。
- 返回完整 RuntimeProfileValidationReport。
- valid=false 时 HTTP 仍为 200。
- 不修改 Draft，不创建 ProfileRevision；除读取身份、Membership 和 Draft 所需的
  Control API 持久化外，不访问 Provider、Secret Backend、Worker 或运行面网络。

### 20.3 PublishProfileRevision

- 必须携带 Expected Draft Revision。
- 先按 Source Draft Revision 检查已发布记录，保证延迟重试幂等。
- 执行 L1、L2、Canonicalization 和 Digest。
- 任何 Error 都阻止发布；Warning 不阻止。
- 在事务内重新检查幂等记录和当前 Draft Revision。
- 首次发布返回 201；同一 Source Draft Revision 重试返回 200 和原 Revision。
- 发布后 Draft 仍可继续修改。
- 已发布 ProfileRevision 的 Spec 永远不能更新或删除。
- 不发布 NATS 事件，不创建 Deployment 或 RuntimeManifest。

## 21. 版本化 Schema 与 Fixture

已实现并版本化以下协议资产：

~~~text
api/schemas/runtimeprofile/v1/
├── runtime-profile-spec.schema.json
├── embed.go
├── README.md
└── examples/
    ├── valid/
    │   ├── minimal.json
    │   ├── model-tool.json
    │   └── knowledge-storage.json
    └── invalid/
        ├── unknown-field.json
        ├── plaintext-secret.json
        ├── duplicate-capability.json
        ├── invalid-secret-ref.json
        ├── missing-chat.json
        ├── unsupported-kind.json
        ├── url-query.json
        └── url-userinfo.json
~~~

Runtime Profile V1 的有效 Fixture 已经由测试确认能够通过公开 JSON Schema、被 Go
Domain 类型解析并通过 L1/L2；无效 Fixture 固定对应的诊断边界。
RuntimeManifest 映射与真实 Worker 适配的证据属于后续 Deployment/Worker 兼容性门禁，
不阻塞 Runtime Profile 纵向切片独立完成。

## 22. Schema、Go 类型与持久化一致性

当前一致性证据包括：

1. 每个 valid Fixture 通过公开 JSON Schema、Go L1/L2 Validator 和类型化 Round Trip。
2. 每个 invalid Fixture 以预期稳定诊断码失败，Schema-only 与 Domain-only 边界被显式测试。
3. L0 单独验证可安全保存，允许尚不满足 L1/L2 的 Draft。
4. 未知字段、重复 Key、明文 Credential、非法 SecretRef、Kind、URL、Capability 和上限失败。
5. Object Key 顺序、Model Capability 集合顺序和等价整数写法不改变 Canonical Document
   与 Digest。
6. Golden Canonical Document 与 SHA-256 Digest 已固定。
7. Application 在发布、单项完整读取、发布幂等返回以及任何向 Deployment Compiler
   或其他子领域暴露完整 ProfileRevision Spec 的读取时重新 Canonicalize，并核对
   Schema Version 与 Spec Digest。
8. Revision 列表使用不含 `spec_jsonb` 的 ProfileRevisionSummary 专用投影，不加载
   完整 Spec，也不执行完整 Spec Canonicalization。
9. HTTP 测试固定 10 个路由、Revision 列表 Summary 与单项完整响应、严格请求解析、
   结构化诊断以及首次发布 201/幂等重试 200。
10. PostgreSQL 集成测试覆盖 CTE 创建回滚、Draft CAS、Revision 不可变 Trigger、发布事务
   回滚、Revision Number 递增、并发单发布和强延迟重试。
11. OpenAPI 的 `RuntimeProfileSpec` 直接引用版本化公开 JSON Schema，并以不同 Schema
    区分包含 Spec 的 RuntimeProfileRevision 与不包含 Spec 的
    RuntimeProfileRevisionSummary。

后续 Deployment/Worker 切片还必须独立验证：Deployment Compiler 对不支持的
ProfileRevision Schema Version/Resource Kind 的显式拒绝、每个 Resource Kind 到
RuntimeManifest Adapter Kind/Version 的映射、Worker 对不支持的 RuntimeManifest Schema
Version/Adapter Kind/Version 的显式拒绝，以及 Manifest 只包含显式绑定资源、具体 Kind
明确定义的必要依赖和最小 SecretRef。这些不是 Runtime Profile V1 的实现完成条件。

## 23. Schema 演进

- v1 一旦接受并进入已发布 ProfileRevision，其语义不得静默修改。
- V1 采用严格关闭的 oneOf；默认情况下增加 Resource Kind 需要发布新的
  RuntimeProfileSpec Schema Version。
- 如果未来希望在同一 Schema Version 下扩展 Kind，必须先设计独立、版本化的 Kind
  Registry 和解析机制，并通过 ADR 替代上述默认规则。
- 破坏性字段变化发布新 Schema Version，不修改历史 ProfileRevision。
- Draft 可以通过显式、可测试且用户确认的迁移转换到新版本。
- 已发布 ProfileRevision 不原地迁移。
- Deployment Compiler 必须按 ProfileRevision 中记录的 Schema Version 和 Resource Kind
  判断能否编译，并把它们显式映射为版本化 RuntimeManifest。
- Worker 只按 RuntimeManifest Schema Version 和其中的运行 Adapter Kind/Version 判断
  能否执行；它不读取 ProfileDraft，也不直接消费整份 ProfileRevision。
- 不受支持的 Profile Schema/Resource Kind 由 Deployment 拒绝，不受支持的 Manifest
  Schema/Adapter Kind/Version 由 Worker 拒绝，任一侧都不能静默回退。
- Capability Registry 若后续引入，必须版本化，不能改变历史字符串的含义。

## 24. 后续兼容性门禁

Runtime Profile V1 的字段、Kind、10 个 HTTP API、诊断结构和强延迟发布幂等已经冻结并
实现。以下问题仍必须在对应切片开始前确认，不能通过修改既有 V1 语义来隐式解决：

1. Agent Slot 和 Runtime Role 到 Profile Resource 的 Deployment Binding 结构。
2. Environment 对 SecretRef 的解析、授权与 Secret Value Version 审计。
3. 四种 Profile Resource Kind 到 RuntimeManifest Adapter Kind/Version 的显式映射。
4. Deployment Compiler 支持的 Profile Schema/Kind 矩阵。
5. Worker 支持的 RuntimeManifest Schema/Adapter Kind/Version 矩阵。
6. 非秘密 Endpoint 的 Environment Egress Policy 与 Operational Preflight。
7. 新 Resource Kind 默认发布新 RuntimeProfileSpec Schema Version；若要在 v1 内扩展，
   必须先通过 ADR 引入独立、版本化的 Kind Registry。
