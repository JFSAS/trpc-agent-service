# Channel Gateway

Channel Gateway 是一个独立 Go Workload：**一个二进制、一个镜像、一个服务部署单元**。
Routing、Admission、Connection、Delivery 是内部业务 Module，不是四个微服务。
公开 Go Connector 保持独立于 Gateway 业务的顶层 Go 包；企业微信已在 Gateway 进程内
直接导入 `platform/im/wecom`，不增加独立 Node Connector 镜像或部署单元。

## 1. 当前已实现的纵向切片

```text
Control route event（Control 已发布绑定的真实 Relay）
  → NATS route consumer
  → 严格 schema 校验 + 完整历史 replay/checkpoint/quarantine
  → PostgreSQL Routing Projection

Telegram webhook
  → 账户绑定路由 + secret 鉴权 + 规范化
  → Admission Receipt / Inbox
  → 固定 RouteSnapshot + Admission + RunRequested Outbox（同事务）
  → NATS execution.run-requested.v1

可选 WeCom account
  → Connection Supervisor / PG owner lease
  → 公开 Go SDK connect/subscribe/callback
  → 规范化 + 同事务 owner guard
  → 共用 Admission / RunRequested Outbox
  → 首次 ReplyOrigin 仅另存于 Gateway Admission
```

- Routing 只消费已发布事实，不访问 Draft 或编译 RuntimeManifest。
- Telegram 采用 `github.com/go-telegram/bot/models` v1.25.0 DTO；自有 webhook
  Handler 在持久 Admission 成功后才返回 HTTP 200，不使用 SDK 的提前 ACK 队列。
- Telegram 入站首期只启用真实非 Bot 用户的私聊非空文本。Callback 记录为 interaction，
  group/edit/bot/service/media/未知能力记录为 ignore，均不变成 prompt。
- 同一外部事件永久对应首次 Receipt；摘要改变返回冲突，路由切换不会重新创建 Run。
- 执行事件通过版本化 JSON Schema 和 generated DTO 验证后写入 Outbox。
- Outbox 到 JetStream 是 at-least-once；稳定 EventID 在重试中保持不变。

**默认服务模式已接线**：Control mTLS 账户目录/凭据、Routing、动态 Telegram 注册/Admission、Outbox relay 与可选 Connection/企微
入站；Delivery Maintenance 在空账户时也维护旧账本；Control 模式由 Runner 独占其生命周期，fixture 模式由 App 独立维护。
新企微文本首次接纳时把 owner instance/epoch/config revision/socket generation
保存到 `reply_origin`；它不进入 SourceDigest、持久 input JSON 或 Execution wire。重放保留
首次 Receipt 和 Origin，旧库 NULL 不推测成当前 socket。

**Delivery 本轮已有源码及本地纵切**：Acceptor、PG Ledger、Dispatcher、严格 ReplyIntent
Adapter、Telegram SDK Sender、Connection 原始会话 reservation 与 WeCom Sender。
发送按 A1 claim → A2 CALLING → Provider → Observation/Finish 处理；UNKNOWN Final 不普通
自动重发，已失效 Origin 不迁移到新 socket，reservation 覆盖调用和有界结果落账窗口。

**这不等于默认启动已启用回复**：`bootstrap.App` 尚未创建 ReplyIntent NATS Consumer，
Control 模式已构造生产发送 Runner/Sender 与托管凭据 bridge；真实 Execution committed-Final verifier
和 ReplyIntent 输入仍待交付。HTTP/WS 的历史本地纵切使用明确的
`committedFinalFixture`，不是真实 Worker。Control route publisher 与真实 Telegram 入站已联通，
Worker 和完整 IM Agent 回复 E2E 仍待联通；Telegram Callback 业务命令与 `answerCallbackQuery` 也尚未接线。

