# provena 真实靶机冒烟（run / chat）

`provena run` 是无头路径：不启浏览器、不启抓包代理、不启 C2，复用同一套智能体核心
（同一套工具注册表、MCP 接线和 RBAC 记录）。本文是
"对已授权真实靶机跑一次端到端冒烟"的标准操作流程。

同一条内核还有对话形态 `provena chat`（多轮上下文、可打断、可恢复），
见「3.5 交互式会话」。

**前提：目标必须是你已获得明确书面授权的机器。** 不要拿生产目标试新配置。

## 0. 编译

```bash
cd Provena
go build -o provena.exe ./cmd/provena      # Windows
go build -o provena ./cmd/provena          # Linux
```

编译通过只说明入口与依赖没问题，不代表功能正确。

### 只有这一种构建

Web 控制台已随 `web/`、`run.sh`、`upgrade.sh`、`deploy/` 一起从本仓库移除，`-tags webconsole`
不再可用：

```bash
go build -o provena.exe ./cmd/provena
```

产物里没有控制台：`provena serve` 会直接报错，`internal/app/routes.go` 那张 HTTP 路由表也已删除，
只剩同签名的空实现（`internal/app/routes_stub.go`），因此没有任何路由被注册。裸参数
（`provena --https`）走同一条兼容分支，得到同样的报错。

注意 `internal/handler` 等包仍会被编译进去，因为共享内核（`app.New`）构造了它们；被移除的是
「命令 + 路由表」这一层。

`run` / `doctor` / `init` / `config` / `version` 不受影响。

## 1. 预检：doctor

```bash
./provena.exe doctor -config config.yaml
```

期望 `0 failure(s)`。重点关注三类失败：

| 输出 | 含义 | 处理 |
| --- | --- | --- |
| `api_key is empty` | `api_key: ${PROVENA_API_KEY}` 展开后为空 | 设置环境变量，或把 key 直接写进 `config.yaml` |
| `api_key still contains an unexpanded ${VAR}` | 变量名写错或拼接异常 | 检查 `${}` 内的变量名拼写 |
| `pi runtime "pi" not found on PATH` | Pi 运行时缺失 | 安装 `pi`，或设 `pi_agent.command` 为绝对路径 |

`warn` 一般可以放行：`database ... does not exist yet` 属于首次运行正常现象。

顺带确认 RBAC 主体真的存在（`-as` 默认 `admin`，缺失会在真实 run 跑到第 3 步
才报错，白等一轮模型调用）：

```bash
python -c "
import sqlite3
con=sqlite3.connect('file:data/conversations.db?mode=ro',uri=True)
print(con.execute('select id,username,enabled from rbac_users').fetchall())
print(con.execute('select * from rbac_user_roles').fetchall())
"
```

需要看到目标用户 `enabled=1`，且 `rbac_user_roles` 里有对应角色。

### 工具链依赖（真实 run 前必须确认）

`provena run` 会把 `tools/*.yaml` 里的**所有**世界工具交给模型，包括需要 Python 的
工具配方。这些配方写的是 `command: "python"`，解析的是 **PATH 上第一个 python**，
不一定是 doctor 报出来的那个。本机上就有三个：

```text
C:\Users\13740\.workbuddy\binaries\python\versions\3.13.12\python.exe   # workbuddy 托管
C:\ProgramData\anaconda3\python.exe                                     # 系统
C:\Users\13740\AppData\Local\Programs\Python\Python39\python.exe
```

不同 shell 的 PATH 顺序不同 → 同一个命令在 A 终端能跑、B 终端报缺依赖。先确认：

```bash
which -a python python3
python -c "import sys; print(sys.executable)"
python -c "import httpx" || echo "httpx 缺失"
```

关键依赖（`tools/http-framework-test.yaml` 等）：

| 依赖 | 用途 |
| --- | --- |
| `httpx` | `http-framework-test` 的 HTTP 引擎，缺失时工具直接 `exit status 1` |
| `requests` | 部分脚本类配方的备用 HTTP 客户端 |

