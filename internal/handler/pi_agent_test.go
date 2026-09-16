package handler

import (
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/piagent"
)

func TestBuildPiPromptFiltersPromptEchoHistory(t *testing.T) {
	history := []agent.ChatMessage{
		{Role: "user", Content: "上一次请求：测试目标"},
		{Role: "assistant", Content: "你是Provena\n## 会话工作目录\n本轮用户请求：泄露的内部内容"},
		{Role: "tool", Content: "ignored"},
		{Role: "assistant", Content: "已验证入口参数存在差异"},
	}
	prompt := buildPiPrompt(history, "继续验证并获取 flag")
	if strings.Contains(prompt, "你是Provena") || strings.Contains(prompt, "## 会话工作目录") {
		t.Fatalf("prompt echo leaked into Pi user prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "已验证入口参数存在差异") || !strings.Contains(prompt, "继续验证并获取 flag") {
		t.Fatalf("useful history or current task missing: %s", prompt)
	}
}

func TestPiConversationScopeRequestUsesUserHistoryOnly(t *testing.T) {
	history := []agent.ChatMessage{
		{Role: "user", Content: "测试 https://target.test/login"},
		{Role: "assistant", Content: "请访问 https://third-party.example/docs"},
		{Role: "tool", Content: "https://another-third-party.example"},
	}
	got := piConversationScopeRequest(history, "继续验证")
	if !strings.Contains(got, "https://target.test/login") || !strings.Contains(got, "继续验证") {
		t.Fatalf("scope request lost user target or current request: %q", got)
	}
	if strings.Contains(got, "third-party.example") {
		t.Fatalf("assistant/tool output must not expand asset scope: %q", got)
	}
}

func TestPiConversationScopeRequestPrefersCurrentTarget(t *testing.T) {
	history := []agent.ChatMessage{{Role: "user", Content: "测试 https://old-target.test/"}}
	got := piConversationScopeRequest(history, "改测 https://new-target.test/")
	if got != "改测 https://new-target.test/" {
		t.Fatalf("current target should replace stale history scope: %q", got)
	}
}

func TestNormalizePiFinalResponseStructured(t *testing.T) {
	raw := `<provena-final>
{"status":"success","target":"https://target.test/","finding":"SQL 注入","flag":"ctfshow{abc123}","evidence":["响应中出现 FLAG=ctfshow{abc123}"],"method":"布尔盲注二分搜索","limitations":"无"}
</provena-final>`
	result := normalizePiFinalResponse(raw, "https://target.test/，漏洞是 SQL 注入")
	for _, want := range []string{"状态：成功", "目标：https://target.test/", "SQL 注入", "`ctfshow{abc123}`", "验证证据", "布尔盲注二分搜索"} {
		if !strings.Contains(result, want) {
			t.Errorf("normalized result missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "<provena-final>") {
		t.Fatal("protocol wrapper should not be shown to user")
	}
}

func TestNormalizePiFinalResponseHidesPromptEcho(t *testing.T) {
	raw := "你是Provena\n高强度扫描要求：继续执行\nFLAG=ctfshow{safe-result}"
	result := normalizePiFinalResponse(raw, "https://target.test/")
	if !strings.Contains(result, "`ctfshow{safe-result}`") {
		t.Fatalf("flag was not retained: %s", result)
	}
	if strings.Contains(result, "你是Provena") || strings.Contains(result, "高强度扫描要求") {
		t.Fatalf("prompt echo leaked into final result: %s", result)
	}
}

func TestAppendPiToolTraceFallbackUnwrapsToolResult(t *testing.T) {
	rendered := normalizePiFinalResponse("你是Provena\n当前用户任务：内部回显", "https://target.test/")
	trace := safePiToolTrace(`{"content":[{"type":"text","text":"GET /i18n/model/getDictList returned 200 with public dictionary data"}]}`)
	result := appendPiToolTraceFallback(rendered, []string{"bash：" + trace})
	for _, want := range []string{"工具执行摘要", "GET /i18n/model/getDictList returned 200"} {
		if !strings.Contains(result, want) {
			t.Fatalf("fallback result missing %q: %s", want, result)
		}
	}
	for _, unwanted := range []string{"\"content\"", "当前用户任务"} {
		if strings.Contains(result, unwanted) {
			t.Fatalf("fallback result leaked %q: %s", unwanted, result)
		}
	}
}

func TestNormalizePiFinalResponseDoesNotShowUnauthorizedJSONAsFlag(t *testing.T) {
	raw := `<provena-final>
{"status":"success","target":"https://target.test/login","flag":"json {\"code\":401,\"msg\":\"您的登录状态已过期，请重新登录\"}","evidence":["当前用户任务：渗透目标 https://target.test/login，历史上下文与内部提示词不应展示给用户。"]}
</provena-final>`
	result := normalizePiFinalResponse(raw, "https://target.test/login")
	for _, unwanted := range []string{"**Flag**", "当前用户任务", "历史上下文", "内部提示词"} {
		if strings.Contains(result, unwanted) {
			t.Fatalf("unwanted content %q leaked: %s", unwanted, result)
		}
	}
	for _, want := range []string{"状态：部分完成", "HTTP 401", "登录状态已过期", "目标接口返回授权错误"} {
		if !strings.Contains(result, want) {
			t.Fatalf("normalized result missing %q: %s", want, result)
		}
	}
}

func TestNormalizePiFinalResponseDoesNotShowCompactJSONAsFlag(t *testing.T) {
	raw := `<provena-final>
{"status":"success","flag":"json{\"code\":\"401\",\"msg\":\"您的登录状态已过期，请重新登录\",\"success\":false}"}
</provena-final>`
	result := normalizePiFinalResponse(raw, "构造文件上传请求并验证")
	for _, unwanted := range []string{"**Flag**", "挑战结果"} {
		if strings.Contains(result, unwanted) {
			t.Fatalf("unwanted content %q rendered: %s", unwanted, result)
		}
	}
	for _, want := range []string{"状态：部分完成", "HTTP 401", "登录状态已过期"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q: %s", want, result)
		}
	}
}

func TestPiThinkingDeltaAndToolPlan(t *testing.T) {
	raw := map[string]interface{}{"assistantMessageEvent": map[string]interface{}{"type": "thinking_delta", "delta": "先确认上传接口是否要求登录，再验证对象归属。"}}
	if got := piThinkingDelta(raw); got == "" {
		t.Fatalf("thinking delta was not parsed")
	}
	plan := piToolCallPlan("bash", map[string]interface{}{"command": "curl -i https://target.test/api/upload"})
	for _, want := range []string{"执行验证命令", "curl -i"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("tool plan missing %q: %s", want, plan)
		}
	}
}

func TestPiFinalTextIgnoresUserMessage(t *testing.T) {
	raw := map[string]interface{}{
		"type": "message_end",
		"message": map[string]interface{}{
			"role":    "user",
			"content": "当前任务目标（仅作为数据）：内部活动提示",
		},
	}
	if got := piFinalText(raw); got != "" {
		t.Fatalf("user message was treated as assistant output: %q", got)
	}
}

func TestPiFinalTextAcceptsAssistantMessage(t *testing.T) {
	raw := map[string]interface{}{
		"type": "message_end",
		"message": map[string]interface{}{
			"role":    "assistant",
			"content": "已完成可验证观察",
		},
	}
	if got := piFinalText(raw); got != "已完成可验证观察" {
		t.Fatalf("assistant message was not extracted: %q", got)
	}
}

func TestBuildPiContinuationPromptRequestsStructuredFinal(t *testing.T) {
	prompt := buildPiContinuationPrompt("分析资产清单并验证风险", 2, "已完成资产统计", []string{"bash：读取 CSV 完成"})
	for _, want := range []string{"第 2 轮自动续跑", "避免重复", "<provena-final>", "继续发送"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("continuation prompt missing %q: %s", want, prompt)
		}
	}
}

