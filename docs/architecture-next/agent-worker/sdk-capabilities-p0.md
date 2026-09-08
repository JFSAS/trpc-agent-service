# Agent 数据能力：P0 SDK 映射与协作契约

日期：2026-09-09。集成基线：`4b1382cc848e55ebfa0088c423b5769394c398e2`。

## 状态与文件归属

这是实现中的 P0，不是全能力可用声明。Worker 当前新增装配 helper 与逻辑 Memory scope 的契约测试；生产 Manifest gate 保持关闭，executor 尚未从新增声明调用这些能力。

- Control：AgentSpec / Profile / Manifest schema、DTO、编解码、领域校验、编译与薄代理。独立工作树 `trpc-agent-service-control-data-contract`。
- Worker：SDK 装配、数据库 Adapter、运行数据拥有方、内部接口、权限与 tracing、Attempt 语义、`worker_v1.go` 门禁、Go 依赖和总集成。
- Web：`web/` 单写入，消费已提交契约；真实保存、回读、校验与发布。
- Gateway：维持 v1 请求与文本 Final，不传新 subject 字段，只做兼容性回归。

## AgentSpec 字段方向

最终规范以 Control 的 JSON Schema / fixtures 为准；本表是已协调的 SDK 映射。

| 字段 | 语义 / SDK 映射 |
| --- | --- |
| `node.memory.tools` | 必填唯一数组，可空；SDK `memory_add/update/delete/clear/search/load` 的完整名称；从最终 `memory.Service.Tools()` 筛选，仅显式选中工具可见 |
| `node.memory.preload_limit` | 可选；缺省/0 关闭；-1 加载全部；正安全整数为 SDK adaptive 条目预算，映射 `WithPreloadMemory`，不是 Token 预算；小于 -1 拒绝 |
| `node.artifact.enabled` | 显式启用 `runner.WithArtifactService`；不自动生成文件工具 |
| `runtime.summary.enabled` | 会话级摘要生成配置；缺省关闭 |
| `runtime.summary.model_slot` | 启用时必填，声明于模型 requirements，进入 Manifest 实际模型闭包 |
| `runtime.summary.event_threshold` | 启用时显式正整数，对应 `summary.WithEventThreshold`；不隐式增加 Token/输出限额 |
| `node.add_session_summary` | 显式 true 消费摘要，依赖全局 summary 启用；映射 `llmagent.WithAddSessionSummary` |
| `node.knowledge_slots` | 保留既有入口，解析检索资源，最终 `llmagent.WithKnowledge` |

Summary enabled=false 禁止带模型/阈值，避免非活动参数。Memory 空工具且 preload 缺省/0 不激活资源闭包。旧对象新增字段缺省必须保持 canonical/digest 兼容，不只检查可解析。Profile 存在资源不自动启用 Agent 能力。

## SDK v1.11.2 装配

Memory 使用最终 SDK `memory.Service`，同时装配工具与 Runner 服务。SDK 六工具从 invocation 获取 MemoryService；因此 Runner 必须传入执行语义包装后的同一服务。禁止 SDK 默认 Session UserID（当前为固定 `session`）直接成为长期记忆隔离键。

Summary 复用 SDK `summary.NewSummarizer(model, summary.WithEventThreshold(n))` 和 Session 摘要方法。PostgreSQL/Redis Session 后端有 Summarizer 选项，但当前 Worker overlay 的摘要方法是 no-op，必须另行补齐持久语义，不能只开上下文注入选项。

Artifact 组合 S3 字节与 Worker SQL 元数据，提供一个 SDK `artifact.Service`。SQL 元数据不增加用户第二逻辑槽。SDK 初始文件版本为 0。管理上传与模型工具开放分开。

Knowledge 的 SDK `knowledge.Knowledge` 是 Search 接口。导入需要独立解析、分块、Embedding、Qdrant 写入与 Worker 文档/索引可见状态链，不把上传内容塞进 Manifest。

当前本地独立数据库 SDK 模块与 root SDK 版本不同；引入前需编译验证，不能将“有缓存源码”当成可直接兼容。

## Memory 作用域与提交语义

逻辑作用域编码：`StableID("mem", JSON(["worker-memory-scope/v1", TenantID, SocialIdentityID, stableAgentID]))`。

- `SocialIdentityID` 沿用可信请求的 Tenant + Provider + Account + Sender。
- `stableAgentID` 来自已验证固定 Manifest 的 `sources.agent.agent_id`。
- 不以 Control 登录 User、SessionID、AgentVersion 或 DeploymentRevision 替代。
- JSON 数组保留组件边界；不隐式修剪/合并不相同身份。
- Scope 派生不是授权；调用前必须完成请求、Manifest 来源、租户和权限验证。
- 同 Agent 跨会话/发布版本保持逻辑作用域。物理后端位置独立，更换目标不自动搬迁数据。

失败 Attempt 不写正式 Memory。同 Attempt 读己之写；成功接受结果须与待应用变更可靠持久关联，PG/Redis 应用幂等可恢复。后续 Run 的可见水位和 pending apply 恢复尚待实现；不使用接受后的内存回调代替可靠记录。自动提取入口同样不得绕过该规则。不新增手动 Memory 管理产品或通用跨库事务平台。

## 分批门禁

1. P0 AgentSpec schema / canonical 回归 + SDK 装配 / scope 基础测试。
2. Profile / Manifest DTO、编译闭包、诊断，默认和旧快照兼容。
3. Session PG/Redis → Summary → Memory PG/Redis → Artifact → Knowledge，逐项集成。
4. Artifact/Knowledge 管理链和真实 Web 保存回读发布。
5. 同固定 Manifest 的 Worker 运行与 Gateway 回归；真实 IM 与 fixture 分开验收。

每项必须具备真实后端读写/隔离/恢复证据才登记可运行。未接通时失败关闭，不能发布后静默忽略能力。保留旧工作树未提交代码，按能力择取，不整目录覆盖 Tracing。

## 测试生命周期修复

重复 race 回归观察到取消测试在 SDK flow 尚读全局 logger 时恢复 logger 的竞争。取消场景改用同一个带 race 检测的测试二进制子进程，保持原断言、固定该进程的 SDK globals 至退出；仍关闭测试 runtime/HTTP fixture，不改生产 logger，不用 sleep 掩盖竞争。
