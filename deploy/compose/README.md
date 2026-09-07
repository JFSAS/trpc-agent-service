# Control API + Channel Gateway 本地 Compose

本目录编排 Control API、Channel Gateway 和它们当前需要的 PostgreSQL / NATS。
`compose.yaml` 是服务基线，`compose.local.yaml` 增加本地构建、回环端口与本地
HTTP Cookie 配置。这里的启动命令说明如何运行当前代码，不替代实际部署验证记录。

当前默认 **Control mTLS 账户来源**已接账户目录、托管凭据/轮换、Telegram 注册与动态入站、
WeCom Supervisor、Delivery Runner 和 observations；Gateway 执行 0001–0010 共 10 个迁移。
Runner 独占 Maintenance 生命周期；显式 fixture 来源才保留 App 独立维护，不叠加两次 Run。
真实 **Control 发布 → Telegram 用户入站 → PG Admission/Outbox → NATS RunRequested**
已验收，运行目标固定为 Binding 对应的 DeploymentRevision/Manifest，见
[真实入站报告](../../docs/architecture-next/channel-gateway/telegram-real-inbound-20260906.md)。

ReplyIntent Consumer、真实 Agent Worker 与 committed-Final verifier、真实企微账号和完整
Final 回复闭环仍待交付；容器 healthy 或 Runner 已启动不表示已有完整 Agent 收发链路。
Connection/0005、Final/0006 与 Runtime/0007 的历史镜像验收分别保留于实施状态 §9–11，
不以旧镜像数字代替当前接线验证。Helm 保持 `FINAL-INTEGRATION`，待全部生产 Workload 完成。

## 1. 部署单元与启动依赖

| Compose 服务 | 镜像/模式 | 职责 |
| --- | --- | --- |
| `postgres` | PostgreSQL 17.6 | 当前本地实例承载 Control 与 Gateway 两个独立 database |
| `control-api` | Control Go 镜像 | Control API 与其自身 migration |
| `gateway-database` | PostgreSQL 镜像，一次性 provision job | 创建/校验 `gateway` role 与 `channel_gateway` database，并设置权限 |
| `nats` | NATS 2.11.8 | 带三角色 ACL 的 JetStream |
| `nats-reconcile` | Gateway 镜像，`reconcile` 模式，一次性 job | 根据声明创建并严格校验 Streams 和 Gateway route consumer |
| `channel-gateway` | Gateway 镜像，默认 Control 来源 | 0001–0011 migration、账户/凭据接入、Routing、Telegram 注册/入站、Outbox relay、WeCom Supervisor、Runner/Maintenance 与 observations；ReplyIntent Consumer 未接入 |

依赖顺序：

```text
postgres healthy ──→ control-api
        └─────────→ gateway-database completed ─┐
nats healthy ────→ nats-reconcile completed ───┴→ channel-gateway
```

`gateway-database` 使用实例管理连接执行 `provision-gateway.sh`，固定数据库/角色名；
已有角色时同步其密码，已有数据库时复用。Gateway 进程使用 `gateway` 角色连接
`channel_gateway`，不复用 Control 的管理 DSN。Gateway migration 由 Gateway
在监听前执行，provision job 只负责数据库与角色。

当前源码执行 0001–0010：0004 为路由积压起点，0005 为 Connection 账户/lease/epoch，
0006 为 nullable Admission `reply_origin` 与 Delivery 账本；0007 仅增加五个 Runtime 查询索引，
0008–0010 为 Control 目录、发送资格与注册账本；旧迁移不改写、不释放总行数容量。Maintenance 对空账户副本也运行。
旧 Admission 的 Origin 保持 NULL，不回填当前 owner/socket。新增账本 schema 不等于启用
发送循环；也没有为 Delivery 增加新的 Compose service、镜像、端口或 Connector 部署单元。

当前是**同一个本地 PostgreSQL 实例中的不同 database/role**，不是另外部署一个
Gateway PostgreSQL 容器。若将 `GATEWAY_DATABASE_URL` 指向外部实例，该实例的
数据库/角色需另行预置；当前 job 只操作 Compose 内的 `postgres`。

## 2. 必需配置与 Secret 引用

从仓库根目录运行命令，并通过外部部署配置、环境变量或本地未提交的配置注入：

