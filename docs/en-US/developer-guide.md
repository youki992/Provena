# Developer Guide

[中文](../zh-CN/developer-guide.md)

This guide is for contributors extending Provena. The project is a Go single-service application with a static frontend, SQLite persistence, Agent/MCP orchestration, and optional high-risk security subsystems.

## Project Layout

```text
cmd/provena/              service entrypoint
internal/app/            app wiring, routes, MCP tool registration
internal/handler/        HTTP handlers
internal/database/       SQLite access
internal/security/       auth, rate limits, shell execution
internal/mcp/            MCP server and external MCP manager
internal/multiagent/     Eino single-agent, multi-agent, middleware
internal/workflow/       graph orchestration runtime
internal/knowledge/      indexing and retrieval
internal/c2/             built-in C2
internal/project/        project fact blackboard
tools/                   YAML command tools
roles/                   role YAML
agents/                  multi-agent Markdown definitions
skills/                  Agent Skills
docs/                    documentation
```

## Development Startup

```bash
```


The frontend is static. Most JS/CSS/template changes only require a browser refresh.


## Long-Running Tasks

For scanning, indexing, batch tasks or external operations, answer:

- Can it be cancelled?
- Can progress be queried?
- Can it be retried?
- Where is the result stored?
- Does state survive page refresh?
- Does it block the HTTP request?

If not, use task tables, event streams, or monitoring.

## Extending Tools

Prefer `tools/*.yaml` for command tools. Use Go built-in tools when the tool needs internal state or structured integration.

Built-in tools should define clear input schemas, handle timeouts and errors, and respect HITL for risky actions.

## Test Priority

High-value tests:

- config hot-apply;
- HITL branches;
- shell timeout/no-output;
- external MCP recovery;
- KB indexing and post-processing;
- SQLite migration compatibility.

## Source Anchors

- App wiring: `internal/app/app.go`
- Config apply: `internal/handler/config.go`
- OpenAPI: `internal/handler/openapi.go`
- Tool executor: `internal/security/executor.go`
- Skill package: `internal/skillpackage/`
