# Configuration Reference

[中文](../zh-CN/configuration.md)

The main configuration file is `config.yaml`. Many fields are editable through the Web settings page, but not every field has the same hot-apply behavior.

## Core Sections

```yaml
server:
  host: 0.0.0.0
  port: 8080
  tls_enabled: true
  # Optional: other trusted Web integrations; Chromium extensions need no entry.
  # cors_allowed_origins:
  #   - https://trusted-integration.example
auth:
  session_duration_hours: 12
ai:
  default_channel: openai-main
  channels:
    openai-main:
      name: OpenAI Main
      provider: openai_compatible
      base_url: https://api.openai.com/v1
      api_key: sk-...
      model: gpt-4.1
agent:
  max_iterations: 12000
  tool_timeout_minutes: 60
```

Change the initial `admin` password from the Web UI after first login. Use HTTPS or a trusted reverse proxy in any shared environment.

Valid Chromium `chrome-extension://<32-character-extension-id>` origins are recognized automatically. The extension must still obtain host permission and authenticate with a password and Bearer token. `server.cors_allowed_origins` remains available as an exact allowlist for other trusted Web integrations; wildcards are not accepted, and changing it requires a restart.

## AI Channels

`ai` is the recommended model configuration entry. In the Web UI, use **System Settings → Basic Settings → AI Channel Configuration**. Saving that form writes `ai.default_channel` and `ai.channels`. The legacy `openai` field remains as a backward-compatible runtime field; on load, Provena ensures a default channel exists and synchronizes the resolved `ai.default_channel` into runtime `openai`.

```yaml
ai:
  default_channel: openai-main
  channels:
    openai-main:
      name: OpenAI Main
      provider: openai_compatible
      base_url: https://api.openai.com/v1
      api_key: sk-...
      model: gpt-4.1
      max_total_tokens: 120000
      max_completion_tokens: 16384
      reasoning:
        mode: on
        effort: high
        allow_client_reasoning: true
        profile: openai_compat
    claude-main:
      name: Claude Main
      provider: claude
      base_url: https://api.anthropic.com/v1
      api_key: sk-ant-...
      model: claude-sonnet-4-5
```

| Field | Meaning |
| --- | --- |
| `ai.default_channel` | Default channel ID for new conversations and requests without an explicit channel. |
| `ai.channels.<id>` | Channel config. IDs are normalized to lowercase letters, digits, and hyphens. |
| `name` | Display name in the Web UI; falls back to the ID. |
| `provider` | `openai_compatible` or `claude`. OpenAI-compatible channels map to runtime `openai`; Claude channels bridge to Anthropic Messages API. |
| `base_url/api_key/model` | Required. Base URL usually includes a version path such as `/v1`. |
| `max_total_tokens` | Shared context budget for compression, attack-chain generation, multi-agent summaries, and similar paths. |
| `max_completion_tokens` | Per-response output cap; default is used when empty. |
| `reasoning` | Default reasoning fields for the channel. Gateway support varies; try `mode: off` first when a provider rejects requests. |

The chat page reads saved channels into the “AI Channel” selector. A non-empty request `aiChannelId` selects a channel for that run/session without sending API credentials through the prompt path. Empty `aiChannelId` follows `ai.default_channel`.

### Environment variable placeholders

String fields such as `base_url` and `api_key` accept `${VAR}` and `${VAR:-default}` placeholders. They are expanded when a channel is resolved — `expandEnvVar` in `internal/config/envexpand.go`, called from `ToOpenAIConfig()` — so credentials can live in the shell, systemd, or a container environment instead of the repository:

```yaml
ai:
  default_channel: default
  channels:
    default:
      provider: openai_compatible
      base_url: ${PROVENA_BASE_URL:-https://api.example.com/v1}
      api_key: ${PROVENA_API_KEY}
      model: deepseek-v4-flash
```

```bash
export PROVENA_API_KEY="sk-..."
```

Two rules that are easy to get wrong:

- An **unset variable expands to an empty string** — no error, no fallback. `provena doctor` reports such a channel as `api_key is empty`; `serve` / `run` only fail once a model is actually called. Run `provena doctor -config config.yaml` after editing.
- Placeholders are expanded at **runtime**, and the file keeps the literal text. Saving a channel from the Web UI leaves untouched fields as `${PROVENA_API_KEY}` instead of writing a plaintext key; fields you retype in the form are saved with the new value.

