# 项目主页与站内文档

- `/`：公开项目主页，不请求 Control API，不执行会话恢复。
- `/docs`：公开文档中心，提供按任务阅读与六篇主题参考文档。
- `/docs/guide.html`：部署与使用图文教程，插图内嵌，可离线保存。
- `/docs/reference/*.html`：可直接阅读的主题参考文档。
- `/console`：原首页的控制台入口，保留角色导航、首次改密、401 登录和 10 秒超时重试。

## 更新文档

1. 编辑 `docs/user-guide/v1/README.md`、其插图，或本目录下的主题 Markdown。
2. 在 `web` 目录运行 `npm run docs:sync`。
3. 运行 `npm run docs:check`、`npm test` 和 `npm run build`。
4. 一起提交文档源文件与 `web/public/docs` 中的生成产物。

站内参考文档采用 `web/scripts/reference.css`；完整指南采用
`web/scripts/render-user-guide.mjs`。仅构建时解析本仓库 Markdown，线上不接收或渲染用户提交的文档。

生成文件随 Web 一起发布，Docker runtime 显式复制 `public`。生产镜像构建不需要读取
Web 构建目录以外的文件。仅修改文档但未同步时，`docs:check` 与测试会报告产物过期。

主页首屏的流程图和对话为设计示意，不代表当前服务健康或真实收发结果。
现有租户控制台业务不变；登录无 `next` 时与首次改密完成后进入 `/console`。
