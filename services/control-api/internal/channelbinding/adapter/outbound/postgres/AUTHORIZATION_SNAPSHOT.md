# 当前账户授权快照

## 目的与边界

`ReadAuthorizationManifest` 与 `ReadAuthorizationPage` 提供当前状态，不复用历史
`ReadAccessPolicy` 的“精确旧 revision”语义。它们由正式 RuntimeService 和独立
mTLS listener 装配，要求显式 `channel_policy_projection` capability；不在公开
Session 路由注册，也不增加 Gateway 的拓扑写权限。

- `POST /internal/v1/channel-authorizations:snapshot`：schema_version/account_id。
- `POST /internal/v1/channel-authorizations:page`：上述字段加 source_epoch、generation、
  after_principal_id；第一页 cursor 为空。调用者不传 tenant、scope、角色或自定义 limit。
- 请求上限 8KiB；manifest 上限 8KiB；每页最多 128 个主体且响应上限 1MiB。
- 全部响应 `no-store`；无 query/compression；权限在请求体解码前检查。
- 账户/策略不存在统一走既有不可枚举错误；epoch 或 generation 不匹配返回 409。
  上层必须抛弃整份暂存分页并重新开始，不拼接、不清除撤销状态、不续期旧 manifest。

## 一致性与完整性

新迁移 0008 为每个账户创建单调 generation，种子覆盖既有账户。数据库 trigger
与 principal INSERT/UPDATE/DELETE、policy head INSERT/UPDATE/DELETE、account
INSERT/UPDATE 在同一个事务内更新 fence。修改回滚时 fence 同时回滚；到版本上限
时整个变更失败。禁止 generation 删除/倒退/跳跃、跨账户移动主体；因此普通 SQL
也不能绕开应用命令后让旧分页保持有效。超级用户关 trigger、备份恢复等不是此 fence
提供的来源恢复保证，仍需 source epoch/恢复审计与运行投影的单调安装检查。

manifest 在单个 repeatable-read 事务内读取：

1. 受信 scope/source epoch、owner 派生 tenant/account/provider。
2. SQL generation、当前 account_revision/enabled。
3. 已提交最新 policy head 的精确 ID/revision/digest；独立重验完整策略文档。
4. 所有当前主体（含 REVOKED）的总数与流式摘要。

主体排序、索引、分页游标比较都显式使用 PostgreSQL `COLLATE "C"`，与 Go 字节
顺序一致。全量主体通过 rows 流式读取，摘要只保留 hash/last/count；不把无限列表
放入事件、manifest 或内存数组。每页在自己的 repeatable-read 事务中验证同一个
SQL generation，最多读取 129 行（第 129 行仅判断是否存在下一页）。

复用的 Tenant owner 端口使用 `SELECT FOR SHARE`，因此这些事务没有设置 PG
`READ ONLY`；它们不写业务数据，保留 owner 现有租户状态校验和有界共享锁。
每个读事务有 5 秒 context 上限。高频变更或大集合超时保持失败/重试，不降级为
不完整集合；实际容量和恢复吞吐仍需 F10/F12 的负载测量。

共享 wire proof 的摘要算法为 SHA256(domain label + 逐条 JCS principal JSON + LF)。
Manifest 由受信 mTLS 通道取得；proof 检查每页完整身份、generation、cursor、顺序、
终止标记、总数及最终 digest。省略、乱序、跨 scope/账户混页、篡改、重复或额外页
都不产生成功证明。任何 Add 错误使 verifier 永久失效，不安装部分成功的列表。

## 新鲜度尚未接线

`captured_at` 取最初读事务的 transaction_timestamp；它不晚于该事务首次快照查询。
`authorization_max_age_ms` 来自校验过的当前策略（上限 30000，仍是设计初始值）。
分页没有新的 capture/fresh_until，也不能把“刚取完最后一页”当作新鲜度起点。

完整 proof 只证明这份账户状态一致。Gateway 下一步仍须固定受信身份、校验 epoch
与单调 generation、扣除请求/分页耗时并处理时钟假设、解析精确策略及其依赖、原子
安装完整主体集合，随后在 Admission 同库事务中检查 fence。SQL generation **不是**
JetStream sequence/ACK watermark，不可据此跳过历史流或宣布缺口恢复完成。
Worker 独立授权、新鲜度/撤销传播门禁和完整 F01–F12 尚未完成。
