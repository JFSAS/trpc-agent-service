# Telegram 双接收模式：代码实现与集成记录

日期：2026-09-07。本文描述本轮源码，不以此前计划或真实 Webhook 历史验收代替新功能验证。

- 工作树：`/Users/jfs/Projects/trpc-agent-service-channel-gateway`。
- 集成分支：`codex/channel-gateway-receive-modes`。
- 开发基线：`f61d49b09b8ae534f39f8065ddec699aa35ee10e`。
- 本轮交付：本地代码、迁移、测试、Compose 配置及说明；不包含远端 main 合并或现有部署更新。
- 同一个 Go Gateway workload；没有新增 Node 镜像、Connector/Poller 服务。Helm 仍等最终 workload 集成。

## 1. 最终采用的账户协议

| 项目 | 实现 |
| --- | --- |
| 配置归属 | `ChannelAccount.config.receive_mode`，不是 ChannelBinding 属性 |
| 新账户默认 | `long_polling`；显式 `webhook` 可选 |
| 旧账户 | Control 0003 迁移为显式 `webhook`，原接收意图保持；账号、连接和目录版本同步递增 |
| 凭据元数据 | 两个稳定槽位；LP 只要求 Token，Secret 可以 `configured=false`；Webhook 启用要求两项 |
| Webhook 路径 | 两种模式均保留服务器生成的路径，租户不提供任意回调 URL |
| 修改 | 先停用，再带 CAS 保存 mode，再显式启用；保存不调用 Telegram |
| 原字段保留 | 切换不清空 Secret、Receipt、cursor 或路由绑定 |
| 物理身份 | Control 对 Telegram Bot ID 建跨 scope 唯一索引，disabled 仍保留占用 |
| 旧创建重试 | `X-Channel-Create-Contract: webhook-v1` 只用于无 config 的旧 Telegram 双凭据请求，保留旧 canonical MAC/receipt |
| 返回兼容 | `X-Channel-Result-Contract` 经 Web BFF 保留，旧 pending 不能被新默认静默解释 |

0003 遇到存量跨 scope 重复物理 Bot 时整个迁移失败，不自动选择赢家。管理面的平台内唯一
约束不等于 Telegram 其他客户端被全局锁住；第三方轮询冲突仍按实际 409 处理。

## 2. 已落地的代码结构

以下路径相对本文工作树；不是尚待创建的占位目录。

```text
platform/im/telegram/
  client.go, updates.go, webhook.go, client_test.go
    单次有界 HTTP 请求、原始 Update JSON；无 SQL/Control/NATS 依赖

services/channel-gateway/
  internal/connection/
    domain/telegramreception/state.go
      物理 owner、调用窗口、持久 cursor、闲置恢复规则
    domain/accountcatalog/
      mode-aware 快照、消费者和授权上下文
    application/telegramruntime/runtime.go
      统一 owner 下的两种接收模式、远端核验和切换
    application/telegramruntime/legacy_registration.go
      保留旧注册表适配器所需类型；生产不再启动独立旧注册 owner
    application/preflight/
      mode/policy、N/A、原始 global 与 effective digest
    adapter/outbound/telegramreceptionpostgres/
      SQL lease、BEGIN_CALL、cursor CAS、最终 Admission fence
    adapter/outbound/telegramregistration/
      公开 Telegram HTTP 包到应用端口的映射
    adapter/outbound/controlhttp/
      共享 API 契约与真实 Control 进程联合测试
  internal/admission/
    adapter/inbound/telegramadapter/normalize.go
      Webhook/长轮询唯一共用的原始 Update 规范化
    adapter/outbound/postgres/store.go
      新接纳前及提交前检查 Telegram polling owner
  internal/bootstrap/
    telegram_polling.go
      注入本地资格，不写进来源消息摘要
    telegram_receive_modes_integration_test.go
      原始 HTTP + mTLS fixture + 真实 PG/NATS 双模式纵切
  migrations/0011_telegram_receive_modes.sql

services/control-api/
  internal/channelbinding/{domain,application,adapter}/
  migrations/0003_telegram_receive_modes.sql

api/schemas/channel/v1/
  preflight_receive_modes.go  # 三端共享 digest/结果验证规则
api/openapi/control/v1/
web/lib/, web/components/, web/app/
  接收模式、可选凭据、CAS/旧 pending、BFF 和模式化预检
web/test/channel-receive-modes-real-e2e.mjs
  真实 Session/BFF 管理操作验收，不调用 Telegram
```