缺失时模型通常会自己调 `install-python-package` 装（会写进上面那个 python 的环境，
不是临时目录）。想避免 run 里浪费一轮，就提前装好：

```bash
python -m pip install httpx requests
```

**Windows 中文环境的已知观感问题**：`exec` 走 PortableGit 的 `sh.exe`，命令输出的
GBK 中文（例如 `ping`）不做转码，回显是乱码。不是 run 的故障，判读时别当成错误。

## 2. dry-run：先验接线，不调模型

```bash
./provena.exe run -config config.yaml \
  -t http://127.0.0.1:3000 \
  --objective "check for common web issues" \
  --max-activities 3 \
  --dry-run
```

`--dry-run` 会加载配置、初始化应用核心、打开 FGS 图，然后直接返回，**不产生任何
模型调用、不接触目标**。期望输出：

```text
dry run: wiring resolved successfully
  channel     : default
  model       : deepseek-v4-flash
  base_url    : https://api.openlux.ai/v1
  pi command  : pi
  tools       : 97 bridge tool(s)
  graph       : data\runs\<run-id>\graph.jsonl
  activities  : up to 3
```

`tools` 数量为 0 或明显偏少，说明 `tools_dir` 没被正确加载，先别往下走。

> dry-run 仍要求 `api_key` 非空（只是不做真实调用），所以先用占位值验证接线：
> `PROVENA_API_KEY=dry-run-placeholder ./provena.exe run ... --dry-run`

## 3. 真实 run

```bash
export PROVENA_API_KEY="<你的密钥>"

./provena.exe run -config config.yaml \
  -t <靶机地址> \
  --objective "<这次要验证什么>" \
  --scope "<授权范围，逗号分隔；省略则等于 -t>" \
  --max-activities 6 \
  --format sarif \
  -as admin
```

关键参数：

- `-t` / `--target`：URL、host 或 host:port。必填。
- `--objective`：不写则退化为"对该目标做低影响安全测试，找出有证据支持的问题"。
  冒烟时建议写具体一点，收敛更快，也更容易判断结果是否合理。
- `--scope`：会作为 `scope` 事实节点种进图里，并渲染进每轮提示词。**留空时只授权
  `-t` 本身**，所以要测多个域名就显式列出。
- `--max-activities`：模型活动轮数上限，默认 6。
- `--activity-timeout <秒>`：单轮**空闲窗口**，默认取 `pi_agent.activity_timeout_seconds`
  （180）。语义见下。
- `--activity-max-timeout <秒>`：单轮**硬顶**，可选。不设则不限。
- `--format`：`md`（默认）/ `json` / `sarif`。只影响 stdout 打印什么；
  三种报告文件都会落盘。
- `-as`：用哪个平台用户的 RBAC 权限跑，默认 `admin`。
- `-v`：额外打印工具返回内容（默认只打印工具名）。报告不对劲时加它重跑。
- `--plain`：退回逐行输出（不流式、不画面板、不着色），给管道、日志和 CI 用。
- `--thinking`：流式打印模型推理块，默认开。`--thinking=false` 关掉。
- `--graph`：FGS 图打印时机，`off` / `activity`（默认，每轮结束）/ `live`（每次图变更）。

### 终端交互（CLI 模式）

默认输出是给终端看的，不是给日志看的：推理过程、助手正文、工具调用分三条 gutter 流式输出，
每轮结束打印耗时与工具调用数，FGS 图直接画在终端里。

```text
Provena run  ·  http://203.0.113.10:8080/
  objective  对 DVWA 靶场做低影响安全测试
  activities  up to 3

▌ activity 1/3
  · The user wants me to perform a security assessment on a DVWA target...
  → fgs_read
  · The FGS is empty of steps - we need to start from scratch.
  → http-framework-test  url=http://203.0.113.10:8080/
  │ The target is confirmed: DVWA v1.10 running on Apache/2.4.25.
  ✓ 42s · 14 tool call(s)

╭─ FGS v24 · 9 node(s) · 12 edge(s) · activity
│ ○ step-4            Test SQL Injection (low)
│ ✓ step-1            Initial Reconnaissance
│ ◆ fact-6b99e5715a0… DVWA Target Confirmed
╰─
```

