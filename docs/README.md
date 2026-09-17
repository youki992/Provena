# Provena Documentation

[中文](#中文文档) | [English](#english-documentation)

Provena is a command-line tool. These documents cover configuring it, running it safely, and
extending it.

## 中文文档

**按目标开始**

- **上手**：[配置参考](zh-CN/configuration.md) → [排错指南](zh-CN/troubleshooting.md)
- **安全使用**：[安全模型](zh-CN/security-model.md) → [安全加固](zh-CN/security-hardening.md)
- **参与开发**：[开发者指南](zh-CN/developer-guide.md) → [测试指南](zh-CN/testing.md) → [贡献规范](zh-CN/contributing-guide.md)

**概念与机制**

- [架构说明](zh-CN/architecture.md)
- [Agent 与角色](zh-CN/agent-and-role-guide.md)
- [Skills 指南](zh-CN/skills-guide.md)
- [工具执行治理](zh-CN/tool-execution-governance.md)
- [人机协同最佳实践](zh-CN/hitl-best-practices.md)
- [Eino 编排与命令行工具](zh-CN/MULTI_AGENT_EINO.md)

**扩展能力**

- [MCP 联邦](zh-CN/mcp-federation.md)
- [知识库](zh-CN/knowledge-base.md)
- [视觉分析](zh-CN/VISION.md)

**命令行专题**

- [无头运行冒烟测试](zh-CN/provena-run-smoke.md)

## English Documentation

**Choose a path**

- **Get running**: [Configuration](en-US/configuration.md) → [Troubleshooting](en-US/troubleshooting.md)
- **Run it safely**: [Security Model](en-US/security-model.md) → [Security Hardening](en-US/security-hardening.md)
- **Contribute code**: [Developer Guide](en-US/developer-guide.md) → [Testing](en-US/testing.md) → [Contributing](en-US/contributing-guide.md)

**Concepts**

- [Architecture](en-US/architecture.md)
- [Agents and Roles](en-US/agent-and-role-guide.md)
- [Skills](en-US/skills-guide.md)
- [Tool Execution Governance](en-US/tool-execution-governance.md)
- [HITL Best Practices](en-US/hitl-best-practices.md)
- [Eino Orchestration and the CLI](en-US/MULTI_AGENT_EINO.md)

**Extending**

- [MCP Federation](en-US/mcp-federation.md)
- [Knowledge Base](en-US/knowledge-base.md)
- [Vision Analysis](en-US/VISION.md)

## Documentation conventions

- Commands assume the repository root unless stated otherwise.
- Examples use placeholders; never commit real credentials or target systems without explicit authorization.
- Runtime behavior and configuration defaults are authoritative in `config.example.yaml` and the source code. If a document differs, report it as documentation drift.
