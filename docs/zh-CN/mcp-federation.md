# MCP 联邦

Provena 同时支持内置 MCP 工具、独立 HTTP MCP 服务和外部 MCP 联邦。MCP 是 Agent 调用工具的主要协议层。

## 内置 MCP

Web 服务内部会创建 MCP Server，并注册：

- YAML 命令工具。
- 内置安全执行工具。
- 知识库工具。
- 项目事实工具。
- 批量任务工具。
- 视觉分析工具。

内置 MCP 由 Provena 自己在运行期调用，不需要额外配置。

## 外部 MCP

外部 MCP 配置在：

```yaml
external_mcp:
  servers: {}
```

也可以通过 Web 的 MCP 管理页面新增、启动、停止和删除。

接口：


管理页面提供“验证”和“保存并验证”按钮。验证成功后会展示工具列表、参数 schema 和耗时；选择具体工具后可填写 JSON 参数进行实际调用。配置错误、连接失败、握手失败、`tools/list` 为空都会单独显示原因。

配置字段支持：`type`（`stdio`/`http`/`sse`）、`command`、`args`、`env`、`url`、`headers`、`timeout`、`max_retries`、`keep_alive`、`terminate_duration` 和 `autoApprove`。旧版 `transport` 字段仍可读取，新配置统一保存为 `type`。

## stdio

stdio MCP 适合本机命令启动的工具服务。

关注点：

- 命令路径必须存在。
- 工作目录正确。
- 环境变量完整。
- 进程退出会导致工具不可用。
- 日志中查看启动失败原因。

## HTTP / SSE

HTTP 或 SSE MCP 适合远端或长期运行服务。

关注点：

- URL 可达。
- 认证头正确。
- TLS 证书可信。
- 代理和防火墙放行。
- 服务端协议版本兼容。

## 工具暴露策略

工具过多会增加上下文成本和误选概率。多代理中可通过：

```yaml
multi_agent:
  eino_middleware:
    tool_search_enable: true
    tool_search_min_tools: 20
    tool_search_always_visible: 12
    tool_search_always_visible_tools:
      - read_file
      - glob
      - grep
      - tool_search
```

让常用工具常驻，其余工具由 `tool_search` 动态解锁。

## 安全建议

- 外部 MCP 只接入可信服务。
- 远端 MCP 必须认证。
- 高风险工具不要常驻上下文。
- 外部 MCP 的文件系统和命令执行能力要单独评估。
- 变更外部 MCP 后查看审计日志。

## 调试

排查顺序：

2. 检查服务日志。
3. 单独运行 stdio 命令。
4. 用 curl 测试 HTTP/SSE 地址。
5. 检查工具是否被角色或 tool_search 策略隐藏。

## MCP 生命周期

外部 MCP 的生命周期不是简单的“添加 URL”：

1. 注册配置：名称、类型、命令或 URL、环境变量。
2. 启动连接：stdio 拉起进程，HTTP/SSE 建立客户端。
3. 拉取工具列表：工具名、描述、schema 进入平台。
4. 暴露给 Agent：受角色、tool_search、HITL 影响。
5. 执行工具：参数校验、调用、记录监控。
6. 连接恢复：进程退出或网络失败后尝试恢复。
7. 停止/删除：从运行时和配置中移除。

排错时要确认卡在哪一步。

## 工具命名规范

工具名应：

- 稳定。
- 小写或 snake_case。
- 表达动作和对象。
- 避免和内置工具重名。

不建议：

```text
run
execute
scan
tool1
```

建议：

```text
burp_send_to_repeater
asset_lookup_domain
cloud_list_public_buckets
```

好的工具名会提升 tool_search 命中率，也降低误调用。

## 外部 MCP 安全审查清单

接入前问：

- 它能读写本机文件吗？
- 它能执行命令吗？
- 它会访问哪些网络？
- 它是否把请求发给第三方？
- 它的工具描述是否可信？
- 它的输出是否可能包含 prompt injection？
- 它是否需要独立运行用户或容器隔离？

只要答案不清楚，就不要放进生产环境常驻工具池。

## 源码锚点

- 外部 MCP Manager：`internal/mcp/external_manager.go`
- 连接恢复：`internal/mcp/connection_recovery.go`
- MCP 工具适配：`internal/einomcp/mcp_tools.go`
- 外部 MCP Handler：`internal/handler/external_mcp.go`
- 工具调用通知：`internal/einomcp/tool_invoke_notify.go`
