package handler

import "strings"

// piNeedsHarness reports whether the request asks Pi to observe or change the
// world. The Harness does not depend on a role or Skill to make this choice;
// the model selects a concrete enabled tool only after it enters the loop.
func piNeedsHarness(request string) bool {
	request = strings.ToLower(strings.TrimSpace(request))
	if request == "" {
		return false
	}
	// Direct operational language always opts into the world-changing loop.
	// Keep this separate from the broader security vocabulary so explanatory
	// questions do not accidentally start a Harness run.
	for _, marker := range []string{
		"请执行", "帮我执行", "请运行", "帮我运行", "请调用", "帮我调用",
		"执行命令", "运行命令", "调用工具", "使用工具", "读取文件", "查看文件",
		"读取日志", "查看日志", "读取目录", "查看目录", "列出目录", "列出文件",
		"写入文件", "修改文件", "创建文件", "删除文件", "访问网址", "请求接口",
		"发送请求", "下载文件", "抓取页面", "搜索网页", "检查端口", "扫描目标",
		"探测目标", "执行脚本", "运行脚本",
		"terminal", "command", "run ", "read ", "write ", "execute ",
		"curl ", "wget ", "nmap ", "ffuf ", "waybackurls", "sqlmap",
	} {
		if strings.Contains(request, marker) {
			return true
		}
	}

	// A question about a security term is ordinary conversation. This guard is
	// intentionally after explicit operational markers, so requests such as
	// "请运行扫描并解释结果" remain executable.
	for _, marker := range []string{
		"什么是", "是什么意思", "怎么理解", "如何理解", "解释一下", "解释", "介绍一下", "介绍",
		"有什么区别", "区别", "为什么",
	} {
		if strings.Contains(request, marker) {
			return false
		}
	}

	// Security actions are world tasks even when the user does not use the
	// exact phrase "渗透测试". Continuation wording is common in the UI and
	// must enter FGS so Decide can inspect or resume the graph.
	for _, marker := range []string{
		"继续挖掘", "继续测试", "继续验证", "挖洞", "挖漏洞", "挖掘漏洞", "找漏洞", "发现漏洞",
		"漏洞挖掘", "漏洞测试", "漏洞探测", "漏洞验证", "漏洞扫描", "授权测试",
		"渗透", "安全测试", "安全审计", "安全评估", "信息收集", "资产收集", "资产发现", "资产侦察", "资产测绘", "子域名枚举",
		"越权", "业务逻辑", "代码审计", "抓包", "请求包", "shell", "bash", "powershell",
	} {
		if strings.Contains(request, marker) {
			return true
		}
	}
	return false
}
