package handler

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/mcp/builtin"
)

// piExecutionGate keeps the security-task workflow ordered at runtime. The
// prompt tells Pi what to do; this gate also prevents the highest-impact
// result-writing/verification bridge tools from running before Skill loading
// and reconnaissance have happened.
type piExecutionGate struct {
	mu         sync.RWMutex
	security   bool
	taskKind   string
	profile    string
	skillPath  string
	stage      string
	firstTool  string
	skillRead  bool
	reconCalls int
	reconReady bool
}

func newPiExecutionGate(request, skillsRoot string) *piExecutionGate {
	kind := piTaskKind(request)
	if kind == "" || strings.TrimSpace(skillsRoot) == "" {
		return nil
	}
	profile := piTestProfileForRequest(request)
	skillName := "pentesting-everything"
	if kind == piTaskKindCTF {
		skillName = "ctf-web"
	}
	return &piExecutionGate{
		security:  true,
		taskKind:  kind,
		profile:   profile.ID,
		skillPath: filepath.Clean(filepath.Join(skillsRoot, skillName, "SKILL.md")),
		stage:     "skill",
	}
}

// observeToolStart is called from the Pi RPC event stream before a tool is
// executed. It returns the current stage and whether the stage changed.
func (g *piExecutionGate) observeToolStart(toolName string, args interface{}) (string, bool, bool) {
	if g == nil || !g.security {
		return "", false, false
	}
	toolName = strings.TrimSpace(toolName)
	g.mu.Lock()
	defer g.mu.Unlock()
	changed := false
	firstTool := g.firstTool == "" && toolName != ""
	if g.firstTool == "" && toolName != "" {
		g.firstTool = toolName
	}
	if strings.EqualFold(toolName, "read") {
		path := filepath.ToSlash(filepath.Clean(piObservationPath(args)))
		required := filepath.ToSlash(g.skillPath)
		lowerPath := strings.ToLower(path)
		requiredRelative := strings.ToLower(filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(g.skillPath)), "SKILL.md")))
		legacyRelative := strings.ToLower(filepath.ToSlash(filepath.Join("src-hunter", "SKILL.md")))
		legacyAllowed := g.taskKind == piTaskKindPentest && (lowerPath == legacyRelative || strings.HasSuffix(lowerPath, "/"+legacyRelative))
		if path != "." && (strings.EqualFold(path, required) || lowerPath == requiredRelative || strings.HasSuffix(lowerPath, "/"+requiredRelative) || legacyAllowed) {
			g.skillRead = true
		}
	}
	if piIsReconObservation(toolName, args) {
		g.reconCalls++
		g.reconReady = true
	}
	next := "skill"
	if g.skillRead {
		next = "recon"
	}
	if g.reconReady {
		next = "validation"
	}
	if g.stage != next {
		g.stage = next
		changed = true
	}
	return g.stage, changed, firstTool
}

func (g *piExecutionGate) snapshot() map[string]interface{} {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return map[string]interface{}{
		"stage":         g.stage,
		"taskKind":      g.taskKind,
		"profile":       g.profile,
		"firstTool":     g.firstTool,
		"skillRead":     g.skillRead,
		"reconCalls":    g.reconCalls,
		"reconComplete": g.reconReady,
	}
}

func (g *piExecutionGate) allowBridgeCall(toolName string, args map[string]interface{}) (bool, string) {
	if g == nil || !g.security {
		return true, ""
	}
	if g.taskKind == piTaskKindCTF && piIsPersistentSecurityWriteTool(toolName) {
		return false, "CTF 执行闸门：挑战结果只保留在本次对话中，禁止写入企业资产、漏洞或项目事实库"
	}
	if !piIsValidationBridgeTool(toolName, args) {
		return true, ""
	}
	g.mu.RLock()
	skillRead, reconReady := g.skillRead, g.reconReady
	g.mu.RUnlock()
	if !skillRead {
		return false, fmt.Sprintf("执行闸门：请先用 read 读取 %s，再调用漏洞验证工具", filepath.Base(filepath.Dir(g.skillPath))+"/SKILL.md")
	}
	if !reconReady {
		return false, "执行闸门：请先完成范围内的信息收集（资产、存活性、端口/服务或指纹），再调用漏洞验证工具"
	}
	return true, ""
}

func piIsPersistentSecurityWriteTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case builtin.ToolCreateAsset, builtin.ToolRecordVulnerability,
		builtin.ToolUpsertProjectFact, builtin.ToolDeprecateProjectFact,
		builtin.ToolRestoreProjectFact:
		return true
	default:
		return false
	}
}

func piIsValidationBridgeTool(toolName string, args map[string]interface{}) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	if lower == strings.ToLower(builtin.ToolRecordVulnerability) {
		return true
	}
	for _, marker := range []string{
		"sqlmap", "dalfox", "xray", "exploit", "payload", "sqli", "xss", "ssrf", "rce", "ssti", "xxe", "idor", "traversal", "file_upload", "file-download", "poc",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if lower == "dddd" || strings.Contains(lower, "nuclei") {
		for key, value := range args {
			if strings.EqualFold(strings.TrimSpace(key), "full_poc") && strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "true") {
				return true
			}
		}
	}
	return false
}

func piIsReconObservation(toolName string, args interface{}) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	for _, marker := range []string{
		"recon", "asset", "subdomain", "dns", "port", "finger", "httpx", "http-framework-test", "katana", "wihscan", "whatweb", "dirsearch", "gobuster", "ffuf", "arjun", "amass", "subfinder", "naabu", "nmap", "masscan", "rustscan", "whois", "crt", "wayback", "fofa", "shodan", "quake", "zoomeye", "search",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	command := strings.ToLower(piObservationCommand(args))
	if command == "" {
		return false
	}
	for _, marker := range []string{
		"curl ", "curl.exe ", "wget ", "httpx ", "nmap ", "masscan ", "rustscan ", "subfinder ", "amass ", "dnsx ", "dig ", "nslookup ", "whois ", "whatweb ", "ffuf ", "gobuster ", "dirsearch ", "katana ", "arjun ", "crt.sh", "web.archive.org",
	} {
		if strings.Contains(command, marker) {
			return !piLooksLikeExploitCommand(command)
		}
	}
	return false
}

func piLooksLikeExploitCommand(command string) bool {
	for _, marker := range []string{
		"sqlmap", "--os-shell", "--file-read", "<script", "union select", "../", "..%2f", "gopher://", "file://", "{{7*7}}", ";id", "|id", "$(id)", "-x '", "--data-raw",
	} {
		if strings.Contains(command, marker) {
			return true
		}
	}
	return false
}