| 变量 | 用途 |
| --- | --- |
| `CONTROL_PROFILE_CREDENTIAL_KEY` | 标准 base64 编码的随机 32 字节，Control Profile 凭据加密 |
| `GATEWAY_POSTGRES_PASSWORD` | provision job 为 `gateway` role 设置的密码 |
| `GATEWAY_DATABASE_URL` | 使用同一 Gateway 密码、`gateway` role 和 `channel_gateway` database 的 DSN |
| `NATS_GATEWAY_PASSWORD` | Gateway 运行角色 |
| `NATS_CONTROL_PASSWORD` | Control 路由发布角色；ACL 已预留，生产发布链路仍待接入 |
| `NATS_RECONCILER_PASSWORD` | 独立 topology 管理角色，仅 reconciliation job 使用 |
| `GATEWAY_TELEGRAM_ACCOUNTS_FILE` | 宿主机 JSON 文件路径；默认空配置 |
| `GATEWAY_TELEGRAM_WEBHOOK_SECRET` | 示例账户引用的 webhook 鉴权 secret；空账户配置时可为空 |

Compose 内 Gateway DSN 的结构为：

```text
postgres://gateway:<URL_ENCODED_GATEWAY_PASSWORD>@postgres:5432/channel_gateway?sslmode=disable
```

DSN 中密码需使用 URL 转义后的值，provision job 的密码变量使用原值。两个值必须
对应同一密码。NATS 三个密码使用各自独立的注入值，不在启动时随机生成。

`.env.example` 同时包含宿主机直接运行的 Control DSN 示例；该示例中的
`127.0.0.1:5432` 不是容器中的 PostgreSQL 地址。运行 Compose 时可不设置
`CONTROL_DATABASE_URL` 以使用基线的 `postgres:5432` 默认 DSN，或显式设置正确的
容器网络 DSN。不要把宿主机连接串原样用于容器。

### Control Profile Key

首次初始化全新数据库时，可用 `openssl rand -base64 32` 生成一次，然后存入持久
部署配置。后续构建、重启和多副本复用同一值。默认 zsh 的非回显输入方式：

```zsh
read -r -s 'CONTROL_PROFILE_CREDENTIAL_KEY?Profile encryption key: '
printf '\n'
export CONTROL_PROFILE_CREDENTIAL_KEY
```

bootstrap 用户密码与 Profile Key 是不同配置。Key 与数据库备份分开保管并维持
恢复关系；当前没有主 Key 轮换流程。`compose-config`、`compose-up` 和
`compose-down` 都会解析必需变量，因此这些操作期间保留同一配置。
配置检查使用 `config --quiet`，不把展开了密码的完整 Compose 配置作为共享输出。

### 同一发布固定 Platform Contract Digest


多副本的 `CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST` 必须由同一份部署发布配置
显式注入，格式为 `sha256:` 加 64 位小写十六进制。Compose 拒绝缺值；Control API
在打开数据库、执行迁移和启动 HTTP 之前计算实际 Platform Contract Digest，
只有与预期值一致才继续启动。Host、冻结实现契约或资源上限不同的副本因此不会进入服务。
现有 `/healthz` 仍返回 204；不匹配的进程在监听前退出，没有可用健康端点。

在发布准备阶段，使用待发布二进制和最终 Host 配置预计算一次；CLI 不需要数据库、
Profile 加密 Key 或预期 Digest，也不会启动服务：

```sh
export CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS='api.openai.com,mcp.example.com,qdrant.internal,state.example.test'
go run ./services/control-api/cmd/control-api -print-deployment-contract-digest
# 或：control-api -print-deployment-contract-digest
```

当前上述 Host 示例的输出是 `sha256:43d9ac6291cf7ccacf544cb122fa4d92b398317fa6020e4bba43e8666e8747b0`。
将经过核对的输出保存为本次发布的配置，并向所有副本注入同一个值，例如：

```sh
export CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST='sha256:43d9ac6291cf7ccacf544cb122fa4d92b398317fa6020e4bba43e8666e8747b0'
```

`.env.example` 固定了与该 Host 示例对应的 Digest。更改 Host 或发布二进制的契约后，
需要重新预计算并统一更新发布配置；默认内建 Host 集合与此 Compose 示例不同，
计算时必须传入实际 Host 配置。不要在各副本的启动脚本中把自身计算结果自动赋给
expected 值，那会绕过多副本一致性门禁。Digest 是平台配置身份，不是凭据。