公开包只处理 Telegram 协议。应用层消费小接口，SQL/HTTP Adapter 向内实现接口，由
bootstrap 组装。Routing、Admission Outbox、Delivery 和 Worker 没有按接收模式复制流程。

## 3. 接收与确认算法

1. 新鲜 Control snapshot + instance qualification 产生 `telegram_receiver` permit。
2. SQL 按物理 Bot ID 获取 owner，绑定 scope/account/source epoch/connection revision/
   instance boot/owner epoch；随后才解析 Token。receiver resolve 不取得 Webhook Secret。
3. GetMe 验证物理身份，GetWebhookInfo 读取真实远端配置。
4. Webhook 模式先安装同版 Handler、持久化注册意图，SetWebhook 后读取确认；LP 模式只
   删除已知平台管理的 Webhook，再确认 URL 为空。两种操作固定 `drop_pending_updates=false`。
5. LP 以持久 next_offset 调用一次 getUpdates（limit 100，timeout 3 秒），按顺序逐条接纳。
6. 同一原始 Update 经 Normalize 获得相同 EventKey 和 SourceDigest；未知 JSON 字段不丢失。
7. Admission 在事务中验证 owner，持久 Receipt/Admission/Outbox；ignore/interaction 也要有 Receipt。
8. 成功后单独以 expected cursor + owner CAS 推进 next_offset；SQL 还要求对应 Receipt 存在。
   Receipt 已提交而 cursor 未更新时，重读先返回原 Receipt，随后重试 cursor，不生成第二个 Run。
9. 任一中间消息失败即停止批次，不跳过它去确认后续消息。NATS 发布仍走既有持久 Outbox。

