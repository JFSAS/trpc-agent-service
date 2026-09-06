# Deployment V1：两份版本、同名匹配与不可变执行快照

- **设计状态**：2026-09-04 用户确认的简化方向已接受；下文给出后续实现契约。
- **实现状态**：Deployment 业务尚未实现；本设计不计入现有业务 API。Profile 前置能力已实现。
- **本地核验工作树**：`/Users/jfs/Projects/trpc-agent-service-channel-gateway`。
- **核验分支 / HEAD**：`codex/channel-gateway` / `bf107766be72cd7fcaa9e878428b97a6aa05260b`。
- **代码基线**：2026-09-05 对齐的 Agent V1、Runtime Profile V1 与直接凭据纵切；未提交的
  Deployment/Gateway 设计保留，未把规划接口计入已实现范围。
- **替代关系**：替代此前 EnvironmentRevision、用户 Binding、SecretVersionHandle，以及用户维护 SecretRef 的方案。
- **相邻契约**：[上位约束](../constraints.md)、[Agent](agent.md)、[AgentSpec](agent-spec.md)、
  [Runtime Profile](runtime-profile.md)、[RuntimeProfileSpec](runtime-profile-spec.md)、
  [Profile 凭据](runtime-profile-credentials.md)。Profile 字段、管理授权与加密语义以这些
  专属契约及实际代码为准，本文只定义 Deployment 消费与编译边界。

本工作树已对齐 Profile 直接录入纵切：Write/Canonical/Read、私有加密表、COW/live/CAS、
`CheckUsable`、`ResolveForAttempt` 与可选 runtime HTTP Adapter 已实现。真实 Deployment
Checker Adapter、Run/Attempt 授权拥有方、可信 Workload 认证与 Worker 接线仍待完成。
当前单一 V1 不维护开发期 ref-only Reader、旧 Digest 或旧数据兼容分支；正常业务中的
不可变 Revision、Manifest 与幂等语义继续保留。

## 1. 结论与用户路径

```text
AgentVersion + ProfileRevision
        ↓
Deployment 校验与编译：按类别和名称精确匹配
        ↓
DeploymentRevision + RuntimeManifest
        ↓
同事务 PostgreSQL Outbox → Relay → 运行投影
```

用户在 Runtime Profile 的资源表单直接填写 API Key、Token 或 DSN；Profile 内部保存凭据，
不要求先建 Secret、不展示用户需维护的 SecretRef。发布 Deployment 时只选择两份确定版本，
不管理额外环境对象，不填写 Slot → Resource 映射表：

| 用途 | AgentVersion | ProfileRevision | 发布结果 |
| --- | --- | --- | --- |
| 客服测试部署 | 客服 Agent v3 | 测试 Profile Revision 2 | 测试 Deployment 的不可变 Revision |
| 客服生产部署 | 同一客服 Agent v3 | 生产 Profile Revision 5 | 生产 Deployment 的不可变 Revision |

测试 / 生产只是名称和配置用途，不是必须创建的第三个实体，也不构成隔离机制。
隔离来自 Tenant / Profile 内部凭据授权、平台执行范围和 Worker 的运行适配。

**V1 不引入 Environment 业务对象**：无实体、生命周期、管理 API、发布必填环境 ID、
隐藏默认 Environment、Overlay、继承或深层合并。平台执行后端、网络范围和资源上限来自
平台部署配置；它们不是用户需要选择的另一个业务对象。

没有 Environment 业务对象，不等于没有 Worker 执行环境。进程、工作区、CodeExecutor、
凭据解析和资源限制仍由 Worker Adapter 管理；这也不意味着 V1 已接入命令执行能力。

保留稳定 Deployment 身份和元数据，直接 Validate / Publish 完整 Input；V1 没有服务端
DeploymentDraft。客户端未完成编辑由客户端保存，未来有真实共享工作副本需求时再设计 Draft。

## 2. 已有事实与本次设计增量

| 当前事实 | Deployment 设计含义 |
| --- | --- |
| AgentSpec 只有 `requirements.models/tools/knowledge`；节点用 `model_slot/tool_slots/knowledge_slots` | 直接消费已有需求声明，不增加 Storage Slot 或新 Agent 字段 |
| 未被节点引用的 Requirement 只产生 Warning，仍可发布 AgentVersion | 区分声明契约集与执行资源闭包，未使用声明不构成节点授权 |
| Runtime Profile 已实现资源 Schema、校验、不可变发布与完整性复核 | Profile 发布成功不等于任何 Agent 均可部署；Deployment 继续做配对校验 |
| 当前 Tool Kind 仅 `mcp_streamable_http`，Capability 仅 `web.search` | “已实现 MCP 网页搜索资源”指配置协议，不代表已实现 MCP Worker Adapter |
| Model / Knowledge / Storage Kind 分别为 `openai_compatible` / `qdrant_openai` / `postgres_state` | Schema 接受 Kind 与平台已接入可执行 Adapter 是两项独立事实 |
| Profile 已实现直接 Write/Canonical/Read 分离、私有加密表、COW/live/CAS、CheckUsable 和 ResolveForAttempt | Deployment 复用真实 Profile Owner 能力；默认内部路由、真实执行授权和 Worker 接线仍待完成 |
| Deployment 只有五个 `doc.go` 和空 `wiring.go` | 没有当前已实现 Deployment API、Compiler、Manifest、Outbox 或 Relay |
| Control 自身迁移仍以 `0001_baseline.sql` 为基线；工作树另有 Gateway 自有 0001–0007 迁移及 RouteProjection / RunRequested / ReplyIntent Final 契约与生成 DTO | Gateway 入站 Outbox 已存在；Deployment 业务表、RuntimeManifestPublished 与 Control 发布 Relay 仍未实现 |
| `services/` 已有 `control-api` 与 Gateway 四 Module；Connection、Final Ledger 与 Runtime 已有实现 | 默认 App 仅新增 Delivery Maintenance；Reply Consumer、生产发送 Runner、真实 Execution/Worker 与完整 IM E2E 仍待交付，详见[Gateway 实施状态](../channel-gateway/implementation-status.md) |

可复核来源包括：`services/control-api/internal/agent/domain/semantic_validation.go`、
`services/control-api/internal/runtimeprofile/domain/spec.go`、两份 JSON Schema、
`services/control-api/internal/deployment/wiring.go`、`api/events/` 与 Migration 目录。

组合 Fixture 必须从当前 Agent 与 Profile 的版本化 Fixture 中显式选择并验证同类别同名
契约；不同资源 key 不会因远端 `tool_name` 相同而自动匹配。独立 Schema Fixture 不等于
Deployment 组合兼容性证据，同名缺失必须得到稳定诊断，不自动回退。

## 3. 领域语言、实体与版本关系

| 术语 | 精确定义与所有者 |
| --- | --- |
| Deployment | Tenant 内稳定的部署身份，包含名称、描述、元数据修订号与最近发布序号；由 Deployment 拥有 |
| DeploymentRevision | 一次成功发布的不可变事实，固定 AgentVersion、ProfileRevision、Input Digest 和对应 Manifest；由 Deployment 拥有 |
| RuntimeManifest | Worker 执行所需的确定配置与节点分配快照；不是完整 Profile、Draft 或待解析用户表单 |
| 同名匹配 | Deployment 按类别和资源 key 解析需求的编译规则；不是 Capability 搜索或用户映射表 |
| 已解析关系 | Compiler 产生的 Slot / Resource / 节点关系；属于输出，不属于用户输入 |
| Runtime Role | Session / Memory 等运行数据服务职责；不是 Agent Tool / Knowledge Slot |
| Profile 内部凭据 | Runtime Profile 拥有的加密记录与生命周期，不是独立用户业务对象；用户通过资源字段管理 |
| CredentialID | 服务端生成的不透明内部身份，绑定 Tenant、Profile、资源实例、用途和目标范围；不是用户 SecretRef 或持有即授权的 token |
| 凭据轮换 | 显式更新同一内部 ID 的值，推进独立 Credential CAS；不同于 Draft 的写时复制替换 |
| 生效版本 | 某个 ChannelBinding 选择的精确 DeploymentRevision；不是 Deployment 的全局 active 指针 |

```text
Tenant
 ├─ Agent → AgentVersion n ───────┐
 ├─ RuntimeProfile → Revision m ─┼─ DeploymentRevision k → RuntimeManifest（1:1）
 └─ Deployment ─────────────────┘

ChannelBinding → 精确 DeploymentRevision k
```

- Deployment 只读 Agent / Runtime Profile 拥有者的完整不可变 Query，不跨模块查询源 SQL。
- 源对象读取要复核内部 Canonical Content、Schema Version、Digest 和 Tenant；Summary 或公开脱敏 View 不用于编译。
- DeploymentRevision 与 RuntimeManifest 是一对一发布记录，不是两套独立 CRUD。
- Manifest 表持有唯一 `deployment_revision_id`；不建立双向循环外键。
- Deployment 的 `latest_revision_number` 是历史发布进度，不代表流量当前生效版本。
- 旧版本被 ChannelBinding 或历史 Run 引用时保留；V1 不引入删除生命周期。

## 4. 最小用例、发布输入与状态变化

### 4.1 Create / Update / Validate / Publish