### Telegram 账户配置（显式 fixture 来源）

本节只适用于显式 `compose.gateway-fixture.yaml` overlay；生产 Control 来源的账户与凭据
由 Control 提供，不能混入静态文件，具体配置见本文 GCI2 节。
fixture 默认 `gateway-accounts.empty.json` 内容为 `[]`。它允许 Gateway 以零账户启动；
这只说明基础设施和路由历史可初始化，不表示已创建 Bot、注册 webhook 或发布 Binding。
空账户时公开 HTTP listener 没有任何 Telegram 账户路由。

启用单个示例账户，可指定仓库中的示例文件：

```sh
export GATEWAY_TELEGRAM_ACCOUNTS_FILE="$PWD/deploy/compose/gateway-accounts.example.json"
# 通过外部配置注入 GATEWAY_TELEGRAM_WEBHOOK_SECRET 后启动。
```

文件只保存引用，不保存 Bot Token 或 secret 值：

```json
[
  {
    "account_id": "telegram-primary",
    "webhook_secret_env": "GATEWAY_TELEGRAM_WEBHOOK_SECRET"
  }
]
```

Gateway 从对应环境变量读取 16–256 个 URL-safe 字符组成的 webhook secret。
它是 Telegram `setWebhook.secret_token` 对应的请求鉴权值，不是 Bot API Token。
fixture 来源不自动执行 BotFather 创建或 `setWebhook` 注册，也不从这个文件构造出站
Bot Token 客户端。生产 Control 来源已接账户 Owner、凭据解析/轮换与 `setWebhook` 注册；
两种来源都不把 webhook secret 当作出站 Bot Token。

文件可以声明最多 100 个稳定账户身份，secret 引用可不同；当前 Compose 只显式
透传了示例变量。增加其他引用时，需在自己的 overlay 中同时把那些环境变量传入
`channel-gateway`。建议使用宿主机绝对文件路径，避免 Compose 相对路径基准歧义。

### 企业微信账户配置（显式 fixture 来源）

企微是 Gateway 内的可选账户功能，不是独立 service/profile/端口。本节文件/环境配置
仅用于显式 fixture overlay；生产来源由 Control 账户目录和凭据解析提供。fixture 默认目录
`deploy/compose/wecom/` 内 accounts.json 为 []，零账户可以启动且不拨号。

| 配置 | 含义 |
| --- | --- |
| `GATEWAY_WECOM_ACCOUNTS_FILE` | **进程**读取的文件路径；直接运行二进制时设置。Compose 固定为 `/etc/gateway/wecom/accounts.json` |
| `GATEWAY_WECOM_ACCOUNTS_DIR` | **Compose 宿主机**目录挂载来源；建议绝对路径，容器中只读挂载整个目录 |
| `GATEWAY_WECOM_BOT_SECRET` | 示例唯一透传的凭据环境变量；只在获 lease 后解析。新增 secret_env 引用须在自己的 overlay 显式透传 |
| `GATEWAY_INSTANCE_ID` | 可选显式实例标识；省略时每进程生成。多个实例应使用各自身份，不共同固定成相同值 |

首次配置可在仓库外准备非秘密账户文件：

```sh
export GATEWAY_WECOM_ACCOUNTS_DIR="$HOME/.config/channel-gateway/wecom"
mkdir -p "$GATEWAY_WECOM_ACCOUNTS_DIR"
chmod 700 "$GATEWAY_WECOM_ACCOUNTS_DIR"
cp deploy/compose/wecom/accounts.example.json "$GATEWAY_WECOM_ACCOUNTS_DIR/accounts.next.json"
# 编辑 accounts.next.json：填写已核验的 Bot ID/稳定账户 ID，保持以下五字段，勿填 Secret 值。
# 编辑并复核完毕后，在同一目录发布：
mv "$GATEWAY_WECOM_ACCOUNTS_DIR/accounts.next.json" "$GATEWAY_WECOM_ACCOUNTS_DIR/accounts.json"
# 再从外部秘密配置注入 GATEWAY_WECOM_BOT_SECRET，执行 just compose-config / just compose-up。
```

文件格式（全部字段必需）：

```json
[
  {
    "account_id": "wecom-account",
    "bot_id": "REPLACE_WITH_VERIFIED_BOT_ID",
    "revision": 1,
    "enabled": true,
    "secret_env": "GATEWAY_WECOM_BOT_SECRET"
  }
]
```

