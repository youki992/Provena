# Eino Orchestration and the CLI

[中文](../zh-CN/MULTI_AGENT_EINO.md)

**Provena's command-line tool does not use Eino.**

`provena run` and `provena chat` drive the Pi harness. `internal/run` imports only
`internal/piagent` and `internal/fgs`, and the model loop itself lives in the `pi` process.
The `internal/agent` package the CLI hands to the tool bridge is the MCP/tool layer, and it
has no Eino dependency at all.

The Eino single-agent and multi-agent orchestration — Deep, Plan-Execute and Supervisor —
lives in `internal/multiagent`, `internal/einomcp` and `internal/einoobserve`. It is reached
only through the console HTTP layer (`internal/handler`) and the web front end, neither of
which this repository ships, so no command here can start it.

The `multi_agent` section still exists in `config.example.yaml` because the configuration
file is shared with the server build. Setting it has no effect on the CLI.

## Source anchors

If you are working on that subsystem, the code is:

- `internal/multiagent/runner.go` — assembles Deep / Plan-Execute / Supervisor
- `internal/multiagent/eino_orchestration.go` — PlanExecute root node and executor middleware
- `internal/multiagent/eino_single_runner.go` — the single-agent Eino path
- `internal/multiagent/eino_middleware.go` — the middleware stack
- `internal/einomcp/` — MCP tools exposed to Eino
- `internal/einoobserve/` — Eino observability callbacks
