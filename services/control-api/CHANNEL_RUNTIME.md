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

内部监听只暴露以下三个工作负载API（均no-store）：

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

当前V1基线直接扩展`0001_baseline.sql`，不引入未稳定版本的兼容迁移。已有旧基线数据库不会
自动获得新表；本地使用独立空数据库验证。升级真实持久部署前应制定明确数据迁移，不以
源代码回滚脚本假装撤销业务数据库。源代码回滚保留修改前已有文件及改动；测试回滚在副本执行。
真实Telegram验收另需真正机器人凭据、可达HTTPS origin、远端注册与用户消息，以及Gateway
Receipt/RunRequested的持久证据；合成远端或普通go test成功不代替这一步。
