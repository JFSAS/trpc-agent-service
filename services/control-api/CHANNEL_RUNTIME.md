# Control Channel V1 runtime

当前已实现账户/绑定的公开管理、完整快照、按用途凭据供应、运行观测、原子Route Outbox和
JetStream Relay。这里的运行配置由平台维护，不是用户必须管理的Environment/Secret对象。

## 1. 启动入口

常规Control配置（数据库、Profile加密key、发布contract digest、Session和bootstrap）继续适用。
额外设置`CONTROL_CHANNEL_CONFIG_FILE`为绝对JSON文件路径。不设置时不注册Channel路由；
提供不完整配置、错误key/cert、错误source epoch或启动时NATS连接失败时，进程退出。

```json
{
  "scope_id": "gateway_pool",
  "source_epoch": "00000000-0000-4000-8000-000000000001",
  "internal_address": "127.0.0.1:18081",
  "tls_cert_file": "/run/channel/control.crt",
  "tls_key_file": "/run/channel/control.key",
  "client_ca_file": "/run/channel/client-ca.crt",
  "credential_keys_file": "/run/channel/credential-keys.json",
  "route_nats_file": "/run/channel/route-nats.json",
  "max_tenant_accounts": 50,
  "workloads": [{
    "principal_id": "spiffe://trpc-agent-service/gateway/gw-1",
    "instance_id": "gw-1",
    "scope_id": "gateway_pool",
    "audience": "control-channel-v1",
    "consumers": ["telegram_registration", "telegram_webhook", "telegram_delivery"]
  }]
}
```

这些是路径示例，不是可用凭据。证书私钥、下面两份key/Producer文件要求owner-only(0600)，
所有JSON拒绝重复key/未知字段/多文档，单文件上限64KiB。内部TLS至少1.3，要求验证客户端链、
ClientAuth EKU和唯一精确URI SAN，再映射平台允许的instance/scope/audience/consumer集合。
Header、Cookie、请求体声明不能扩大该身份。Workload映射上限32个。

credential-keys.json的结构：

```json
{
  "active_key_id": "platform-k1",
  "keys": {
    "platform-k1": {
      "encryption_key": "<independent random 32-byte key in base64>",
      "mac_key": "<independent random 32-byte key in base64>"
    }
  }
}
```

保持旧key可读以解密已有账户并校验原Idempotency Receipt；keyring最多8项，
活动key用于新写入。凭据替换更新模块私有当前值及credential/connection/account版本，
不是建立用户Secret产品。Profile凭据使用自己的模块和key，不复用Channel凭据。

route-nats.json的结构：

```json
{
  "url": "tls://broker.internal:4222",
  "user": "control",
  "password": "<platform producer password>",
  "ca_file": "/run/channel/nats-ca.crt"
}
```

本地隔离联调允许`nats://127.0.0.1:PORT`；非loopback地址要求TLS。URL不接受内嵌认证/query。
Producer仅publish `control.channel-route.v1`、subscribe `_INBOX.control.>`；客户端固定
`CustomInboxPrefix("_INBOX.control")`。不为Control授予JetStream管理/读消息权限。
流由部署工具预建，不由业务启动自动创建：`CHANNEL_ROUTES_V1`、FileStorage、LimitsPolicy、
DiscardNew、MaxBytes64MiB、MaxMsgSize16384、replicas按部署配置、无自动时间/条数淘汰，
DenyDelete/DenyPurge。建议dedup窗口2分钟，业务永久幂等仍由Gateway EventID/generation保证。

## 2. API和执行边界

公开11个操作见[OpenAPI](../../api/openapi/control/v1/channel-public.yaml)：OWNER写、成员读，
写入要求真实非restricted Session并在提交事务下重查Identity/租户授权。
创建默认disabled；保存凭据不触发Provider探测。Binding选择精确deployment_id/revision_number，
读取拥有方校验的Published Revision/Manifest，不读取Draft/latest。

内部监听保留以下三个运行工作负载API（均no-store）；独立预检API见§5：

| Method | Path | Wire schema / limit |
| --- | --- | --- |
| GET | `/internal/v1/channel-accounts/snapshot` | account-snapshot.schema.json；完整目录≤1000账户、≤2MiB |
| POST | `/internal/v1/tenants/{tenant_id}/channel-accounts/{account_id}/credentials:resolve` | request≤16KiB、response≤64KiB；闭合schema、同连接版本、限定用途 |
| POST | `/internal/v1/channel-account-observations` | observations.schema.json；≤100条/128KiB；成功204 |

所有请求拒绝query；snapshot拒绝body。内部HTTP执行预算5秒。凭据只在已授权的resolve成功
响应中返回，不进入快照、普通详情、Receipt、Outbox或日志。registration必须同时解析同连接
版本的bot_token和webhook_secret；Gateway在取值前后自行核验其Grant/Fence，Control不查
Gateway内部lease表。observations属于诊断而非运行授权，收到同seq重试不会刷新freshness。

账户目录/route可异步到达；Gateway核验账户许可、route generation≥min_route_generation，
在Admission事务内固定真实Control发布的目标三元组。这里不新增Manifest正文下载API，也不
宣称Worker已经可以执行。新消息才触发RunRequested，启用Binding本身不创建Run。

## 3. Relay及运维恢复

