# Eino 编排与命令行工具

[English](../en-US/MULTI_AGENT_EINO.md)

**Provena 的命令行工具不使用 Eino。**

`provena run` 与 `provena chat` 驱动的是 Pi harness。`internal/run` 只 import
`internal/piagent` 和 `internal/fgs`，模型循环本身跑在 `pi` 进程里。CLI 交给工具桥的
`internal/agent` 是 MCP／工具层，完全没有 Eino 依赖。

Eino 的单代理与多代理编排（Deep、Plan-Execute、Supervisor）位于
`internal/multiagent`、`internal/einomcp` 和 `internal/einoobserve`。它只通过控制台 HTTP 层
（`internal/handler`）和前端接入，而这两者在当前仓库里都不存在，因此没有任何命令能启动它。

`config.example.yaml` 里仍保留 `multi_agent` 段，是因为配置文件与服务端构建共用。在命令行
下设置它不会产生任何效果。

## 关键文件索引

如果你要改这套子系统，代码位置是：

- `internal/multiagent/runner.go` —— 组装 Deep / Plan-Execute / Supervisor
- `internal/multiagent/eino_orchestration.go` —— PlanExecute 根节点与 Executor 中间件
- `internal/multiagent/eino_single_runner.go` —— 单代理 Eino 路径
- `internal/multiagent/eino_middleware.go` —— 中间件栈
- `internal/einomcp/` —— 暴露给 Eino 的 MCP 工具
- `internal/einoobserve/` —— Eino 可观测性回调
