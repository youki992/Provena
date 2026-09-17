# 测试指南

Provena 的测试包括 Go 单测、配置验证、CLI 冒烟和 MCP 工具验证。

## Go 单测

运行全部内部测试：

```bash
go test ./internal/...
```

运行指定包：

```bash
go test ./internal/workflow
go test ./internal/multiagent
go test ./internal/handler
```

常见重点包：

- `internal/security`
- `internal/mcp`
- `internal/multiagent`
- `internal/workflow`
- `internal/knowledge`
- `internal/project`
- `internal/handler`
- `internal/c2`

## 构建测试

两种构建都要过，它们编译的是不同的文件集：

```bash
go build -o provena ./cmd/provena                        # 默认：纯 CLI
```

`internal/app` 里 `routes.go` / `routes_stub.go` 和 `cmd/provena` 里
`serve.go` / `serve_stub.go` 是互补的 build tag 对，只测一种构建会漏掉另一半。
构建通过不代表功能正确，但能发现入口、依赖和静态类型问题。

## 配置验证

启动前检查：

- YAML 缩进。
- 模型配置。
- 数据库路径可写。
- `tools_dir`、`roles_dir`、`skills_dir`、`agents_dir` 是否存在。
- HTTPS 证书路径是否正确。

启动后在 Web 设置页测试：

- OpenAI 兼容模型。
- 视觉模型。
- 工具列表。
- 外部 MCP 状态。

## 工具测试

新增或修改 `tools/*.yaml` 后：

- 在工具列表中确认 schema。
- 用低风险参数执行。
- 检查错误输出是否可读。
- 检查超时是否生效。
- 检查 HITL 是否按预期拦截。

不要用生产目标测试新工具。

## MCP 测试

外部 MCP：

- stdio：先在终端独立运行命令。
- HTTP/SSE：用 curl 检查连通性。
- 在对话中确认工具是否可被 `tool_search` 找到。

## 知识库测试

步骤：

1. 放入小型 Markdown 文档。
2. 扫描知识库。
3. 重建索引。
4. 搜索文档中的关键词和同义表达。
5. 查看检索日志。

如果使用真实 embedding API，注意配额和速率限制。

## 测试金字塔

建议测试分层：

| 层级 | 目标 | 示例 |
| --- | --- | --- |
| 单元测试 | 纯逻辑正确 | 表达式、chunk、脱敏、超时格式 |
| Handler 测试 | HTTP 行为 | 参数校验、状态码、权限 |
| 集成测试 | 多模块协作 | 外部 MCP、知识库索引、HITL |
| 冒烟测试 | 用户路径可用 | 登录、对话、工具、设置 |

不要用端到端手测代替单元测试，也不要用单元测试代替高风险靶场验证。

## 回归测试重点

修改这些模块时必须扩大测试范围：

- `internal/multiagent/`：测流式、工具调用、摘要、重试、HITL。
- `internal/security/`：测认证、Shell、超时、无输出。
- `internal/database/`：测旧数据兼容。

## 测试数据管理

不要用真实客户数据做测试。建议准备：

- 小型 Markdown 知识库样例。
- 本地假 MCP Server。
- 本地可控 HTTP 目标。
- 一个无害的本地 MCP 服务。
- 临时 SQLite 数据库。

测试完成后删除临时数据库和上传文件，避免污染开发环境。

## 失败用例比成功用例更重要

至少覆盖：

- 模型 API 401/429/500。
- MCP 进程启动失败。
- 工具超时。
- HITL 拒绝。
- 知识库索引中断。
- 数据库不可写。

这些才是用户真实会遇到的问题。

## 源码锚点

已有测试集中在：

- `internal/handler/*_test.go`
- `internal/multiagent/*_test.go`
- `internal/workflow/*_test.go`
- `internal/knowledge/*_test.go`
- `internal/security/*_test.go`
- `internal/mcp/*_test.go`
- `internal/c2/*_test.go`
