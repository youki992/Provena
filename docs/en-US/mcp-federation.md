# MCP Federation

[中文](../zh-CN/mcp-federation.md)

Provena supports built-in MCP tools, standalone HTTP MCP services, and external MCP federation at the same time. MCP is the primary protocol layer through which Agents call tools.

## Built-in MCP

Provena creates an internal MCP server and registers:

- YAML command tools.
- Built-in security execution tools.
- Knowledge base tools.
- Project fact tools.
- Batch task tools.
- Vision analysis tools.

Built-in MCP is called by Provena itself at runtime and needs no extra configuration.

## External MCP

External MCP is configured in:

```yaml
external_mcp:
  servers: {}
```

MCP configuration can be validated: validation performs a real MCP handshake and `tools/list`, then reports the discovered tools, input schemas, and duration. A specific tool can be invoked with JSON arguments to make a real call. Invalid configuration, connection failures, handshake failures, and an empty `tools/list` are each reported separately with their own reason.

Supported configuration fields are `type` (`stdio`/`http`/`sse`), `command`, `args`, `env`, `url`, `headers`, `timeout`, `max_retries`, `keep_alive`, `terminate_duration`, and `autoApprove`. The legacy `transport` field is still readable; new configurations are saved uniformly as `type`.

## stdio

stdio MCP suits tool services started by a local command.

Concerns:

- The command path must exist.
- The working directory is correct.
- Environment variables are complete.
- If the process exits, the tool becomes unavailable.
- Check the logs for the reason startup failed.

Concerns:

- The URL is reachable.
- Authentication headers are correct.
- The TLS certificate is trusted.
- Proxies and firewalls allow it through.
- The server-side protocol version is compatible.

## Tool Exposure Strategy

Too many tools increase context cost and the probability of mis-selection. In a multi-agent setup you can use:

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

to keep frequently used tools resident while the rest are dynamically unlocked by `tool_search`.

## Security Recommendations

- Connect external MCP only to trusted services.
- Remote MCP must be authenticated.
- Do not keep high-risk tools resident in context.
- Evaluate the filesystem and command-execution capabilities of an external MCP separately.
- Review the audit logs after changing an external MCP.

## Debugging

Troubleshooting order:

2. Check the service logs.
3. Run the stdio command on its own.
5. Check whether the tool is hidden by a role or `tool_search` policy.

## MCP Lifecycle

The lifecycle of an external MCP is not simply "adding a URL":

1. Register configuration: name, type, command or URL, environment variables.
3. Pull the tool list: tool names, descriptions, and schemas enter the platform.
4. Expose to the Agent: affected by role, tool_search, and HITL.
5. Execute the tool: argument validation, invocation, and recording/monitoring.
6. Connection recovery: attempt recovery after a process exit or network failure.
7. Stop/delete: remove it from the runtime and the configuration.

When troubleshooting, determine which step it is stuck at.

## Tool Naming Conventions

Tool names should be:

- Stable.
- Lowercase or snake_case.
- Express an action and an object.
- Avoid clashing with the names of built-in tools.

Not recommended:

```text
run
execute
scan
tool1
```

Recommended:

```text
burp_send_to_repeater
asset_lookup_domain
cloud_list_public_buckets
```

Good tool names improve the `tool_search` hit rate and reduce mis-invocation.

## External MCP Security Review Checklist

Before connecting, ask:

- Can it read and write local files?
- Can it execute commands?
- Which networks does it access?
- Does it send requests to third parties?
- Are its tool descriptions trustworthy?
- Can its output contain prompt injection?
- Does it need to run under a separate user or container isolation?

As long as the answers are unclear, do not put it into the production environment's resident tool pool.

## Source Anchors

- External MCP Manager: `internal/mcp/external_manager.go`
- Connection recovery: `internal/mcp/connection_recovery.go`
- MCP tool adapter: `internal/einomcp/mcp_tools.go`
- External MCP Handler: `internal/handler/external_mcp.go`
- Tool invocation notification: `internal/einomcp/tool_invoke_notify.go`
