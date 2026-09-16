# Provena v3 Minimal

第三版是独立配置，不覆盖旧版 `config.yaml`。

## 能力边界

- FGS：`pi_agent.harness_mode: fgs`
- Skill：只加载 `skills-v3/src-6k-skill`
- 基础工具：`tools-v3/http-framework-test.yaml`、`tools-v3/web-search.yaml`、`tools-v3/query-execution-result.yaml`
- Pi 本地证据：仅通过 Pi 底层允许的读取能力分析工作区与抓包
- 资产发现与任务收集：唯一外部 MCP `arl`
- 已关闭：Eino 多代理入口，以及 `tools/` 中的第三方 CLI 扫描器（FOFA/ZoomEye/Quake/Shodan/nmap/ffuf 等）
- 保留：Provena 自身内置 MCP 工具（漏洞/资产/项目事实/Vision/WebShell/C2/抓包/执行结果等）；ARL MCP 负责外部资产收集
- FGS 单轮保护：`pi_agent.activity_timeout_seconds` 默认 180 秒；Pi 单轮超时会阻止对应 Step 并返回 `partial`，不会被 600 分钟总任务超时拖住

## 启动

在 ARL 服务已运行（默认 `http://127.0.0.1:5018`）的前提下：

```powershell
$env:OPENAI_API_KEY = "你的模型密钥"
$env:ARL_TOKEN = "你的ARL Token"
go run -tags webconsole ./cmd/provena serve -config config.v3-minimal.yaml
```

Provena 会通过 stdio 自动拉起 `arl_mcp.server`。如果你已经单独启动 ARL MCP，可将配置中的 `type/command/args/working_dir` 改为 `type: http` 与对应 MCP URL。

旧版仍使用原来的 `config.yaml`，两套配置可并存。