gutter 的含义：

| gutter | 内容 | 来源事件 |
| --- | --- | --- |
| `·` | 模型推理 | `message_update` → `thinking_delta` |
| `│` | 助手正文 | `message_update` → `text_delta` |
| `→` | 工具调用（含关键参数摘要） | `tool_execution_start` |
| `←` | 工具结果（需 `-v`） | `tool_execution_end` |

图例：`●` 进行中、`○` 待办、`✓` 完成、`!` 阻塞、`✗` 放弃、`◆` 事实、`⚑` 发现。
`origin` / `goal` 这类结构性节点不打印；节点按「进行中的 step → 其余 step → finding → fact」排序。

**打断执行**：第一次 `Ctrl+C` 立刻中止当前轮（杀 Pi 进程树、关管道），已写入图的证据保留，
run 以 `status=cancelled` 结束并仍然产出报告；再按一次直接 `exit 130` 强退。

进程树回收在 Windows 上走 **Job Object**（`internal/security.ProcessTree`，
`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`）。`taskkill /F /T` 依赖父子链，中间进程一退出、
孙进程被内核重新挂载后就再也枚举不到；Job 是成员制，进程一旦加入，它之后派生的子孙都跑不掉，
句柄关闭时还会做最后一次清扫 —— 所以上一轮遗留的后台扫描器不会被带进下一轮。
Unix 侧本来就是独立会话 + `kill(-pgid)`，不需要额外机制。

`--plain` 下上述全部退化为逐行输出：

```text
activity 1/3...
  -> fgs_read
graph v12 nodes=9 edges=12 trigger=activity
  [completed] step-1 Initial Reconnaissance
```

排查 Pi 侧问题时可以打开 `PROVENA_PI_DEBUG=1`，它会把实际启动参数、退出码、
读到的行数和 Pi 的 stderr 打到 stderr：

```bash
PROVENA_PI_DEBUG=1 ./provena.exe run -t <目标> --max-activities 1
```

一个典型用途：模型配置指向了中转站不支持的模型时，Pi 会「干净地」跑完一轮却什么都没做。
调试输出里的 `--model` 一行能立刻暴露这种配置漂移。

### 单轮时间是怎么被限制的

轮次之间是**无状态**的，FGS 图是唯一外置状态。单轮由**空闲窗口**约束，不是固定墙钟：

- 每一轮至少能跑满 `activity_timeout` 秒（保底）；
- 之后每收到一个 Pi 事件，就把截止时间往后推 `activity_timeout` 秒。

所以**只有"没有产出"的轮次才会被砍** —— 一个还在持续调用工具、持续产出证据的轮次可以一直跑下去。
这是"卡死检测"而不是"限时"，快和深度不必二选一。

需要可预测的总成本时再加硬顶：

```bash
# 快速冒烟：45 秒没动静就砍，且单轮绝不超过 2 分钟
provena run -t <目标> --max-activities 2 --activity-timeout 45 --activity-max-timeout 120

# 深度评估：只在卡死时收手，单轮不限时
provena run -t <目标> --max-activities 8 --activity-timeout 180
```

超时是**真的把这一轮砍断**：Pi 进程树在到期瞬间被杀，该轮已写入图的 step 停在未完成状态，
run 继续下一轮。日志里能区分两种原因：

- `activity stalled: no Pi event for 180s` —— 空闲窗口触发，模型或工具卡住了；
- `activity hit the 120s ceiling` —— 硬顶触发，轮次还在干活但预算用完了。


## 3.5 交互式会话：`provena chat`

`provena run` 是"一轮接一轮、无状态"的批处理；`provena chat` 是它的对话形态 ——
一个常驻的 Pi 进程，多轮之间**保留上下文**，可以来回追问，接近 Codex CLI / Claude Code
的使用手感。

