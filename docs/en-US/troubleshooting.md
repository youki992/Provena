# Troubleshooting

[中文](../zh-CN/troubleshooting.md)

Debug by layer. Do not change random config before locating the failing layer.

## Diagnostic Order

1. Process: is the service alive, any panic?
2. Network: can the target be reached from this machine?
5. Model: does model test pass?
6. Tools: do tool list and schemas look right?
7. Database: is `data/` writable, any lock?

## Minimal Commands

```bash
lsof -i :8080
curl -k -I https://127.0.0.1:8080/
curl -k -I https://127.0.0.1:8080/static/logo.png
ls -lh data/
```

If an external MCP server is involved, test that server on its own.

## Common Issues

Page inaccessible:

- wrong protocol, especially HTTPS vs HTTP;
- self-signed cert warning;
- port occupied;

Login fails:

- wrong RBAC user password;
- config not applied/restarted;
- stale cookie;
- audit throttling repeated failures.

## Common Misdiagnoses

- "Model is broken": HITL is waiting.
- "Tool missing": tool_search hides it from current context.
- "Knowledge base useless": index not rebuilt or risk type too narrow.
- "Config saved but ineffective": listener/TLS changes need restart.
- "Robot silent": platform callback or signature config wrong.

## Issue Template

```text
Version:
Startup method:
Access path:
Relevant config:
Steps:
Expected:
Actual:
Server logs:
Browser console:
API response:
```
