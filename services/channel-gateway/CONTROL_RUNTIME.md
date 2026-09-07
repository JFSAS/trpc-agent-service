# Control-backed Gateway Runtime (GCI2)

## 本地启动

在仓库根目录执行。先由部署所有者准备 Gateway PG 和已 reconcile 的
NATS；Control 发放与 Gateway 实例对应的 mTLS 身份、scope 和 source epoch。
Bot Token / webhook secret 只从 Control `credentials:resolve` 取得，不写入本机 env 文件。

```bash
cd "$(git rev-parse --show-toplevel)"
export GATEWAY_ACCOUNT_SOURCE=control
export GATEWAY_HTTP_ADDRESS=127.0.0.1:8090
export GATEWAY_ADMIN_ADDRESS=127.0.0.1:8091
export GATEWAY_INSTANCE_ID=gw-1
# 以下变量由受限运行配置提供，示例不是有效凭据/服务地址：
# GATEWAY_DATABASE_URL, GATEWAY_MIGRATION_DATABASE_URL
# GATEWAY_NATS_URL, GATEWAY_NATS_USER, GATEWAY_NATS_PASSWORD
# GATEWAY_CONTROL_URL, GATEWAY_CONTROL_SCOPE_ID, GATEWAY_CONTROL_SOURCE_EPOCH
# GATEWAY_CONTROL_CA_FILE, GATEWAY_CONTROL_CERT_FILE, GATEWAY_CONTROL_KEY_FILE
# GATEWAY_PUBLIC_ORIGIN
export GATEWAY_NATS_TOPOLOGY_FILE=deploy/nats/streams.yaml
go build -o ./bin/channel-gateway ./services/channel-gateway/cmd/channel-gateway
./bin/channel-gateway
```

`fixture` 是显式开发来源；生产 `control` 模式拒绝静态 Telegram/WeCom 账户输入。
空模式只保留 Go 内部 fixture 构造兼容，不由 `LoadConfig` 默认选择。

## 实际运行接线

1. 私有 mTLS HTTP 客户端拉完整快照，PG 原子发布、30 秒新鲜度按请求开始计。
2. 可信本地账户使用上下文绑定 scope/source/tenant/account/连接版本/实例资格/客户端代。
3. WeCom 凭据 bridge 获得 Supervisor 原始 OwnerGrant，解析前后复核；源失败和换代取消旧 Client。
4. Telegram 各副本安装当前版本 Secret 的 immutable Handler；账户级持久注册 fence 的
   持有者批量解析同版 token+secret，GetMe 核对物理身份，BEGIN_CALL 后 SetWebhook。
   注册不依赖先有 Binding 或先收到一条 webhook，不使用企微租约。
5. Admission 在资格 guard 后取得幂等/预算/owner/route 锁；已存在 Receipt 仍优先返回。
6. Delivery A1 把资格/Client 绑定摘要写入 Claim；A2 必须再次匹配。实际 Sender 也在调用前
   检查原上下文。Finish/Observation/Maintenance 不重新申请当前账户资格。
7. Control 模式默认启动有界 Delivery Runner，由 Runner 独占 Maintenance 生命周期；fixture 才独立维护。不存在 Final 输入时不会制造发送任务。
8. 状态变化及 30 秒心跳经 mTLS 上报，失败不修改授权或更新 freshness。

