# Control 策略与当前授权快照 reader

此 adapter 使用 pinned scope/epoch、HTTPS origin、private CA 和 client cert。
TLS 1.3、不使用环境代理、不跟随重定向、单请求 5 秒、响应/header/连接数有界，
错误不带 response body 或 URL/TLS 诊断中的敏感内容。

## 历史文档

`Fetch` 服务已接线的策略通知消费者：校验可信通知后按精确 ID/revision/digest 读取
不可变策略。成功不更新当前授权新鲜度。历史路径与当前快照共用有界 HTTP transport
及精确 reference decoder，但快照不会伪造 publication event 来调用历史入口。

## 当前集合

`ReadAuthorization(ctx, trustedTarget, stage)`：

1. 记录本机 monotonic 请求起点，向当前 manifest POST 请求状态，要求响应 no-store。
2. 校验 scope/epoch/tenant/account/provider，再读取 manifest 的精确策略引用，并核对
   策略年龄上限；不先向 stage 传主体，也不以旧的策略事件选择“当前”版本。
3. 顺序请求同 generation 的全部分页；每页不超过 128 条，闭合 Schema 和共享 proof
   校验后才传给 stage。整次读取最多 30 秒且不超过最初起点加策略年龄上限，HTTP、
   分页、策略解析和 stage callback 均消耗这同一预算。
4. 最终 count/digest/终止证明全部通过后，返回 manifest、精确策略及本机起止期限。
   返回值没有无限主体数组。任何错误都返回零值，包括 stage 后发生的错误。

stage 必须只写未提交的暂存区并响应 context，不能因为看到 Complete=true 就提交。
调用方只有在 ReadAuthorization 和自身安装 fence 都成功后才发布整个集合。正式
`authorizationpostgres.Store.Refresh` 已实现这一规则，包括实际 DB 行摘要回读。
409 返回 SNAPSHOT_CHANGED，不自动重试或给旧 manifest 续期；上层必须丢弃所有暂存。

期限基于此 adapter 发出新、不可缓存的受信 mTLS 当前读取之前的本机 monotonic 起点，
不依赖客户端/Control 相同 wall clock，也不使用 CapturedAt 延长有效期。该规则依赖
Control 当前接口为每个请求打开新的数据库快照；不能把它替换成缓存的历史响应。
返回的本机时间不作为跨进程/PG 的授权凭据。PG installer 在调用 reader 前另取 DB
clock anchor，使用自己的时钟域；分布式时钟运维条件仍需 F10/F12 验证。

实现和协议测试已移动到公开包 `platform/channel/authorization/controlhttp`；此包保留
Gateway 的兼容别名。周期刷新调度器位于公开包 `platform/channel/authorization/refresh`。
Gateway 当前刷新已有显式 opt-in 装配；Worker 使用独立身份、数据库及 opt-in 装配。
生产 Admission 授权 guard/emission 尚未启用，Worker 授权请求继续零 Attempt 等待。
每条消息同步读取 Control 不是目标实现。完整 F01–F12 验收保持未完成。
