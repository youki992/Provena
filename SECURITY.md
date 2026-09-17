# Security Policy

[中文](#安全政策) | [English](#security-policy)

## Security Policy

Provena is a command-line security testing agent. It runs tools on your machine, calls MCP
servers, and drives an external `pi` runtime that talks to your model provider. Treat it as a
high-privilege tool: whatever it can reach, it can act on.

### Supported Versions

This project does not currently maintain multiple long-term support branches. Security fixes
are expected to land on the latest mainline release/source tree.

If you are running an older version, please reproduce the issue against the latest code
before reporting when possible.

### Reporting a Vulnerability

Please do not publicly disclose exploitable details before maintainers have had a reasonable
chance to investigate.

Preferred report contents:

- affected version or commit;
- how you built and ran it, plus the relevant configuration;
- clear reproduction steps;
- impact assessment;
- affected component, such as tool execution, MCP wiring, the Pi bridge, the FGS graph, the
  report writers, or the Python tool recipes;
- whether the issue needs a model response to trigger;
- suggested mitigation, if known.

If the project repository has private vulnerability reporting enabled, use that channel.
Otherwise, open a minimal public issue that states there is a security concern and avoid
posting exploit details, credentials, target data, or weaponized payloads.

### Scope

In scope:

- unsafe command execution or argument handling in the built-in tools;
- path traversal or unintended file read/write through tools or Skills;
- external MCP trust-boundary flaws;
- the loopback Pi bridge accepting unauthenticated tool calls;
- credentials or target data leaking into reports, logs or the FGS graph;
- settings that silently disable a documented safety control;
- security-impacting configuration handling bugs.

Out of scope:

- reports against systems you do not own or are not authorized to test;
- denial-of-service testing against public services without permission;
- social engineering, phishing, or credential theft;
- issues caused only by intentionally disabling documented security controls;
- vulnerabilities in third-party tools invoked by Provena, unless Provena makes them
  materially worse.

### Authorized Use Boundary

Provena must only be used for education, research, and authorized security testing. Do not use
it against systems without explicit permission.

High-risk capabilities such as shell execution, payload generation, external MCP tools, and
bulk scanning should only be used in controlled, authorized environments.

### Operational Hardening

Before pointing it at anything real:

- keep `config.yaml` readable only by you — it holds your model credentials;
- review every external MCP server before enabling it;
- keep high-risk tools out of the global HITL allowlist;
- treat `data/runs/<id>/` as sensitive: a report contains target data and tool output;
- never commit `data/`, `config.yaml` or `tools/bin/`;
- install scanner binaries from their upstream releases and check what you downloaded.

See:

- [Security Model](docs/en-US/security-model.md)
- [Security Hardening](docs/en-US/security-hardening.md)

---

# 安全政策

Provena 是一个命令行安全测试智能体。它会在你的机器上执行工具、调用 MCP 服务，并驱动一个
外部 `pi` 运行时与你的模型服务通信。请把它当作高权限工具：它能触达的资源，它就能操作。

## 支持版本

本项目目前不维护多个长期支持分支。安全修复通常会合入最新主线版本或源码树。

如果你运行的是旧版本，建议在报告前尽量用最新代码复现问题。

## 漏洞报告

在维护者有合理时间调查前，请不要公开披露可利用细节。

建议报告内容：

- 受影响版本或 commit；
- 你的构建与运行方式，以及相关配置；
- 清晰复现步骤；
- 影响评估；
- 受影响组件，例如工具执行、MCP 接线、Pi 桥、FGS 图、报告写出或 Python 工具配方；
- 是否需要模型回复才能触发；
- 已知缓解建议。

如果仓库启用了私有漏洞报告，请优先使用该渠道。否则可以提交一个最小公开 Issue，说明存在
安全问题，但不要发布利用细节、凭证、目标数据或武器化载荷。

## 范围

范围内：

- 内置工具中不安全的命令执行或参数处理；
- 通过工具或 Skills 造成的路径穿越或意外读写文件；
- 外部 MCP 信任边界问题；
- 回环 Pi 桥接受了未认证的工具调用；
- 凭证或目标数据泄露进报告、日志或 FGS 图；
- 静默关闭了文档化安全控制的配置项；
- 影响安全的配置处理缺陷。

范围外：

- 针对未授权系统的报告；
- 未经许可的拒绝服务测试；
- 社工、钓鱼或凭证窃取；
- 仅因主动关闭文档化安全控制导致的问题；
- 第三方工具自身漏洞，除非 Provena 明显放大了风险。

## 授权使用边界

Provena 仅可用于教育、研究和授权安全测试。不要在没有明确授权的系统上使用。

Shell 执行、payload 生成、外部 MCP 工具、批量扫描等高风险能力，只应在受控且授权明确的
环境中使用。

## 运行加固

在把它指向真实目标之前：

- `config.yaml` 只让自己可读 —— 里面是模型凭证；
- 启用外部 MCP 前逐个审查；
- 高风险工具不要加入全局 HITL 白名单；
- 把 `data/runs/<id>/` 当作敏感数据：报告里有目标信息和工具输出；
- 不要提交 `data/`、`config.yaml`、`tools/bin/`；
- 扫描器二进制从上游 Release 获取，并核对你下载的东西。

参见：

- [安全模型](docs/zh-CN/security-model.md)
- [安全加固指南](docs/zh-CN/security-hardening.md)
