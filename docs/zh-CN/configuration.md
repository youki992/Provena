# 配置参考

Provena 的主配置文件是 `config.yaml`。大多数配置也可以在 Web 的“系统设置”中修改，保存后再应用。生产环境中，建议把敏感值放在受控配置系统中，并限制 `config.yaml` 的文件权限。

## 基础配置

```yaml
version: "vX.Y.Z" # 占位符；请使用 config.example.yaml 中当前发布版本的值
server:
  host: 0.0.0.0
  port: 8080
  tls_enabled: true
  tls_auto_self_sign: true
  # 可选：其他可信 Web 集成；Chromium 浏览器插件无需配置
  # cors_allowed_origins:
  #   - https://trusted-integration.example
auth:
  session_duration_hours: 12
log:
  level: info
  output: stdout
```

- `version`：版本号。
- `server.host/port`：Web 服务监听地址和端口。
- `server.tls_*`：HTTPS 配置。生产环境建议使用 `tls_cert_path` 和 `tls_key_path`。
- Chromium 浏览器插件的合法 `chrome-extension://<32位插件ID>` Origin 会被自动识别，无需配置。插件仍需按域授权，并使用密码登录与 Bearer Token 调用 API。
- `server.cors_allowed_origins`：仅供其他可信 Web 集成使用的额外 Origin 精确白名单；不支持 `*`，修改后需重启服务。
- `auth.session_duration_hours`：登录会话有效期（小时）。登录密码由 RBAC 用户管理，首次启动时在控制台输出 `admin` 初始密码。
- `log.output`：可以是 `stdout`、`stderr` 或文件路径。

## AI 通道与模型配置

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

`ai` 是推荐的模型配置入口。系统设置页对应路径是 **系统设置 → 基本设置 → AI 通道配置**，保存后写入 `ai.default_channel` 和 `ai.channels`。旧版 `openai` 字段仍保留为兼容运行时字段；加载配置时会确保至少有一个默认通道，并把 `ai.default_channel` 解析后的配置同步到运行时 `openai`。

通道字段：

| 字段 | 说明 |
| --- | --- |
| `ai.default_channel` | 默认通道 ID。未显式选择通道的运行使用它。 |
| `ai.channels.<id>` | 通道配置。ID 会归一化为小写、数字和短横线，例如 `Qwen_Max` 会变成 `qwen-max`。 |
| `name` | 展示名。留空时使用通道 ID。 |
| `provider` | `openai_compatible` 或 `claude`。`openai_compatible` 会在运行时映射为 `openai`；`claude` 会桥接到 Anthropic Messages API。 |
| `base_url/api_key/model` | 必填。Base URL 通常需要包含版本路径，如 OpenAI/兼容网关的 `/v1`。 |
| `max_total_tokens` | 上下文压缩、攻击链构建、多代理摘要等共用的总预算。 |
| `max_completion_tokens` | 单次模型输出上限；未填时使用默认值。 |
| `reasoning` | 该通道的默认推理扩展字段。不同网关支持差异较大，异常时先尝试 `mode: off`。 |

对话页的“AI 通道”下拉框会读取已保存通道。请求体中的 `aiChannelId` 非空时仅对本次/本会话运行配置生效，不会把 API Key 发送给模型；为空时跟随 `ai.default_channel`。

### 环境变量占位符

`base_url`、`api_key` 等字符串字段支持 `${VAR}` 与 `${VAR:-默认值}` 占位，在解析通道时
（`internal/config/envexpand.go` 的 `expandEnvVar`，由 `ToOpenAIConfig()` 调用）展开。
这样密钥可以只存在于 shell / systemd / 容器环境里，不落进仓库：

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

两条容易踩的规则：

- 变量**未设置就展开成空串**，既不会报错也不会回退。`provena doctor` 会把这种通道判为
  `api_key is empty`；`serve` / `run` 则要等到真正调用模型时才失败。改完配置先跑
  `provena doctor -config config.yaml`。
- 占位符是**运行时展开**，写回文件时保留原文。通过 Web 设置页保存通道时，没动过的字段
  仍然是文件里的 `${PROVENA_API_KEY}`，不会变成明文密钥；在表单里填了新值则保存新值。

### base_url 的版本路径不能省