| 用例 | 输入 | 状态变化 |
| --- | --- | --- |
| CreateDeployment | 名称、描述，`Idempotency-Key` | 创建稳定身份，`metadata_revision=1`，`latest_revision_number=null` |
| UpdateDeploymentMetadata | 名称、描述，`expected_metadata_revision` | 仅元数据 CAS +1，不改变 Revision、Manifest 或 ChannelBinding |
| ValidateDeploymentRevision | 完整两来源 Input | 只读 Report；没有持久草稿、Receipt 或发布记录 |
| PublishDeploymentRevision | 完整 Input，Expected Latest，`Idempotency-Key` | 原子新增 Revision、Manifest、Outbox、Receipt，推进 Latest |
| Get / List Deployment、Get / List Revision | Tenant 与稳定 ID / 精确 Revision Number | 只读；Revision List 为摘要投影 |
| SwitchChannelBindingRevision | ChannelBinding ID、目标精确 Revision、Binding CAS | 后续 ChannelBinding 用例，更新路由选择，不改历史 DeploymentRevision |

名称建议沿用现有元数据规则：去除首尾空白、非空、最多 128 Unicode code points；描述最多
4096 code points。不承诺名称唯一；稳定 ID 唯一。语义无变化的 PATCH 返回原对象且不增加
metadata_revision，过期 CAS 仍返回冲突。输入大小限制在平台契约中统一冻结。

### 4.2 Publish 请求

`TenantID` 来自经验证的 Session + Membership 与路径，不从下面的 Body 建立可信身份。
`deployment_id` 来自路径；首次 `expected_latest_revision_number` 为 null，后续为精确正整数。
`Idempotency-Key` 是 Header，不是 Input 字段。

```json
{
  "expected_latest_revision_number": null,
  "input": {
    "schema_version": "v1",
    "agent": {
      "agent_id": "11111111-1111-4111-8111-111111111111",
      "version_number": 3
    },
    "profile": {
      "profile_id": "22222222-2222-4222-8222-222222222222",
      "revision_number": 2
    }
  }
}
```

Input 是关闭协议，只接受 `schema_version`、`agent`、`profile`；嵌套字段如例所示。
没有 Environment 引用、`tool_bindings`、`model_bindings`、`knowledge_bindings`、用户 Runtime
Role 映射或任意 Worker Option。未知字段、重复 JSON Key、null ID、非正整数版本、Draft / latest
选择器均拒绝。ID 与版本号沿用源拥有者协议，不从用户提供的 Digest 判断源可信性。

Validate Body 为同一 `input`，不接收 Expected Latest 或 Idempotency Header。
Validate 返回结果不预留版本，不产生可供 Publish 绕过校验的 token；Publish 重新取得事实。

### 4.3 状态和生效切换

```text
Create → Deployment（Latest=null）
Publish P2 → DeploymentRevision 1 + Manifest 1（Latest=1）
Publish P5, expected=1 → DeploymentRevision 2 + Manifest 2（Latest=2）
ChannelBinding 从 Revision 1 改指 Revision 2 → 新 Run 使用 Revision 2
ChannelBinding 改回 Revision 1 → 后续新 Run 回滚到 Revision 1
```

发布不自动切流。切换由 ChannelBinding 所有者验证 Tenant、目标存在性、可用 Manifest 与
Binding CAS，在自身事务内写 Binding + Outbox；不复用 Deployment 的 Latest CAS。
这些是后续最小切换语义，不是本次新增的第九个 Deployment HTTP 路由。

控制面 Binding 更新成功与运行投影已应用是两种状态。Gateway 仅在同一 Tenant 的目标 Manifest
已经可用且 Digest 验证通过时接纳对应新 Revision；乱序到达时等待 / 重试，不猜 latest，也不
静默回退。Gateway Admission 固定 Manifest 身份；已接纳 Run 和其重试沿用旧快照。
同一个 Deployment 的不同 Channel 可以同时使用不同 Revision。

## 5. 同名资源匹配算法

### 5.1 声明契约集与执行资源闭包

```text
声明契约集 D = AgentVersion.requirements 的全部 models / tools / knowledge
节点使用集 U = 所有实际节点 model_slot / tool_slots / knowledge_slots 的并集
执行资源闭包 C = Resolve(U) + 所选运行角色 + 这些资源的必要内嵌依赖
```

D 中每项均执行同名存在性与 Capability 校验；未被任何节点引用的声明产生稳定 Warning，
不因此挂载工具。C 才进入 Manifest，并接受执行 Adapter、内部凭据使用权、网络与资源上限校验。
Profile 中不属于 D 的额外资源不参与候选搜索；不属于 C 的资源不被复制或授权。

源 Profile 的完整性 / Schema 错误仍由完整 Query 拒绝，这与忽略有效的多余资源不同。

### 5.2 确定性步骤

```text
1. 读取已授权、固定且完整性通过的 AgentVersion 与 ProfileRevision。
2. 按类别 models → tools → knowledge，按资源 key 字典序遍历 D。
3. 对每个 requirement(category, name)，只查 Profile[category][name]。
4. 不存在 → DEPLOYMENT_RESOURCE_MISSING；不枚举替代候选。
5. 存在 → 使用已冻结 Profile Kind 规则取得 Provided Capabilities。
6. Model 要求 required ⊆ provided；Tool / Knowledge 要求对应单一 capability 被满足。
7. 不满足 → DEPLOYMENT_CAPABILITY_MISMATCH；同名不代表有相同能力。
8. 根据每个节点的 Slot 列表构造已解析节点配置，计算 U，再扩展为 C。
9. 为 C 选择已接入 Adapter，展开必要依赖，执行静态范围 / 上限检查并输出 credential uses。
10. 生成最小 Manifest，规范化后计算 Digest；纯 Compiler 不读取动态凭据状态。
11. Application 单独调用 ProfileCredentialChecker，将结果作为 Report / 发布准入条件；
    全部 Error 消失才允许提交发布，动态检查结果不进入 Compiler 输入或 Content。
```

精确名称比较遵循源协议的小写标识符；不做模糊匹配、大小写折叠、同义词、Capability
候选挑选或类别间回退。`models.primary` 只匹配 `models.primary`，`tools.search` 只匹配
`tools.search`，`knowledge.docs` 只匹配 `knowledge.docs`。

Profile Tool 的资源 key 与 MCP `tool_name` / `toolset_name` 分别表示平台资源身份和远端
调用名称。远端名称不同不影响同名资源 key 规则；它只是已选资源的具体配置。

### 5.3 Capability 与依赖

| 类别 | 本基线资源及 Provided 规则 | Compiler 要点 |
| --- | --- | --- |
| Model | `openai_compatible` 的 `capabilities` 数组；至少 chat | 检查 Agent 显式需求；节点最终有 callable entry 时还要满足 tool_call |
| Tool | `mcp_streamable_http`，仅 web.search | 精确资源 key + Capability，再检查平台是否接入这个 Adapter |
| Knowledge | `qdrant_openai` 推导 knowledge.search | 保留 Qdrant endpoint / collection、可选 key 引用及内嵌 Embedding 全部必需字段 |
| Storage | `postgres_state` 推导 storage.session / storage.memory | 按第 6 节运行角色处理，不进入 Slot 算法 |

Knowledge 的 Embedding 在资源内部，不是 `models` 表里的另一个 Slot；C 必须包含其 model、
base_url、dimensions、所需内部凭据关联，以及 Qdrant host / port / tls / collection 等必需参数。
当前代码使用 `api_key_credential_id` 等受信内部关联；Compiler 读取 Profile 拥有者的内部快照，
不把用户填写的名称当成已配置凭据。
禁止凭空增加 embedding_model_binding。

静态 Adapter 契约须明确 Knowledge 是可调用检索入口还是受控内部服务。V1 建议将所选
`qdrant_openai` 映射为 Manifest 明列的知识检索入口；由此推导出的 tool_call 要求与普通 Tool
相同，不能只在 `tool_slots` 非空时检查。其他映射策略必须更新同一 Contract / Fixture，
不得在 Worker 启动后偷偷派生入口。

## 6. Storage 最小运行角色规则

**Storage 不属于 AgentSpec V1 Slot**。既有 AgentSpec 不增加 `requirements.storage` 或
`storage_slot`；Deployment Input 也不增加角色映射表。

V1 采用以下明确的 Deployment Consumer 约定，而不是修改 Profile Schema：

| 运行角色 | Profile 选择规则 | 缺省 / 错误行为 |
| --- | --- | --- |
| session | 只选择 `storage.session`，必需 | 缺少报 DEPLOYMENT_STORAGE_ROLE_MISSING；不自动选任意 postgres_state |
| memory | Profile 显式提供 `storage.memory` 时选择，可选 | 不存在即禁用；存在但 Kind / 能力 / Adapter 不支持时报错误，不静默忽略 |
| 其他角色 | V1 不选 | 额外 storage key 不自动启用，不增加 artifact 角色 |

- 上述名称是运行角色到资源的固定约定，不是 Agent Slot，也不是让用户填写映射表。
- `postgres_state` 同时提供两类能力，不构成从多个候选中挑选资源的理由。
- 不把 session 资源自动复用为 memory。用户确实需要同一后端时，可在 Profile 中显式配置
  session / memory 两条资源填写同一后端配置；服务器分别管理其凭据关联，不允许用户
  提交任意 CredentialID 复用别的资源。数据命名空间仍必须按 Tenant、运行 Session 隔离。
- `storage.memory` 的存在仅选择数据服务，不授予模型任何 memory 管理 Tool 或执行入口；
  Worker 的 Memory 适配必须禁用框架自动派生 Tool，或将其判定为不兼容而拒绝启动。