历史 Delivery/0006 的六迁移镜像与本地验收保留于实施状态 §10.4。当前 Runtime 新增
Maintainer、Runner、LocalOwner、0007 与 CGR-36 分类，**最终验收已通过**，见
[实施状态 §11](../../docs/architecture-next/channel-gateway/implementation-status.md#11-delivery-runtime-v1实现与本轮验收)；不复用旧镜像数字。

## 2. 代码所有权

```text
services/channel-gateway/
├── cmd/channel-gateway/           # 单二进制入口与工具模式
├── internal/
│   ├── bootstrap/                # 配置、composition、双 listener、shutdown
│   ├── routing/                  # Projection / replay / generation guard
│   ├── admission/                # Receipt / 同事务 Inbox+Admission+Outbox
│   │   ├── domain/
│   │   ├── application/
│   │   └── adapter/
│   │       ├── inbound/{telegramadapter,wecomadapter}/
│   │       └── outbound/postgres/ # 首次 ReplyOrigin、只读回复快照
│   ├── connection/               # owner/epoch、Supervisor、受限 Sender reservation
│   │   ├── domain/
│   │   ├── application/
│   │   └── adapter/outbound/{postgres,wecomclient}/
│   ├── delivery/                 # Control 模式启用 Maintenance/Runner；ReplyIntent Consumer 待接入
│   │   ├── domain/               # Final barrier、分片、certainty、retry policy
│   │   ├── application/          # Acceptor / Dispatcher / Maintainer / Runner / ports
│   │   └── adapter/
│   │       ├── inbound/eventadapter/
│   │       └── outbound/{postgres,telegram,wecomadapter}/
│   └── infra/nats/               # 声明加载、ACL生成、topology校验/协调、传输
├── migrations/                   # Gateway 自己的 schema 与执行器
└── Dockerfile                    # distroless nonroot，单 Go 二进制
```

Connection / Delivery 是同一 Workload 内的真实 Module；目录存在不代表默认部署已启动
所有用例。公开协议库位于仓库顶层 `platform/im/wecom`，不持有 PG/NATS/租约/业务账本。
跨 Workload wire 位于 `api/events/control/v1` 和 `api/events/execution/v1`；
传输 DTO 位于 `gen/events/...`，内部 Domain 不依赖 Provider SDK 或传输 DTO。

## 3. 构建与二进制模式

从仓库根目录：

```sh
just gateway-build
```

生成 `bin/channel-gateway`。同一二进制支持：

| 调用 | 作用 |
| --- | --- |
| `channel-gateway` | 运行 Gateway 服务 |
| `channel-gateway reconcile` | 使用独立 reconciler 凭据，按 topology 声明创建/校验 NATS 资源 |
| `channel-gateway nats-config permissions.yaml` | 输出 NATS `server.conf`，只包含 secret 环境引用 |
| `channel-gateway probe URL` | 两秒超时的 HTTP probe；仅 204 算成功 |

对应 `just` 命令还包括 `gateway-reconcile` 和 `nats-config`。`reconcile` 成功打印
`NATS_RECONCILE=PASS`。服务模式只验证已有 topology，不使用管理员身份静默修复。

## 4. 服务配置

| 环境变量 | 说明 / 默认值 |
| --- | --- |
| `GATEWAY_DATABASE_URL` | 必需；使用 Gateway 自己的 database/role |
| `GATEWAY_NATS_URL` | 必需；NATS 地址 |
| `GATEWAY_NATS_USER` / `GATEWAY_NATS_PASSWORD` | 成对配置；Compose 服务使用 `gateway` 用户 |
| `GATEWAY_NATS_TOPOLOGY_FILE` | 默认 `deploy/nats/streams.yaml`；容器挂载到 `/etc/gateway/streams.yaml` |
| `GATEWAY_HTTP_ADDRESS` | public listener，默认 `:8090` |
| `GATEWAY_ADMIN_ADDRESS` | 独立 admin listener，默认 `:8091` |
| `GATEWAY_ACCOUNT_SOURCE` | 默认 `control`；`fixture` 显式启用开发账户文件，生产拒绝静态账户输入 |
| `GATEWAY_TELEGRAM_ACCOUNTS_FILE` | 仅 fixture 模式必需；最多 64 KiB 的 JSON 数组文件，允许 `[]`，最多 100 个账户 |
| 每个账户的 `webhook_secret_env` 所指变量 | 16–256 个 `[A-Za-z0-9_-]` 字符；只在运行时读取 |
| `GATEWAY_WECOM_ACCOUNTS_FILE` | 仅 fixture 模式的可选企微账户投影文件；空数组不拨号；字段为 account_id/bot_id/revision/enabled/secret_env |
| `GATEWAY_INSTANCE_ID` | Control 模式必需且匹配 mTLS 身份；fixture 模式省略时生成进程实例 ID，多副本各自独立 |
| 每个企微账户的 `secret_env` 所指变量 | 取得 owner lease 后由 Connection 解析；文件仅保存引用 |
| `GATEWAY_WECOM_URL` | 可选 SDK 连接地址覆盖；未指定时采用公开 SDK 默认地址，本地 WS 测试显式注入 |

生产所需 mTLS 文件、scope/source epoch、公网 origin 见 [Control Runtime](CONTROL_RUNTIME.md)。以下为显式 fixture 账户示例：

```json
[
  {
    "account_id": "telegram-primary",
    "webhook_secret_env": "GATEWAY_TELEGRAM_WEBHOOK_SECRET"
  }
]
```

配置的稳定 `account_id` 绑定 `POST /v1/telegram/telegram-primary`，不从 Provider
请求中的 account/tenant/binding 字段选择内部身份。Webhook secret 对应 Telegram
注册时的 `secret_token`，不是 Bot Token。上述静态 fixture 不做远端注册；Control 模式通过独立用途读取托管凭据并协调 webhook。

空账户数组用于先启动基础设施。它不代表 Bot 已创建或已配置，不代表有可接纳新 Run
的 Binding；也不会注册任何账户 webhook 路由。`readyz` 表达基础设施/投影状态，
不是“至少配置了一个 Bot”检查。

Compose 的一次性 `gateway-database` job 创建 `gateway` role 和 `channel_gateway`
database。当前源码在监听前依次执行 0001–0010：0004 持久化路由积压 episode，0005
拥有 Connection 账户/epoch/lease/replaced 隔离，0006 新增 Admission 的 nullable
`reply_origin` 和 Delivery intents/parts/attempts/observations 账本。旧 Admission 的 Origin
保持 NULL；0007 仅新增 Runtime 查询五索引，不改旧 SQL 或业务事实、不释放容量。
0008–0010 分别增加 Control 账户目录、发送资格绑定与 Telegram 注册账本。
独立维护与发送循环分开，Control 模式启动 Runner，仍不启动 ReplyIntent 消费；既有迁移记录保留，
Gateway 数据与 Control database、Profile 凭据解析职责分离。
详见 [本地 Compose](../../deploy/compose/README.md)。

## 5. HTTP、健康与关闭

| Listener | 路由 | 行为 |
| --- | --- | --- |
| public `8090` | `POST /v1/telegram/{configured_account_id}` | 鉴权、规范化、等待持久 Receipt 后返回 200 |
| admin `8091` | `GET /livez` | 进程存活返回 204 |
| admin `8091` | `GET /readyz` | route replay/来源年龄/连续积压/quarantine 与 PG 预算通过；若启用 Connection，还要求共享配置源及 Supervisor 主循环正常；通过返回 204，否则 503 |

Public listener 不注册 `/livez`、`/readyz`。Admin listener 应留在管理网络。单个 Bot 未认证、
standby 或失败不自动撤销整个 Gateway readiness；204 不代表发送 Runner 已运行或 Final 可发送。
Maintenance 已独立启动，但当前 probe 未映射其 Snapshot 失败状态。

Webhook 拒绝非法方法、错误鉴权、超过 1 MiB 的 body、重复 JSON key 和多余 JSON
对象。账户身份来自注册配置；收到的原始 Provider JSON 只在请求期间存在。
HTTP 状态：401 鉴权失败、400 输入错误、413 超限、409 事件摘要冲突、503 暂时不可用。

默认新接受预算：未发布 Outbox 10,000 条、最老 10 分钟、新 Inbox 每固定分钟窗
10,000 条；后者也约束 ignore/interaction。默认 receipt lookup 与新接受各 128 并发，
单次接受总预算 10 秒。这些是配置初值，不是压测容量承诺。真正的容量门禁位于
PostgreSQL 接受事务内，readiness 只观察结果。

路由健康区分 5 分钟来源观察年龄与 60 秒连续已知积压：PG 保存积压起点，部分进展、
观察和重启不续期，完整追平才恢复。Resolve 与接纳事务 guard 同时执行门禁；旧 Receipt
仍可重放。它不是 Control 发布到停用的端到端 SLA。
NATS 短时断连且尚未越过 freshness/budget 边界时可报告 degraded 并继续使用 Outbox。
路由历史缺口、同序号不同内容、投影损坏会进入显式阻断状态，不通过忽略错误继续 ready。
Shutdown 同时关闭新 Admission gate，并 drain 已进入 Commit 的请求；首次 Receipt
在停止期间仍可 replay。Connection 正常关闭先 quiesce、保持续租并有界 drain，再关闭
Client/释放 owner；失租立即取消。已实现的 Sender reservation 使调用与结果落账都计入
该 drain。默认 Maintenance 随 App 取消并参与后台退出等待，PG/NATS 在其退出后关闭；
Control 模式 Runner 同样参与有界关闭。未发布 Outbox 在重启后重试。

## 6. NATS 持久化与权限

`deploy/nats/streams.yaml` 拥有 Stream 声明；`deploy/nats/permissions.yaml` 经
`nats-config` 生成 `server.conf`，其中仍是 `$NATS_*_PASSWORD` 引用。

- `control` 只发布 Control route subject；这项 ACL 不代表生产发布者已经实现。
- `gateway` 发布 RunRequested，消费/ACK route durable，并验证 Stream/Consumer Info。
- `reconciler` 持有 topology 管理权限；与常驻 Gateway 角色分离。
- 当前尚无 Worker 角色和 Worker consumer，也没有 ReplyIntent Stream/consumer 的生产接线。
  `execution.reply-intent.v1` schema 与 eventadapter 已存在，不代表 NATS 已订阅该 subject。

Route Stream 保留完整历史，route consumer ACK 不删除历史。Run Stream 使用
WorkQueue retention，未来 Worker ACK 会释放已消费消息。两个 Stream 容量满时
拒绝新消息，不丢弃旧历史；不兼容 topology 需要显式迁移。

## 7. 验证

```sh
just gateway-test
go vet ./services/channel-gateway/...
```

真实集成验证需要专门的 PostgreSQL 与 NATS 测试实例：

```sh
# 外部注入 GATEWAY_TEST_DATABASE_URL、GATEWAY_TEST_NATS_URL。
export GATEWAY_TEST_ALLOW_NATS_RESET=1
just gateway-integration
```

未设置相应变量时测试会 Skip；带 reset 开关的 suite 会重建测试 broker 中固定命名
的 Streams，因此使用独立测试 broker。PostgreSQL Adapter 测试建立隔离 schema，
使用真实 workload migrations 和真实 routing generation guard，结束后清理。

单独验证 Admission 的真实 PostgreSQL 行为：

```sh
# 外部注入 GATEWAY_TEST_DATABASE_URL。
go test -race -count=1 ./services/channel-gateway/internal/admission/...
```

覆盖 concurrent single-winner、SourceDigest 冲突、route-switch replay、SQL 与 COMMIT
失败回滚、预算并发门禁、停止、claim CAS、过期与 crash recovery。测试 broker 的
PubAck、样本 RunRequested、服务 ready 都不等于 Agent E2E；后续需联通真实 Control
publisher、Worker 和生产 Delivery 接线，再验证外部 IM 回复闭环。

历史切片已记录真实 61 秒 PG apply-lag 红绿、旧库升级、生产 Consumer.Run 周期观察
驱动的双副本 HTTP 故障测试与 Gateway/API/gen race；SDK/Connection 的实际树和镜像历史
结果见[实施状态](../../docs/architecture-next/channel-gateway/implementation-status.md) §8.3/§9。
这些结果作为历史证据保留，本次 README 更新没有重新执行它们。

本 Delivery 修改副本已有两条本地纵切：真实 PG + Telegram SDK/httptest HTTP 的有序 Final，
以及真实 PG + Supervisor/公开 SDK/httptest WS 的企微原 callback → A2 → ACK → Observation/
Finish 与跨 owner Origin 拒绝。执行授权来自显式 committed-Final fixture，不是真实 Worker；
没有使用真实 IM 账户。单独复现这两条测试只需专用 PG，不使用/重置 NATS：

```sh
# 外部注入专用 GATEWAY_TEST_DATABASE_URL；缺失时测试 Skip。
go test -race -count=1 ./services/channel-gateway/internal/bootstrap \
  -run '^(TestTelegramDeliveryRealPGHTTPOrderedFinal|TestWeComDeliveryRealPGWSFinalAndOriginalOwnerFence)$'
```

历史切片工作树、镜像及运行结果见实施状态 §10.4，Runtime 切片见 §11。
GCI2 已在 Control 模式启动 Runner；GCI3 验证真实 Telegram 入站。ReplyIntent Consumer
和真实 Worker 完成证明仍待接线，见实施状态 §13–14。

## 8. 设计入口

- [Gateway 设计总览](../../docs/architecture-next/channel-gateway/README.md)
- [四个业务 Module](../../docs/architecture-next/channel-gateway/module-boundaries.md)
- [Delivery Runtime V1](../../docs/architecture-next/channel-gateway/delivery-runtime-v1.md)
- [设计复审](../../docs/architecture-next/channel-gateway/design-review.md)
- [Admission 实现边界](internal/admission/README.md)
- [Telegram Adapter 能力](internal/admission/adapter/inbound/telegramadapter/README.md)
- [Control route wire](../../api/events/control/v1/README.md)
- [RunRequested / ReplyIntent wire](../../api/events/execution/v1/README.md)
- [公开 WeCom Go SDK](../../platform/im/wecom/README.md)
- [Telegram 出站 Sender 与凭据边界](internal/delivery/adapter/outbound/telegram/README.md)

Helm 继续排在平台 `FINAL-INTEGRATION`，待 Control、Gateway、Worker、Local IM、
前端等所有生产 Workload 完成后统一处理。
