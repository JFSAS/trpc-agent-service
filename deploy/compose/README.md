# Control API 本地 Compose

当前只编排 Control API 与 PostgreSQL。`compose.yaml` 是服务基线，
`compose.local.yaml` 增加本地镜像构建、回环端口和本地 HTTP Cookie 配置。
它不加入 NATS、Gateway、Worker、Local IM 或可观测性组件。

## 必需配置：Profile 凭据加密 Key

启动前必须从外部配置注入 `CONTROL_PROFILE_CREDENTIAL_KEY`：标准 base64 编码的
随机 32 字节。应用启动会严格验证格式与长度，Compose 也拒绝缺值。没有默认 Key，
不要求 Secret 文件，bootstrap 用户密码与这个 Key 是两种独立的配置。

首次初始化全新数据库时，可以用 `openssl rand -base64 32` 生成一次，再把结果保存在
自己的持久部署配置中。后续操作复用同一值，不在每次构建、启动或重启时重新生成。
以下输入方式适用于项目默认 zsh，不回显 Key，也不把实际值写入命令历史：

```zsh
read -r -s 'CONTROL_PROFILE_CREDENTIAL_KEY?Profile encryption key: '
printf '\n'
export CONTROL_PROFILE_CREDENTIAL_KEY
```

如果部署系统已经注入该环境变量，跳过输入步骤。保持同一 Key 可用于本地
`compose-config`、`compose-up` 和 `compose-down`；这些命令都会解析 Compose 文件。
多副本必须使用同一 Key。不要提交、打印或记录实际值；使用下面的 quiet 配置校验，
而不是分享带解析后环境变量的完整 Compose 输出。

Profile 加密数据需要 Key 才能恢复；将 Key 与数据库备份分开保管并维持可恢复关系。
当前未实现主 Key 轮换流程，直接改值会破坏已有密文和 MAC 的可用性。

## 启动与检查

从仓库根目录运行，先完成上述环境变量注入：

```sh
just compose-config
just compose-up
curl --fail http://127.0.0.1:8080/healthz
```

- `compose-config` 校验基线与 local overlay，使用 `config --quiet`。
- `compose-up` 先校验，再本地构建并等待容器启动；数据库迁移由 Control API 在监听前执行。
- 默认回环端口为 8080；设置 `CONTROL_API_HTTP_PORT` 后，健康检查也使用对应端口。
- Compose 的 `CONTROL_BOOTSTRAP_MODE` 默认为 `auto`；已有 Operator 时不会重新创建。
  `CONTROL_BOOTSTRAP_USERNAME` 与 `CONTROL_BOOTSTRAP_PASSWORD` 支持环境变量覆盖。
- Compose 自带的用户/数据库默认值只适用于本地调试；Profile 加密 Key 仍必须显式提供。
- 实际启动成功由容器状态、迁移日志与 `/healthz` 共同验证，文档中的命令不是运行记录。

停止容器且保留命名数据卷：

```sh
just compose-down
```

## 当前运行边界

Control API 提供 11 个 Runtime Profile 管理操作，包括直接录入、COW 与 live 凭据更新。
Profile 的内部批量解析 Adapter 已有实现，但默认 bootstrap 不注册
`POST /internal/v1/runtime-profiles/credentials/resolve`。真实 Run/Attempt 授权拥有方与
可信工作负载认证需成对接入，Worker 批次初始化和内部加密传输仍由后续任务完成。
配置上述 Key 不代表这些运行面链路已启用。

完整变量清单见 [Control API](../../services/control-api/README.md)，部署所有权见
[部署目录结构](../../docs/architecture-next/operations/deployment.md)，凭据协议见
[Runtime Profile 凭据](../../docs/architecture-next/control-api/runtime-profile-credentials.md)。