- 最多 100 个账户；拒绝未知/重复字段、缺字段、非整数 revision、重复账户/Bot 与无效引用。
  revision 是连接配置代次，独立于 Routing 的 generation；所有副本更新必须单调。
  同 revision 修改 enabled/secret_env 属于冲突，旧 revision 拒绝；Bot 身份绑定后不更换。
- 需要热更新文件时，在**同一宿主目录**写完整临时文件，再 atomic rename 到 accounts.json。
  Compose 挂载目录而非单文件，下一次轮询重新打开路径；默认轮询 1 秒，不是全副本发布 SLA。
- **停用**使用更高 revision + enabled=false，并分发到所有副本；数据库更新使旧 grant 的
  Renew/Check/新接纳失效。仅删去文件条目不是全局禁用，其他副本可能仍持有旧配置。
- **凭据轮换**：先把新环境引用/值注入全部目标进程，再将文件 revision 递增、secret_env
  指向新引用。若值没有预先注入，须重建/重启进程并注入新的环境；同名引用更换值也应
  协调递增配置 revision。修改宿主 shell 的 export 不会修改已经运行容器/进程的环境。
  引用名变化不代表 Bot 身份变化，Secret 不写入 accounts.json 或普通日志。
- **replaced** 的当前 revision 持久隔离，普通重启/lease 过期不解除；只允许可信更高配置
  revision 恢复。若隔离写失败，不宣称跨副本封禁成立，也不主动 Release 加速接管。
- 默认 Adapter 临时接纳最多 6 次/2s，100ms 起步封顶 400ms，重试保持同一归一化输入；
  耗尽后由明确 Retryable 交给 Supervisor。Supervisor 最多 3 次 1/2/4s 快速重建，之后
  每 60s 一次半开；短暂 Ready 不清预算。预算在本进程/账户/revision 内，不跨重启持久化。
- 单 Bot 不 ready、认证错误、被替换或被其他副本持有，不导致共享 readiness 立即失败；
  完整配置源、Supervisor 主循环、PG、Routing 与预算门禁仍需正常。当前 Control 来源
  已上报账户 observations，但共享 /readyz 仍不表示某个 Bot 一定可用，也不等于完整可观测性。

获得连接只说明本地租约/协议链路建立。新消息仍需有效 Routing 投影；忽略/交互决策也
通过 owner guard。可回复的首次 Admission 另存原 owner/epoch/revision/socket generation；
相同事件跨 owner 重放仍保留首次 Origin。默认服务没有 Agent 自动回复；测试发布者、
订阅者和 committed-Final fixture 均不是真实 Control/Worker。

## 3. NATS 声明、生成物与三个角色

两份声明各自拥有不同的事实：

```text
streams.yaml ─────→ channel-gateway reconcile ──→ JetStream Streams / Consumer
permissions.yaml → channel-gateway nats-config → server.conf → nats-server ACL
```

- `deploy/nats/streams.yaml` 声明两个 Stream 的 subject、retention、容量和副本数。
- `deploy/nats/permissions.yaml` 声明角色与 secret 环境变量引用。
- `deploy/nats/server.conf` 是生成物，只保存 `$NATS_*_PASSWORD` 引用；NATS Server
  启动时解析真实值，生成命令不会展开 secret。
- Gateway 运行角色只验证 topology；需要创建 topology 时由 `reconciler` 执行。
  已存在但不兼容的 topology 会报错，reconcile 不静默改写 retention 或破坏历史。

| 角色 | 当前发布/读取权限 |
| --- | --- |
| `control` | 发布 `control.channel-route.v1`，接收 `_INBOX.control.>` PubAck 回复 |
| `gateway` | 发布 RunRequested；读取两条 Stream 的 Info；拉取/ACK 唯一 route consumer；接收 `_INBOX.gateway.>` 回复 |
| `reconciler` | 管理 `$JS.API.>`，接收 `_INBOX.reconciler.>` 回复 |

Gateway 角色没有 Control route 发布权限，也没有 Stream 创建/删除等管理权限。
这里尚无 Worker 角色；Worker 权限、consumer 与 ACK 流程在 Worker 实现时增加。
`execution.reply-intent.v1` 的 JSON Schema/eventadapter 已存在，但当前 topology/ACL 和
默认 bootstrap 没有为其启用生产 consumer；发布一个 ReplyIntent 不会自动触发回复。

