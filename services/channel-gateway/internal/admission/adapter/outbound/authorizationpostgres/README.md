# Gateway 当前授权快照原子安装

`Store.Refresh(ctx, trustedTarget, reader)` 接受当前快照 reader，不接收历史事件来冒充
当前状态。`controlpolicy.Client.ReadAuthorization` 实现该 port；双方只依赖 Admission
领域 port 与共享 wire Schema，不互相引用 adapter 实现。

## 事务与可见性

1. 打开 PG 事务，先取 `clock_timestamp()` 和随机 stage ID，再调用 mTLS reader。
2. 每页闭合校验、身份/generation/cursor 校验及流式摘要后，用 CopyFrom 写当前事务的
   `gateway_authorization_staging`。不取活跃 head 锁；外部事务看不到未提交暂存行。
3. reader 成功后，独立重验完整 manifest、精确策略文档及其年龄上限，并从**实际暂存
   行**重算 count/digest。没有 complete 页、错误被 reader 吞掉、重复 external ID、
   不匹配文档或摘要，均回滚。
4. 取得账户级 advisory lock 和 head FOR UPDATE，只在安装阶段串行化。较早开始、
   较晚到达的并发读取返回 SUPERSEDED，不覆盖或阻断后来已安装的观察。
5. 之后开始的读取若发生 epoch/账户身份冲突、generation/账户/策略回退、同 generation
   内容冲突，持久阻断现有 head。保留旧主体和策略，不清除撤销；没有自动解封路径。
6. 安装在同一事务内替换 head 与完整主体集，**从实际安装行**再次校验摘要，再清理暂存
   行并提交。失败或取消不会暴露半份集合；持久阻断的提交分支也先删除暂存数据。

Gateway runtime 的数据库级权限只有 CONNECT，本实现不执行 CREATE TEMP TABLE，
也不新增 TEMP/CREATE 权限。暂存表由迁移 0015 创建，Refresh 只用既有 DML 权限。
暂存行在成功/阻断提交前删除，失败和崩溃由事务回滚；不需要后台 TTL 清扫孤儿行。

## 期限与来源

- `fresh_until = 最初 PG anchor + 当前策略 authorization_max_age_ms`，不是最后一页
  完成时间，也不是把客户端 wall clock/ExpiresAt 序列化后写进数据库。
- HTTP、stage、账户锁等待、bulk 安装、重验都消耗原期限；提交前再次读取 DB clock。
  延迟提交不会改变固定截止时间。显式检测 DB clock 回到本次 anchor 之前。
- 分布式数据库时钟稳定性和小幅回拨仍是 F10/F12 必须校准/监测的前提，不宣称已实现
  30 秒撤销 SLA。客户端另用本机 monotonic deadline 限制整次读取。
- SQL generation 是 Control 账户当前状态版本，不是 JetStream ACK/offset；本安装
  不跳过历史流、不修改来源历史 pin、也不自动恢复已阻断的源。

## 当前完成边界

此包存储的是完整授权**源数据**，不是 AdmissionAuthorization。它尚未接入生产
周期刷新/Bootstrap、Admission 同事务决策、Worker 当前授权或 Session/Quota/工具
完整依赖验证。`blocked_reason` 和 `fresh_until` 后续必须在同库接纳事务中检查。
默认开关和既有热路径保持不变；任何直接查询暂存表的授权路径都是错误接线。

真实测试覆盖仅 DML 且无 TEMP/CREATE 的角色、257 主体/撤销保留、旧集合在 stage
期间可见、失败/取消/deferred commit 回滚、SQL 阻断持久性、乱序并发与期限。
故意改写暂存行或安装行的触发器也被摘要回读检测。另有真实 mTLS client + HTTP 协议
夹具 + PG 原子安装测试；HTTP 对端不是正式 Control 进程，也没有真实 Bot/Worker。

同一个 policy ID/revision 的内容不可变，独立于快照 generation：即使主体或账户
变化推进了 generation，相同策略 revision 的 digest 变化仍持久阻断为
`SNAPSHOT_CONFLICT`。数据库 guard 同时约束 digest 和完整 policy JSONB。

原子安装实现现复用公开的 `platform/channel/authorization/postgres`。Gateway adapter
保持原来的 New/Refresh/错误契约与所属表；Worker 使用另一固定表族和自有数据库。
共享算法不会共享运行状态、数据库权限或来源资格。