Route Relay只领取本scope、本模块event_type，15秒租约+随机claim token+attempt fencing；
5秒发布预算，收到正确stream/非零sequence的PubAck后才标记PUBLISHED。临时失败指数重试
1～60秒并保留正文；正文完整性失败进入FAILED。不要改写不可变payload/digest/event_id。
调查并修复后，运维可以在受控数据库维护中将合法FAILED事件的投递元数据重新置PENDING，
清除claimed_by/claimed_until/last_error并设置available_at，仍使用原ID/正文；损坏正文先
恢复可信数据再重发。V1没有自动删除历史Outbox、管理重试API或全局调度服务。

public/internal任一监听失败将关闭另一监听；关闭过程取消Relay/清理任务，再关闭NATS/DB。
未获得Gateway应用ACK协议之前，普通查询`gateway_application`始终UNKNOWN，不能根据
PUBLISHED推断机器人连接就绪。observations提供带版本和服务器时间的独立诊断。

## 4. 验证与发布

```sh
go test -count=1 -race ./services/control-api/... ./api/...
go vet ./services/control-api/... ./api/...
go build ./services/control-api/cmd/control-api
```

真实PG测试要求`CONTROL_TEST_DATABASE_URL`，每个测试创建/清理独立schema；缺失时相关测试skip。
真实JetStream测试另外要求`CONTROL_TEST_NATS_CONFIG_FILE`：一份0600 JSON，包含url、
admin_user/admin_password、control_user/control_password。它必须指向专用可清理测试broker，
测试会创建/删除`CHANNEL_ROUTES_V1`；不能使用联合联调或生产broker。验证报告必须记录实际skip数。

已部署Channel基线之后，Telegram预检使用新增`0002_channel_preflights.sql`，不改写
`0001_baseline.sql`。正常启动迁移只增加两张诊断表及索引，保留已有账户/凭据/路由数据。
SQL文件编号不表示产品V2。旧镜像不认识`telegram_preflight` consumer，回滚必须同步恢复
旧镜像与旧Channel配置；保留新诊断表，不通过删表回滚历史业务数据。源代码回滚在副本执行。
真实Telegram验收另需真正机器人凭据、可达HTTPS origin、远端注册与用户消息，以及Gateway
Receipt/RunRequested的持久证据；合成远端或普通go test成功不代替这一步。


## 5. Telegram 只读预检

Control 已实现独立 `PreflightService`、PostgreSQL Store、2公开/3私有入口与维护循环。
Gateway Provider 调用和跨端真实验收由各自任务交付；本节不以Control测试代替线上诊断。
冻结协议见[Telegram预检V1](../../docs/architecture-next/control-api/telegram-preflight-v1.md)，
公开和私有文档分别为[Session OpenAPI](../../api/openapi/control/v1/preflight-public.yaml)与
[mTLS OpenAPI](../../api/openapi/control/v1/preflight-internal.yaml)。

| Method | Path | 语义 |
| --- | --- | --- |
| POST | `/v1/tenants/{tenant_id}/channel-accounts/{account_id}/preflights` | ACTIVE OWNER + 非restricted Session + Idempotency-Key；202固定创建回执 |
| GET | `/v1/tenants/{tenant_id}/channel-accounts/{account_id}/preflights/{preflight_id}` | ACTIVE MEMBER；去秘密结果，无列表/latest |
| POST | `/internal/v1/channel-preflights:claim` | mTLS + 显式telegram_preflight；200一项grant或204 |
| POST | `/internal/v1/channel-preflights/{preflight_id}/credentials:resolve` | 只读取该任务固定BotToken；不读取WebhookSecret |
| POST | `/internal/v1/channel-preflights/{preflight_id}:complete` | 8项闭合事实；相同claim/payload历史重放204 |

要让Gateway领取预检，平台在其已有workload `consumers`数组中显式增加
`telegram_preflight`。不新增用户业务对象或新的环境变量；未增加该consumer的已有运行权限
保持原样，预检私有操作返回403。普通resolve仍拒绝disabled账户，不复用预检授权绕过它。

创建只要求已保存、停用的Telegram账户，不依赖Binding、Deployment或目录READY。
任务120秒、每租约30秒、最多2次领取；数据库时间控制所有期限。每账户1活跃/3次每分钟，
每租户20活跃/30次每分钟，Control按principal/instance每秒2次claim，在数据库事务中执行。
Gateway配置摘要第一次领取后固定；重新领取或首次完成遇另一合法配置持久STALE。
当前Gateway配置新鲜度保持UNCONFIRMED，不将静态origin合法当作公网可达或真实投递。

创建事务先稳定排序锁当前及旧任务请求者Identity，再锁Tenant/Account/Task；当前Session
与OWNER检查、幂等回执先于旧任务收敛。撤销Session后拒绝的请求对预检表也零写入。
后台claim/resolve/首次complete检查持久ACTIVE用户与OWNER，不依赖原Session仍登录。
历史完成回执只确认已提交事实；撤权后原claim/同payload可以204，但不重新读取凭据。

Bootstrap在已有关闭可等待的维护goroutine中每秒处理最多64个任务，每轮5秒context；
按数据库时钟收敛超时/失效并有界清理。GET也收敛未领取120秒任务，不依赖维护及时运行。
完成事实不可变，结果有效期5分钟；task/创建回执保留24小时，claim回执保留5分钟。
每分钟原Observation清理继续执行；不增加独立调度服务或消息队列。

真实PG+Session+mTLS回归覆盖完整链路、配置变更STALE、请求者撤权、创建幂等与Session
提交前撤销零写入；八张运行表完整内容哈希保持不变。Provider真实响应、Gateway账本和
用户看到的Web结果属于协调联合验收，不由Control写入或模拟。