Telegram 用更高 offset 确认旧 Update，所以客户端不使用 SDK 内存 offset 循环。协议事实见
[Telegram getUpdates](https://core.telegram.org/bots/api#getupdates)。

### 闲置与失败恢复

- 最近持久消息超过 6 天时用 offset=0 做不确认探测，处理官方说明的一周无新消息后随机
  update_id。只有新消息已持久化后 SQL 才允许有条件向低值重设 cursor；不用负 offset。
- 相同 EventKey 不同内容仍拒绝，不以“闲置恢复”跳过 digest 冲突。
- owner lease 30 秒；单次本地 HTTP 最多 8 秒，持久调用不确定窗口 10 秒。
- 新连接版本使旧资格失效，但新 owner 仍等旧调用窗口结束；disabled 本身不证明远端静默。
- 失败退避至少 5 秒，409 为 POLLING_CONFLICT/30 秒，429 采用有界 retry_after；不紧密重试。
- 所有请求和 worker 有界，取消后 join；standby 的 WAITING_FOR_OWNER 不冒充当前 polling owner。
- 当前八个 worker 分批协调；大规模账号吞吐/公平性尚未压测，本轮不宣称生产容量指标。

积压仍受 Telegram 保留期影响。PG fencing 约束本平台的后续行为，不撤销对方已接收的 HTTP。

## 4. 远端接管与授权

自动协调只接受空 Webhook，或与 SQL 保存的 managed/pending URL 精确相同的端点；未知
端点报告 WEBHOOK_CONFLICT，V1 没有强制接管按钮。旧注册记录迁移采用同时满足“观测 URL
与当前期望精确相同”和“历史持久 READY 证据”的规则，不能只凭持有 Token 就接管。

`telegram_receiver` 两种模式共用，要求 owner_epoch，禁止 registration_epoch，只取 Token。
`telegram_webhook` 仅 Webhook 模式取 Secret；`telegram_registration` 为旧协议保留且仅允许
Webhook；Delivery consumer 两种模式不变。Control 校验 mTLS/scope/consumer/版本；Gateway
SQL 校验真实 owner。Observation 明确 receive_mode，LP READY 要求 owner_epoch。

## 5. 预检与 Web

新任务使用 `diagnostic_policy=telegram-receive-modes-v1`，带 mode、connection_revision 和
`effective_config_digest`。旧历史任务继续旧校验；旧 worker 不领取新 policy 任务。

LP 的第 3/6/7 项固定 NOT_APPLICABLE：公共 origin、Webhook delivery errors、旧 Secret
恢复材料。第 4 项无 Webhook 为 WEBHOOK_NONE/PASS；存在任何 Webhook 为
WEBHOOK_BLOCKS_LONG_POLLING/FAIL、relation=DIFFERENT。实际消息投递仍为 DELIVERY_NOT_TESTED。

同一 lease 必须保留原 global 和 effective digest；只有新 lease 才能在 LP effective 配置
相同的情况下接受无关 origin 变化。Webhook digest 仍包含 origin。不能用 N/A 扩大其他检查项。
只读预检仍仅 getMe/getWebhookInfo，不 getUpdates、不 Set/DeleteWebhook、不发消息。

Web 已接入创建/编辑/详情、停用后切换、Secret 可选展示、启用确认、运行状态、预检历史、
旧 pending 与真实 BFF。Owner/CAS/幂等由 Control 最终裁决，Web 不直接碰 Telegram Token API。

## 6. 部署变更与回退

- 同一 `channel-gateway` 镜像增加公开 Go 客户端及 0011；Control 镜像增加 0003；Web 更新。
- Compose 让 `GATEWAY_PUBLIC_ORIGIN` 对纯 LP 可空；混合部署启用 Webhook 仍逐账户要求 origin。
- 增加 `GATEWAY_HTTPS_PROXY` / `GATEWAY_HTTP_PROXY` / `GATEWAY_NO_PROXY` 到 Gateway 环境；
  仍需出站 Telegram、Control mTLS、数据库、NATS，不新增公网入站要求或新的 NATS subject。
- 采用协调升级窗口：先停旧 Gateway 接收并排空旧预检，升级 Control 和迁移，再新 Gateway/Web，
  最后开放模式写入。严格旧 snapshot 客户端不保证与新增字段混部。
- 源码回滚验证只在另一份副本进行；不等于回滚数据库或远端 Webhook。出现新 LP 账户后，
  不直接把运行实例降到 Webhook-only 二进制；恢复部署须保留当前模式数据并单独协调。

## 7. 验证范围与可重复入口

专用 PostgreSQL/NATS、HTTP Telegram 替身和实际 Control Session/mTLS 分开验证。

```bash
# 在本工作树；PG/NATS 配置仅指向隔离测试资源。
go test -race -count=1 -p=1 ./services/channel-gateway/... ./platform/im/... ./api/...
go test -race -count=1 -json ./api/... ./services/control-api/...
go vet ./...
cd web && npm ci && npm test && npm run lint && npm run build
```

真实 PG/NATS 测试须设置 `GATEWAY_TEST_DATABASE_URL`、`GATEWAY_TEST_NATS_URL`、
`GATEWAY_TEST_ALLOW_NATS_RESET=1`。Control 套件另设 `CONTROL_TEST_DATABASE_URL` 和
`CONTROL_TEST_NATS_CONFIG_FILE`。真实 Control 子进程预检另需当前构建二进制及 owner-only
运行态 `{url,user,password}` NATS 配置，不是测试驱动的 admin/control 多用户配置。

本地已通过：公开客户端 offset/原始 JSON/重定向/错误脱敏、owner/call/cursor SQL、
HTTP→Receipt→Admission→Outbox→NATS、无 origin Token-only LP、双向模式切换、跨入口去重。
Control 完整 race 回归为 39 个有测试包、1392 项测试、0 跳过/0 失败；Web 回归 569 项、
tsc/build 通过。Web 专用真实后端/BFF 70 断言/21 请求，浏览器 31 断言，后置只读 13 断言/
4 请求通过；1440/768/390 三宽无横向溢出，所有创建的合成账户保持 disabled。

最终整仓命令、退出状态、源码 hash 和回滚副本测试写入工作树 ignored artifacts 目录的
`telegram-receive-modes-20260907/VERIFICATION.txt`，以该实测报告为最终完成依据。
本轮没有真实 Bot 切换、Worker 执行或回复闭环验收；此前真实 Webhook 记录保持原日期。
