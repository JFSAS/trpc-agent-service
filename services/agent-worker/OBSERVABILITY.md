# Worker V1 运行信号

Worker 默认向 stdout 输出结构化 JSON；可通过显式配置将低基数 Metrics 发送给 OTLP HTTP
Collector。这些信号不是 Receipt、Completion、授权或费用账本，观测失败不改变执行结果。

## 信号与含义

| 信号 | 内容 |
| --- | --- |
| `worker.operation` JSON | 固定 operation/result，内部 Tenant/Run/Attempt ID，阶段时长；不携带原始错误 |
| `worker.backlog` JSON | 队列/运行/重试数量，未发布 Reply，最老未完成 Run 和 Reply age，本机活跃数 |
| `worker.operation.count` | 按固定 operation/result 统计操作尝试，不是唯一业务完成数 |
| `worker.operation.duration` | 操作时长直方图，单位秒，包含 prepare/execute/Session/commit/renew/Drain |
| `worker.provider.tokens` | SDK 实际返回的 input/output/total usage；不预占、不结算、不施加额度 |
| `worker.backlog` Metrics | `queued/running/retry_wait/reply_pending/active_local` 五种固定 kind |
| `worker.oldest.age` | 最老 unsettled Run/unpublished Reply 的秒数；不是每租户 label |
| `worker.storage.sample.success` | 最近一次积压采样是否成功；0 时不要将旧 gauge 当作最新事实 |
| `worker.observation.dropped` | stdout 写入阻塞时丢弃的观测事件数量 |

operation 包括 intake、manifest、claim、prepare、credential_resolve、session_open、session_load、execute、session_stage、complete、
renew、fence、terminalize、manifest_apply、wire_reject、reply_publish、startup、drain 等有限枚举。
Session 初始化额外记录封闭的 stage（parse_config/connect/target_query/namespace_query/table_query/candidate_acl），
用来区分连接构造、目标校验和表权限；stage 不携带底层错误，不加入 Metrics label。
result 区分冲突、容量、Manifest 等待、Session 等待、凭据拒绝、Session 准备/完整性、依赖错误、
失租、取消、deadline。常规成功续租/Fence/等待轮询只记指标，避免逐次轮询刷日志。

成功 complete 计数表示数据库调用成功或重放，并非不重复的 Run 数；唯一 Completion 仍查
Worker 账本。execute 表示 SDK 执行阶段，不将它包装成所有实际 HTTP 请求的网络计数。
Provider 没返回 usage 时不合成 0 Token 成本。各指标均无 Tenant/Run/Session、URL、正文、
自由文本错误等高基数 label；关联 ID 仅出现在结构化日志中。

积压来自 Worker 自有表的只读查询，使用未完成 Run/未发布 Reply 的 partial index，不扫描
已完成历史、不加业务行锁。数据库级 gauge 在共享同一 Execution DB 的副本间重复，查询
使用 `max` 而不是 `sum`；`active_local` 才是进程局部数。采样超时只使 sample.success=0，
不把观测读取故障升级为新的 Run 失败或 readiness 判定。

## 显式启用 OTLP Metrics

可在 `WORKER_CONFIG_FILE` 的根对象增加：

```json
{
  "telemetry": {
    "metrics_endpoint": "https://collector.example.com/v1/metrics",
    "export_interval": "10s",
    "export_timeout": "2s"
  }
}
```

这是需要合并进完整 Worker 配置的片段，不是独立启动配置。三个字段缺一、未知字段、null、
带 userinfo/query/fragment 的 endpoint 都会拒绝。HTTP 仅用于字面回环地址的本地 Collector；
远程使用 HTTPS 和系统信任根，默认不降低证书或主机名校验。

省略 telemetry 对象时不会创建 exporter，也不会从环境变量偷偷继承 OTLP 网络目标；JSON
阶段/积压日志仍工作。显式 exporter 使用后台 PeriodicReader，有界 export timeout，无透明
无限重试；不会持有 Loki/Tempo/Grafana 凭据。Resource 固定 service.name=agent-worker、
service.version=worker-v1、namespace=agent-platform，并记录 worker instance；deployment
环境当前标为 unspecified，实际环境可由部署的 Collector 资源处理器标注。

stdout 采用 256 条有界队列。队列满时丢弃观测并递增 dropped 指标，业务流程不等待日志 I/O。
关闭时先停业务任务，再在既有 HTTP shutdown timeout 内 flush/shutdown；阻塞的 stdout 或
失联 Collector 不让进程无限等待。Metrics 与日志不是不可丢弃的业务 Audit。

## 运行检查

- intake 增长但 queued/oldest_run 持续增长：检查 Manifest 等待、Session 队首、prepare 和依赖错误。
- complete 成功但 reply_pending/oldest_reply 增长：检查 NATS PubAck、权限与 Reply Relay。
- Reply 已发布而用户未收到：继续查 Gateway 的 Completion proof、Delivery 状态和 Provider 回执，
  不把 Worker 的 publish 成功当作外部发送成功。
- fenced/renew 错误或 observation.dropped 增长：分别检查 lease/DB 延迟和日志采集阻塞。

## 验证与边界

`internal/infra/telemetry` 测试验证实际 OTLP protobuf HTTP 导出、低基数标签、敏感负例、
Collector 失联、阻塞 stdout、有界关闭、禁用 exporter 不继承环境目标。Processor 测试验证
观察成功/失败阶段不改变执行次数、Completion 或失败分类。真实 PG gate 验证积压随
Accept → Claim → Complete → Reply 发布变化，已发布历史不计入待处理积压。

本文件不宣称平台级 Trace、Collector/Prometheus/Loki/Grafana 部署、告警规则或 Dashboard
已完成。跨 NATS 的 Trace Carrier 仍需单独明确协议，不向现有严格 Reply wire 塞新字段。