### base_url must keep its version path

`base_url` has to be the prefix that actually accepts API requests, which for most gateways is `https://host/v1`. Dropping `/v1` usually does not give a clean 404 — the gateway answers with a **200 and an HTML landing page**. The harness receives something that is not an SSE stream, parses nothing, and every round ends as “successful, but did nothing”:

```text
▌ activity 1/1
  ✓ 7s · 0 tool call(s)                        ← no error, normal status, unchanged graph
```

`provena run` prints that hint alongside the empty round. To tell “missing `/v1`” apart from “the model really did nothing”, probe once with `pi` in print mode (not rpc):

```bash
# Same provider config provena uses; output means the channel works, silence means it does not
pi -p "Reply with the single word: pong" --provider provena --model <model>
```

Then compare the two paths with `curl` to settle it:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://host/chat/completions ...     # 200 with HTML → /v1 is missing
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://host/v1/chat/completions ...  # expected
```

### Which file a config change affects

Saving from the Web UI writes back to the file passed as `-config` at startup (default `config.yaml`), after copying the original to `config.yaml.backup`. The command line and the Web UI therefore edit the same file, so a UI change is never invisible to `provena run`; conversely, after editing the YAML by hand, reload it in the UI.

Common Web UI operations:

- Add: click `+`, fill required fields, then save.
- Set default: select a channel, click **Set as default**, then save/apply.
- Copy: duplicate the current form, useful for the same provider with a different model.
- Delete: keep at least one channel; the default channel is protected from bulk delete.
- Probe: use **Test connection** or **Bulk probe** to validate API key, Base URL, and model.

## Hot-Apply Boundaries

`POST /api/config/apply` coordinates model config, tool description mode, MCP tool registration, knowledge components, robot restarts, and C2 runtime reconciliation. It does not make every field instantly effective.

| Section | Usually hot-applies | Extra action |
| --- | --- | --- |
| `ai.default_channel` / `ai.channels` | new requests use the resolved default or selected channel | running streams keep their current state; reload config for the frontend channel list |
| `openai` | compatibility field, usually synchronized from the default AI channel | prefer maintaining new config in `ai.channels` |
| `agent.max_iterations` | new tasks | existing tasks continue |
| `hitl.tool_whitelist` | new approval checks | pending approvals are not re-decided |
| `knowledge.enabled` | initializes/updates components | scan and index are still required |
| `knowledge.embedding` | updates retriever/indexer config | rebuild index for existing vectors |
| `robots` | restarts long-lived connections | platform callback settings must still match |
| `c2.enabled` | reconciles C2 runtime | verify existing listeners/sessions manually |
| `server.port/tls` | usually needs process restart | listener settings are not ordinary hot state |

## Fallback Relationships

- `vision.api_key/base_url/provider` can inherit from the resolved default AI channel.
- `hitl.audit_model` can inherit from the resolved default AI channel.
- `knowledge.embedding.base_url/api_key` can inherit from model settings.
- rerank config can inherit from embedding/openai.
- `database.knowledge_db_path` can be separate or reuse the main DB.

When debugging, inspect both the child config and the fallback parent.

## Recommended Values

| Field | Conservative | Aggressive | Decide by |
| --- | --- | --- | --- |
| `agent.tool_timeout_minutes` | 10-30 | 60+ | long scanners |
| `shell_no_output_timeout_seconds` | 300-600 | 1200+ | quiet tools |
| `knowledge.indexing.batch_size` | 5-10 | 20+ | embedding API limits |
| `knowledge.indexing.rate_limit_delay_ms` | 300-800 | 0-100 | 429 frequency |
| `retrieval.top_k` | 3-5 | 8-12 | context budget |
| `similarity_threshold` | 0.35-0.45 | 0.5+ | recall vs precision |
| `audit.retention_days` | 15-30 | 90+ | compliance and disk |

## Change Template

Before changing config, write down:

```text
Purpose:
Sections:
Expected impact:
Rollback:
Validation endpoints:
```

After changing, validate the specific subsystem rather than trusting the save message.

## Source Anchors

- Config structs: `internal/config/config.go`
- Env expansion: `internal/config/envexpand.go`
- Config API and apply: `internal/handler/config.go`
- Route registration: `internal/app/routes.go` (`setupRoutes`)
- C2 reconciliation: `internal/app/c2_lifecycle.go`
