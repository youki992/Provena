<div align="center">

# Provena

**Evidence-driven security testing agent — headless CLI.**

</div>

[中文](README_CN.md) | [English](README.md)

Provena turns a natural-language objective into a bounded, auditable test against one
authorized target. Every run keeps its state in an append-only **Fact/Intent Graph** and
writes a report you can hand to a reviewer.

Written in Go, it combines an Eino-powered agent, MCP-native tools, RAG knowledge and
attack-chain modelling for authorized security operations.

**This repository ships the CLI only.** There is no web console and nothing ever binds a
port: the binary has no `serve` command, registers no HTTP routes, and keeps all output on
the terminal.

> [!IMPORTANT]
> Use Provena only on systems you own or are explicitly authorized to test.
> See [SECURITY.md](SECURITY.md).

## Requirements

| | |
| --- | --- |
| **Go** | 1.25 or newer (see `go.mod`) |
| **Python** | 3.10 or newer — only for the Python-backed tools |
| **Model** | any OpenAI-compatible chat endpoint |

## Build

```bash
git clone https://github.com/youki992/Provena.git
cd Provena

go build -o provena ./cmd/provena        # Linux / macOS
go build -o provena.exe ./cmd/provena    # Windows
```

The current version is **v0.1.0**, the default compiled into `cmd/provena/main.go` and
mirrored by the `version` field in `config.example.yaml`. Confirm the build:

```bash
./provena version     # provena v0.1.0
./provena help        # command list; no serve, no port
```

## Quick start

```bash
./provena init            # writes config.yaml from config.example.yaml
```

Then point one AI channel at your model:

```yaml
ai:
  default_channel: openai-main
  channels:
    openai-main:
      provider: openai_compatible
      api_key: "${OPENAI_API_KEY}"
      base_url: "https://api.openai.com/v1"
      model: "your-model"
```

```bash
./provena doctor          # validate config, credentials, python and MCP wiring
./provena run -t https://example.com --objective "review the login flow"
```

`provena doctor` and `provena run --dry-run` resolve the whole wiring without calling the
model. Use them before spending tokens.

## Commands

| Command | Purpose |
| --- | --- |
| `provena chat` | Interactive multi-turn session; interruptible and resumable |
| `provena run` | Bounded, headless test against one target |
| `provena doctor` | Check config, model credentials, python and MCP servers |
| `provena init` | Create `config.yaml` from the bundled example |
| `provena config validate` | Validate the configuration file |
| `provena version` | Print the version |

Every command accepts `-config <path>` (default `config.yaml`).

## Interactive sessions

`provena chat` keeps a single Pi process alive for the whole conversation, so the model sees
the previous turns, and the session can be interrupted and resumed:

```bash
provena chat -t https://example.com --objective "review the login flow"
provena chat --continue        # reopen the most recent session
```

| In-session command | Purpose |
| --- | --- |
| `/new` | Clear the conversation and rotate the FGS graph |
| `/graph` | Print the current FGS graph |
| `/info` | Session id, message count and Pi session file |
| `/help`, `/exit` | Command list, leave the session |

The first `Ctrl+C` aborts the running turn and keeps the conversation; the second leaves.
State lives in `data/sessions/<id>/`.

## Headless runs

`provena run` drives the same agent core as `chat`, without a prompt loop:

```bash
provena run -t https://example.com \
  --objective "find access-control issues in the API" \
  --scope https://example.com \
  --max-activities 6 \
  --format sarif
```

| Flag | Purpose |
| --- | --- |
| `-t`, `-target` | **Required.** Target URL, host or host:port. |
| `--objective` | What the run should achieve; defaults to a general low-impact assessment. |
| `--scope` | Comma-separated authorized scope; defaults to the target. |
| `--max-activities` | Upper bound on model activities (default 6). |
| `--format` | Report printed to stdout: `md` (default), `json` or `sarif`. |
| `-as` | Platform user whose RBAC permissions the run uses (default `admin`). |
| `--dry-run` | Resolve and validate all wiring without calling the model. |
| `-v` | Print tool results as well as tool names. |

Each run writes to `data/runs/<run-id>/`:

