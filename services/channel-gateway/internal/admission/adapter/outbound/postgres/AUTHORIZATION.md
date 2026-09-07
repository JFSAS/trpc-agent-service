# Admission 当前授权事务门禁

`WithAuthorizationGuard` 是与 Connection/Route guard 同级的同库 adapter 装配口。
`authorizationpostgres.Store` 实现该口，不在入站热路径访问 Control。

## 事务顺序

1. 先查询已有 Receipt。命中只校验 SourceDigest，返回首次决定，包括 `denied`。
2. 沿用账户/事件去重/连接 owner/路由 fence。事件锁下再次检查 Receipt。
3. 新 Run 候选读取当前授权 head `FOR SHARE`；它与快照安装的 `FOR UPDATE` 互斥。
   持锁后读取同 generation 的主体。校验 scope/epoch、租户、账户、完整策略摘要、
   主体状态、主体 allowlist、`message.send` 操作和群范围。来源认证后的规范化
   SenderID/ConversationKind 是输入；不从文本/昵称推断身份或私聊。
4. 未知、过期、阻断、损坏、缺依赖返回暂时不可用，不写永久拒绝。确定拒绝写
   `denied` Receipt，不占 Run 容量，不建 Admission/RunRequested。
5. 允许分支写既有 Receipt、Admission、RunRequested Outbox。两种确定决定都在
   同一事务写 `gateway_admission_authorizations` 和
   `gateway_authorization_audit_outbox`。事实固定 actor、租户、路由 generation、
   主体 revision、策略 revision/digest、decision/reason 及原快照期限；没有完整
   策略或主体名单。audit_event_id 由数据库生成，可供后续幂等投递。
6. 最后以 DB clock 重验期限；锁不阻止时间流逝。过期或审计写入失败会回滚全部
   事实/Outbox/预算。重复事件不重新授权，也不重复产生审计事件。

## 本轮边界与后续必须完成的工作

- 当前只显式装配到真实 PG 集成测试，**生产 Bootstrap 尚未接入**。已有刷新开关
  不会自动开启这个门禁，不能据此宣称生产多租户授权已完成。
- `PUBLIC_LIMITED` 不产生 ALLOW，直到隔离 Session/低配额/敏感工具隔离就绪。
- ALLOWLIST 的本地主体/操作/群判定已实现，但 SessionPolicy/TenantQuota 引用的
  完整已发布依赖验证、入站租户配额必须在生产启用前接入；当前 ALLOW 是该门禁
  的局部判断，不是对这些依赖已验证的声明。
- AdmissionAuthorization 已进入 RunRequested 的可选闭合 authorization 字段，
  Worker intake 保留它并纳入身份摘要；当前 Worker Claim 与迁移 0005 拒绝为这类
  请求分配 Attempt。下一步接独立授权投影和 Attempt/工具/审批检查，替换该等待
  门禁而不是删除它。审计 relay/容量/保留策略也仍待完成。
- ConversationKind 暂作为进程内规范化元数据（JSON 排除），授权事实另行保存。
  不改变旧 RunRequested v1 闭合 schema，也不破坏从既有 input 重建 ReplySnapshot。
- 0016 是新增迁移；不修改已发布迁移。回滚演练仅恢复源码副本，不删除数据库事实。