新增迁移 `0009_control_use_binding.sql`、`0010_telegram_registration.sql`；0001–0008 保持原哈希。
上述账户目录/注册阶段不包含 Worker。后续 Worker V1 已新增 ReplyIntent durable Consumer、
真实 mTLS committed-Final verifier 和 0011 transport receipt migration，配置与事务边界见
[Gateway Worker V1 Reply 接管](README.md#worker-v1-reply-接管)。历史入站记录仍不等同于回复验收。

## Telegram 真实入站就绪条件

- Control 对真实 Telegram Account 发布 enabled 的最新完整快照，保存两项同版托管凭据。
- Gateway mTLS principal 可读取该 scope/tenant/account 以及 registration/webhook 用途。
- 公共 HTTPS origin 实际转发到 Gateway 公共 listener，Telegram 能访问当前账户路径。
- Control Relay 只发布已提交且真实固定发布目标的 route 三元组，NATS retained route 流及
  Gateway replay/checkpoint 正常；账户 route floor 已追上。
- registration 当前版本 READY；源新鲜，readyz 204。随后才请用户在机器人私聊发文本。
- 收消息验收为 Telegram HTTP → Receipt/Admission/Outbox → NATS RunRequested，固定
  tenant、binding、DeploymentRevision 和 Manifest 身份。它不等于 Worker 已执行/已回复。

## 远端注册的事实边界

PG fence 不会撤回 Telegram 已接收的旧 HTTP。超时/失去资格时保留原 operation 的 UNKNOWN
或迟到事实；旧 ACK 不标记新版本 READY，不恢复旧 Secret。当前 holder 按持久 next_due
重新协调最新期望值；正常 READY 每 60 秒重申，失败有 5 秒退避。每次注册的本地 lease 25 秒，
解析/SDK 操作各最多 5 秒；进程最多 8 个并发账户。凭据材料不进入注册表或 observations。


## 2026-09-06 真实入站验收记录

实际 Control mTLS 与 Gateway 进程已接入真实 Telegram。06:55:45 +08:00 的测试消息
update_id=`309271229` / message_id=`6`，产生
admission/event_id=`c00f77d6b3a94dcd98b77ccf071b047b`，
run_id=`45ccc1ec2d4b600a656ed0b489497e5c`。PG Inbox/Admission/Outbox/published各1，
NATS RUN_REQUESTS_V1 sequence1 的严格Schema和规范payload匹配，DeliveryIntent=0。

[完整脱敏验收报告](../../docs/architecture-next/channel-gateway/telegram-real-inbound-20260906.md)
区分真实入站与 Worker/Manifest正文/模型/Storage/回复执行，并说明 PubAck 交叉证据而非原帧抓取。
本次目标的模型与Storage配置仅为 admission-only fixture，没有宣称其真实执行能力。


## 自建 Telegram Bot API origin

`GATEWAY_TELEGRAM_API_URL` 是可选的运维配置，空值沿用 SDK 默认 `https://api.telegram.org`。
同一个固定 origin 同时注入 registration 的 GetMe/SetWebhook 和 Delivery 的 GetMe/SendMessage，
不由租户 Account/Profile 字段选择；它与入站 `GATEWAY_PUBLIC_ORIGIN` 是两个方向。
支持 HTTPS origin，或字面量 loopback IP 的 HTTP origin（如 `http://127.0.0.1:8081`）；
拒绝非 loopback HTTP、userinfo、非根路径、query 和 fragment。HTTPS 保留系统证书与主机名验证，
不提供跳过验证。Compose 基线显式透传该可选变量；空值不改变公共端点。

自建端点仍接收 Control 托管的 Bot Token；GetMe 的物理 Bot ID 必须与 Account 一致。
账户资格、注册 fence、webhook secret、Route、Worker committed proof 和 Delivery A2 门禁保持不变。
联合 gate 可将此 origin 指向本地外部 Telegram HTTP fixture，同时运行真实 Control/Gateway/
Worker 二进制及 PG/NATS。该结果证明内部跨进程闭环，不代表真实 Telegram 账号验收。
## Telegram 接入预检（Gateway 实现，跨端验收独立）

Gateway 在 `control` 模式由 LoadConfig 默认启用独立预检 Runner，固定4个执行槽、
每实例共享最多2次claim/s；它不依赖账户enabled、运行目录READY、Binding或Worker。
Go直接构造Config的调用方需明确设置 `TelegramPreflightEnabled`；fixture模式不运行预检。

部署继续使用同一个channel-gateway镜像/进程。升级顺序是先发布Control预检接口并给
该Gateway的mTLS principal增加 `telegram_preflight` consumer kind，再部署Gateway，
最后启用页面功能。该名称不是新的凭据purpose，Token仍是 `telegram.bot_token`。
Control未升级或尚未授予诊断权限时，可设置：

```bash
export GATEWAY_TELEGRAM_PREFLIGHT_ENABLED=false
```

设置true启用诊断不会启用任何账户、注册Webhook或发消息；凭据只经新的诊断resolve取得，
普通disabled凭据守卫不改变。预检只用getMe/getWebhookInfo，所有结果仍保留
`DELIVERY_NOT_TESTED`和旧secret不可恢复的说明。独立HTTP/mTLS替身测试不等于
真实Control Handler、数据库任务或真实Telegram已完成验收。

新增设计见 [Telegram预检执行设计](../../docs/architecture-next/channel-gateway/telegram-preflight-v1.md)。
无需新Node镜像、Connector进程、Gateway迁移或NATS subject；Helm仍在最终workload集成阶段。