- 只配置 `storage.conversation_state` 不满足 Deployment V1 的 session 约定。开发期示例
  直接调整为 `storage.session`，无需保留旧数据；目标运行中已发布的快照仍不可变。

Manifest 的 `storage_roles` 保存已解析资源 key / 配置引用。运行时 Tenant / Session namespace
由受信 Run Context 推导，不在 Profile 中添加 namespace 或任意 SQL 配置。

## 7. 平台配置与静态校验

### 7.1 PlatformExecutionContract 不是业务对象

平台在构建 / 部署 Control API 和 Worker 时提供一份关闭的 `PlatformExecutionContract`。
它包含 compiler_version、manifest_schema_version、runtime_contract_version、配置 version / digest、
已接入 Adapter 的 Kind / version / entrypoint 规则、允许 endpoint 范围、执行后端及资源上限。
这是本地发布配置与版本化 Consumer Fixture，不是注册中心、策略服务或调度平台。

- Bootstrap 加载并校验一次配置；单次 Validate / Publish 捕获同一不可变快照。
- Compiler 显式接收该快照；不在函数内部读取可变全局配置、探测网络或枚举 Worker 节点。
- 多副本在同一发布契约下必须加载相同 Digest；不一致时按部署就绪检查拒绝服务。
- 用户不传 Contract ID，也不获得它的 CRUD、选择器或生命周期；名称 test / prod 不触发默认合并。
- 不支持的 Worker 版本拒绝执行 Manifest，不能以“有其他可用节点”改变编译结果。

确定性定义为：相同 Canonical AgentVersion、ProfileRevision、Input、Compiler Version 和
PlatformExecutionContract 快照，产生相同 Canonical Manifest Content 与 Digest。
两份业务版本相同、但平台发布配置升级时，新发布结果可以变化；旧 Manifest 不重新编译。
本次发布新生成的 DeploymentRevisionID / ManifestID / EventID 与时间属于 Envelope，
不进入确定性 Content；源 VersionID / ProfileRevisionID 与内部 CredentialID 是固定输入，
必须进入相应 Content。平台 Contract 的 version / digest 和实际非秘密限制也进入 Content。

### 7.2 三种不同检查

| 检查 | 负责方 / 时机 | 是否阻塞发布 |
| --- | --- | --- |
| 静态兼容性 | 纯 Compiler：同名、能力、Schema/Kind/Adapter、执行范围、资源上限、确定性大小 | 是；对固定输入与配置确定性完成 |
| 凭据可用事实 | Application 调用 ProfileCredentialChecker.CheckUsable，检查 C 的内部凭据元数据 | 是；已配置、归属、用途、目标范围与发布使用授权，不读取或试用真实值 |
| 在线运行诊断 | Worker / 独立诊断操作：Provider 在线、真实 key 可用、远端 MCP 当前响应 | 不进入发布 DB 事务，也不是发布强制 Provider 探测 |

凭据配置状态是校验时刻的事实，不进入 Canonical Content / Digest，也不改变纯 Compiler 的确定性。Validate 结果不承诺随后
发布或执行成功。发布重新检查；执行再次授权，见第 10 节。元数据后端不可用属于依赖故障，
不是资源格式不合法。整个发布过程不把 Provider health check 放进事务。

### 7.3 平台执行范围

只检查 C 的 endpoint、Adapter、数据服务、有效 generation 参数、循环 / 并发和执行预算。
平台上限不通过偷偷截断用户配置满足：越界给出诊断；合法的执行预算被明确固化到 Manifest。
Profile 当前写入路径从 DSN 提取并固定非秘密 destination（host/port/database/username/sslmode），
Deployment 将其纳入静态执行范围与 Manifest；API endpoint 同样固定。Worker 解析后复核值的
实际目标与快照一致。当前 Profile 已实现固定 `destination`（`host/port/database/username/sslmode`）
及 DSN 提取校验；Deployment 消费该受信结果，不另行扩展 Profile 字段或接受任意连接选项。

Worker 在执行时仍强制当前网络控制与资源硬上限，紧急收紧优先于旧快照；拒绝越界，不重写
Manifest 或切换到其他资源。V1 不承诺固定 IP / DNS 永久不变。

## 8. 按需工具接入与节点隔离

```text
最终入口 = Agent 声明 + 节点选择
         ∩ Profile 同名资源
         ∩ 平台已接入且允许的 Adapter / 配置
```

平台“安装 / 支持某工具”不是 Agent 默认权限，也不是所有节点默认权限。

1. Compiler 为每个 LLM 节点保存独立 `tool_resources`、`knowledge_resources` 和
   `callable_entries`；容器节点不自动继承子节点 Tool。
2. Worker 只向该节点注册 Manifest 明列的入口。可信执行器决定当前 NodeID，模型参数不能
   冒充别的节点；调用 Dispatcher 同样复核 NodeID → EntryID 关系，不能仅过滤展示列表。
3. MCP Adapter 可以复用连接，但只暴露所选具体 `tool_name`；不把 MCP server 枚举出的整个
   Toolset 挂到节点。远端新增工具不会扩大授权；枚举可用于建立连接 / 核验接口，不用于自动选用。
4. Logical EntryID 使用类别 + Resource Key 区分 Tool / Knowledge；具体 Provider callable name
   的生成、长度、冲突拒绝由静态 Adapter Contract 固定，不得依据运行时枚举顺序生成。
5. 若知识适配产生检索 callable，则必须出现在 `callable_entries`，并参与 Model tool_call
   能力与平台入口数校验。
6. 框架配置 Executor、Skills、扩展目录、自动插件加载或其他 Option 可能派生额外工具或
   执行入口。Worker Adapter 必须显式关闭这些隐式行为，或仅生成 Manifest 已授权的入口；
   只检查普通工具列表并不构成完整控制。无法抑制额外入口的适配组合属于不兼容实现。
7. Session / Memory 数据服务不产生模型 callable。后续若需要 memory 管理工具，必须先有
   新的关闭资源协议、Agent 声明与节点选择，不借运行服务绕过按需原则。

当前 Tool 协议仅 MCP 网页搜索；框架内建工具、命令执行资源、Executor/Skills 配置尚未冻结。
V1 不接受任意 builtin / command Kind、脚本、可执行代码、SDK Option 或开放配置 Blob。
未来接入仍遵守上述公式和节点级入口契约，不建立动态工具注册中心或 Sandbox Manager。

## 9. RuntimeManifest 最小结构与不变量

### 9.1 Envelope 与 Content

发布 Envelope 保存 `manifest_id`、`tenant_id`、`deployment_id`、`deployment_revision_id`、
`revision_number`、发布时间、Content Digest。Content 保存固定来源及执行所需数据。
以下为待实现的当前 V1 结构示例；Profile 来源使用 `schema_version=v1`，内部 `credential` /
`destination` 是 Deployment
编译结构，不等于 Profile 公开写入 DTO。具体 Profile 协议以其拥有者文档冻结为准。
带占位说明的 Digest 不是已计算 Golden Fixture，协议实现时必须生成真实 Digest。

```json
{
  "schema_version": "v1",
  "compiler_version": "deployment-compiler-v1",
  "runtime_contract_version": "worker-manifest-v1",
  "platform_contract": {
    "version": "platform-v1",
    "digest": "sha256:<platform-contract-digest>"
  },
  "tenant_id": "33333333-3333-4333-8333-333333333333",
  "sources": {
    "agent": {
      "agent_id": "11111111-1111-4111-8111-111111111111",
      "version_number": 3,
      "version_id": "44444444-4444-4444-8444-444444444444",
      "schema_version": "v1",
      "digest": "sha256:<agent-spec-digest>"
    },
    "profile": {
      "profile_id": "22222222-2222-4222-8222-222222222222",
      "revision_number": 2,
      "revision_id": "55555555-5555-4555-8555-555555555555",
      "schema_version": "v1",
      "digest": "sha256:<profile-spec-digest>"
    }
  },
  "agent_plan": {
    "root": "main",
    "nodes": {
      "main": {"kind": "sequence", "children": ["researcher", "writer"]},
      "researcher": {
        "kind": "llm", "instruction": "检索并整理资料",
        "model_resource": "primary", "tool_resources": ["search"],
        "knowledge_resources": [], "callable_entries": ["tools/search"]
      },
      "writer": {
        "kind": "llm", "instruction": "根据已有信息撰写答案",
        "model_resource": "primary", "tool_resources": [],
        "knowledge_resources": [], "callable_entries": []
      }
    }
  },
  "resources": {
    "models": {
      "primary": {
        "adapter_version": "openai-compatible-v1", "kind": "openai_compatible",
        "model": "support-model", "base_url": "https://model.example/v1",
        "credential": {
          "credential_id": "66666666-6666-4666-8666-666666666666",
          "purpose": "model_api_key", "audience_digest": "sha256:<model-destination-digest>"
        },
        "capabilities": ["chat", "tool_call"]
      }
    },
    "tools": {
      "search": {
        "adapter_version": "mcp-web-search-v1", "kind": "mcp_streamable_http",
        "server_url": "https://search.example/mcp", "toolset_name": "web",
        "tool_name": "search", "auth": {"kind": "none"}, "capability": "web.search"
      }
    },
    "knowledge": {},
    "storage": {
      "session": {
        "adapter_version": "postgres-state-v1", "kind": "postgres_state",
        "destination": {"host": "state.example", "port": 5432, "database": "agent_state", "tls": "verify-full"},
        "credential": {
          "credential_id": "77777777-7777-4777-8777-777777777777",
          "purpose": "storage_dsn", "audience_digest": "sha256:<storage-destination-digest>"
        }
      }
    }
  },
  "resolved_requirements": {
    "models": {"primary": "primary"}, "tools": {"search": "search"}, "knowledge": {}
  },
  "storage_roles": {"session": "session"},
  "execution": {
    "backend": "worker-process-v1", "allowed_endpoint_hosts": ["model.example", "search.example", "state.example"],
    "max_run_seconds": 120, "max_tool_calls": 16, "max_output_tokens": 4096
  }
}
```