| Stream | Subject | 当前保留语义 |
| --- | --- | --- |
| `CHANNEL_ROUTES_V1` | `control.channel-route.v1` | Limits retention、64 MiB、单条 16 KiB、保留完整路由历史；consumer ACK 不删除历史 |
| `RUN_REQUESTS_V1` | `execution.run-requested.v1` | WorkQueue retention、256 MiB、单条 1 MiB；未来 Worker consumer ACK 后释放消息 |

两条 Stream 当前为 FileStorage、单副本、无 age 自动淘汰，容量耗尽时拒绝新消息
而不是淘汰旧消息。删除/清空禁用；恢复或容量迁移需要显式操作设计。
Run 的 WorkQueue ACK 是未来 Worker 的接收确认，不是 Telegram webhook 的 HTTP ACK。
当前没有 Worker，因此待消费 Run 消息会积累，而不是自动完成 Agent 执行。

权限声明变更后生成配置：

```sh
just nats-config
```

这只重新生成 `server.conf`；变更权限后还需重启/重载相应 NATS 部署。
`streams.yaml` 变更则需要重新运行 reconciliation，并处理严格兼容校验结果。

## 4. 启动、检查与停止

完成必需变量注入后，从仓库根目录执行：

```sh
just compose-config
just compose-up

curl --fail http://127.0.0.1:8080/healthz
curl --fail --output /dev/null http://127.0.0.1:8091/livez
curl --fail --output /dev/null http://127.0.0.1:8091/readyz

docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.local.yaml ps --all
```

- `compose-config` 使用 quiet 配置校验；`compose-up` 本地构建并等待服务。
- 默认回环映射：Control `8080`、Gateway public `8090`、Gateway admin `8091`。
  覆盖变量分别为 `CONTROL_API_HTTP_PORT`、`GATEWAY_HTTP_PORT`、`GATEWAY_ADMIN_PORT`。
- `8090` 只承载 `POST /v1/telegram/{account_id}`；`/livez` 与 `/readyz` 仅在 `8091`。
  对外反向代理只应转发 public listener，admin listener 留在管理网络。
- 两个一次性 job 成功时状态为 exited 0，其他服务运行；reconcile 成功输出
  `NATS_RECONCILE=PASS`。
- Gateway 的 `/readyz` 返回 204 表示 route replay 已初始化且无已检测的缺口/来源陈旧/
  超限连续积压/quarantine，数据库可查询且预算未饱和。已有 route row 数量不是 readiness 条件。
  0004 迁移新增持久积压起点；来源观察年龄上限 5 分钟，连续已知积压满 60 秒另行拒绝
  新 Run。部分推进和重启不续期，完整追平才恢复；旧 Receipt 重放保持原结果。
- NATS 短时断连、尚未越过 freshness/budget 限制时，readiness 可保持 204 并带
  `X-Gateway-State: degraded`。超限后返回 503。
- `CONTROL_BOOTSTRAP_MODE` 默认 `auto`；已有 Operator 时不重复创建。
- `CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS` 是无通配符的精确目标 Host 集合，真实部署须按平台策略配置；同一发布固定 expected contract digest。
- Control 在监听前执行自己的迁移；`/healthz` 返回204，与 Gateway 双 listener 的探针各自独立。

也可直接使用镜像中的同一个二进制检查内部管理端口：

```sh
docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.local.yaml \
  exec channel-gateway /channel-gateway probe http://127.0.0.1:8091/readyz
```

停止容器并保留命名数据卷：

```sh
just compose-down
```

Shutdown 先关闭新 Admission gate，再有界 drain 已进入处理的请求；Connection 正常 drain
期间续租，Client 关闭后释放 lease，失租/期限耗尽则取消。新增 Sender reservation 的 Drain
覆盖在途发送与有界结果落账。默认 Maintenance 随 App 取消并在关闭 PG/NATS 前完成退出；
生产发送 Runner 尚未启用，其组合停止/claim 门禁与后续装配一起验收。PostgreSQL Outbox 保留未完成
投递，重启后继续处理。命名卷保存 PostgreSQL 与 JetStream 数据；停止容器不删除账户
identity/epoch 或 replaced 隔离。

## 5. 验证入口与完成边界

```sh
just gateway-build
just gateway-test
```

