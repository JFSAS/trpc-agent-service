# Worker V1 NATS 声明

`streams.yaml` 是五个 Stream 的容量与持久化声明，`permissions.yaml` 是运行身份 ACL
的单一来源。`server.conf` 由以下命令生成，密码只保留环境变量引用：

```bash
go run ./services/channel-gateway/cmd/channel-gateway nats-config deploy/nats/permissions.yaml > deploy/nats/server.conf
```

| Stream | Subject | Retention | Publisher / durable |
| --- | --- | --- | --- |
| CHANNEL_ACCESS_POLICIES_V1 | control.channel-access-policy.v1 | Limits | Control / consumer 尚待接入 |
| CHANNEL_ROUTES_V1 | control.channel-route.v1 | Limits | Control / channel-gateway-routes-v1 |
| RUN_REQUESTS_V1 | execution.run-requested.v1 | WorkQueue | Gateway / agent-worker-runs-v1 |
| RUNTIME_MANIFESTS_V1 | control.runtime-manifest.published.v1 | Limits | Control / worker-manifests-v1 |
| REPLY_INTENTS_V1 | execution.reply-intent.v1 | WorkQueue | Worker / channel-gateway-replies-v1 |

Reconciler 创建四个 durable。运行进程只绑定和验证，不创建 Stream 或 consumer。
每个 durable 使用 explicit ACK、DeliverAll、30 秒 AckWait、64 MaxAckPending 和无限次数
恢复投递；没有消息本地无限排队。共享 Worker database 的副本共享两条 Worker durable。

容量为 Route/AccessPolicy 各 64 MiB，其余各 256 MiB；message size 为 Route/AccessPolicy
各 16 KiB，其余各 1 MiB。
所有 Stream 采用 FileStorage、DiscardNew、禁 Delete/Purge、无 MaxAge/自动 GC；满容量时
PG Outbox 保留原始事实并等待重试，不淘汰未处理输入。2 分钟 broker dedup window 只是
传输优化，PG Receipt/Run/Completion/Final 的唯一性才是业务保证。离线恢复窗口由容量
和实际积压增长决定，不以“无 MaxAge”承诺无限存储；容量报警和源回填仍由 Workload 运维
接线执行，改保留策略前需核对持久源与回填验收。

Worker 仅能发布 Reply、查询自己的三条 Stream 配置、拉取并 ACK Run/Manifest durable；
Gateway 仅发布 Run、消费 Route/Reply；Control 仅发布 Route/Manifest/AccessPolicy；reconciler 是唯一
拓扑管理身份。各角色 reply inbox 精确隔离。运行服务没有 `$JS.API.>` 管理权限。

实测 ACL 门禁：

```bash
# 环境提供专用 broker、四个 NATS_*_PASSWORD 及 GATEWAY_TEST_TOPOLOGY_FILE。
go test -count=1 -v ./services/channel-gateway/internal/infra/nats -run 'Test(Broker|WorkerBroker)PermissionsIntegration'
```

测试覆盖允许的真实 PubAck、Run/Manifest pull+ACK、Gateway Reply pull+ACK，及跨 owner
发布、跨 durable 消费、拓扑写入、错误凭据的拒绝。

## AccessPolicy relay 的生命周期与权限

Control 在启用 Channel 时装配并启动访问策略 relay；随主服务取消而停止，并在关闭
NATS/数据库前等待它退出（共享 shutdown deadline）。无需新增进程或部署单元。
Reconciler 根据 streams.yaml 创建/核对第五条 retained Stream；Control 仅增加精确的
发布 subject，不获得 Stream 管理权。Gateway 获得该 Stream 的 INFO 权限，以保持
全拓扑只读核验；当前尚无 policy consumer/pull/ACK 权限，等待消费/投影代码一起接入。

新增测试 `TestControlPolicyNotificationPermissionsIntegration` 使用实际生成的 server.conf
验证受限 Control 的持久 PubAck，以及向 Run 发布、修改 policy stream 被 broker 拒绝。
既有 Gateway 负例也覆盖向 policy subject 发布被拒绝。此轮只在一次性 broker 中核验，
不表示现有部署已切换，也不表示授权投影或撤权已生效。Helm 仍在全部 Workload 后处理。

## Scoped policy projection consumers

Both declarations accept explicit `policy_scopes`; their defaults remain empty.
In `streams.yaml`, the top-level list provisions one retained pull durable per
scope through the normal `channel-gateway reconcile` command. In permissions.yaml,
the Gateway role's list expands to exact INFO/NEXT/ACK subjects for those scopes.
The durable name is `channel-gateway-policy-` plus the full SHA256 of the scope ID;
operators never need to hand-maintain that hash. Lists are bounded to 64 unique,
valid scope IDs. Configured policy consumers require the 64MiB/16KiB stream contract.

Example declaration changes (replace the illustrative scope with the actual
Control-assigned scope; never infer it from a tenant or Bot ID):

```yaml
# streams.yaml — top-level
policy_scopes: [gateway-pool]

# permissions.yaml — within the existing user: gateway role
policy_scopes: [gateway-pool]
```

Regenerate server.conf with `channel-gateway nats-config permissions.yaml` before
applying the broker configuration. Reconcile creates absent consumers and verifies
existing ones; incompatible configuration requires an explicit migration rather
than silent update/recreation. Runtime Gateway verifies only its selected scoped
consumer through Bootstrap, not every other scope's consumer. Do not grant wildcard
consumer access or topology-write APIs to make startup succeed.

Control's verified workload mapping must separately include
`channel_policy_projection` in its existing `consumers` list, preserving all prior
required capabilities and the exact scope/audience/instance mapping. This grants
internal immutable policy reading, not external-user authorization. There is no
implicit mapping or runtime privilege escalation.

Only then explicitly set `GATEWAY_POLICY_PROJECTION_ENABLED=true`. Bootstrap also
requires its Control scope to appear in the topology declaration. Defaults remain
disabled/empty: no existing deployment or workload mapping is changed by this code.
A running history consumer is not current authorization; complete snapshots,
freshness, Principal revocation and Admission/Worker checks remain separate.