上述数值和 Adapter version 是说明例，不证明当前平台已有对应实现。正式 Schema / Fixture
必须冻结实际已接入矩阵与限制，严禁仅修改 allowlist 字符串即声称实现存在。

### 9.2 不变量

- Content 固定 AgentVersion 和 ProfileRevision 的所属 ID、版本记录 ID、序号、Schema 和 Digest。
- 执行计划保留 root、节点 Kind、指令、组合边、loop 限制和有效 generation 配置等行为所需字段；
  不复制编辑器状态、完整 Profile 元数据或无用 Requirement。上述例子是无 generation 的简化计划。
- `resources` 恰好包含 C；每条已解析节点引用 / Role 引用必须存在且类型正确；孤儿资源拒绝。
- `resolved_requirements` 只记录 U 对应解析结果，声明但未使用项只进 Report，不制造工具权限。
- 平台有效限制与所需 Adapter Version 固定，不能靠运行时默认 Option 改变语义。
- Content Digest 使用 RFC 8785 Canonical JSON 的 SHA-256。Map 顺序、诊断生成顺序和
  本次发布新生成的 Envelope ID 不影响 Content；来源中的稳定 CredentialID 参与摘要，
  Draft 写时复制换 ID 会改变来源 / Manifest Digest，同 ID live 轮换不改变。
  数组按其语义规范化：顺序执行 children 保留顺序，集合型列表排序去重。
- 完整读取重新 Canonicalize 并复核 Digest；Revision Summary 不加载 Input / Manifest JSONB。
- Worker 验证 Envelope / Content Tenant 与 Run 一致、Schema / Adapter Version 支持、Digest 正确，
  然后只从该 Manifest 装配配置；不读取 Draft、不追随新 Profile、不重做名称匹配。
  新 Attempt 读取凭据值是第 10 节明确的内部接口依赖，不是回查配置或重新编译。
- 不携带真实凭据、密文、nonce、加密密钥、动态 configured/status 或 credential_revision。
  只保留必要的内部 CredentialID、用途与固定 audience；其 Tenant/Profile 来自已验证来源。
  Publication Event 与运行投影保持同一边界；ID 本身不授予解析权限。
- 固定配置不等于固定外部世界：Credential Value、Provider 可用性、网络和平台紧急限制可改变。

### 9.3 内部执行结构与公开脱敏读取

第 9.1 节是持久化、受信内部 Query 和运行事件的完整 Manifest，不是公开 GET 的原样返回值。
公开 Deployment / Revision 读取投影为不含内部 CredentialID 的 `manifest_view`；Digest 明确
标记为内部 Canonical Manifest 的完整性标识，不声称可以从该 View 重算。公开接口既不接受
CredentialID，也不把脱敏 View 作为 Worker 可执行 Manifest。Publish / Replay / Get 使用同一
版本化固定投影，可展示静态 credential_present 而非动态状态。运行消费者走受信事件 / 投影。

不可变发布响应和 Receipt 只保留固定身份、Digest 与固定脱敏表示；不附带会随 live 轮换 / 撤销
变化的 configured/status/credential_revision。确需展示当前凭据状态时，读取 Profile 独立动态
状态视图，不能修改历史 Receipt 的响应。完整性复核始终针对存储的完整内部 Manifest。

## 10. Profile 直接录入凭据：内部存储、发布检查与执行取值

### 10.1 用户只有 Profile 表单，内部有三种表示

用户在 Model API Key、MCP Bearer Token、Qdrant / Embedding API Key、Storage DSN 字段
直接填写真实值，不先创建 Secret，不管理 SecretRef、凭据名称映射或独立 Secret 页面。
这不是把真实值塞进 ProfileRevision / Manifest，而是 Runtime Profile 拥有者在入口拆分数据：

```text
Profile Write DTO（普通配置 + 字段级 keep / replace / clear，replace 可携带 write-only value）
    → Profile Application 授权、提取凭据、生成内部关联并加密
    → 同一 PostgreSQL 事务：Draft CAS + 非秘密 Canonical Spec + 加密凭据记录
    → Public Read View（普通配置 + configured/state；无值和内部 ID）
    → Publish ProfileRevision（固定普通配置与内部 CredentialID，无真实值或密文）
```

| 表示 | 保存 / 返回内容 | 使用者 |
| --- | --- | --- |
| Write DTO | 明确的字段操作；仅 replace 接受真实值；客户端不提交 CredentialID | Profile 写入用例，不作为可直接持久化的 Spec |
| 内部 Canonical Spec | 普通配置、固定 destination、服务端内部 ID / 用途关联 | Profile 存储、完整性校验与 Deployment 的受信完整 Reader |
| Public Read View | 普通配置、凭据是否已配置 / 状态等独立动态展示 | 用户读取；不回显值，不用掩码充当可写值 |

公开 `spec_digest` 若保留该名称，表示内部 Canonical Spec 的完整性标识，不承诺从脱敏 View
重算相同摘要；新读协议必须显式说明其语义。动态凭据状态与轮换序号单独返回，不进入不可变
Revision、Canonical Digest 或发布 Receipt 的固定响应。凭据值也不进入公开无密钥哈希。

### 10.2 草稿编辑与 live 更新是两个明确动作

| 动作 | 内部 ID / CAS | 对已有发布的影响 |
| --- | --- | --- |
| Draft keep | 服务端从原资源实例解析原关联；校验 Draft ExpectedRevision | 保持原 ID；未配置时不伪造已配置状态 |
| Draft replace | 写时复制（copy-on-write），生成新 CredentialID；新密文 + Draft CAS 原子提交 | 旧 Revision 保持原 ID，未发布草稿不改变生产凭据 |
| Draft clear | 仅移除 Draft 关联，推进 Draft CAS；必需认证配置在发布校验时报缺失 | 不撤销历史凭据，不影响历史 Revision |
| Profile 内显式“更新已使用凭据”：replace | 同一 ID 更新值，独立 ExpectedCredentialRevision CAS +1 | 引用该 ID 的已有 Deployment 的新 Attempt 使用新值；无需重新发布配置 |
| Profile 内显式“更新已使用凭据”：clear | 同一 ID 终止性撤销，独立 Credential CAS +1 | 后续解析失败；保留历史关联用于解释 / 审计 |

同一表单可提供这些动作，但命令协议与影响范围必须区分。空字符串、掩码与省略字段不是隐式
替换 / 清除；关闭 DTO 显式表达操作，禁止把 GET 的脱敏结构直接当 SaveDraft 提交。
已 revoked 的 ID 不通过普通 replace 复活；重新录入用 Draft 新 ID，随后发布新 ProfileRevision
与 DeploymentRevision，避免历史 Deployment 被隐式恢复。

凭据管理（新写入、替换、撤销）在新协议中采用 OWNER-only；已有 MEMBER 非秘密编辑权限不等于
可轮换生产凭据。可保留未改变用途和目的地的已有字段关联，但 keep 不授予任意 ID 使用权。
Deployment 的发布使用权、Profile 的管理权、Worker 的值解析权分别检查。

live 操作用可信 Tenant、Profile、精确 ProfileRevision 与资源字段定位；服务端解析目标 ID，
校验用途、当前关联 / association_token 与 ExpectedCredentialRevision，不接受用户任意 CredentialID。
这也允许管理已从 Draft 移除、仍被旧 Deployment 使用的凭据。并发失败不留下孤儿值或半个配置更新。
新凭据写命令的幂等记录只保存受密钥保护的请求指纹（keyed MAC）与脱敏结果，绝不保存明文
或可离线猜测的普通 value SHA-256；此协议由 Profile 拥有者定义，不复用 Deployment Input Digest。

### 10.3 最小持久化：Profile 拥有加密记录，不新建 Secret 产品

可以使用既有 PostgreSQL，由 Runtime Profile 模块拥有最小加密记录。概念字段为：
`tenant_id/profile_id/credential_id/resource_instance/purpose/audience_digest/state/credential_revision`
及 `ciphertext/nonce/key_id`。内部 ID 不重用，租户 / Profile 复合归属由持久化和用例共同校验。
明文必须在写入 Spec 之前提取：现有 SaveDraft 直接存原始 Spec，仅放开敏感字段校验会造成明文落库。

使用成熟 AEAD 实现（例如 AES-GCM）、每次加密唯一 nonce；AAD 绑定内部身份、用途与目标范围。
加密主密钥由平台独立运行配置注入 Profile Owner，不和密文放在同一数据库表，不交给 Deployment
模块、Worker 或用户。数据库备份单独不应足以解密。这里是最小加密 Adapter 与密钥注入契约，
不是新 KMS / Vault 服务建设；实现时验证错误密钥、AAD 不符和密文篡改均失败。
这些选择依据 [OWASP Cryptographic Storage 指南](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html)。