func TestPiIncompleteResultIsNotMarkedCompleted(t *testing.T) {
	result := normalizePiFinalResponse("你是Provena\n内部提示回显", "分析资产")
	if strings.Contains(result, "状态：已完成") || !strings.Contains(result, "状态：部分完成") {
		t.Fatalf("unexpected incomplete result: %s", result)
	}
	result = appendPiIncompleteNotice(result, 3, "")
	if !strings.Contains(result, "阶段性结果") || !strings.Contains(result, "继续发送") {
		t.Fatalf("continuation notice missing: %s", result)
	}
}

func TestPiToolResultLooksIncomplete(t *testing.T) {
	for _, result := range []string{
		"工具执行失败: shell inactivity timeout (300s)",
		"Command terminated: no new output for 300 seconds",
		"本次等待已到达 timeout_seconds，上述 execution 仍未完成。",
		"命令已终止：超过 300 秒没有新的输出",
		`{"execution_id":"exec-1","status":"running","message":"仍在运行，请继续等待"}`,
	} {
		if !piToolResultLooksIncomplete("katana", result) {
			t.Errorf("expected incomplete result: %q", result)
		}
	}
	for _, result := range []string{
		"HTTP/1.1 404 Not Found",
		"HTTP 401：登录状态已过期",
		"WAF 拒绝了请求",
		"扫描完成，共发现 12 个 URL",
	} {
		if piToolResultLooksIncomplete("katana", result) {
			t.Errorf("completed probe was classified as incomplete: %q", result)
		}
	}
}