真实数据库与 broker 验证需指向专门的测试实例：

```sh
# 外部注入 GATEWAY_TEST_DATABASE_URL 和 GATEWAY_TEST_NATS_URL。
# 该 broker 必须为独立测试实例；此开关允许测试重置其 Streams。
export GATEWAY_TEST_ALLOW_NATS_RESET=1
just gateway-integration
```

单纯 `go test` 成功不证明 PostgreSQL/NATS 已实测：未提供测试变量时相应测试会 Skip。
集成测试可能重建固定命名的测试 Streams；使用与运行服务分离的测试 broker。
具体已执行结果以本轮 implementation-status 和审计输出为准。

历史企微 Adapter、Connection owner/lease 与直接 SDK 装配的实际树/镜像结果保留于
[实施状态](../../docs/architecture-next/channel-gateway/implementation-status.md) §9；Final/0006
记录保留于 §10。2026-09-05 Runtime 历史验收见 §11，当前 Control 运行接线和真实
Telegram 入站验收见 §13–14，不以旧镜像或旧成功数字替代。

当前源码已有 Delivery Final barrier、Acceptor、PG Ledger、Dispatcher、Telegram Sender、
Connection 受限 reservation 与 WeCom Sender；本地纵切已用真实 PG + httptest HTTP/WS 验证
原回复目标、A2 后发送、ACK/UNKNOWN、Observation/Finish 和旧 Origin 拒绝。执行授权使用
显式 committed-Final fixture，不是真实 Worker/IM 账号验证。只复现这两条纵切时无需 NATS：

```sh
# 外部注入专用 GATEWAY_TEST_DATABASE_URL；未配置会 Skip。
go test -race -count=1 ./services/channel-gateway/internal/bootstrap \
  -run '^(TestTelegramDeliveryRealPGHTTPOrderedFinal|TestWeComDeliveryRealPGWSFinalAndOriginalOwnerFence)$'
```

当前 Control `bootstrap.App` 已接托管凭据解析/轮换与 Delivery Runner，并由 Runner 独占
Maintenance；ReplyIntent NATS Consumer 和真实 Execution committed-Final verifier 仍未接入。
**Delivery/0006 历史切片**的实际工作树、镜像及运行验收记录于实施状态 §10.4；没有新增
Connector 部署单元。**2026-09-05 Runtime 切片**的七迁移镜像与空账户维护记录见 §11.4；
该数字不是当前十迁移镜像验收声明。健康检查、样本 RunRequested、迁移已执行或空账户
启动都不等于 Agent E2E。精确规则见[Runtime V1](../../docs/architecture-next/channel-gateway/delivery-runtime-v1.md)。

Control Runtime Profile 的 11 个管理操作仍独立存在；默认 bootstrap 尚未注册内部
凭据解析路由，真实 Run/Attempt 授权、Workload 认证和 Worker 接线仍属后续工作。

相关说明：

- [Channel Gateway 服务](../../services/channel-gateway/README.md)
- [Control API](../../services/control-api/README.md)
- [部署所有权](../../docs/architecture-next/operations/deployment.md)
- [Runtime Profile 凭据](../../docs/architecture-next/control-api/runtime-profile-credentials.md)


### NATS 三角色密码的当前输入边界

当前 bundled NATS 2.11.8 使用不加引号的 `$NATS_*_PASSWORD` 配置引用。Server 会再次
按配置语法解析环境变量内容，不保证任意随机字符串都被当作 string；数字、布尔字面量
或特定数字/单位前缀可能导致启动失败。不要把引用包在双引号里，也不要给同一密码变量
人为嵌入引号后同时提供给 Server 和客户端。

为 bundled 部署分别生成三个独立密码时，可使用下面的字母前缀 + 随机十六进制格式：

```bash
printf 'nats_%s\n' "$(openssl rand -hex 24)"
```