Profile 写入 / 解析路径有意接触真实值，不能笼统声称整个 Control API 永不接触凭据。
Deployment 模块始终只消费元数据。普通响应、Spec JSONB、Revision、Manifest、Outbox、Receipt、
日志、Trace、错误和导出均无值 / 密文；日志中 URL、Provider 返回体和写入 Body 也须脱敏。
加密主 Key 轮换与用户凭据轮换分开，当前实现没有自动 Key 轮换；未来若加入后台重新加密，
不得改变内部 ID、配置 Digest 或值语义。

### 10.4 两个消费方接口与真实接线

| 层 | 接口 / 职责 | 结果边界 |
| --- | --- | --- |
| Profile Owner | 写入、加密存储、内部关联、管理授权、轮换 / 撤销与完整内部 Query | 普通读为脱敏 View；内部 Canonical Query 只含非秘密关联 |
| Deployment Application | `ProfileCredentialChecker.CheckUsable(tenant, actor, profile_id, profile_revision_id, uses)` | 检查 C 中的 configured/state、归属、用途、audience 和发布使用授权；不返回真实值 |
| Compiler | 从固定来源提取必要 credential uses，校验用途 / 固定目标并生成 Manifest | 无凭据 I/O；动态状态不改变 Manifest Content |
| Worker | `RuntimeCredentialResolver.ResolveForAttempt(trusted_execution_context, manifest_id, manifest_digest, uses)` | 重新授权后取得当前 Attempt 必要的真实值集合 |

接口由真正消费方定义；适配器调用 Profile 拥有者能力。`uses` 从 C 的可信资源字段推导，
包含内部 ID、用途与固定 audience，不从模型参数或用户提交的任意列表建立权限。Model 获准
不意味着可当 DSN 使用；同 Tenant 不意味着跨 Profile 可用；ID 泄露也不能直接获得值。
发布检查只证明检查时刻已配置且获准，不试用 API Key，也不保证之后仍可解析 / Provider 接受。

**首版接线**：Deployment Checker 通过现有 Control API 内的 Profile 模块接口取得元数据；
Worker Adapter 通过该服务中 Profile Owner 拥有的**内部认证批量接口**获取值，不跨模块直接
查询凭据 SQL，也不从公共 GET、Outbox 或消息总线分发密文 / 明文。Profile 消费方法与
可选 runtime HTTP Adapter 已实现；默认 bootstrap 不注册该路由，真实执行授权与 Worker
接线属于后续计划。这不是 Deployment 的公开管理路由。

内部接口必须验证 workload 执行身份及其服务端可验证的 Tenant / Attempt / lease / Manifest
授权上下文，核对 Manifest Digest、Profile 来源、必要闭包、用途和 audience；不只信任请求 Body
的 Tenant 或 ID，也不把 ID 当 bearer token。可信执行上下文来自 Runtime 的 Run/Attempt/lease
拥有者；首版 ExecutionAuthorizationVerifier 在线调用既有执行拥有方的可信内部 Query，
核验 active Attempt、当前 lease epoch / fence 与 Worker 归属。签名只证明 issuer / audience /
固定 Manifest，未过期签名不能代替当前 lease 状态核验；不跨域读表，不新增调度 / 策略服务。
该查询是新 Attempt 的另一项显式可用性依赖。查询后的在途撤销仍由接收与执行侧 fence 处理，
不宣称瞬时撤销。尚无真实 Verifier 适配时，不把运行面的授权解析链路声明为已贯通。
内部响应是有意返回真实值的唯一读出口，使用受保护
传输、禁用缓存和 Body 日志，失败不返回部分可执行批次。凭据仅在受控 Adapter 内存中注入客户端，
不作为工具参数、模型上下文或其他节点可读取的通用变量；Worker 按 Manifest 节点关系装配客户端。

**可用性代价必须显式接受**：需要凭据的新 Attempt 初始化依赖 Profile Owner 内部接口；无凭据闭包则不调用。服务不可用时，
新 Attempt 等待 / 重试或返回稳定依赖错误；已有完整初始化的 Attempt 复用内存值继续运行。
配置、路由与 Manifest 仍不依赖 Control API 同步查询。V1 不承诺 Control 中断时所有新执行
都能离线启动，也不为保留这一旧承诺新建凭据分发平台。

### 10.5 Attempt 生命周期、轮换与目的地

- 完整验收要求响应 Tenant / Profile / Attempt / Manifest Digest 与请求上下文相同，
  ID / purpose / audience 集合与闭包完全一致（无缺失、多余、重复），且接收时 Worker 仍持有
  有效 lease；全部通过才初始化客户端，否则丢弃整批，不部分使用。
- Worker 在首次批量响应完整验收后缓存该批次；当前 Attempt 后续连接重试只复用它，不再次
  Resolve。Resolver 一次读取中同一 ID 必须一致；不承诺多个独立 ID 的业务轮换原子性。
- 解析响应结果不确定、进程崩溃或 lease 失效时，结束当前 Attempt；恢复使用新 AttemptID
  并重新解析。V1 不支持同 Attempt 跨进程透明恢复，不建凭据版本 pin 表或历史密文重放库。
- 新 Attempt 可以取得轮换后的值，仍沿用原 Manifest 的配置和内部 ID；ProfileRevision、
  DeploymentRevision、Manifest 字节 / Digest、Outbox 和已发布响应均不变化。
- live clear 或失去执行授权使新解析失败，不回退其他 ID、Profile、Tenant 或历史值。
  已在内存的值何时失效取决于 Provider 与 Worker 生命周期，撤销不等于远程抹除内存。
- endpoint / Kind / 认证方式 / 用途 / 目标范围变化时，keep 被拒绝；用新 ID 和新配置 Revision。
  删除后同名重建产生新资源实例 / ID；复制 Profile 不隐式继承源凭据，需重新录入。
- DSN 在录入时拆出固定非秘密 destination；同 ID live replace 只允许认证材料变化，新的 DSN
  host / port / database / username / sslmode 不同则拒绝轮换，改走 Draft 新配置 + 新 ID。
  Worker 解析后再次比对目标与 audience；不允许以“轮换”绕过发布网络校验。
- 后续凭据轮换 / 撤销不改历史发布事实。授权用户重放旧 Deployment Publish Receipt 仍返回
  原结果，是否能运行由新 Attempt 的执行期授权决定，不虚构发布检查能消除 TOCTOU。

### 10.6 当前 V1 原位收敛，不维护开发期历史兼容

当前尚未形成稳定对外版本。直接录入凭据在现有 `/v1` API 与 `schema_version=v1` 下调整，
Write DTO、内部 Canonical Spec、Public Read View 是三种不同表示，不是三个协议代际。
不增加 `/v2`、V1/V2 双栈、旧 ref-only Reader/Validator、兼容 DTO 或旧引用迁移门禁。

当前代码、V1 Schema、OpenAPI、Fixture 和开发数据库基线已替换旧 `*_ref` 输入，
Profile 私有表保存加密值，Canonical Spec 只持有受信内部关联。开发期数据可显式重建，
不增加仅为旧格式保留的 Reader 或历史存储。执行过旧 `0001_baseline.sql` 的数据库
不会因重启自动补出后来追加的凭据表；测试需要当前基线初始化的数据库。

当前 V1 已实现的 Profile 内部字段包括 Model / Embedding 的 `api_key_credential_id`、MCP
`auth.credential_id`、Qdrant 的 `qdrant_api_key_credential_id`、Storage 的 `dsn_credential_id`
及非秘密 `destination`；Compiler 转换为第 9 节统一 usage 结构。公开 Write 使用字段级
`action` / `value`，Read 用独立 `credential_states`，不透出内部 ID。具体 Profile 字段由其
拥有者的专属文档、Schema 和代码共同约束，Deployment 不复制另一份字段定义。

**无需保存开发期历史，不等于取消目标运行模型的不可变性。** 新设计落地后发布的
ProfileRevision / DeploymentRevision / Manifest 仍是固定执行快照；运行中的 Draft 编辑、
显式凭据轮换、并发 CAS、发布幂等和切换生效版本仍按本文执行。后文“旧 Revision / 历史 Run”
案例描述新设计内部的正常生命周期，不要求兼容本次设计调整前的开发数据。

Runtime Profile 已交付当前 V1 输入提取 / 加密存储 / Read View / OWNER-only 凭据管理与
消费方法；Deployment 继续拥有校验 / Manifest / 发布路线图。Profile 已有实现与 Deployment /
Worker 仍待接入分别记录，Profile 单元或集成测试成功不替代完整发布 / 运行链路验收。

## 11. 校验分层、诊断与错误分类

### 11.1 顺序

1. 身份 / Tenant / Session / 角色验证。
2. L0：传输、重复 JSON Key、大小、关闭 DTO 与精确版本引用。
3. 源读取：固定版本所属 Tenant、访问权、Canonical Content / Digest 完整性。
4. L1：匹配全部 D、Capability、节点使用集与 Storage Role 选择。
5. L2：C 的已接入 Adapter、派生入口、执行范围、有效配置与资源上限。
6. L3：C 的内部凭据已配置 / 归属 / 使用授权；非 Provider 在线探测。
7. Canonical Manifest、完整 Event Envelope 大小及发布事务复核。

某层缺少前提就不生成依赖该前提的噪声诊断。例如缺少 tools.search 时不再报告其 credential missing。
诊断使用稳定 code、severity、source、JSON Pointer、category / name / node_id（适用时）；
source 明确为 input、agent、profile 或 platform，避免 Pointer 看似属于请求 Body。
按 source、path、code、category、name、node_id 排序；message 可本地化，不作为机器契约。