func piRecommendedSkillPaths(request, skillsRoot string) []string {
	if strings.TrimSpace(skillsRoot) == "" {
		return nil
	}
	kind := piTaskKind(request)
	if kind == "" {
		return nil
	}
	lower := strings.ToLower(request)
	if kind == piTaskKindCTF {
		return []string{filepath.Clean(filepath.Join(skillsRoot, "ctf-web", "SKILL.md"))}
	}
	profile := piTestProfileForRequest(request)
	names := []string{"pentesting-everything"}
	if piLooksLikeActualPentestTask(request) {
		// This compact router is loaded for every real test type, including API
		// testing. The large H1/WooYun files remain on-demand.
		names = append(names, filepath.Clean(filepath.Join(skillsRoot, "src-hunter", "references", "case-index.md")))
	}
	archivePath := filepath.Clean(filepath.Join(skillsRoot, "pentesting-everything", "PentestingEverything-2.0.0", filepath.FromSlash(profile.ArchiveReadme)))
	if profile.ArchiveReadme != "" {
		names = append(names, archivePath)
	}
	names = append(names, profile.SkillNames...)
	if piLooksLikeActualPentestTask(request) && profile.ID != piProfileAPI {
		names = append(names, "wooyun-legacy")
	}
	if strings.Contains(lower, "api") || strings.Contains(lower, "graphql") || strings.Contains(lower, "接口") {
		names = append(names, "api-breaker")
	}
	if strings.Contains(lower, "源码") || strings.Contains(lower, "source") || strings.Contains(lower, "js") || strings.Contains(lower, "apk") {
		names = append(names, "source-code-hunting")
	}
	if strings.Contains(lower, "黑板") || strings.Contains(lower, "资产") || strings.Contains(lower, "项目") {
		names = append(names, "pentest-blackboard")
	}
	if strings.Contains(lower, "资产") || strings.Contains(lower, "测绘") || strings.Contains(lower, "fofa") || strings.Contains(lower, "zoomeye") || strings.Contains(lower, "quake") || strings.Contains(lower, "shodan") || strings.Contains(lower, "网络空间") || strings.Contains(lower, "子域") {
		names = append(names, "space-asset-discovery")
	}
	if strings.Contains(lower, "fofa") {
		names = append(names, "fofa")
	}
	if strings.Contains(lower, "zoomeye") || strings.Contains(lower, "zoom eye") {
		names = append(names, "zoomeye-ai-search")
	}
	if strings.Contains(lower, "quake") {
		names = append(names, "quake-search")
	}
	paths := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		path := name
		if !filepath.IsAbs(path) || !strings.HasSuffix(strings.ToLower(path), ".md") {
			path = filepath.Clean(filepath.Join(skillsRoot, name, "SKILL.md"))
		} else {
			path = filepath.Clean(path)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

type piTestProfile struct {
	ID            string
	Label         string
	ArchiveReadme string
	SkillNames    []string
}

const (
	piProfileAPI        = "api"
	piProfileWeb        = "web"
	piProfileNetwork    = "network"
	piProfileCloud      = "cloud"
	piProfileMobile     = "mobile"
	piProfileAD         = "active_directory"
	piProfileContainer  = "container"
	piProfileWireless   = "wireless"
	piProfileIoT        = "iot"
	piProfileBlockchain = "blockchain"
	piProfileLLM        = "llm"
	piProfileMCP        = "mcp"
	piProfileCode       = "code_review"
	piProfileForensic   = "forensic"
	piProfileOSINT      = "osint"
	piProfilePhishing   = "phishing"
	piProfileThick      = "thick_client"
	piProfileConfig     = "configuration_review"
	piProfileDevSecOps  = "devsecops"
	piProfileThreat     = "threat_modeling"
)

const piPacketCaptureRoutingMarker = "[packet-capture-api]"

func piTestProfileForRequest(request string) piTestProfile {
	lower := strings.ToLower(strings.TrimSpace(request))
	if strings.Contains(lower, "[packet-capture-api]") || containsAnyFold(lower, "抓包", "请求包", "burp", "yakit", "rest api", "graphql", "openapi", "swagger", "接口测试", "api 测试") {
		return piTestProfile{ID: piProfileAPI, Label: "API 测试", ArchiveReadme: "API Pentesting/README.md", SkillNames: []string{"api-breaker", "attack-surface-recon", "pentest-verification"}}
	}
	profiles := []piTestProfile{
		{ID: piProfileAD, Label: "Active Directory 测试", ArchiveReadme: "Active Directory Pentesting/README.md", SkillNames: []string{"active-directory-attack"}},
		{ID: piProfileCloud, Label: "云安全测试", ArchiveReadme: "Cloud Pentesting/README.md", SkillNames: []string{"cloud-attack-methods"}},
		{ID: piProfileMobile, Label: "移动端测试", ArchiveReadme: "Mobile Pentesting/Readme.md", SkillNames: []string{"binary-mobile-reversing"}},
		{ID: piProfileContainer, Label: "容器与 Kubernetes 测试", ArchiveReadme: "Container & Kubernetes  Assessment/README.md", SkillNames: []string{"specialized-attack-playbooks"}},
		{ID: piProfileWireless, Label: "无线测试", ArchiveReadme: "Wi-Fi Pentesting/README.md", SkillNames: []string{"wireless-hardware-attack"}},
		{ID: piProfileIoT, Label: "IoT 测试", ArchiveReadme: "IoT Pentesting/README.md", SkillNames: []string{"wireless-hardware-attack"}},
		{ID: piProfileBlockchain, Label: "区块链测试", ArchiveReadme: "BlockChain Pentesting/README.md", SkillNames: []string{"blockchain-contract-attack"}},
		{ID: piProfileLLM, Label: "LLM 安全测试", ArchiveReadme: "LLM Security Assessment/readme.md", SkillNames: []string{"ai-llm-app-attack"}},
		{ID: piProfileMCP, Label: "MCP 安全测试", ArchiveReadme: "MCP Security Assessment/readme.md", SkillNames: []string{"specialized-attack-playbooks"}},
		{ID: piProfileCode, Label: "代码安全审查", ArchiveReadme: "Secure Code Review/README.md", SkillNames: []string{"source-code-hunting"}},
		{ID: piProfileForensic, Label: "取证分析", ArchiveReadme: "Forensic/Readme.md", SkillNames: []string{"specialized-attack-playbooks"}},
		{ID: piProfileOSINT, Label: "OSINT 情报分析", ArchiveReadme: "OSINT/README.md", SkillNames: []string{"space-asset-discovery"}},
		{ID: piProfilePhishing, Label: "钓鱼与社交工程仿真", ArchiveReadme: "Phishing Assessment/README.md", SkillNames: []string{"initial-access-phishing"}},
		{ID: piProfileThick, Label: "厚客户端测试", ArchiveReadme: "Thick Client Pentesting/readme.md", SkillNames: []string{"specialized-attack-playbooks"}},
		{ID: piProfileConfig, Label: "配置审查", ArchiveReadme: "Configuration Review/Readme.md", SkillNames: []string{"specialized-attack-playbooks"}},
		{ID: piProfileDevSecOps, Label: "DevSecOps 与 CI/CD 测试", ArchiveReadme: "DevSecOps/README.md", SkillNames: []string{"specialized-attack-playbooks"}},
		{ID: piProfileThreat, Label: "威胁建模", ArchiveReadme: "Threat Modeling/Readme.md", SkillNames: []string{"specialized-attack-playbooks"}},
		{ID: piProfileNetwork, Label: "网络与基础设施测试", ArchiveReadme: "Network Pentesting/readme.md", SkillNames: []string{"recon-port-scan", "recon-fingerprint"}},
	}
	profileMarkers := map[string][]string{
		piProfileAD:         {"active directory", "域控", "域环境", "ad 测试", "kerberos"},
		piProfileCloud:      {"aws", "azure", "gcp", "云安全", "云渗透"},
		piProfileMobile:     {"android", "ios", "apk", "ipa", "移动端", "移动应用"},
		piProfileContainer:  {"docker", "kubernetes", "k8s", "容器"},
		piProfileWireless:   {"wifi", "wi-fi", "无线网络", "802.11"},
		piProfileIoT:        {"iot", "物联网", "固件"},
		piProfileBlockchain: {"blockchain", "区块链", "智能合约", "solidity"},
		piProfileLLM:        {"llm", "大模型", "提示词注入", "ai 安全"},
		piProfileMCP:        {"mcp server", "mcp 安全", "模型上下文协议"},
		piProfileCode:       {"源码", "源代码", "代码审计", "sast", "secure code", "白盒"},
		piProfileForensic:   {"forensic", "取证", "内存镜像", "磁盘镜像", "事件日志"},
		piProfileOSINT:      {"osint", "公开情报", "开源情报"},
		piProfilePhishing:   {"phishing", "钓鱼", "社交工程"},
		piProfileThick:      {"thick client", "厚客户端", "桌面客户端"},
		piProfileConfig:     {"配置审查", "安全基线", "cis benchmark"},
		piProfileDevSecOps:  {"devsecops", "ci/cd", "供应链安全"},
		piProfileThreat:     {"威胁建模", "threat modeling"},
		piProfileNetwork:    {"网络渗透", "网络测试", "防火墙", "端口扫描", "基础设施"},
	}
	for _, profile := range profiles {
		if containsAnyFold(lower, profileMarkers[profile.ID]...) {
			return profile
		}
	}
	return piTestProfile{ID: piProfileWeb, Label: "Web 应用测试", ArchiveReadme: "Web Application Pentesting/README.md", SkillNames: []string{"src-hunter", "attack-surface-recon", "web-attack-methods", "pentest-verification"}}
}

func containsAnyFold(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// piLooksLikeActualPentestTask distinguishes authorized real-world testing
// from CTF/flag work. WooYun is a historical business-logic reference for
// pentest prioritization; loading it for challenge solving adds noise and can
// make old case patterns look like current-target evidence.
func piLooksLikeActualPentestTask(request string) bool {
	return piTaskKind(request) == piTaskKindPentest
}

const (
	piTaskKindCTF     = "ctf"
	piTaskKindPentest = "pentest"
)

// piTaskKind routes challenge work separately from authorized real-world
// testing. Challenge markers win even when the request also contains a URL or
// words such as "SQL injection", because a challenge target must not inherit
// the enterprise asset/vulnerability workflow.
func piTaskKind(request string) string {
	lower := strings.ToLower(strings.TrimSpace(request))
	for _, marker := range []string{
		"ctf", "flag", "get the flag", "挑战题", "挑战", "靶场", "picoctf", "tryhackme", "hackthebox",
	} {
		if strings.Contains(lower, marker) {
			return piTaskKindCTF
		}
	}
	for _, marker := range []string{
		"渗透", "安全测试", "安全审计", "安全评估", "漏洞挖掘", "漏洞验证", "授权测试",
		"src", "bug bounty", "pentest", "penetration", "security audit", "业务逻辑",
		"越权", "支付安全", "代码审计", "wooyun", "案例参考", "历史案例", "目标范围",
		"资产测绘", "网络空间测绘", "子域名枚举", "漏洞扫描", piPacketCaptureRoutingMarker,
	} {
		if strings.Contains(lower, marker) {
			return piTaskKindPentest
		}
	}
	// Keep the existing behavior for plainly security-shaped requests such as
	// "测试 https://target" without letting a bare URL enter pentest mode.
	if hasPiURL(lower) {
		for _, marker := range []string{"测试", "检查", "验证", "扫描", "探测", "enumerate", "recon"} {
			if strings.Contains(lower, marker) {
				return piTaskKindPentest
			}
		}
	}
	return ""
}

func hasPiURL(value string) bool {
	return strings.Contains(value, "http://") || strings.Contains(value, "https://")
}

func piLooksLikeSecurityTask(request string) bool {
	return piTaskKind(request) != ""
}

// piTaskRoutingRequest decides whether a new Pi request is a security task.
// A role or an old project scope is not sufficient evidence: users commonly
// keep a security role selected while asking an unrelated conversational
// question. Only an explicit security request, a selected packet group, or a
// short continuation of an existing security request may inherit that scope.
func piTaskRoutingRequest(history []agent.ChatMessage, current string, packetGroupCount int) string {
	current = strings.TrimSpace(current)
	if packetGroupCount > 0 {
		return current + "\n" + piPacketCaptureRoutingMarker
	}
	if piTaskKind(current) != "" {
		return current
	}
	if !piIsSecurityContinuation(current) {
		return current
	}
	inherited := piConversationScopeRequest(history, current)
	if piTaskKind(inherited) != "" {
		return inherited
	}
	return current
}

func piIsSecurityContinuation(current string) bool {
	lower := strings.ToLower(strings.TrimSpace(current))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"继续", "接着", "下一步", "复测", "重测", "再测", "重新验证", "继续验证", "继续测试", "继续扫描",
		"continue", "resume", "follow up",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
