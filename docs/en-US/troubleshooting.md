# Troubleshooting

[中文](../zh-CN/troubleshooting.md)

This document lists common problems by symptom. Start by checking the service logs.

## Model Not Responding

Check:

- Whether the AI channel selected for the current conversation exists; if it is empty, `ai.default_channel` is used.
- Whether `ai.channels.<id>.base_url` contains the correct path, such as `/v1`.
- Whether `ai.channels.<id>.api_key` is valid.
- Whether `ai.channels.<id>.model` exists.
- Whether the provider supports the `reasoning` field of the current channel.

You can use the model test in the system settings. If the gateway reports 400, first try:

```yaml
ai:
  channels:
    your-channel:
      reasoning:
        mode: off
```

## Tool Execution Failure

Check:

- Whether the tool command is installed on PATH.
- Whether the `tools/*.yaml` parameter schema is correct.
- Whether it was rejected by HITL.
- Whether it exceeded `agent.tool_timeout_minutes`.
- Whether a long period of no Shell output triggered `shell_no_output_timeout_seconds`.

See `tools/README.md` for tool configuration.

## MCP Cannot Connect

Built-in MCP:

- Check `mcp.enabled`.
- Check `mcp.port`.
- Check `auth_header` and `auth_header_value`.

External MCP:

- stdio: check the command path, working directory, and environment variables.

## Knowledge Base Unavailable

Check:

- `knowledge.enabled: true`.
- Whether the embedding configuration is correct.
- Whether the sources have already been scanned and the index rebuilt.
- Whether `data/knowledge.db` is writable.
- Whether the embedding service returns 429 or times out.

If indexing fails a lot, lower:

```yaml
knowledge:
  indexing:
    batch_size: 5
    rate_limit_delay_ms: 600
```

## Database Lock or Write Failure

Check:

- Whether `data/` is writable.
- Whether multiple instances share the same SQLite file.
- Whether the disk is full.
- Whether WAL/SHM files were copied abnormally.

In production, do not let multiple processes write to the same SQLite database at the same time.

## Diagnostic Order

When you hit a problem, do not change the config right away. First locate the layer:

1. Process: is the service still running, are there any panics in the logs.
2. Network: can this machine reach the target.
5. Model: does the model test pass.
6. Tools: is the tool list and each individual schema normal.
7. Database: is `data/` writable, is there any lock.

Locate the layer first, then change parameters. Otherwise it is easy to misjudge a proxy problem as a model problem.

## Minimal Diagnostic Commands

```bash
# Process and port
lsof -i :8080

# Is local HTTPS reachable
curl -k -I https://127.0.0.1:8080/

# Are static assets reachable
curl -k -I https://127.0.0.1:8080/static/logo.png

# Inspect the database files
ls -lh data/
```

## Common Misdiagnoses

- "The model is broken": in fact HITL is suspended waiting for approval.
- "The tools were not loaded": in fact tool_search hides most of the tools.
- "The knowledge base has no effect": in fact the index was not rebuilt, or the risk_type filter is too narrow.
- "The config was saved but does not take effect": in fact the listening port/TLS needs a restart.

## Issue Report Template

When submitting an issue, it helps to attach:

```text
Version/commit:
How you ran it:
Target and authorized scope:
Relevant config section:
Steps to reproduce:
Expected result:
Actual result:
Terminal output:
Report files:
```

With this information, locating the problem is usually much faster.