```bash
./provena.exe chat -t http://10.0.0.5:8080/ --objective "对授权目标做低影响测试"

# 目标可选：不给就是自由问答，仍然能用全部工具
./provena.exe chat

# 接着上次继续（同一 -sessions 目录下最近的一次会话）
./provena.exe chat --continue
```

| 参数 | 说明 |
|---|---|
| `-t` / `-target` | 授权目标，可省略；只影响系统提示里写入的目标与范围 |
| `--objective` / `--scope` | 本次会话的目的与授权范围 |
| `-sessions` | 会话状态目录，默认 `data/sessions`，每次会话一个子目录 |
| `--continue` | 恢复该目录下最近一次会话，而不是新开 |
| `-as` | 用哪个平台用户的 RBAC 权限跑，默认 `admin` |
| `--plain` / `-v` / `--thinking` / `--graph` | 与 `run` 同义 |

会话内的本地命令：

| 命令 | 作用 |
|---|---|
| `/new` | 清空对话（同一个进程），并把 FGS 图轮换到新文件 |
| `/graph` | 打印当前 FGS 图 |
| `/info` | 显示会话 id、消息数、Pi 的 session 文件 |
| `/help` | 列出这些命令 |
| `/exit` | 退出（也可用 Ctrl+D / 管道 EOF） |

**打断是两段式**：轮次进行中第一次 `Ctrl+C` 中止当前轮（对话与已写入图的证据都保留），
第二次才退出；停在输入提示符时第一次按只提示，避免误触退出。

### 会话状态长什么样

```text
data/sessions/20260916-141743-003afe03/
├── graph.jsonl                    # FGS 图（只追加）
└── pi/                            # Pi 自己的 session 文件，--continue 靠它恢复上下文
```

`/new` 会把图轮换成 `graph-<时间戳>.jsonl`，旧的留在原地不删 —— 便于回溯。

### 30 秒验证（不用真终端也能测）

`chat` 读 stdin 逐行处理，所以可以直接管道喂进去，用来确认多轮上下文与 `/new` 是否正常：

```bash
printf '记住口令 PINEAPPLE-7743，只回复“已记住”。\n口令是什么？只回复口令本身。\n/new\n口令是什么？不记得就说不记得。\n/exit\n' \
  | ./provena.exe chat --graph off
```

期望：第二轮能复述出口令（上下文生效），`/new` 之后第三轮说不记得（会话真的被清空）。

## 4. 判读产物

每次 run 写到 `data/runs/<run-id>/`：

| 文件 | 内容 |
| --- | --- |
| `graph.jsonl` | 只追加的 Fact/Intent 图，每行一次 mutation，带 `seq` 与时间戳 |
| `report.md` | 人读报告 |
| `report.json` | 机器可读 |
| `report.sarif` | SARIF 2.1.0，可直接喂给代码扫描平台 |

stdout 结尾的 `status` 语义：

| status | 含义 | 冒烟判定 |
| --- | --- | --- |
| `dry_run` | 只验了接线 | 接线通过 |
| `goal_complete` | 模型输出了 `GOAL_COMPLETE` | ✅ 最理想 |
| `no_progress` | 连续 2 轮图的 version 没变（卡住了） | ⚠️ 链路通但收敛有问题 |
| `max_activities` | 轮数用尽仍未收敛 | ⚠️ 正常兜底，可加轮次重跑 |
| `cancelled` | 收到 SIGINT/SIGTERM | 人为中断 |

**冒烟通过的最低标准**：`graph.jsonl` 里出现 `submit_fact` / `submit_finding` 产生的
节点（说明桥接工具真的被模型调用了），且 `report.md` 不是空壳。

注意两个 headless 的刻意行为，别误判为 bug：

- `packet_capture.enabled` 和 `c2.enabled` 在 run 里被**强制关闭**，所以不会产生抓包
  文件、不会起监听。
- 每轮提示词里含有固定的输出契约与"只执行低影响、可逆操作"的约束，模型不会跑破坏性动作。

### 空轮次：`✓ Ns · 0 tool call(s)`