func TestBuildPiIncompleteRecoveryPromptIsNotRigidGlobalRoundLimit(t *testing.T) {
	got := buildPiIncompleteRecoveryPrompt([]string{"katana"}, []string{"katana（恢复第 1/2 次）"})
	for _, want := range []string{"工具尚未真正完成", "katana", "wait_tool_execution", "缩小范围", "不要输出 success"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recovery prompt missing %q: %s", want, got)
		}
	}
}

func TestBuildPiIncompleteToolFinalResponseIsPartial(t *testing.T) {
	got := buildPiIncompleteToolFinalResponse("测试 https://target.test/", 3, "katana 连续未完成", []string{"katana：shell inactivity timeout"})
	if !strings.Contains(got, `"status":"partial"`) || !strings.Contains(got, "katana 连续未完成") {
		t.Fatalf("incomplete tool final must be partial: %s", got)
	}
}

func TestCleanPiEvidenceRemovesPromptEchoAndCompactsText(t *testing.T) {
	evidence := cleanPiEvidence([]string{
		"当前用户任务：渗透目标 https://target.test/login，历史结论与提示词回显",
		"响应中出现 code=401，msg=您的登录状态已过期",
		"响应中出现 code=401，msg=您的登录状态已过期",
	})
	if len(evidence) != 1 || !strings.Contains(evidence[0], "code=401") {
		t.Fatalf("unexpected cleaned evidence: %#v", evidence)
	}
}

func TestCleanPiEvidenceForRequestDropsForeignURLs(t *testing.T) {
	got := cleanPiEvidenceForRequest([]string{
		"目标响应来自 https://target.test/api/login，状态码 200",
		"旧工作区文件包含 https://old-target.test/login 的内容",
		"没有 URL 的本地响应摘要",
	}, "测试 https://target.test/")
	if len(got) != 2 || strings.Contains(strings.Join(got, "\n"), "old-target.test") {
		t.Fatalf("foreign evidence was not removed: %#v", got)
	}
}

func TestNormalizePiFinalResponseDropsForeignEvidence(t *testing.T) {
	raw := `<provena-final>
{"status":"partial","target":"https://target.test/","evidence":["目标接口 https://target.test/api 返回 200","旧文件包含 https://old-target.test/login 的响应"]}
</provena-final>`
	got := normalizePiFinalResponse(raw, "测试 https://target.test/")
	if !strings.Contains(got, "https://target.test/api") || strings.Contains(got, "old-target.test") {
		t.Fatalf("foreign evidence leaked into normalized result: %s", got)
	}
}