```json
{
  "valid": false,
  "compiler_version": "deployment-compiler-v1",
  "platform_contract_digest": "sha256:<platform-contract-digest>",
  "diagnostics": [
    {
      "code": "DEPLOYMENT_RESOURCE_MISSING",
      "severity": "error", "source": "agent",
      "path": "/requirements/tools/search",
      "category": "tools", "name": "search",
      "message": "ProfileRevision 缺少同名 tools.search"
    }
  ]
}
```

### 11.2 稳定诊断分类

| Code | 含义 |
| --- | --- |
| DEPLOYMENT_INPUT_INVALID | 关闭 DTO、非法版本、未知字段或 Body 结构错误 |
| DEPLOYMENT_SOURCE_SCHEMA_UNSUPPORTED | 源 Schema 与 Compiler Contract 不兼容 |
| DEPLOYMENT_RESOURCE_MISSING | 缺少同类别、同名资源 |
| DEPLOYMENT_CAPABILITY_MISMATCH | 同名资源能力不满足声明，或节点实际入口要求 tool_call |
| DEPLOYMENT_UNUSED_REQUIREMENT | Warning：已声明但节点未使用，不进入 Manifest |
| DEPLOYMENT_STORAGE_ROLE_MISSING | 必需 storage.session 缺少 |
| DEPLOYMENT_STORAGE_ROLE_UNSUPPORTED | 所选 Storage 不支持对应运行角色 |
| DEPLOYMENT_ADAPTER_UNSUPPORTED | Kind 已知但平台未接入所需版本 / 行为组合 |
| DEPLOYMENT_ENTRYPOINT_UNSUPPORTED | 隐式派生入口、未冻结 builtin / command / Skills 行为或入口冲突 |
| DEPLOYMENT_EXECUTION_RANGE_DENIED | 所选 endpoint / 后端配置不在平台静态执行范围 |
| DEPLOYMENT_LIMIT_EXCEEDED | 有效配置或资源消耗边界超过平台上限 |
| DEPLOYMENT_CREDENTIAL_UNAVAILABLE | 未配置、撤销、归属 / 用途 / audience 不符或不获准使用；对外隐藏存在性和内部 ID |
| DEPLOYMENT_MANIFEST_TOO_LARGE | Manifest 或完整 Event Envelope 超过传输预算 |

HTTP 身份 / 不存在 / CAS / 依赖错误不伪装成以上静态诊断：401 未认证，403 Restricted Session
或角色不足，404 同租户目标不存在 / 跨租户对象隐藏，400 传输 DTO 错误，413 Body 过大，
409 CAS / Idempotency 冲突，422 Publish 校验失败，503 Profile 凭据元数据依赖不可用，500 持久化
完整性或跨记录关系损坏。Validate 对可解析请求的兼容性不通过返回 200 + `valid=false`；
认证、传输、源不存在和依赖故障仍用对应非 200 状态，不包装成 valid=false。

## 12. 发布原子性、并发与幂等

### 12.1 授权

建议 V1 角色矩阵：有效 MEMBER / OWNER 可读、创建、改元数据、Validate；Publish 为 OWNER-only。
Platform Operator 没有目标 Tenant Membership 时不获得隐式权限。所有 Query、Receipt、CAS、
唯一键、外键与 Outbox 都含 Tenant。读取旧 Receipt 前也重新验证当前 Session、Membership 与用例权限。

### 12.2 Receipt 与 Digest

- Create 唯一键：`(tenant_id, operation=create, scope=tenant_id, key_hash)`。
- Publish 唯一键：`(tenant_id, operation=publish, scope=deployment_id, key_hash)`。
- Header Key 非空、限制长度、不记录明文到日志；数据库保存其稳定哈希。
- Request Digest 覆盖规范化用户 Body（包括 Expected Latest）及 operation / scope / tenant；
  不把当前平台配置或 凭据动态元数据事实混进 Request Digest。
- Input Digest 覆盖两来源选择；Compilation Digest / Manifest Digest 另覆盖实际来源 Digest、
  Compiler / 平台版本和编译 Content。两者不混用。
- 同 Key 同 Request Digest：返回原对象；同 Key 不同 Digest：409，禁止二次发布。
- Receipt-first：匹配旧 Receipt 后不再检查当前 Latest、重新编译或以当前凭据状态 / 平台配置阻断
  返回历史发布事实；否则“成功后 Profile 前进 / 凭据轮换再重试”会错误地产生新结果。
- Create Receipt 保存原始 Canonical 创建响应及 Digest；Create → PATCH → 重放仍返回原创建
  表示，不根据当前名称重新拼接，避免相同 Key 返回不同响应。
- Publish Receipt 引用不可变 Revision / Manifest 并保存固定公开响应身份 / 脱敏 View 与 Digest。完整重放必须重新验证
  两者内容和 Tenant / ID 关系，损坏返回 500。

### 12.3 新发布顺序

```text
当前授权 → 查 Receipt
  ├─ 已存在且匹配 → 验证历史完整性 → 原响应（200）
  └─ 不存在
      → 读取固定 AgentVersion / ProfileRevision
      → 捕获平台 Contract → 同名匹配 / 闭包 / 只读 Profile 凭据元数据检查
      → 编译 Canonical Content → 构造发布 Envelope / Event
      → PostgreSQL Transaction
          锁定 Tenant-scoped Deployment
          在锁后再查 Receipt（匹配即返回历史结果）
          仅无 Receipt 才复核 Expected Latest
          插入 DeploymentRevision
          插入唯一 RuntimeManifest
          插入相同身份与 Digest 的 Outbox Event
          插入 Command Receipt
          CAS 推进 Latest
        Commit → 返回新结果（201）
```