最容易被误读的一种失败：**exit code 0、status 正常、每轮都说成功**，但图版本不变、
一条证据都没有。这一般不是目标的问题，而是模型通道的问题，终端会直接告诉你：

```text
▌ activity 1/1
  · model error: 403 status code (no body)
  ✓ 6s · 0 tool call(s)
    no tool calls — check the model channel (quota, model id, base_url)
```

`· model error:` 那一行来自 Pi 终止事件里的 `stopReason: "error"` + `errorMessage`。
常见成因：中转站余额不足、模型 id 该网关不支持、key 失效、base_url 少了 `/v1`。

想看更细的链路就打开 `PROVENA_PI_DEBUG=1`，它除了参数、退出码和 stderr，还会打事件普查：

```text
[pi-debug] events=map[agent_end:1 message_end:2 response:1 turn_end:1 ...]
[pi-debug] deltas=map[]
```

`deltas` 为空说明模型一个流式增量都没产生；`events` 里没有 `tool_execution_*`
说明本轮压根没执行工具。两个信号合起来就能区分「模型没答」和「答了但没干活」。

## 5. 失败分诊

按报错出现的阶段定位：

| 阶段报错 | 原因 |
| --- | --- |
| `no usable AI channel; configure ai.default_channel and ai.channels` | `default_channel` 指向的 channel 不存在，或 channels 为空 |
| `AI channel "x" is incomplete: api_key and model are required` | key 展开为空，或 `model` 没填 |
| `cannot load user "admin"` / `user "admin" is disabled` | `rbac_users` 里没有该用户或 `enabled=0` |
| `cannot resolve RBAC access for "admin"` | 用户存在但没分配角色 |
| `cannot open FGS graph` | `--out` 目录不可写 |
| `activity stalled: no Pi event for Ns` | 空闲窗口触发，该轮卡住了。**不是致命错误**，图保留已记录内容，下一轮换方法继续 |
| `activity hit the Ns ceiling` | 硬顶触发。轮次还在干活但预算用完 —— 想让它跑完就调大或去掉 `--activity-max-timeout` |
| `activity N failed: ...` | 单轮失败。**不是致命错误**，图会保留已记录内容，下一轮换方法继续 |
| 卡在 `activity 1/6...` 很久 | 单轮超时（默认 180 秒）未触发前一直在等模型；检查 `base_url` 连通性 |

工具层报错（打印成 `工具调用失败` / `工具名称: xxx`，**不计入** run 的 status，
所以必须自己看日志）：

| 工具层报错 | 原因 |
| --- | --- |
| `tool authorization denied: missing authenticated principal` | 桥接没把 run 主体带进工具调用上下文。全部世界工具都会这样失败，只剩 FGS 工具和 Pi 原生文件工具能用。已在 `internal/run/bridge.go` 修复，`TestBridgeAttachesPrincipalToToolCalls` 是回归测试 |
| `Missing dependency: httpx` | 见上文「工具链依赖」，`python` 环境缺包 |
| 中文输出乱码 | `exec` 的 GBK 未转码，观感问题 |
| `命令执行失败: exit status 28` | curl 连接超时。先手工 `curl` 一次确认目标可达，再判断是否为网络抖动 |

每轮失败都会打 `activity N failed: <err>` 并继续，最终仍会写报告 —— 所以"有报告"
不等于"跑通了"，必须结合 `status` 和 `graph.jsonl` 一起看。

## 6. 清理

```bash
# 冒烟产物按需保留或删除，run-id 见 stdout
rm -rf data/runs/<run-id>

# run 会在 conversations.db 里建一条 "provena run: <target>" 会话
# 以及对应的漏洞/资产记录，视需要清理
```

不要在开发库里堆积冒烟数据；真实评估结果另存。

## 源码锚点

- `cmd/provena/run.go` — 命令行参数与摘要输出
- `internal/run/run.go` — 活动循环、状态机、报告落盘
- `internal/run/prompt.go` — 单轮提示词与输出契约
- `internal/run/tools.go` / `bridge.go` — FGS 工具与 Pi 桥接
- `cmd/provena/doctor.go` — 预检项清单