func TestNormalizePiFinalResponseRendersMultipleFindingsWithoutFlag(t *testing.T) {
	raw := `<provena-final>
{"status":"success","target":"https://target.test","findings":[{"title":"SQL 注入","severity":"高","target":"GET /search?q=","finding":"参数 q 可控并可触发数据库错误。","evidence":["响应状态码从 200 变为 500","数据库错误信息被回显"],"impact":"可读取未授权数据","remediation":"使用参数化查询"},{"title":"越权访问","severity":"中","target":"GET /api/users/2","finding":"更换对象 ID 后可读取其他用户信息。","evidence":["用户 A 会话访问用户 B 数据"],"remediation":"校验资源归属"}],"evidence":["总体测试完成"]}
</provena-final>`
	result := normalizePiFinalResponse(raw, "https://target.test")
	for _, want := range []string{"漏洞发现（2）", "1. SQL 注入", "2. 越权访问", "风险等级", "修复建议", "补充证据"} {
		if !strings.Contains(result, want) {
			t.Fatalf("normalized result missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "Flag") {
		t.Fatalf("flag section should be omitted when no flag exists: %s", result)
	}
}

func TestPiSystemPromptContainsEfficiencyAndOutputRules(t *testing.T) {
	prompt := buildPiSystemPrompt("工作目录：C:/tmp/project")
	for _, want := range []string{"无状态执行器", "不要使用角色、Skill、RAG、跨轮对话记忆", "选择最小、最合适的工具", "不要重复相同操作", "失败、超时或无输出必须如实报告"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Pi system prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "必须读取") || strings.Contains(prompt, "角色授权") {
		t.Fatalf("Pi Harness prompt still contains role/Skill gating: %s", prompt)
	}
	if !strings.Contains(prompt, "工作目录：C:/tmp/project") {
		t.Fatal("runtime context missing from system prompt")
	}
}

func TestPiGeneralRequestDoesNotInheritSecurityContinuation(t *testing.T) {
	history := []agent.ChatMessage{{Role: "user", Content: "测试 https://target.test/api 并验证漏洞"}}
	if got := piTaskRoutingRequest(history, "你是谁", 0); got != "你是谁" {
		t.Fatalf("general question inherited security scope: %q", got)
	}
	if got := piTaskRoutingRequest(history, "继续验证", 0); !strings.Contains(got, "https://target.test/api") {
		t.Fatalf("security continuation lost prior scope: %q", got)
	}
	prompt := buildPiSystemPromptForTask("旧项目目标不应注入", false)
	for _, unwanted := range []string{"旧项目目标不应注入", "<provena-final>", "优先调用资产发现", "项目黑板索引"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("general prompt contains security/runtime content %q: %s", unwanted, prompt)
		}
	}
}

func TestPiNeedsHarnessOnlyForWorldActions(t *testing.T) {
	for _, request := range []string{"你是谁", "解释一下 FGS 是什么", "帮我运行 waybackurls 扫描目标", "对 example.com 做授权渗透测试", "继续挖掘*.qipeilong.cn，这是目标，然后这是授权：https://security.example.test"} {
		got := piNeedsHarness(request)
		want := strings.Contains(request, "运行") || strings.Contains(request, "渗透") || strings.Contains(request, "继续挖掘")
		if got != want {
			t.Fatalf("piNeedsHarness(%q) = %v, want %v", request, got, want)
		}
	}
}

func TestPiNeedsHarnessRecognizesSecurityActionSynonyms(t *testing.T) {
	for _, request := range []string{
		"挖漏洞 https://target.test",
		"找漏洞：target.test",
		"发现漏洞并记录证据，目标是 target.test",
		"继续测试 *.target.test",
		"已获得授权，使用 ARL 的 MCP 对 target.test 进行信息收集",
		"对 target.test 做资产发现",
	} {
		if !piNeedsHarness(request) {
			t.Fatalf("security action did not enter Harness: %q", request)
		}
	}
}

func TestPiNeedsHarnessDoesNotEnterForSecurityConceptQuestions(t *testing.T) {
	for _, request := range []string{
		"漏洞挖掘是什么意思",
		"解释一下什么是授权测试",
		"为什么漏洞扫描失败",
		"FGS 和普通对话有什么区别",
		"ARL MCP 是什么",
	} {
		if piNeedsHarness(request) {
			t.Fatalf("concept question unexpectedly entered Harness: %q", request)
		}
	}
}

func TestPiActiveToolNamesIncludesBridgeTools(t *testing.T) {
	got := piActiveToolNames(nil, []piagent.BridgeTool{{Name: "dddd_recon"}, {Name: "create_asset"}, {Name: "dddd_recon"}})
	for _, want := range []string{"read", "bash", "edit", "write", "dddd_recon", "create_asset"} {
		found := false
		for _, name := range got {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("active Pi tools missing %q: %v", want, got)
		}
	}
	if len(got) != 6 {
		t.Fatalf("expected duplicate bridge tools to be deduplicated: %v", got)
	}
}

func TestPiToolCandidatePromptMatchesDescriptions(t *testing.T) {
	tools := []piagent.BridgeTool{
		{Name: "sqlmap", Description: "使用 sqlmap 进行 SQL 注入检测与验证"},
		{Name: "katana", Description: "Katana Web 爬虫：JS、URL、API 与端点发现"},
		{Name: "nmap", Description: "端口和服务扫描"},
	}
	got := piToolCandidatePrompt("CTF 页面 JS 敏感信息和隐藏 API 路由", tools)
	if !strings.Contains(got, "katana") {
		t.Fatalf("description-matched tool missing: %s", got)
	}
	if strings.Contains(got, "固定调用顺序") == false {
		t.Fatalf("candidate prompt should explain that ranking is advisory: %s", got)
	}
}

func TestPiSecurityKickoffPromptRoutesSkillsAndRecon(t *testing.T) {
	paths := []string{
		`C:\skills\src-hunter\SKILL.md`,
		`C:\skills\attack-surface-recon\SKILL.md`,
	}
	prompt := buildPiSecurityKickoffPrompt("测试 https://target.test", "测试 https://target.test", paths, []piagent.BridgeTool{{Name: "arl_fingerprint"}})
	for _, want := range []string{"首个工具调用使用 read", "src-hunter/SKILL.md", "attack-surface-recon", "先做信息收集", "漏洞验证方向"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("security kickoff prompt missing %q: %s", want, prompt)
		}
	}
}

