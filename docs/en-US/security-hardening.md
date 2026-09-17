# Security Hardening

[中文](../zh-CN/security-hardening.md)

This checklist covers pre-production and continuous hardening for Provena.

## Before Going Live

- Keep `config.yaml` readable only by you; it holds your model credentials.
- Never commit `config.yaml`, `data/` or `tools/bin/`.
- Restrict access by IP, VPN, or bastion.
- Enable `audit.enabled`.
- Review every external MCP server before enabling it.
- Do not expose standalone HTTP MCP without strong auth and network isolation.
- Connect only trusted external MCP services.
- Back up `config.yaml`, `data/`, and custom resource directories.

## HITL Allowlist Baseline

Minimal allowlist:

```yaml
hitl:
  tool_whitelist:
    - read_file
    - glob
    - grep
    - tool_search
```

Do not globally allowlist:

- `execute`;
- high-risk external MCP tools;
- delete, write, upload, persistence tools.

## File Permissions

```bash
chmod 600 config.yaml
chmod 700 data
```

Run under a dedicated OS user. Avoid root unless explicitly required.

## External MCP Review

Before connecting:

- Can it execute commands?
- Can it read/write local files?
- Does it send data to third parties?
- Does it authenticate?
- Can output contain untrusted model/web content?
- Should it run in a container or separate user?

After connecting:

- keep high-risk tools out of allowlist;
- review tool list changes;
- audit config changes.

## Retention

Suggested:

- audit: 30-90 days;
- monitor: 90-180 days;
- uploads: clean after project;
- Tool outputs: keep only what the report needs;
- knowledge base: no real credentials or customer secrets.

## Periodic Review

Weekly:

- failed logins and unusual IPs;
- config changes;
- external MCP changes;
- long-running tools;
- unexpected external MCP servers;
- report files left behind in `data/runs/`;
- disk and DB size.

Project closeout:

- clean temp workspaces;
- delete unnecessary uploads;
- archive evidence;
- delete stale reports and workspaces.
- export audit records.
