# 排错指南

本文按现象列出常见问题。优先查看终端输出、`data/runs/<id>/` 下的报告，以及 `provena doctor` 的检查结果。

## 模型无响应

检查：

- 当前对话选择的 AI 通道是否存在；为空时会使用 `ai.default_channel`。
- `ai.channels.<id>.base_url` 是否包含正确路径，如 `/v1`。
- `ai.channels.<id>.api_key` 是否有效。
- `ai.channels.<id>.model` 是否存在。
- 服务商是否支持当前通道的 `reasoning` 字段。

可在系统设置中使用模型测试。若网关报 400，先尝试：

```yaml
ai:
  channels:
    your-channel:
      reasoning:
        mode: off
```

## 工具执行失败

检查：

- 工具命令是否已安装到 PATH。
- `tools/*.yaml` 参数 schema 是否正确。
- 是否被 HITL 拒绝。
- 是否超过 `agent.tool_timeout_minutes`。
- Shell 长时间无输出是否触发 `shell_no_output_timeout_seconds`。

工具配置可参考 `tools/README.md`。

## MCP 连不上

内置 MCP：

- 检查 `mcp.enabled`。
- 检查 `mcp.port`。
- 检查 `auth_header` 和 `auth_header_value`。

外部 MCP：

- stdio：检查命令路径、工作目录、环境变量。

## 知识库不可用

检查：

- `knowledge.enabled: true`。
- embedding 配置是否正确。
- 是否已经扫描并重建索引。
- `data/knowledge.db` 是否可写。
- 嵌入服务是否 429 或超时。

如果索引大量失败，降低：

```yaml
knowledge:
  indexing:
    batch_size: 5
    rate_limit_delay_ms: 600
```

## 数据库锁或写入失败

检查：

- `data/` 是否可写。
- 是否多个实例共用同一个 SQLite 文件。
- 磁盘是否满。
- 是否异常复制了 WAL/SHM 文件。

生产环境不要让多个进程同时写同一份 SQLite 数据库。

## 诊断顺序

遇到问题时不要直接改配置，先定位层级：

1. 进程：服务是否还在，日志是否有 panic。
2. 网络：本机能否访问该目标。
5. 模型：模型测试是否通过。
6. 工具：工具列表和单个 schema 是否正常。
7. 数据库：`data/` 是否可写，有无锁。

先定位层级，再改参数。否则容易把一个代理问题误判成模型问题。

## 最小诊断命令

```bash
# 进程和端口
lsof -i :8080

# 本机 HTTPS 是否通
curl -k -I https://127.0.0.1:8080/

# 静态资源是否通
curl -k -I https://127.0.0.1:8080/static/logo.png

# 查看数据库文件
ls -lh data/
```

如果经过 Nginx，再分别测代理地址和回源地址，确认问题在哪一层。

## 常见误判

- “模型坏了”：实际是 HITL 挂起等待审批。
- “工具没加载”：实际是 tool_search 隐藏了大部分工具。
- “知识库没效果”：实际是索引没重建或 risk_type 过滤过窄。
- “配置保存了但没生效”：实际是监听端口/TLS 需要重启。

## 故障报告模板

提交问题时建议附：

```text
版本/提交：
运行方式：
目标与授权范围：
相关配置段：
复现步骤：
预期结果：
实际结果：
终端输出：
报告文件：
```

有了这些信息，定位速度通常会快很多。