func TestPiRecommendedSkillPathsForWebAPI(t *testing.T) {
	paths := piRecommendedSkillPaths("测试 https://target.test/api 接口", `C:\skills`)
	joined := strings.Join(paths, "\n")
	for _, want := range []string{"src-hunter", "attack-surface-recon", "pentest-verification", "web-attack-methods"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("recommended skills missing %q: %v", want, paths)
		}
	}
}

func TestPiRecommendedSkillPathsForAssetProviders(t *testing.T) {
	paths := piRecommendedSkillPaths("授权测试目标资产，使用 FOFA、ZoomEye、Quake 和 Shodan 做网络空间测绘", `C:\skills`)
	joined := strings.ToLower(strings.Join(paths, "\n"))
	for _, want := range []string{"space-asset-discovery", "fofa", "zoomeye-ai-search", "quake-search"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("recommended asset skill missing %q: %v", want, paths)
		}
	}
}

func TestPiRecommendedSkillPathsForActualPentestIncludesWooYun(t *testing.T) {
	paths := piRecommendedSkillPaths("对 https://target.test 做授权渗透测试，重点检查支付和业务逻辑漏洞", `C:\skills`)
	joined := strings.ToLower(strings.Join(paths, "\n"))
	if !strings.Contains(joined, "wooyun-legacy") {
		t.Fatalf("actual pentest should recommend WooYun reference: %v", paths)
	}
	if !strings.Contains(joined, "case-index.md") {
		t.Fatalf("actual pentest should recommend the case router: %v", paths)
	}
}

func TestPiRecommendedSkillPathsForAPIPentestIncludesCaseIndex(t *testing.T) {
	paths := piRecommendedSkillPaths("对 https://target.test/api 做授权 API 参数测试", `C:\\skills`)
	joined := strings.ToLower(strings.Join(paths, "\n"))
	if !strings.Contains(joined, "case-index.md") {
		t.Fatalf("API pentest should recommend the case router: %v", paths)
	}
	if strings.Contains(joined, "ctf-web") {
		t.Fatalf("API pentest must not route to CTF skills: %v", paths)
	}
}

func TestPiRecommendedSkillPathsForCTFExcludesWooYun(t *testing.T) {
	paths := piRecommendedSkillPaths("CTF 挑战 https://target.test，获取 flag", `C:\skills`)
	joined := strings.ToLower(strings.Join(paths, "\n"))
	if strings.Contains(joined, "wooyun-legacy") {
		t.Fatalf("CTF should not recommend WooYun reference: %v", paths)
	}
}

func TestPiResultLinkerParsesAssetTargets(t *testing.T) {
	cases := []struct {
		input, protocol, domain, ip string
		port                        int
	}{
		{input: "https://target.test/login", protocol: "https", domain: "target.test", port: 443},
		{input: "192.0.2.10:8080", protocol: "tcp", ip: "192.0.2.10", port: 8080},
	}
	for _, tc := range cases {
		got, ok := parsePiAssetTarget(tc.input)
		if !ok {
			t.Fatalf("parsePiAssetTarget(%q) failed", tc.input)
		}
		if got.Protocol != tc.protocol || got.Port != tc.port || got.Domain != tc.domain || got.IP != tc.ip {
			t.Fatalf("parsePiAssetTarget(%q) = %#v", tc.input, got)
		}
	}
	if _, ok := parsePiAssetTarget("GET /api/login"); ok {
		t.Fatal("relative endpoint should not become an asset without a host")
	}
}

