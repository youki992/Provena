<div align="center">

# Provena

**Evidence-driven security testing agent · CLI**

Turns one natural-language objective into a bounded, auditable test against an authorized target

[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/dl/)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux-0078D6?style=flat-square&logo=windows&logoColor=white)](#requirements)
[![Version](https://img.shields.io/badge/version-v0.1.0-2ea44f?style=flat-square)](https://github.com/youki992/Provena/releases)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square)](LICENSE)

[中文](README.md) · [English](README_EN.md)

<img src="images/chat.jpg" alt="A provena chat session: session banner, the /help command list, and the FGS graph printed by /graph" width="100%">

</div>

Provena turns a natural-language objective into a bounded, auditable security test against one authorized target.

A run drives the Pi harness through a fact/intent graph: the model decides what to do next, Provena executes the tools, and every piece of evidence is recorded in an append-only FGS graph that is replayed into the report.

It is written in Go and ships as a single binary. Tools are declared as YAML recipes and reached over MCP, so the same agent can call a local scanner, a remote MCP server or one of Provena's built-in helpers.

> [!IMPORTANT]
> This repository is the command-line tool: `chat`, `run`, `doctor`, `init`, `config` and `version`.
> Use it only on systems you own or are explicitly authorized to test — see [SECURITY.md](SECURITY.md).

---

## Highlights

| | Capability | What it means |
| :---: | --- | --- |
| 🧠 | **Pi harness driven** | The model only decides; Provena executes. Every turn is replayable |
| 🕸️ | **Append-only FGS graph** | Observations land in a fact/intent graph, claims must carry evidence, and self-corrections leave a trail |
| 🧰 | **100+ tool recipes** | Declared in YAML, called over MCP; a local scanner and a remote MCP server look the same |
| 📄 | **Three report formats** | `md` / `json` / `sarif` — SARIF drops straight into CI |
| 🙋 | **HITL approval** | High-risk actions are approved first, by a human or by the audit agent |
| 📦 | **One binary** | Windows / Linux, no server, no console |

The lifecycle of one run:

```text
provena run -t <target>
      │
      ├─► model decides ──► tool runs ──► fact recorded ──► FGS graph updated
      │        ▲                                                  │
      │        └──────────────── replayed into the next turn ◄────┘
      │
      └─► data/runs/<run-id>/   graph.jsonl + report.{md,json,sarif}
```

---

## Requirements

| | |
| --- | --- |
| **OS** | 64-bit Windows or Linux. The bundled-scanner launcher (`tools/bundled_tool.py`) has no macOS path. |
| **Go** | 1.25 or newer (see `go.mod`) |
| **Node.js + npm** | to install and run Pi (next row) |
| **Pi** | the `pi` CLI on `PATH`. Install it with `npm install -g @mariozechner/pi-coding-agent`. Provena drives it as the agent runtime (`pi --mode rpc`); set `pi_agent.command` to point at a different executable. |
| **Model** | any OpenAI-compatible chat endpoint. Provena hands the selected channel's `provider`, `base_url`, `api_key` and `model` to Pi, so configure it under `ai.channels`. |
| **Python** | 3.10 or newer |

`provena doctor` checks the config, the AI channel, the pi runtime, python, the tools/skills/agents directories and every configured MCP server before a run does any work.

---

## Build

```bash
git clone https://github.com/youki992/Provena.git
cd Provena

go build -o provena ./cmd/provena        # Linux
go build -o provena.exe ./cmd/provena    # Windows
```

The current version is **v0.1.0**, the default compiled into `cmd/provena/main.go` and mirrored by the `version` field in `config.example.yaml`. Confirm the build:

```bash
./provena version     # provena v0.1.0
./provena help        # command list; there is no serve command
```

---

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

> [!TIP]
> `provena doctor` and `provena run --dry-run` resolve the whole wiring without calling the model. Use them before spending tokens.

---

## Commands

| Command | Purpose |
| --- | --- |
| `provena chat` | Interactive multi-turn session; interruptible and resumable |
| `provena run` | Bounded command-line test against one target |
| `provena doctor` | Check config, model credentials, pi, python and MCP servers |
| `provena init` | Create `config.yaml` from the bundled example |
| `provena config validate` | Validate the configuration file |
| `provena version` | Print the version |

Every command accepts `-config <path>` (default `config.yaml`).

---

## Interactive sessions

`provena chat` keeps a single Pi process alive for the whole conversation, so the model sees the previous turns, and the session can be interrupted and resumed:

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

The first <kbd>Ctrl</kbd>+<kbd>C</kbd> aborts the running turn and keeps the conversation; the second leaves. State lives in `data/sessions/<id>/`.

---

## Command-line runs

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

| File | Contents |
| --- | --- |
| `graph.jsonl` | The append-only Fact/Intent Graph (the run's durable state) |
| `report.md` · `report.json` · `report.sarif` | Findings, evidence and severity |

> [!WARNING]
> Findings recorded here are evidence-backed observations, **not automatically confirmed vulnerabilities**. Verify them before acting.

---

## Tools

### Nothing is bundled — download the scanners yourself

This repository ships **no third-party scanner binaries and no `tools/bin` directory**. Download the tools you want from their own upstream release pages and put them where Provena looks for them.

Tool behaviour, the YAML schema and how to add your own are documented in [tools/README.md](tools/README.md) (Chinese) and [tools/README_EN.md](tools/README_EN.md) (English).

<details>
<summary><b>Bundled scanners — <code>tools/bin/&lt;tool&gt;/&lt;platform&gt;/&lt;tool&gt;[.exe]</code></b></summary>

<br>

These tools are launched through [`tools/bundled_tool.py`](tools/bundled_tool.py), which resolves the executable **inside the repository** rather than from `PATH`. Place each binary at:

```
tools/bin/<tool>/<platform>/<tool>[.exe]
```

where `<platform>` is `windows-amd64` on Windows and `linux-amd64` on Linux.

| Tool | Definition | Expected path | Upstream |
| --- | --- | --- | --- |
| `amass` | `tools/amass.yaml` | `tools/bin/amass/<platform>/amass[.exe]` | `owasp-amass/amass` |
| `subfinder` | `tools/subfinder.yaml` | `tools/bin/subfinder/<platform>/subfinder[.exe]` | `projectdiscovery/subfinder` |
| `ffuf` | `tools/ffuf.yaml` | `tools/bin/ffuf/<platform>/ffuf[.exe]` | `ffuf/ffuf` |
| `gau` | `tools/gau.yaml` | `tools/bin/gau/<platform>/gau[.exe]` | `lc/gau` |
| `katana` | `tools/katana.yaml` | `tools/bin/katana/<platform>/katana[.exe]` | `projectdiscovery/katana` |
| `waybackurls` | `tools/waybackurls.yaml` | `tools/bin/waybackurls/<platform>/waybackurls[.exe]` | `tomnomnom/waybackurls` |
| `nmap` | `tools/nmap.yaml` | see below | install it |

The first six are ordinary open-source releases: grab the archive for your platform from the project's release page and copy the executable to the path above. For example, on Linux:

```bash
mkdir -p tools/bin/ffuf/linux-amd64
# unpack the ffuf release archive, then
cp ffuf tools/bin/ffuf/linux-amd64/ffuf
chmod +x tools/bin/ffuf/linux-amd64/ffuf
```

The one exception:

- **`nmap` is not something you drop in.** On Windows, install Nmap normally: the launcher checks `%ProgramFiles%\Nmap\nmap.exe` and `%ProgramFiles(x86)%\Nmap\nmap.exe` before `tools/bin/nmap/windows-amd64/nmap.exe`. On Linux the launcher only looks at `tools/bin/nmap/linux-amd64/nmap`, so either copy the binary there or point the recipe at your system install — set `command: "nmap"` and drop the `tools/bundled_tool.py` entries from `args` in `tools/nmap.yaml`.

Resolution order and exceptions:

- **Missing binary** — the run does not fail. The tool reports every path it checked and the agent continues without it.

</details>

<details>
<summary><b>Python-backed tools</b></summary>

<br>

Several recipes call `python` / `python3` and are resolved against `PATH` (with `py` as a Windows fallback). Create a virtual environment and install the shared dependencies:

```bash
python -m venv venv
source venv/bin/activate          # Windows: venv\Scripts\activate
pip install -r requirements.txt
```

Tools such as `http-framework-test`, `api-fuzzer` and the ARL recipes expect this environment to be active.

</details>

<details>
<summary><b>Tools that expect a globally installed binary</b></summary>

<br>

Most of the 100+ recipes in `tools/` call a command by name and expect it on `PATH` — nmap, nikto, sqlmap, nuclei, gobuster, hydra, hashcat and so on. Install them with your package manager or from upstream:

```bash
# Linux (Kali / Debian / Ubuntu)
sudo apt install -y nmap sqlmap nikto gobuster hydra hashcat john binwalk
```

On Windows there is no single package-manager line that covers them: install each tool from its own project, usually a signed installer or a release archive you unpack somewhere on `PATH`.

Missing tools are skipped at runtime rather than failing the run.

</details>

---

## Configuration

[`config.example.yaml`](config.example.yaml) is the authoritative template. At minimum, configure one AI channel as shown in [Quick start](#quick-start).

`openai` is a backward-compatible runtime field; maintain new model settings under `ai.channels`. [`config.example.yaml`](config.example.yaml) is the authoritative reference and every section is commented.

---

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
├── images/          # README preview image
├── config.example.yaml
└── SECURITY.md
```

Directories you populate yourself and that are not part of the repository: `tools/bin/` (scanner binaries), `skills/` (Agent Skills), `mcp-servers/` (external MCP servers), `data/` (run state, database, sessions) and `config.yaml`.

---

## Documentation

| Document | Contents |
| --- | --- |
| [Troubleshooting](docs/en-US/troubleshooting.md) | Common problems by symptom |
| [MCP federation](docs/en-US/mcp-federation.md) | Built-in MCP, external MCP and tool naming |
| [Knowledge base](docs/en-US/knowledge-base.md) | Retrieval, rerank and content authoring |
| [Skills guide](docs/en-US/skills-guide.md) | SKILL.md structure and progressive disclosure |
| [Bilingual documentation index](docs/README.md) | Entry point for all documents |

The command-line smoke test guide is currently Chinese only: [`docs/zh-CN/provena-run-smoke.md`](docs/zh-CN/provena-run-smoke.md).

---

## License

Provena is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

## Disclaimer

**This tool is for educational and authorized testing purposes only.**

Provena is a professional security testing tool designed to help security researchers, penetration testers and IT professionals conduct security assessments and vulnerability research **with explicit authorization**.

**By using this tool, you agree to:**

- Use it only on systems for which you hold clear written authorization
- Comply with all applicable laws, regulations and ethical standards
- Take full responsibility for any unauthorized use or misuse
- Never use it for illegal or malicious purposes

**The developers are not responsible for any misuse.** Ensure your usage complies with local law and that you have obtained explicit authorization from the target system owner.

For vulnerability reporting and hardening guidance, see [SECURITY.md](SECURITY.md).