`base_url` 必须是**真正接受 API 请求的前缀**，多数网关是 `https://host/v1`。少了 `/v1`
往往不会得到一个干净的 404，而是网关给你一个 **200 + HTML 首页**——harness 拿到的不是
SSE 流，解析不出任何内容，于是每轮都以「成功但什么都没做」收场：

```text
▌ activity 1/1
  ✓ 7s · 0 tool call(s)                        ← 不报错、status 正常、图版本不变
```

`provena run` 的终端提示会同时点出这一条。要区分「缺 `/v1`」和「模型真不干活」，
用 `pi` 的 print 模式（不是 rpc）单跑一次最直接：

```bash
# 用与 provena 相同的 provider 配置探测；有输出＝通道正常，空输出＝通道有问题
pi -p "Reply with the single word: pong" --provider provena --model <model>
```

再用 `curl` 对比两条路径即可定案：

```bash
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://host/chat/completions ...     # 200 但返回 HTML → 缺 /v1
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://host/v1/chat/completions ...  # 正常
```

### 改配置会影响哪个文件

Web 设置页保存时直接写回启动时 `-config` 指向的那个文件（默认 `config.yaml`），并先把
原文件复制一份到 `config.yaml.backup`。配置只在下次运行时生效，不存在
“UI 改了但 `provena run` 读不到”的情况；反之手改 YAML 后也需要在 UI 里重新加载，
否则页面上显示的仍是旧值。

常用操作：

- 新增：点击左侧 `+`，填写必填字段后保存。
- 设默认：选中通道后点击“设为默认”，保存并应用后新请求生效。
- 复制：以当前表单内容创建副本，适合为同一服务商配置不同模型。
- 删除：默认通道不能作为批量删除目标；删除后需保留至少一个通道。
- 探活：单通道“测试连接”或左侧“批量探活”会调用模型测试接口，适合验证 Key、Base URL 和模型名。

## Agent

```yaml
agent:
  max_iterations: 12000
  tool_timeout_minutes: 60
  shell_no_output_timeout_seconds: 1200
  workspace_root_dir: ""
  system_prompt_path: ""
```

- `max_iterations`：单代理、多代理主执行器和子代理的默认迭代上限。
- `tool_timeout_minutes`：单次工具最长运行时间。
- `shell_no_output_timeout_seconds`：Shell 长时间无输出时终止。
- `workspace_root_dir`：会话工作区根目录，建议不要设置到系统 `/tmp`。
- `system_prompt_path`：单代理系统提示词覆盖文件。

## HITL

```yaml
hitl:
  default_reviewer: audit_agent
  retention_days: 90
  tool_whitelist: [read_file, list_dir, glob, grep, tool_search]
  audit_model:
    provider: ""
    base_url: ""
    api_key: ""
    model: ""
```

- `default_reviewer`：`human` 或 `audit_agent`。
- `tool_whitelist`：全局免审批工具列表，会与会话白名单合并。
- `audit_model`：审计 Agent 独立模型；留空复用主模型。
- `audit_agent_prompt` / `audit_agent_prompt_review_edit`：可覆盖默认审批策略。

更多策略见 [人机协同最佳实践](hitl-best-practices.md)。

## 工具与 MCP

```yaml
security:
  tools_dir: tools
  tool_description_mode: full
mcp:
  enabled: false
  host: 0.0.0.0
  port: 8081
  auth_header: "X-MCP-Token"
  auth_header_value: ""
external_mcp:
  servers: {}
```

- `security.tools_dir`：内置工具 YAML 目录。
- `tool_description_mode`：`short` 更省 token，`full` 更完整。
- `mcp.enabled`：是否启动独立 HTTP MCP 服务。
- `mcp.auth_header_value`：外部调用 MCP 时的共享密钥，生产环境必须设置。
- `external_mcp.servers`：外部 MCP 联邦配置。

工具 YAML 规则见 `tools/README.md`。

## 知识库

```yaml
knowledge:
  enabled: false
  base_path: knowledge_base
  embedding:
    provider: openai
    model: text-embedding-v4
    base_url: ""
    api_key: ""
  retrieval:
    top_k: 5
    similarity_threshold: 0.4
  indexing:
    chunk_size: 512
    chunk_overlap: 50
    batch_size: 10
```