func TestPiAssetScopeRejectsThirdPartyAndLocalEndpoints(t *testing.T) {
	scope := piAssetScopeFromRequest("渗透测试 https://wx.tourzj.edu.cn/")
	for _, target := range []string{
		"https://wx.tourzj.edu.cn/syswx/login.aspx",
		"https://bgpt.tourzj.edu.cn:3270/",
		"https://rzzx.tourzj.edu.cn:8091/cas",
	} {
		if !piAssetTargetInScope(target, scope) {
			t.Fatalf("same target domain should be in scope: %s", target)
		}
	}
	for _, target := range []string{
		"https://github.com/vuejs/vue",
		"https://rzplatform.dofun.work/api",
		"http://127.0.0.1:8090/openapi/v1/cardreader",
		"https://221.214.181.187:8006/",
	} {
		if piAssetTargetInScope(target, scope) {
			t.Fatalf("third-party or local endpoint must be rejected: %s", target)
		}
	}

	got := piLoopTargets("https://wx.tourzj.edu.cn/", []string{
		"工具结果：https://bgpt.tourzj.edu.cn:3270/base/config.js",
		"工具结果：https://github.com/vuejs/vue",
		"工具结果：http://127.0.0.1:8090/openapi/v1/faceReader/collect",
	})
	if len(got) != 2 || !containsPiString(got, "https://wx.tourzj.edu.cn/") || !containsPiString(got, "https://bgpt.tourzj.edu.cn:3270/base/config.js") {
		t.Fatalf("unexpected filtered asset targets: %#v", got)
	}
}

func TestPiAssetScopeMatchesExactIPOnly(t *testing.T) {
	scope := piAssetScopeFromRequest("测试 https://192.0.2.10:8443/")
	if !piAssetTargetInScope("192.0.2.10:9000", scope) {
		t.Fatal("same target IP with another port should be in scope")
	}
	if piAssetTargetInScope("192.0.2.11:8443", scope) {
		t.Fatal("different IP should not be in scope")
	}
}

func TestPiAssetScopeSupportsBareTargetsAndRejectsLocalTargets(t *testing.T) {
	scope := piAssetScopeFromRequest("目标域名：target.test，目标 IP：192.0.2.10")
	if !piAssetTargetInScope("https://api.target.test:8443/health", scope) {
		t.Fatal("bare target domain should define the asset scope")
	}
	if !piAssetTargetInScope("192.0.2.10:8080", scope) {
		t.Fatal("bare target IP should define the asset scope")
	}
	for _, target := range []string{"http://localhost:3000", "http://127.0.0.1:8090", "http://[::1]:8080"} {
		if piAssetTargetInScope(target, piAssetScopeFromRequest("目标："+target)) {
			t.Fatalf("local target must never be persisted: %s", target)
		}
	}
}