每个角色独立运行一次并存入秘密配置。建议的 bundled 输入契约为
`^[A-Za-z][A-Za-z0-9_-]{31,127}$`；**当前 Compose 仍仅检查非空，没有实现该正则预检**。
预检与明确诊断属于 [CGR-28](../../docs/architecture-next/channel-gateway/design-review.md#113-cgr-28bundled-compose-的-nats-密码解析边界)
的后续工作；它不应改变通用 Gateway Auth 对外部自管 NATS 的兼容性。


## GCI2: Control 账户目录成为生产默认源

最新代码的 `LoadConfig` 默认 `GATEWAY_ACCOUNT_SOURCE=control`，生产 Compose 已切换到
该模式。上述静态账户说明仅用于显式 fixture overlay；历史仅维护的验收另按日期保留。当前 Control 模式
启动 account catalog refresh、WeCom Supervisor、Telegram registration reconciler、
Delivery Runner（复用原 Dispatcher，并独占 Maintenance 生命周期）和 observations 上报。ReplyIntent
NATS 消费与真实 Worker committed-Final verifier 仍未接入，不能把 Runner 启动等同 Agent E2E。

没有新增 Connector 容器或 Node 镜像；Telegram Go SDK 与公开 `platform/im/wecom` 在
同一个 Gateway Go workload 中使用。Helm 留到所有 workload 完成后。

生产配置：

- `GATEWAY_CONTROL_URL`: 实际 Control 内部 HTTPS origin；CA 必须覆盖其主机名。
- `GATEWAY_CONTROL_SCOPE_ID` / `GATEWAY_CONTROL_SOURCE_EPOCH`: Control 发放的固定来源身份。
- `GATEWAY_INSTANCE_ID`: mTLS principal 对应的实例 ID；每个同时运行的副本必须不同。
- `GATEWAY_CONTROL_CERTS_DIR`: 本机目录，只读挂载 `ca.pem`、`client.pem`、`client-key.pem`。
- `GATEWAY_PUBLIC_ORIGIN`: Webhook 账户按需使用的平台 HTTPS origin；纯长轮询可留空。路径由账户目录决定，不接受租户任意 URL。
- `GATEWAY_HTTPS_PROXY` / `GATEWAY_HTTP_PROXY` / `GATEWAY_NO_PROXY`: 可选出站代理；默认 bypass 本地及内部服务，不在文档或日志记录代理凭据。
- 既有 Gateway PG / NATS 三角色配置保持不变。SDK Token/Secret 不再由此 Compose 注入。

Control 不可达时公共 listener 保持运行，但新工作拒绝且 readyz 返回 503；snapshot 恢复
后重新建立本实例资格。注册 READY 仅是某版本有界时间的观测。源 epoch 不自动信任更新。

开发静态账户只能显式附加 overlay：

```sh
docker compose -f deploy/compose/compose.yaml \
  -f deploy/compose/compose.local.yaml \
  -f deploy/compose/compose.gateway-fixture.yaml config --quiet
```

生产启动命令与真实联调前提见 `services/channel-gateway/CONTROL_RUNTIME.md`。


## Telegram 双接收模式（源码实现，发布前须联合验收）

仍使用上面的单个 `channel-gateway`，不新增 polling 或 connector 服务/镜像。Control
管理 `ChannelAccount.config.receive_mode`；新账户默认 `long_polling`，旧账户迁移为显式
`webhook`。静态 fixture overlay 仍是原 Webhook 测试入口，不冒充 Control 管理面配置。

纯长轮询的 Compose 可不配置 `GATEWAY_PUBLIC_ORIGIN`，不需要公网入站端口映射；需可用的
Telegram 出站网络、Control mTLS、Gateway PostgreSQL 和 NATS。混合模式部署的启用
Webhook 账户若缺 origin，会报告 CONFIG_INVALID 并影响 readiness，不被长轮询成功掩盖。

模式修改需先停用，再保存配置，再显式启用。保存不触 Telegram；Gateway 在新的接收
实例启动前处理旧 owner 和在途请求。只协调空 Webhook 或本平台有持久管理证据的端点；
遇到外部未知 Webhook 会报告 WEBHOOK_CONFLICT，不盲目接管。两方向都不主动丢弃积压，
持久 cursor/Receipt 不随模式清空。积压仍受 Telegram 保留期约束。

本版本使用协调升级窗口：先停止旧 Gateway 接收并排空旧预检，再部署双读 Control、
新 Gateway/Web 和迁移，最后开放新模式写入。旧程序的严格快照校验不支持任意混部；
不要先对旧 Gateway 发布含 receive_mode 的新快照。源码/镜像回退不等于远端 Webhook
恢复，已存在 LP 账户时不直接降级到 Webhook-only 构建。Helm 继续延后。

实现与验证入口见 [双模式开发记录](../../docs/architecture-next/channel-gateway/telegram-receive-modes-implementation.md)。
