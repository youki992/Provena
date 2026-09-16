# Provena 本地二次开发说明

## 已完成
- `skills/src-hunter/`：完整导入本地 src-hunter Agent Skill。
- `mcp-servers/dddd/dddd-mcp.exe`：预留 dddd MCP stdio 服务目录。
- `web/templates/index.html` + `web/static/`：新增深色 SOC 工作台界面。

## dddd MCP 配置
```yaml
external_mcp:
  servers:
    dddd:
      type: stdio
      command: mcp-servers/dddd/dddd-mcp.exe
      external_mcp_enable: true
      description: "dddd 资产发现、端口识别、Web 探测、指纹与报告解析"
      timeout: 1800
```

## 本地运行
```powershell
Copy-Item config.example.yaml config.yaml
go run .\cmd\server --config config.yaml
```