func TestPiCapturedPacketScopeFiltersForeignHosts(t *testing.T) {
	scope := piAssetScopeFromRequest("对 https://target.test 做漏洞测试")
	cases := []struct {
		host string
		port int
		want bool
	}{
		{host: "target.test", port: 443, want: true},
		{host: "api.target.test", port: 8443, want: true},
		{host: "target.test.evil.test", port: 443, want: false},
		{host: "cdn.third-party.test", port: 443, want: false},
		{host: "192.0.2.11", port: 443, want: false},
	}
	for _, tc := range cases {
		if got := piCapturedPacketInScope("https", tc.host, tc.port, scope); got != tc.want {
			t.Errorf("piCapturedPacketInScope(https, %q, %d) = %v, want %v", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestPiCapturedPacketScopeAllowsExplicitLocalTarget(t *testing.T) {
	for _, target := range []struct {
		host string
		port int
		raw  string
	}{
		{host: "localhost", port: 3000, raw: "http://localhost:3000"},
		{host: "127.0.0.1", port: 8080, raw: "http://127.0.0.1:8080"},
		{host: "::1", port: 8080, raw: "http://[::1]:8080"},
	} {
		scope := piAssetScopeFromRequest("CTF 本地目标 " + target.raw)
		if !piCapturedPacketInScope("http", target.host, target.port, scope) {
			t.Fatalf("explicit local target should be available to Pi: %s", target.host)
		}
	}
}

func TestPiCapturedPacketScopeRejectsPacketsWithoutFrozenTarget(t *testing.T) {
	scope := piAssetScopeFromRequest("分析用户选中的抓包分组")
	if piCapturedPacketInScope("https", "target.test", 443, scope) {
		t.Fatal("packets must be rejected when the conversation has no explicit target")
	}
}

func TestPiFindingScopeRejectsForeignFindingTarget(t *testing.T) {
	scope := piAssetScopeFromRequest("测试 https://target.test/")
	if piFindingInScope(piFinalResult{Target: "https://target.test/"}, piFinding{Target: "https://old-target.test/login"}, scope) {
		t.Fatal("a foreign finding target must not pass because the result target is in scope")
	}
}

func TestPiResultLinkerRequiresEvidenceCompleteFinding(t *testing.T) {
	result := piFinalResult{Target: "https://target.test", Method: "GET request"}
	item, ok := normalizePiFindingForStorage(result, piFinding{
		Title: "SQL 注入", Severity: "高", Target: "/search?q=", Finding: "参数可控", Evidence: []string{"数据库错误"},
		Impact: "可读取数据", Remediation: "使用参数化查询",
	})
	if !ok || item.vulnerabilityType != "SQL 注入" {
		t.Fatalf("complete finding was rejected or type was not inferred: %#v, %v", item, ok)
	}
	_, ok = normalizePiFindingForStorage(result, piFinding{
		Title: "越权访问", Severity: "中", Target: "/api/users/2", Finding: "可读取其他用户数据",
		Evidence: []string{"用户 A 读取用户 B"}, Remediation: "校验归属",
	})
	if ok {
		t.Fatal("finding without a reproducible method should not be auto-persisted")
	}
}

func TestAppendPiResultLinksUsesNavigableRoutes(t *testing.T) {
	got := appendPiResultLinks("## 执行结果\n\n- 状态：成功", piResultLinks{
		Vulnerabilities: []piLinkedVulnerability{{ID: "v-1", Title: "SQL 注入"}},
		Assets:          []piLinkedAsset{{ID: "a-1", Target: "https://target.test:443"}},
		Facts:           []piLinkedFact{{ProjectID: "p-1", FactKey: "finding/sql-login", Summary: "登录接口 SQL 注入"}},
	})
	for _, want := range []string{"#vulnerabilities?id=v-1", "#asset-library?q=https%3A%2F%2Ftarget.test%3A443", "#projects?id=p-1&fact_key=finding%2Fsql-login"} {
		if !strings.Contains(got, want) {
			t.Errorf("linked result missing %q: %s", want, got)
		}
	}
}

func TestPiRoundFingerprintUsesResponseWhenNoTools(t *testing.T) {
	first := piRoundEvidenceFingerprint(piRoundObservation{Response: "等待下一步验证"})
	second := piRoundEvidenceFingerprint(piRoundObservation{Response: "等待下一步验证"})
	if first == "" || first != second {
		t.Fatalf("same no-tool response should have a stable fingerprint: %q %q", first, second)
	}
	if first == piRoundEvidenceFingerprint(piRoundObservation{Response: "已完成验证"}) {
		t.Fatal("different no-tool responses should not share a fingerprint")
	}
}

func TestPiLoopObservationFingerprintIgnoresVolatileExecutionIDs(t *testing.T) {
	first := piLoopObservationFingerprint(piLoopObservation{
		ToolName: "get_tool_execution",
		Args:     map[string]interface{}{"execution_id": "exec-1"},
		Result:   `{"execution_id":"11111111-1111-1111-1111-111111111111","status":"running"}`,
	})
	second := piLoopObservationFingerprint(piLoopObservation{
		ToolName: "get_tool_execution",
		Args:     map[string]interface{}{"execution_id": "exec-2"},
		Result:   `{"execution_id":"22222222-2222-2222-2222-222222222222","status":"running"}`,
	})
	if first == "" || first != second {
		t.Fatalf("volatile execution identifiers should not hide a repeated call: %q %q", first, second)
	}

	changed := piLoopObservationFingerprint(piLoopObservation{
		ToolName: "get_tool_execution",
		Args:     map[string]interface{}{"execution_id": "exec-3"},
		Result:   `{"execution_id":"33333333-3333-3333-3333-333333333333","status":"completed","result":"new evidence"}`,
	})
	if first == changed {
		t.Fatal("a meaningful tool result change must remain visible to loop detection")
	}
}

func TestPiRepeatedCycleDetectsAlternatingRounds(t *testing.T) {
	history := make([]string, 0, 6)
	for _, value := range []string{"a", "b", "a", "b", "a", "b"} {
		history = appendPiLoopFingerprint(history, value)
	}
	length, rounds := piRepeatedCycle(history)
	if length != 2 || rounds != 6 {
		t.Fatalf("cycle = (%d, %d), want (2, 6)", length, rounds)
	}
}

func TestPiCompletionReasonNoProgress(t *testing.T) {
	if got := piCompletionReason(false, true, true, false, ""); got != "pi_no_progress_stopped" {
		t.Fatalf("completion reason = %q", got)
	}
}

func TestPiRoundNovelEvidenceIgnoresRepeatedAuthorizationFailures(t *testing.T) {
	seen := make(map[string]struct{})
	first := piRoundObservation{Tools: []piLoopObservation{{
		ToolName: "bash",
		Result:   `HTTP/1.1 200 {"body":{},"status":"401","message":"Full authentication is required to access this resource"}`,
	}}}
	if piRoundHasNovelEvidence(first, seen) {
		t.Fatal("an authorization-only response should not count as effective progress")
	}
	second := piRoundObservation{Tools: []piLoopObservation{{
		ToolName: "bash",
		Result:   `HTTP/1.1 200 {"body":{},"status":"401","message":"Full authentication is required to access this resource"}`,
	}}}
	if piRoundHasNovelEvidence(second, seen) {
		t.Fatal("a repeated authorization failure should not count as new evidence")
	}
}

func TestPiRoundNovelEvidenceIgnoresWorkspaceChurnAndEquivalentErrorPages(t *testing.T) {
	seen := make(map[string]struct{})
	workspace := piRoundObservation{Tools: []piLoopObservation{{
		ToolName: "bash",
		Args:     map[string]interface{}{"command": "pwd; find . -maxdepth 2 -type f -printf '%p %s bytes\\n' | sort"},
		Result:   "./error.html 2415 bytes\\n./latest_login_pic.html 2687 bytes",
	}}}
	if piRoundHasNovelEvidence(workspace, seen) {
		t.Fatal("workspace enumeration should not count as security progress")
	}

	first := piRoundObservation{Tools: []piLoopObservation{{
		ToolName: "bash",
		Args:     map[string]interface{}{"command": "curl -k https://wx.tourzj.edu.cn/syswx/login_pic.aspx?x=1"},
		Result:   "HTTP/1.1 200 OK Date: 14:01:00 Compilation Error CS0433 GetRemoteObj.DLL Temporary ASP.NET Files",
	}}}
	if !piRoundHasNovelEvidence(first, seen) {
		t.Fatal("first disclosure response should count as progress")
	}

	repeated := piRoundObservation{Tools: []piLoopObservation{{
		ToolName: "bash",
		Args:     map[string]interface{}{"command": "python requests.get('https://wx.tourzj.edu.cn/syswx/login_pic.aspx?probe=2')"},
		Result:   "HTTP/1.1 200 OK Date: 14:02:00 Compilation Error CS0433 GetRemoteObj.DLL Temporary ASP.NET Files",
	}}}
	if piRoundHasNovelEvidence(repeated, seen) {
		t.Fatal("query-string and timestamp changes should not count as new disclosure evidence")
	}
}

func TestPiRoundNovelEvidenceKeepsDifferentEndpointEvidence(t *testing.T) {
	seen := make(map[string]struct{})
	first := piRoundObservation{Tools: []piLoopObservation{{
		ToolName: "bash",
		Args:     map[string]interface{}{"command": "curl https://target.test/login_pic.aspx"},
		Result:   "HTTP/1.1 200 OK Compilation Error CS0433",
	}}}
	second := piRoundObservation{Tools: []piLoopObservation{{
		ToolName: "bash",
		Args:     map[string]interface{}{"command": "curl https://target.test/admin.aspx"},
		Result:   "HTTP/1.1 200 OK Compilation Error CS0433",
	}}}
	if !piRoundHasNovelEvidence(first, seen) || !piRoundHasNovelEvidence(second, seen) {
		t.Fatal("a different endpoint should remain visible as a distinct evidence location")
	}
}

func TestExtractPiTargetKeepsAllTargets(t *testing.T) {
	got := extractPiTarget("测试 https://one.test:8443、https://two.test:9443 和 https://one.test:8443")
	if got != "https://one.test:8443、https://two.test:9443" {
		t.Fatalf("targets = %q", got)
	}
}
