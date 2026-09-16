# Provena Windows x64 开箱即用版

## 启动

1. 将压缩包完整解压到可写目录（不要在压缩包内运行）。
2. 双击 `START.bat`，等待终端显示 `ONLINE`。
3. 浏览器打开 [http://127.0.0.1:8080/](http://127.0.0.1:8080/)。
4. 首次启动会显示一次性的管理员密码，请立即保存并登录修改。
5. 在「系统设置」填写自己的 AI API Key、Base URL 和模型。

## 已随包提供

- `Provena.exe`、完整 Web 界面，以及便携 Node.js 22 / Pi Agent 0.73.1 运行时。
- 默认 Agents、Roles、Skills、知识库和工具定义。
- Windows 版 Amass、dddd（含 Nuclei/规则库）、FFUF、GAU、Katana、Subfinder、WIHscan、VScanPlus。
- `requirements.txt`，供需要 Python 的可选工具安装依赖时使用。

## 注意事项

- 包内默认使用明文 HTTP 8080，双击 `Provena.exe` 也可启动；推荐始终使用 `START.bat`，它会先检查内置运行时。
- AI 对话固定由 Pi Agent 执行。Node.js 和 Pi Agent 已随包提供，其他使用人无需安装 Node、npm、Go 或 Pi。
- 需要用户自行准备并填写可用的 AI API Key；配置中的示例 Key 只是占位符。
- 项目黑板默认启用；如果曾经使用旧配置，请确认 `project.enabled: true`。
- 抓包代理默认端口为 `9081`，需在平台设置中启用后使用；Web 管理端口为 `8080`。
- 便携包不含用户数据、聊天附件、日志或真实 API Key；这些内容保存在本地目录。
- 核心 Web 平台无需 Go、Node 或 Pi 安装。部分可选安全工具仍可能依赖 Python 3，缺失时会在执行对应工具时提示。
- 请只在自有或得到明确授权的目标上使用安全测试能力。
- 升级前请备份本目录下的 `config.yaml`、`data/` 及自行新增的工具/技能文件。