启用后会注册知识库检索工具，并开放管理接口。详细说明见 [知识库](knowledge-base.md)。

## 数据库

```yaml
database:
  path: data/conversations.db
  knowledge_db_path: data/knowledge.db
```

默认使用 SQLite。`knowledge_db_path` 为空时可复用会话数据库；独立文件更便于迁移知识库。

## 审计与监控

```yaml
audit:
  enabled: true
  retention_days: 15
  max_detail_bytes: 8192
monitor:
  retention_days: 90
```

- `audit` 记录平台操作，不记录对话正文和每次工具调用正文。
- `monitor` 管理工具执行记录保留时间。

## 配置修改建议

- 先在测试环境验证模型、MCP、知识库和高风险工具。
- 改动 `tools_dir`、`roles_dir`、`skills_dir`、`agents_dir` 后，检查 Web 页面是否能列出对应资源。
- 避免开启不需要的外部 MCP。
- 修改敏感配置后，检查审计页面是否有异常登录或配置变更记录。

## 配置应用机制


实务判断：

| 配置段 | 应用后通常立即生效 | 需要额外动作 |
| --- | --- | --- |
| `ai.default_channel` / `ai.channels` | 新请求使用解析后的默认或选定通道 | 正在运行的任务不会被强制切换 |
| `openai` | 兼容字段；通常由默认 AI 通道同步 | 新配置优先维护 `ai.channels` |
| `agent.max_iterations` | 新 Agent 任务生效 | 已运行任务按启动时状态继续 |
| `security.tool_description_mode` | 工具重新暴露时生效 | 模型已有上下文不会回滚 |
| `hitl.tool_whitelist` | 新工具调用审批判断生效 | 已挂起审批不自动重判 |
| `knowledge.enabled` | 会尝试初始化/更新组件 | 启用后仍需扫描和索引 |
| `knowledge.embedding` | 检索器/索引器配置更新 | 已有向量通常需要重建索引 |
| `robots` | 会触发连接重启 | 平台回调配置仍需在平台侧正确 |
| `server.port/tls` | 通常需要重启进程 | 监听地址不是普通热更新 |

## 配置优先级和派生关系

几个字段有“留空复用”的关系：

- `vision.api_key/base_url/provider` 留空时复用 `openai`。
- `hitl.audit_model` 留空时复用默认 AI 通道解析后的 `openai`。
- `knowledge.embedding.base_url/api_key` 留空时复用主模型或 embedding 默认配置。
- `knowledge.retrieval.rerank.base_url/api_key` 留空时复用 embedding/openai。
- `database.knowledge_db_path` 留空时可以复用主会话数据库，但独立文件更利于备份。

这类配置排障时不要只看子配置段，也要看它会回落到哪个上级配置。

## 参数取值建议

| 参数 | 保守值 | 激进值 | 判断依据 |
| --- | --- | --- | --- |
| `agent.tool_timeout_minutes` | 10-30 | 60+ | 扫描工具是否常跑长任务 |
| `shell_no_output_timeout_seconds` | 300-600 | 1200+ | 工具是否长时间静默 |
| `knowledge.indexing.batch_size` | 5-10 | 20+ | embedding 服务批量限制 |
| `knowledge.indexing.rate_limit_delay_ms` | 300-800 | 0-100 | 服务商 RPM 和 429 情况 |
| `retrieval.top_k` | 3-5 | 8-12 | 内容质量和上下文预算 |
| `similarity_threshold` | 0.35-0.45 | 0.5+ | 召回优先还是精度优先 |
| `audit.retention_days` | 15-30 | 90+ | 合规要求和磁盘空间 |
| `monitor.retention_days` | 30-90 | 180+ | 是否需要长周期复盘 |

## 变更前后验证模板

修改配置前记录：

```text
变更目的：
涉及配置段：
预期影响：
回滚方式：
验证接口：
```

修改后验证：

```bash
  -H "Authorization: Bearer <token>"
```

再按配置类型验证模型、工具或知识库。

## 源码锚点

- 配置结构：`internal/config/config.go`
- 环境变量展开：`internal/config/envexpand.go`
- Web 配置接口：`internal/handler/config.go`
- 路由注册：`internal/app/routes.go`（`setupRoutes`）