- 若首次查空后的编译准备/凭据元数据检查因动态依赖失败，在仍有效的请求期限内用新
  数据库快照最终查询一次 Receipt：匹配则验证历史完整性并重放，异摘要则冲突，仍无记录
  才返回原错误；当前身份授权不跳过，数据库故障不以缓存充当成功。该规则与 Gateway 的
  [Receipt-first 并发窗口](../channel-gateway/module-boundaries.md#57-receipt-first-的并发失败窗口)
  一致，不要求等待尚未提交的并发结果，也不以无限重试扩大事务。
- 首次 Expected Latest=null，仅在没有 Revision 时成功；以后必须等于当前 Latest。
- 同 Key 并发：最多一个事务产生结果。事务采用 READ COMMITTED，等待行锁的事务在取得锁后
  用新的语句快照查询 Receipt，再检查 Latest；这样赢家提交后，输家返回 200 同结果而非错误的
  409。若实现使用更高隔离级别，序列化失败需重新进入 receipt-first。Create 无现存 Deployment
  行可锁，由 Receipt 唯一键仲裁，冲突后在新事务读取原创建响应，不产生第二个身份。
- 不同 Key、相同 Expected Latest 并发：只有一个成功，另一个 409；不默默把请求改到下一个序号。
- 新 Key + 新的正确 Expected Latest，即使两来源和 Content 相同也允许形成新发布记录；不按内容
  去重业务发布。跨平台升级时 Content 可不同。
- 外部 I/O 不在发布 DB 事务内。Profile 凭据操作由其拥有者单独提交，Deployment 发布
  不加锁控制其轮换，不把检查时状态当永久授权；执行重检处理 TOCTOU，即使同用 PostgreSQL
  也不建立跨模块凭据写事务或复制凭据行。
- 事务任一步失败全部回滚；不留下 Revision 无 Manifest、业务无 Outbox 或 Receipt 无响应。

### 12.4 持久化轮廓

| 表 / 记录 | 关键内容与约束 |
| --- | --- |
| deployments | Tenant、稳定 ID、metadata_revision、latest_revision_number、名称/描述；Tenant 复合唯一键 |
| deployment_revisions | Tenant、Deployment、连续序号、来源精确 ID/序号/Digest、Canonical Input、Input Digest、发布时间；Tenant+Deployment+Number 唯一 |
| runtime_manifests | Tenant、deployment_revision_id 唯一 FK、Canonical Content、Content Digest；禁止 UPDATE/DELETE |
| deployment_command_receipts | Tenant+operation+scope+key_hash 唯一、Request Digest、原始创建快照或不可变发布结果引用 |
| control_outbox | 稳定 Event ID、所属 Tenant/Aggregate、版本化 Payload、投递状态 / 次数 / 时间；Payload 与投递状态分别约束 |

Revision 与 Manifest 的不可变列由数据库 Trigger 保护；业务元数据和 Outbox 投递状态可更新。
完整 Store 写入校验 Revision / Manifest / Event / Receipt 的 Tenant、ID、版本、Digest 一致性；
同事务读取保证 Query 不观察半状态。Summary Query 不读取大型 JSONB。

V1 不提供 Deployment / Receipt 删除，Receipt 保留支撑延迟重试。Outbox 是可按既有恢复窗口
清理的传输队列；历史重放只依赖保留的 Receipt + Revision + Manifest，不要求已清理的 Outbox
行仍存在。清理 Outbox 不影响不可变发布事实或幂等结果；Relay 对尚在队列中的 Payload 验证
固定 Event ID、Schema 与 Manifest Digest，拒绝腐化记录。

当前 Profile 凭据表已经纳入 Control 的 `0001_baseline.sql`；Deployment 后续表设计与
Control Migration 所有者协调，不为了保留开发期旧格式固定占用 `0002`。Control 与 Gateway
各自迁移自己的表、使用各自 ledger / advisory-lock domain；本文不向 Gateway Migration
加入 Control 表。开发基线需要真 PG 初始化、重复执行和事务失败回滚测试；稳定发布后的
升级迁移另按当时的演进契约设计。

## 13. PostgreSQL Outbox 与消息发布衔接

严格保留 ARC-401 / ARC-402：业务事实与 Outbox 同事务；Application 不直接发 NATS；Relay
负责传输、不拥有编译或切流决策，Worker 不承担 Control Outbox Relay。

建议版本化事件 `RuntimeManifestPublished.v1`：包含 EventID、TenantID、Deployment / Revision
身份、Manifest Envelope、完整 Canonical Content 与 Digest。事件传输使用公共 Schema / Fixture，
不共享 Control 内部 Domain Go 类型。Trace context 仅来自可信 Telemetry，过滤并限制长度；
不透传任意请求 Header、baggage 或秘密值。

- Relay 使用稳定 EventID 发布 JetStream，发布失败重试；即使进程在发送后更新投递状态前崩溃，
  Consumer 按 EventID 幂等，不依赖消息系统的有限去重窗口代替业务幂等。
- Subject、Stream、权限和最大 Envelope 大小由平台版本化部署契约提供；不按实时 Worker 列表选择。
- 编译后检查完整 Event Envelope（含 ID、时间、Trace 上限）而不只检查 Manifest JSON 字节。
- Outbox 清理晚于消费者恢复窗口和审计保留期。V1 的完整 Manifest 运行投影不做历史清理，
  与保留的 DeploymentRevision 对齐，确保旧 Revision 随时可重新绑定；不只保留近期 Run 的快照。
- 建议 Execution RuntimeManifest Projection 保存完整快照；Gateway 路由投影只存绑定与 Ref/Digest。
  新投影初始化或灾备重建从保留的不可变 Manifest 通过拥有者 Query / 导出做异步回填；这是后续
  Runtime Slice 的启动 / 恢复工作，不增加用户配置对象，也不是 Worker 执行时同步查询 Control。
  运行投影缺失时等待修复，不从 mutable Profile 或 Control API 热路径拼装替代物。未来引入投影
  GC 前，必须覆盖当前 Binding、Run 与重新切换目标的保留和非热路径回填规则。

完成层级：Control Publication 表示 DB 中已有 Revision / Manifest / PENDING Outbox；Distribution
表示 Relay 已投递且消费者已应用；Runtime 表示固定快照可完成真实 Run。三个层级独立验收。

## 14. 规划中的 HTTP 与 Module Interface

以下八路由仅属于待实现设计，本次不加入 OpenAPI 或“当前已实现 API”清单：

| 方法 | 路径 | 结果 |
| --- | --- | --- |
| POST | `/v1/tenants/{tenant_id}/deployments` | Create：201，幂等重放 200 |
| GET | `/v1/tenants/{tenant_id}/deployments` | List Deployment：200 |
| GET | `/v1/tenants/{tenant_id}/deployments/{deployment_id}` | Get Deployment：200 |
| PATCH | `/v1/tenants/{tenant_id}/deployments/{deployment_id}` | Metadata CAS：200 |
| POST | `/v1/tenants/{tenant_id}/deployments/{deployment_id}/validate` | 只读 Report：200 |
| POST | `/v1/tenants/{tenant_id}/deployments/{deployment_id}/revisions` | Publish：201，幂等重放 200 |
| GET | `/v1/tenants/{tenant_id}/deployments/{deployment_id}/revisions` | Summary List：200 |
| GET | `/v1/tenants/{tenant_id}/deployments/{deployment_id}/revisions/{revision_number}` | Revision + 脱敏 manifest_view：200 |

Application 对外只有四 Command + 四 Query。内部使用方 Port 为：

- TenantAccess：当前 Actor 的 Tenant 角色 / Session 限制。
- AgentVersionReader / ProfileRevisionReader：所属模块完整内部不可变 Query，不消费公开脱敏 View。
- ProfileCredentialChecker：通过 Profile 拥有者检查必要凭据元数据与发布使用授权，不取值。
- PublicationStore / QueryStore：Receipt、原子发布与专用投影。

纯 Compiler 是本模块内部函数，接收值快照，不因配置而创建远程“平台契约服务”。
平台 Contract 作为 Bootstrap 依赖传入；ID / Clock 只用于发布 Envelope。
Adapter 实现放在真正的使用方 Seam；Bootstrap 是唯一组装入口，不复制源模块 Domain 或 Repository。

## 15. 设计验收案例

下列案例是后续测试规格，不是本轮已执行的 Deployment 功能测试。

### AC-01：同一 AgentVersion 使用两份不同 ProfileRevision

- 同一 Tenant 下客服 Agent v3 声明 models.primary、tools.search，节点选择 search。
- 测试 Profile Revision 2 与生产 Profile Revision 5 都提供同名资源及 storage.session；连接配置 / 各自内部凭据不同，用户直接录入而非填写引用名。
- 分别发布两个 Deployment，成功固定相同 AgentVersion 与各自 ProfileRevision；Manifest 不交叉混用。
- 不创建环境对象，不因为名称含 test / prod 而套用隐式配置。

### AC-02：Profile 缺少同名工具

- Agent 要求 tools.search，Profile 只有 tools.web_search，其 tool_name=search。
- 返回 DEPLOYMENT_RESOURCE_MISSING，定位 `/requirements/tools/search`。
- 不按照远端 tool_name、相似名称或 web.search Capability 自动选候选；无发布记录。

### AC-03：同名工具能力不匹配

- Agent 的 tools.search 要求 `data.lookup`，Profile 的 tools.search 是合法 MCP `web.search`。
- 源 AgentSpec 允许能力字符串，Profile 也合法，但配对失败：DEPLOYMENT_CAPABILITY_MISMATCH。
- 名称相同不跳过能力校验；无 Manifest / Outbox 写入。

### AC-04：Profile 含额外工具，但 Agent 没有声明使用

- Profile 含 search 与 debug，Agent 只声明 / 选择 search。
- debug 不进入 Manifest、不查询其凭据使用权，也不挂载到任何节点；有效额外资源不阻塞发布。
- 再补 Agent 声明 debug 但节点没引用的变体：做声明同名 / Capability 校验并给 UNUSED Warning，
  仍不挂载 debug。researcher 有 search，writer 没有 search；模型直接调用未授权 EntryID 被拒绝。

### AC-05：跨 Tenant / Profile 的同名资源与凭据隔离

- Tenant A 与 B 均有名为 primary 的模型资源，各自在 Profile 表单录入 Key；无需同名 SecretRef。
- 内部 ID 分别绑定各自 Tenant / Profile / 资源实例 / 用途 / audience，名称相同不会复用值。
- Checker / Resolver 依据可信上下文，A 身份请求 B 的 ID 或同 Tenant 另一 Profile 的 ID 均失败；
  对外返回 CREDENTIAL_UNAVAILABLE，不暴露存在性；缓存和批次键包含 Tenant / Profile / Attempt。
- Model 用途授权不自动变为 DSN 用途授权；不建立全局 Secret 名称回退或开发期旧引用兼容。

### AC-06：Profile 新 Revision 不改变旧 DeploymentRevision

- DeploymentRevision 1 固定 Profile Revision 2；Profile 后来发布 Revision 3。
- 旧 Manifest 字节 / Digest / 节点分配保持不变；旧 Run / 重试不跟随 Revision 3。
- 使用新 Key 和正确 Latest 发布才生成新 Revision；旧 Key 延迟重放返回旧结果。
- Publication Outbox 清理后切回旧 Revision：保留的完整运行投影仍可应用；新建投影先完成异步
  回填再接纳 Run，不因队列历史已清理而永久等待。
- 同一内部 CredentialID 的显式 live 轮换是另一个维度：Manifest 不变，后续 Attempt 可用新值，不宣称凭据字节固定。

### AC-07：某个节点有命令工具，其他节点没有

- **当前 V1 负例**：Profile 未冻结 command / builtin Kind，提交此类资源被 Profile 协议拒绝；
  Compiler 的不兼容源 / Adapter 负例也拒绝它，不把命令能力伪装成 MCP web.search。
- **后续扩展验收**：待关闭资源协议与 Worker Adapter 实现后，Agent 声明 tools.command，
  仅 executor 节点选择它；Manifest 只有 executor 的 callable_entries 含命令入口。
- writer / sibling 节点既无工具注册，也无直接 dispatch 权限、共享 Executor 默认入口或 Skills
  派生入口。即使 Worker 二进制已安装 CodeExecutor，未声明节点仍没有权限。
- 这是一条未来接入必须满足的隔离契约，不是当前 command 工具已实现或已跑通的证据。

### AC-08：无 Environment、无用户绑定表的完整发布流程

1. 用户在 Profile 表单填写 primary/search/session 配置及凭据，保存脱敏配置 / 加密记录，
   再发布 Agent v3 与确定 ProfileRevision；不创建 Secret、不填写 SecretRef。
2. 用户 Create Deployment，仅填写名称 / 描述和幂等 Key。
3. 用户 Validate，Body 仅包含两份精确版本选择；平台捕获静态 Contract、校验必要 Profile 内部凭据元数据。
4. Compiler 精确匹配、检查能力、编译节点使用闭包；Report 为 valid=true。
5. 用户 Publish，增加 Expected Latest=null 和 Idempotency Header；单事务生成 Revision 1、
   Manifest、Outbox、Receipt。无 Environment API、默认对象、Overlay 或任何绑定表字段。
6. 相同请求重试返回相同发布事实；无重复 Event。
7. 后续 ChannelBinding 显式选择 Revision 1，运行投影就绪后新 Run 固定 Manifest，Worker 解析
   Manifest 所需同租户 / 同 Profile 内部凭据并按节点执行；这一最后环节属于后续 Runtime Slice 验收。

### AC-09：真实值写入与三种表示隔离

- Model、MCP bearer、Qdrant / Embedding key、DSN 分别直接录入；GET 只返回已配置状态。
- 检查 Write DTO 到存储的拆分：Spec JSONB / Revision / Manifest / Outbox / 普通响应 / 日志 /
  Trace / Receipt 均无真实值和密文，只有 Profile 专属密文列可保存密文；内部解析是唯一读值出口。
- keep / replace / clear 显式操作；掩码回写、任意 CredentialID、MEMBER 凭据写入均拒绝。
- 公开写入 / 读取和内部 Canonical 统一使用当前 V1；更新相应 Schema / Fixture，不另开协议代际。
  开发期 ref-only 数据无需保留，不为其增加 Reader、迁移或发布兼容门禁。

### AC-10：草稿隔离、live 轮换、撤销与 Attempt

- Draft replace 生成新 ID，Draft clear 仅解绑；不改变已有 Manifest 引用凭据或当前执行。
- OWNER 显式 live replace 同一 ID，Credential CAS +1；并发过期 CAS 返回冲突，不覆盖赢家。
- 旧 ProfileRevision / Manifest / 发布 Receipt 保持字节与 Digest；当前 Attempt 复用旧批次，
  新 Attempt 获得新值；live clear 后新解析失败，普通 replace 不复活，重新录入需新 ID + 发布。
- 首次批量响应不确定 / Worker 崩溃 / lease 失效终止 Attempt，恢复用新 AttemptID，不混用批次。
- Profile Owner 不可用：已初始化 Attempt 继续，新 Attempt 明确依赖失败；不退回明文快照。

### AC-11：关联、目的地、存储与幂等故障

- 删除同名重建 / Profile 复制不继承 ID；修改 endpoint 或认证方式后 keep 被拒绝。
- DSN live 轮换若改变 host/port/database/TLS/目标选项则拒绝，改走新配置 + 新 ID；Worker 复核目标。
- 历史 ProfileRevision 资源字段可定位 live 管理目标；伪造另一 Profile / 用途 / audience 均失败。
- Draft CAS、加密写入或事务失败时配置与凭据均不部分生效；错误 AAD / nonce / 密文篡改失败。
- 凭据写命令幂等重放不重复轮换，不在 Receipt 保存值 / 普通值哈希；动态 status 不污染不可变响应。

## 16. 后续实现顺序与门禁

| 阶段 | 交付 | 必须验证 |
| --- | --- | --- |
| 0 Profile 凭据前置纵切（已实现并对齐） | Write / Canonical / Read、Schema / Fixture / 加密基线、Draft COW、live CAS、CheckUsable、ResolveForAttempt 与可选 runtime HTTP Adapter 已存在；真实 Deployment Checker Adapter 后续接线 | 回归权限、轮换 / 撤销、三种表示与 Profile 侧 AC-09～11；真实 Worker 生命周期验证留给阶段 7 |
| 1 协议与消费约定 | 两来源 Input、Manifest、Report、Event Schema / Fixture，Storage role-key、静态 Adapter matrix / limits | 同名正反例、内部凭据关联、严格字段、节点入口、Payload 大小、真实来源契约一致 |
| 2 Compiler | 名称匹配、D/U/C、角色选择、派生入口、Canonicalization / Digest | AC-01～06/08 的纯编译测试，AC-07 当前负例；顺序变化不改变 Digest |
| 3 Application | 四 Command / 四 Query、权限、两 Source Reader、CredentialChecker、Receipt-first、CAS | Fake 验证语义；Create→PATCH→Replay、平台升级 / 轮换后 Replay、无外部事务 I/O |
| 4 PostgreSQL + HTTP | 开发基线、Store、不可变 Trigger、Outbox、Gin、Bootstrap、OpenAPI | 真 PG 生命周期、八路由、Tenant 隔离、并发 / 故障回滚、腐化读取、Summary 投影 |
| 5 Control Publication 闭环 | 实际 ProfileCredentialChecker Adapter、闭合契约门禁、CI / Compose | 非 Fake 发布、零 Skip PG Integration、完整 Event Envelope、已实现清单只同步真实路由 |
| 6 可选 Relay | JetStream 投递 / 重试 / Consumer 幂等 | 发后崩溃重投、乱序、EventID 重复、窗口 / 清理与历史 Receipt 重放 |
| 7 后续 Runtime | ChannelBinding 切换、运行投影、Worker Adapter、真实消息 E2E | 固定 Manifest、节点权限、凭据轮换 / 撤销、隐式入口关闭；Owner 中断时当前 Attempt 复用 / 新 Attempt 依赖失败 |

没有 Environment 前置阶段，没有新增动态工具注册中心、调度平台、策略微服务或 Sandbox Manager。
Worker Consumer Contract 与平台允许的真实实现需要同步验证，但不通过实时节点探测完成。
尚未冻结的数值 / Adapter version 在阶段 1 的版本化 Fixture 中明确，不作为用户额外配置步骤。

现有命令用于当前代码回归，均不代表 Deployment 已实现：

```bash
GOCACHE=/private/tmp/trpc-agent-deployment-v1-gocache go test -count=1 ./...
GOCACHE=/private/tmp/trpc-agent-deployment-v1-gocache just openapi
GOCACHE=/private/tmp/trpc-agent-deployment-v1-gocache just test-race
GOCACHE=/private/tmp/trpc-agent-deployment-v1-gocache just vet
just compose-config
```

实现阶段新增 Deployment Schema / Compiler / HTTP 的明确测试门禁，并扩展当前 `just openapi` /
`just test-race`；当前 recipes 尚未覆盖 Deployment。真实 PostgreSQL Integration 要显式 DSN，
并解析测试事件拒绝任何 Skip；普通 `go test ./...` 在缺 DSN 时会跳过真实 PG 测试。
不运行 `just fmt` 来“验证文档”，它会写业务 Go 文件。

测试还应覆盖：阻塞同 Key 第二事务取锁，赢家提交后输家返回 200 而非 CAS 冲突；
空 / 多 Storage 候选不猜测；MCP 整个 Toolset 过滤；Knowledge 派生入口需要
Model tool_call；Profile 凭据依赖不可用与未配置 / 无权的区别；非闭包额外资源；Artifact/Event
limit-1 / limit / limit+1；当前已实现 API 清单不受设计文档变化影响。

## 17. V1 非目标与扩展点

V1 非目标：独立 Environment、环境 ID / 默认对象 / Overlay / 继承 / 深层合并；用户异名
映射表；服务端 DeploymentDraft；全局 Activate/Deactivate；审批 / Canary / 调度平台；
独立 Secret 管理产品 / 用户 SecretRef / SecretVersionHandle 固定历史值 / 凭据分发平台；
为此次开发期字段调整新增协议代际、兼容双栈、旧数据迁移或历史保留；内建或命令资源协议、Executor/Skills
开放配置；实时 Worker 能力枚举；Provider 健康探测进入发布事务；Profile 完整复制进 Manifest；
Worker 动态重选资源；设计文档本身不增加 Web 或 OpenAPI 路由，也不代表已自动提交实现。

真实需求出现后再扩展：异名映射、新 Tool Kind、受控 Executor/Skills 协议、凭据历史值固定
与跨进程 Attempt 恢复、发布审批或共享 Draft。每项都要同步输入 / Manifest / Adapter 契约
与测试；稳定发布后的协议破坏性变化再显式升级版本并保留既有运行快照语义，
不通过开放 map 或隐藏默认行为偷渡。

## 18. 完成定义与交接

本次文档完成表示：两来源模型、无 Environment / 无用户绑定表、同名算法、Storage、Profile 直接录入 / 内部凭据、
平台静态校验、节点工具边界、发布事务、Outbox、十一个验收案例和路线图已经一致表达。
Profile 前置能力已实现；Deployment 的规划 API 与 Worker 执行能力仍待代码和测试证据。

后续声明 **Control Publication 已实现** 至少满足：

1. Input / Manifest / Event Schema、Golden Fixtures 与 Compiler / Worker Contract 一致。
2. 两个完整内部 Source Reader 与真实 ProfileCredentialChecker Adapter 贯通；复用已实现
   的 Profile 写入、加密存储和元数据检查，不用 Fake 成功替代真实拥有者接线。
3. Tenant / Session / 角色、名称匹配、闭包和节点入口校验覆盖全部设计案例。
4. Revision / Manifest / Outbox / Receipt 原子提交，CAS、强延迟幂等、不可变性与损坏读取通过真 PG。
5. 八路由、DTO、OpenAPI、Summary / Full Query 与真实 Handler 一致。
6. Unit / Contract / Race / Vet / Build / Compose 和机器断言零 Skip 的真 PG 门禁通过。
7. 若 Relay 未交付，明确 Outbox=PENDING、尚未分发；若 Gateway / Worker 未交付，保持运行面未实现。

后续改动按文件所有权协作：Deployment 修改自身协议、Compiler 与发布模块；Profile
专属文档、Schema、示例和存储由 Profile Owner 维护。本文已按当前主线对齐 Profile 事实，
继续深化 Deployment 时复用该契约，不把历史设计快照重新覆盖为旧 ref-only 实现。
