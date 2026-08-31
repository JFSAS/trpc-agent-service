# 文档索引

本仓库的架构文档分为两类：

- [`architecture-next/`](architecture-next/README.md)：当前重构使用的规范性文档，
  用于约束后续代码、协议、数据库和部署设计。
- 根目录 [`README.md`](../README.md)：项目入口、当前状态和生产 Workload 概览。

## 当前阶段

目前处于新架构的约束确认阶段。先固定系统边界、依赖方向和不可违反的
工程规则，再分别设计 Control API、Channel Gateway 和 Agent Worker。

当前规范性入口：

- [下一代架构文档](architecture-next/README.md)
- [架构级约束](architecture-next/constraints.md)

文档中的“已接受”表示设计决策已经确认；它不表示对应代码已经实现或通过
端到端验证。实现状态必须单独记录。