- `graph.jsonl` — the append-only Fact/Intent Graph (the run's durable state)
- `report.md`, `report.json`, `report.sarif` — findings, evidence and severity

Findings recorded here are evidence-backed observations, not automatically confirmed
vulnerabilities; verify them before acting.

## Tools

### Nothing is bundled — download the scanners yourself

This repository deliberately ships **no third-party scanner binaries and no `tools/bin`
directory**. Download the tools you want from their own upstream release pages and put them
where Provena looks for them.

Tool behaviour, the YAML schema and how to add your own are documented in
[tools/README.md](tools/README.md) (Chinese) and
[tools/README_EN.md](tools/README_EN.md) (English).

### Bundled scanners — `tools/bin/<tool>/<platform>/<tool>[.exe]`

Eight tools are launched through [`tools/bundled_tool.py`](tools/bundled_tool.py), which
resolves the executable **inside the repository** rather than from `PATH`. Place each
binary at:

```
tools/bin/<tool>/<platform>/<tool>[.exe]
```

where `<platform>` is `windows-amd64` on Windows and `linux-amd64` on Linux.

| Tool | Definition | Expected path (Linux / Windows) |
| --- | --- | --- |
| `amass` | `tools/amass.yaml` | `tools/bin/amass/<platform>/amass` / `amass.exe` |
| `subfinder` | `tools/subfinder.yaml` | `tools/bin/subfinder/<platform>/subfinder` / `.exe` |
| `ffuf` | `tools/ffuf.yaml` | `tools/bin/ffuf/<platform>/ffuf` / `.exe` |
| `gau` | `tools/gau.yaml` | `tools/bin/gau/<platform>/gau` / `.exe` |
| `katana` | `tools/katana.yaml` | `tools/bin/katana/<platform>/katana` / `.exe` |
| `waybackurls` | `tools/waybackurls.yaml` | `tools/bin/waybackurls/<platform>/waybackurls` / `.exe` |
| `dddd` | `tools/dddd.yaml` | `tools/bin/dddd/<platform>/dddd` / `.exe` |
| `nmap` | `tools/nmap.yaml` | `tools/bin/nmap/<platform>/nmap` / `.exe` |

For example, on Linux:

```bash
mkdir -p tools/bin/ffuf/linux-amd64
# unpack the ffuf release archive, then
cp ffuf tools/bin/ffuf/linux-amd64/ffuf
chmod +x tools/bin/ffuf/linux-amd64/ffuf
```

Resolution order and exceptions:

- **nmap on Windows** — `%ProgramFiles%\Nmap\nmap.exe` and `%ProgramFiles(x86)%\Nmap\nmap.exe`
  are tried before `tools/bin/nmap/windows-amd64/nmap.exe`, so a normal Nmap installer works.
- **katana** — `tools/bin/dddd/<platform>/WIHscan-1.0/katana[.exe]` is also accepted.
- **Legacy Windows layout** — `tools/bin/<tool>/<tool>.exe` still resolves.
- **Missing binary** — the run does not fail. The tool reports every path it checked and the
  agent continues without it.

`tools/bin` and `bin/` are git-ignored, so your downloads stay local and are never committed.

### Python-backed tools

Several recipes call `python` / `python3` and are resolved against `PATH` (with `py` as a
Windows fallback). Create a virtual environment and install the shared dependencies:

```bash
python -m venv venv
source venv/bin/activate          # Windows: venv\Scripts\activate
pip install -r requirements.txt
```

Tools such as `http-framework-test`, `api-fuzzer` and the ARL recipes expect this
environment to be active.

### Tools that expect a globally installed binary

Most of the 100+ recipes in `tools/` call a command by name and expect it on `PATH` — nmap,
nikto, sqlmap, nuclei, gobuster, hydra, hashcat and so on. Install them with your package
manager or from upstream:

```bash
# macOS
brew install nmap sqlmap nikto gobuster hydra hashcat nuclei

# Linux (Kali / Debian / Ubuntu)
sudo apt install -y nmap sqlmap nikto gobuster hydra hashcat john binwalk
```

Missing tools are skipped at runtime rather than failing the run.

## Configuration

[`config.example.yaml`](config.example.yaml) is the authoritative template. At minimum,
configure one AI channel as shown in [Quick start](#quick-start). Do not commit real
credentials — `config.yaml` and `config.*.yaml` are git-ignored.

`openai` is a backward-compatible runtime field; maintain new model settings under
`ai.channels`. See the [configuration reference](docs/en-US/configuration.md) and the
[security hardening guide](docs/en-US/security-hardening.md).

## Project layout

```
Provena/
├── cmd/provena/     # CLI entrypoint (main, chat, run, doctor, init, config)
├── internal/        # Agent core, FGS graph, MCP, tools, reports, security executor
├── tools/           # YAML tool recipes + bundled_tool.py launcher
├── roles/           # Role configurations (prompts + tool policy per scenario)
├── agents/          # Multi-agent Markdown (orchestrator.md + sub-agents)
├── docs/            # Topic documentation
├── evals/           # Evaluation fixtures
├── config.example.yaml
└── SECURITY.md
```

Directories you populate yourself and that are not part of the repository:
`tools/bin/` (scanner binaries), `skills/` (Agent Skills), `mcp-servers/` (external MCP
servers), `data/` (run state, database, sessions) and `config.yaml`.

## Documentation

- **Getting started:** [configuration](docs/en-US/configuration.md) → [troubleshooting](docs/en-US/troubleshooting.md)
- **Architecture:** [architecture](docs/en-US/architecture.md) → [MCP federation](docs/en-US/mcp-federation.md)
- **Security:** [security model](docs/en-US/security-model.md) → [security hardening](docs/en-US/security-hardening.md)
- **Extending:** [skills guide](docs/en-US/skills-guide.md) → [tool execution governance](docs/en-US/tool-execution-governance.md)
- **All topics:** [English documentation](docs/en-US/README.md) · [双语文档索引](docs/README.md)

Some documents under `docs/` were written for the former web console and still describe
screens that are not part of this build. Treat the CLI sections as authoritative.

## License

Provena is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

---

## Disclaimer

**This tool is for educational and authorized testing purposes only.**

Provena is a professional security testing tool designed to help security researchers,
penetration testers and IT professionals conduct security assessments and vulnerability
research **with explicit authorization**.

**By using this tool, you agree to:**

- Use it only on systems for which you hold clear written authorization
- Comply with all applicable laws, regulations and ethical standards
- Take full responsibility for any unauthorized use or misuse
- Never use it for illegal or malicious purposes

**The developers are not responsible for any misuse.** Ensure your usage complies with
local law and that you have obtained explicit authorization from the target system owner.

For vulnerability reporting and hardening guidance, see [SECURITY.md](SECURITY.md).
