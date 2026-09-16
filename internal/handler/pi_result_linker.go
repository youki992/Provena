package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/mcp/builtin"

	"go.uber.org/zap"
	"golang.org/x/net/publicsuffix"
)

// piResultLinks are the durable resources created or reused from one Pi run.
// They are deliberately small so they can be rendered into the chat result
// without exposing raw tool responses or internal prompts.
type piResultLinks struct {
	Vulnerabilities []piLinkedVulnerability
	Assets          []piLinkedAsset
	Facts           []piLinkedFact
}

type piLinkedVulnerability struct {
	ID    string
	Title string
}

type piLinkedAsset struct {
	ID     string
	Target string
}

type piLinkedFact struct {
	ProjectID string
	FactKey   string
	Summary   string
}

func mergePiResultLinks(dst, src piResultLinks) piResultLinks {
	for _, item := range src.Vulnerabilities {
		dst.Vulnerabilities = appendUniquePiVulnerability(dst.Vulnerabilities, item)
	}
	for _, item := range src.Assets {
		dst.Assets = appendUniquePiAsset(dst.Assets, item)
	}
	for _, item := range src.Facts {
		dst.Facts = appendUniquePiFact(dst.Facts, item)
	}
	return dst
}

type piAssetTarget struct {
	Raw      string
	Host     string
	IP       string
	Domain   string
	Port     int
	Protocol string
}

var piURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"'` + "`" + `，。、；]+`)
var piBareTargetPattern = regexp.MustCompile(`(?i)(?:(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}|(?:\d{1,3}\.){3}\d{1,3})(?::\d{1,5})?`)

// persistPiFinalResult turns a structured final result into links to the
// existing vulnerability, asset and project-fact stores. All writes go
// through the normal MCP path, so RBAC, conversation binding, HITL and tool
// execution auditing remain in effect.
func (h *AgentHandler) persistPiFinalResult(ctx context.Context, principal authctx.Principal, conversationID, request, raw string, roleTools []string) piResultLinks {
	var links piResultLinks
	if h == nil || h.agent == nil || h.db == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(principal.UserID) == "" {
		return links
	}
	result, ok := parsePiFinalResult(raw)
	if !ok || len(result.Findings) == 0 {
		return links
	}

	projectID := h.conversationProjectID(conversationID)
	canWriteVuln := principal.HasPermission("vulnerability:write") && piRoleAllowsTool(roleTools, builtin.ToolRecordVulnerability)
	canWriteAsset := principal.HasPermission("asset:write") && piRoleAllowsTool(roleTools, builtin.ToolCreateAsset)
	canWriteFact := projectID != "" && h.config != nil && h.config.Project.Enabled &&
		principal.HasPermission("project:write") && piRoleAllowsTool(roleTools, builtin.ToolUpsertProjectFact)

	existingVulns := h.loadPiExistingVulnerabilities(principal, conversationID, projectID)
	assetSeen := make(map[string]struct{})
	factSeen := make(map[string]struct{})
	targetFactSeen := make(map[string]struct{})
	assetScope := piAssetScopeFromRequest(request)

	for _, finding := range result.Findings {
		if !piFindingInScope(result, finding, assetScope) {
			continue
		}
		normalized, complete := normalizePiFindingForStorage(result, finding)
		if !complete {
			continue
		}

		var vulnID string
		if canWriteVuln {
			vulnID = findPiExistingVulnerability(existingVulns, normalized.title, normalized.target)
			if vulnID == "" {
				args := map[string]interface{}{
					"title":              normalized.title,
					"description":        normalized.description,
					"severity":           normalized.severity,
					"vulnerability_type": normalized.vulnerabilityType,
					"target":             normalized.target,
					"preconditions":      normalized.preconditions,
					"reproduction_steps": normalized.reproductionSteps,
					"evidence":           normalized.evidence,
					"impact":             normalized.impact,
					"recommendation":     normalized.recommendation,
					"retest_notes":       normalized.retestNotes,
				}
				if execution, err := h.agent.ExecuteMCPToolForConversation(authctx.WithPrincipal(ctx, principal), conversationID, builtin.ToolRecordVulnerability, args); err == nil && execution != nil && !execution.IsError {
					vulnID = piVulnerabilityIDFromResult(execution.Result)
					if vulnID == "" {
						vulnID = h.findPiVulnerabilityID(principal, conversationID, projectID, normalized.title, normalized.target)
					}
				} else if err != nil && h.logger != nil {
					h.logger.Warn("Pi 结果自动记录漏洞失败", zap.String("conversationId", conversationID), zap.String("title", normalized.title), zap.Error(err))
				}
			}
			if vulnID != "" && findPiExistingVulnerability(existingVulns, normalized.title, normalized.target) == "" {
				existingVulns = append(existingVulns, &database.Vulnerability{ID: vulnID, Title: normalized.title, Target: normalized.target})
			}
		}
		if vulnID != "" {
			links.Vulnerabilities = appendUniquePiVulnerability(links.Vulnerabilities, piLinkedVulnerability{ID: vulnID, Title: normalized.title})
		}

		candidateTargets := piFindingTargets(result, finding)
		var linkedAssetIDs []string
		for _, candidate := range candidateTargets {
			if !piAssetTargetInScope(candidate, assetScope) {
				continue
			}
			assetTarget, parsed := parsePiAssetTarget(candidate)
			if !parsed {
				continue
			}
			assetKey := piAssetKey(assetTarget)
			if _, seen := assetSeen[assetKey]; seen {
				continue
			}
			assetSeen[assetKey] = struct{}{}
			if !canWriteAsset {
				continue
			}
			args := piAssetArgs(assetTarget, projectID, normalized.title, candidate)
			execution, err := h.agent.ExecuteMCPToolForConversation(authctx.WithPrincipal(ctx, principal), conversationID, builtin.ToolCreateAsset, args)
			if err != nil || execution == nil || execution.IsError {
				if err != nil && h.logger != nil {
					h.logger.Warn("Pi 结果自动记录资产失败", zap.String("conversationId", conversationID), zap.String("target", candidate), zap.Error(err))
				}
				continue
			}
			assetID := piAssetIDFromResult(execution.Result)
			if assetID != "" {
				linkedAssetIDs = append(linkedAssetIDs, assetID)
				links.Assets = appendUniquePiAsset(links.Assets, piLinkedAsset{ID: assetID, Target: piAssetDisplayTarget(assetTarget)})
			}
		}

		if !canWriteFact {
			continue
		}
		findingKey := piStableFactKey("finding", normalized.title+"|"+normalized.target)
		if _, seen := factSeen[findingKey]; seen {
			continue
		}
		factSeen[findingKey] = struct{}{}
		linksIn := []interface{}{}
		for _, candidate := range candidateTargets {
			if !piAssetTargetInScope(candidate, assetScope) {
				continue
			}
			assetTarget, parsed := parsePiAssetTarget(candidate)
			if !parsed {
				continue
			}
			targetKey := piStableFactKey("target", piAssetDisplayTarget(assetTarget))
			if _, seen := targetFactSeen[targetKey]; !seen {
				targetFactSeen[targetKey] = struct{}{}
				h.persistPiFact(ctx, principal, conversationID, projectID, map[string]interface{}{
					"fact_key":   targetKey,
					"category":   "target",
					"summary":    piSummary(fmt.Sprintf("目标资产 %s 已在本轮测试中验证", piAssetDisplayTarget(assetTarget)), 200),
					"body":       fmt.Sprintf("目标：%s\n来源：Pi Agent\n本轮对话：%s\n资产 ID：%s", piAssetDisplayTarget(assetTarget), conversationID, strings.Join(linkedAssetIDs, ", ")),
					"confidence": "confirmed",
				})
			}
			linksIn = append(linksIn, map[string]interface{}{"from": targetKey, "type": "discovered_on", "confidence": "confirmed"})
			break
		}
		factArgs := map[string]interface{}{
			"fact_key":                 findingKey,
			"category":                 "finding",
			"summary":                  piSummary(normalized.title+"："+normalized.description, 200),
			"body":                     normalized.factBody(vulnID, linkedAssetIDs),
			"confidence":               "confirmed",
			"related_vulnerability_id": vulnID,
		}
		if len(linksIn) > 0 {
			factArgs["links"] = linksIn
		}
		if h.persistPiFact(ctx, principal, conversationID, projectID, factArgs) {
			links.Facts = appendUniquePiFact(links.Facts, piLinkedFact{ProjectID: projectID, FactKey: findingKey, Summary: piSummary(normalized.title, 160)})
		}
	}
	return links
}

type piStoredFinding struct {
	title, severity, target, description, vulnerabilityType                         string
	preconditions, reproductionSteps, evidence, impact, recommendation, retestNotes string
}

func normalizePiFindingForStorage(result piFinalResult, finding piFinding) (piStoredFinding, bool) {
	stored := piStoredFinding{
		title:             firstPiText(finding.Title, finding.Name, finding.Finding),
		severity:          normalizePiSeverity(finding.Severity),
		target:            firstPiText(finding.Target, finding.Location, result.Target),
		description:       firstPiText(finding.Finding, finding.Description),
		impact:            strings.TrimSpace(finding.Impact),
		recommendation:    firstPiText(finding.Remediation, finding.Recommendation),
		reproductionSteps: firstPiText(finding.Method, result.Method),
	}
	evidenceScope := result.Target
	stored.evidence = strings.Join(cleanPiEvidenceForRequest(finding.Evidence, evidenceScope), "\n- ")
	if stored.evidence == "" {
		stored.evidence = strings.Join(cleanPiEvidenceForRequest(result.Evidence, evidenceScope), "\n- ")
	}
	if stored.title == "" || stored.severity == "" || stored.target == "" || stored.description == "" || stored.reproductionSteps == "" || stored.evidence == "" || stored.impact == "" || stored.recommendation == "" {
		return stored, false
	}
	stored.vulnerabilityType = inferPiVulnerabilityType(stored.title, stored.description)
	stored.preconditions = "按当前测试上下文执行；如需认证或特定权限，以验证方法中的前置条件为准。"
	stored.retestNotes = "修复后重新执行上述复现步骤，确认异常响应、越权数据或注入效果不再出现。"
	return stored, true
}

func (f piStoredFinding) factBody(vulnerabilityID string, assetIDs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "结论：%s\n\n", f.description)
	fmt.Fprintf(&b, "目标与入口：%s\n\n", f.target)
	fmt.Fprintf(&b, "验证方法：%s\n\n", f.reproductionSteps)
	b.WriteString("关键证据：\n- ")
	b.WriteString(f.evidence)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "实际影响：%s\n\n", f.impact)
	fmt.Fprintf(&b, "修复建议：%s\n\n", f.recommendation)
	if vulnerabilityID != "" {
		fmt.Fprintf(&b, "关联漏洞 ID：%s\n", vulnerabilityID)
	}
	if len(assetIDs) > 0 {
		fmt.Fprintf(&b, "关联资产 ID：%s\n", strings.Join(assetIDs, ", "))
	}
	return safePiDisplayText(b.String(), 12000)
}

func (h *AgentHandler) persistPiFact(ctx context.Context, principal authctx.Principal, conversationID, projectID string, args map[string]interface{}) bool {
	execution, err := h.agent.ExecuteMCPToolForConversation(authctx.WithPrincipal(ctx, principal), conversationID, builtin.ToolUpsertProjectFact, args)
	if err != nil || execution == nil || execution.IsError {
		if err != nil && h.logger != nil {
			h.logger.Warn("Pi 结果自动写入项目事实失败", zap.String("conversationId", conversationID), zap.String("projectId", projectID), zap.Error(err))
		}
		return false
	}
	return true
}

// persistPiLoopEvidence records only observed targets and tentative facts when
// the loop guard stops a run. It intentionally never invents a vulnerability:
// confirmed findings must come from a structured result or record_vulnerability.
func (h *AgentHandler) persistPiLoopEvidence(ctx context.Context, principal authctx.Principal, conversationID, request string, roleTools []string, traces []string) piResultLinks {
	return h.persistPiStoppedEvidence(ctx, principal, conversationID, request, roleTools, traces, "连续重复验证")
}

// persistPiStoppedEvidence keeps only durable, user-authorized partial state
// when a Pi run stops before its structured final block. It deliberately does
// not create vulnerability records from unstructured tool output.
func (h *AgentHandler) persistPiStoppedEvidence(ctx context.Context, principal authctx.Principal, conversationID, request string, roleTools []string, traces []string, reason string) piResultLinks {
	var links piResultLinks
	if h == nil || h.agent == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(principal.UserID) == "" {
		return links
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "任务提前停止"
	}
	projectID := h.conversationProjectID(conversationID)
	// record_vulnerability is already durable when Pi calls it. Recover its ID
	// from the tool result so an auto-stopped run still links to the vulnerability
	// page instead of losing the result only because Pi did not emit a final block.
	var existingVulns []*database.Vulnerability
	existingVulnsLoaded := false
	for _, trace := range traces {
		if !strings.HasPrefix(strings.TrimSpace(trace), builtin.ToolRecordVulnerability+"：") {
			continue
		}
		if id := piVulnerabilityIDFromResult(trace); id != "" {
			if !existingVulnsLoaded {
				existingVulns = h.loadPiExistingVulnerabilities(principal, conversationID, projectID)
				existingVulnsLoaded = true
			}
			for _, vuln := range existingVulns {
				if vuln != nil && vuln.ID == id {
					links.Vulnerabilities = appendUniquePiVulnerability(links.Vulnerabilities, piLinkedVulnerability{ID: vuln.ID, Title: vuln.Title})
					break
				}
			}
		}
	}
	canWriteAsset := principal.HasPermission("asset:write") && piRoleAllowsTool(roleTools, builtin.ToolCreateAsset)
	canWriteFact := projectID != "" && h.config != nil && h.config.Project.Enabled &&
		principal.HasPermission("project:write") && piRoleAllowsTool(roleTools, builtin.ToolUpsertProjectFact)
	assetScope := piAssetScopeFromRequest(request)
	targets := piLoopTargets(request, traces)
	assetIDs := make([]string, 0, len(targets))
	for _, candidate := range targets {
		if !piAssetTargetInScope(candidate, assetScope) {
			continue
		}
		target, ok := parsePiAssetTarget(candidate)
		if !ok {
			continue
		}
		if canWriteAsset {
			execution, err := h.agent.ExecuteMCPToolForConversation(authctx.WithPrincipal(ctx, principal), conversationID, builtin.ToolCreateAsset, piAssetArgs(target, projectID, "Pi Agent 验证目标", candidate))
			if err == nil && execution != nil && !execution.IsError {
				if id := piAssetIDFromResult(execution.Result); id != "" {
					assetIDs = append(assetIDs, id)
					links.Assets = appendUniquePiAsset(links.Assets, piLinkedAsset{ID: id, Target: piAssetDisplayTarget(target)})
				}
			} else if err != nil && h.logger != nil {
				h.logger.Warn("Pi 循环收敛时保存资产失败", zap.String("conversationId", conversationID), zap.String("target", candidate), zap.Error(err))
			}
		}
	}
	if canWriteFact {
		factKey := piStableFactKey("loop", conversationID)
		body := fmt.Sprintf("Pi Agent 已因%s。\n\n目标：%s\n\n工具证据摘要：\n- %s\n\n关联资产 ID：%s\n\n说明：这是阶段性观察事实，不代表已确认漏洞。", reason, strings.Join(targets, "、"), strings.Join(uniquePiStrings(traces), "\n- "), strings.Join(assetIDs, ", "))
		if h.persistPiFact(ctx, principal, conversationID, projectID, map[string]interface{}{
			"fact_key":   factKey,
			"category":   "finding",
			"summary":    piSummary("Pi Agent 已"+reason+"，已保留阶段性证据", 200),
			"body":       safePiDisplayText(body, 12000),
			"confidence": "tentative",
		}) {
			links.Facts = appendUniquePiFact(links.Facts, piLinkedFact{ProjectID: projectID, FactKey: factKey, Summary: "重复验证收敛证据"})
		}
	}
	return links
}

func piLoopTargets(request string, traces []string) []string {
	scope := piAssetScopeFromRequest(request)
	values := make([]string, 0, 1+len(traces))
	values = append(values, piURLPattern.FindAllString(request, -1)...)
	for _, trace := range traces {
		for _, match := range piURLPattern.FindAllString(trace, -1) {
			values = append(values, match)
		}
	}
	filtered := make([]string, 0, len(values))
	for _, value := range uniquePiStrings(values) {
		if piAssetTargetInScope(value, scope) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func (h *AgentHandler) loadPiExistingVulnerabilities(principal authctx.Principal, conversationID, projectID string) []*database.Vulnerability {
	if h == nil || h.db == nil {
		return nil
	}
	filter := database.VulnerabilityListFilter{ConversationID: conversationID}
	if projectID != "" {
		filter = database.VulnerabilityListFilter{ProjectID: projectID}
	}
	items, err := h.db.ListVulnerabilitiesForAccess(200, 0, filter, database.RBACListAccess{UserID: principal.UserID, Scope: principal.ScopeFor("vulnerability:read")})
	if err != nil {
		return nil
	}
	return items
}

func (h *AgentHandler) findPiVulnerabilityID(principal authctx.Principal, conversationID, projectID, title, target string) string {
	for _, vuln := range h.loadPiExistingVulnerabilities(principal, conversationID, projectID) {
		if strings.EqualFold(strings.TrimSpace(vuln.Title), title) && strings.TrimSpace(vuln.Target) == strings.TrimSpace(target) {
			return vuln.ID
		}
	}
	return ""
}

func findPiExistingVulnerability(items []*database.Vulnerability, title, target string) string {
	for _, vuln := range items {
		if vuln != nil && strings.EqualFold(strings.TrimSpace(vuln.Title), title) && strings.TrimSpace(vuln.Target) == strings.TrimSpace(target) {
			return vuln.ID
		}
	}
	return ""
}

func piFindingTargets(result piFinalResult, finding piFinding) []string {
	var candidates []string
	if target := strings.TrimSpace(finding.Target); target != "" {
		candidates = append(candidates, target)
	}
	if location := strings.TrimSpace(finding.Location); location != "" {
		candidates = append(candidates, location)
	}
	if target := strings.TrimSpace(result.Target); target != "" {
		candidates = append(candidates, target)
	}
	return uniquePiStrings(candidates)
}

func piFindingInScope(result piFinalResult, finding piFinding, scope piAssetScope) bool {
	hasAbsoluteTarget := false
	for _, candidate := range []string{result.Target, finding.Target, finding.Location} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if targets := piScopeTargets(candidate); len(targets) > 0 {
			hasAbsoluteTarget = true
			for _, target := range targets {
				if !piAssetTargetInScope(target, scope) {
					return false
				}
			}
			continue
		}
		if _, parsed := parsePiAssetTarget(candidate); parsed {
			hasAbsoluteTarget = true
			if !piAssetTargetInScope(candidate, scope) {
				return false
			}
		}
	}
	// Relative locations are valid only when the structured result does not
	// provide a conflicting absolute target; the user request remains the scope.
	return hasAbsoluteTarget || len(scope.roots) > 0 || len(scope.ips) > 0
}

func piResultTargetInScope(target string, scope piAssetScope) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if _, parsed := parsePiAssetTarget(target); !parsed {
		return true
	}
	return piAssetTargetInScope(target, scope)
}

func parsePiAssetTarget(raw string) (piAssetTarget, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return piAssetTarget{}, false
	}
	if match := piURLPattern.FindString(raw); match != "" {
		raw = match
	}
	raw = strings.Trim(strings.TrimSpace(raw), "`'\".,;:，。；）)]}")
	if raw == "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "GET ") || strings.HasPrefix(raw, "POST ") {
		return piAssetTarget{}, false
	}
	parseValue := raw
	if !strings.Contains(parseValue, "://") {
		parseValue = "//" + parseValue
	}
	u, err := url.Parse(parseValue)
	if err != nil || u.Hostname() == "" {
		return piAssetTarget{}, false
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return piAssetTarget{}, false
	}
	protocol := strings.ToLower(strings.TrimSpace(u.Scheme))
	if protocol == "" {
		protocol = "tcp"
	}
	port := 0
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	} else if protocol == "https" {
		port = 443
	} else if protocol == "http" {
		port = 80
	}
	target := piAssetTarget{Raw: raw, Host: host, Port: port, Protocol: protocol}
	if ip := net.ParseIP(host); ip != nil {
		target.IP = strings.ToLower(ip.String())
	} else {
		target.Domain = strings.ToLower(strings.TrimSuffix(host, "."))
	}
	return target, target.IP != "" || target.Domain != "" || target.Host != ""
}

type piAssetScope struct {
	roots []string
	ips   map[string]struct{}
}

func piAssetScopeFromRequest(request string) piAssetScope {
	scope := piAssetScope{roots: make([]string, 0, 2), ips: make(map[string]struct{})}
	for _, raw := range piScopeTargets(request) {
		target, ok := parsePiAssetTarget(raw)
		if !ok {
			continue
		}
		if target.IP != "" {
			scope.ips[target.IP] = struct{}{}
			continue
		}
		if target.Domain == "" {
			continue
		}
		root := target.Domain
		if registered, err := publicsuffix.EffectiveTLDPlusOne(target.Domain); err == nil {
			root = strings.ToLower(registered)
		}
		if !containsPiString(scope.roots, root) {
			scope.roots = append(scope.roots, root)
		}
	}
	return scope
}

// piScopeTargets extracts targets only from user-provided scope text. Bare
// domains and IPs are supported because follow-up requests often say only
// "继续" while the original request contains a target without a URL scheme.
func piScopeTargets(request string) []string {
	values := append([]string{}, piURLPattern.FindAllString(request, -1)...)
	values = append(values, piBareTargetPattern.FindAllString(request, -1)...)
	return uniquePiStrings(values)
}

// piAssetTargetInScope prevents URLs copied from frontend bundles, libraries,
// documentation, SSO providers, localhost device bridges, and other external
// dependencies from becoming project assets. The project scope is derived from
// the user's original target, not from arbitrary URLs returned by a tool.
func piAssetTargetInScope(raw string, scope piAssetScope) bool {
	target, ok := parsePiAssetTarget(raw)
	if !ok {
		return false
	}
	if piIsLocalAssetTarget(target) {
		return false
	}
	if target.IP != "" {
		_, allowed := scope.ips[target.IP]
		return allowed
	}
	if target.Domain == "" || len(scope.roots) == 0 {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(target.Domain, "."))
	for _, root := range scope.roots {
		root = strings.ToLower(strings.TrimSuffix(root, "."))
		if host == root || strings.HasSuffix(host, "."+root) {
			return true
		}
	}
	return false
}

// piCapturedPacketInScope is used before packet evidence enters an agent
// prompt. It follows the user's frozen target scope, but permits explicitly
// selected loopback targets for local CTFs (unlike persistent asset writes).
func piCapturedPacketInScope(scheme, host string, port int, scope piAssetScope) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	raw := strings.TrimSpace(scheme) + "://" + host
	if port > 0 {
		raw = strings.TrimSpace(scheme) + "://" + net.JoinHostPort(host, strconv.Itoa(port))
	}
	target, ok := parsePiAssetTarget(raw)
	if !ok {
		return false
	}
	if target.IP != "" {
		_, allowed := scope.ips[target.IP]
		return allowed
	}
	if target.Domain == "" {
		return false
	}
	hostname := strings.ToLower(strings.TrimSuffix(target.Domain, "."))
	for _, root := range scope.roots {
		root = strings.ToLower(strings.TrimSuffix(root, "."))
		if hostname == root || strings.HasSuffix(hostname, "."+root) {
			return true
		}
	}
	return false
}

func piIsLocalAssetTarget(target piAssetTarget) bool {
	if strings.EqualFold(strings.TrimSuffix(target.Domain, "."), "localhost") {
		return true
	}
	if target.IP == "" {
		return false
	}
	ip := net.ParseIP(target.IP)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func containsPiString(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func piAssetArgs(target piAssetTarget, projectID, title, sourceQuery string) map[string]interface{} {
	args := map[string]interface{}{
		"project_id": projectID, "host": target.Host, "ip": target.IP, "domain": target.Domain,
		"port": target.Port, "protocol": target.Protocol, "title": title,
		"source": "pi_agent", "source_query": sourceQuery, "tags": []string{"pi-agent"},
	}
	if target.Port == 0 {
		delete(args, "port")
	}
	if projectID == "" {
		delete(args, "project_id")
	}
	return args
}

func piAssetKey(target piAssetTarget) string {
	identity := target.Domain
	if identity == "" {
		identity = target.IP
	}
	if identity == "" {
		identity = target.Host
	}
	return strings.ToLower(strings.Join([]string{identity, strconv.Itoa(target.Port), target.Protocol}, "|"))
}

func piAssetDisplayTarget(target piAssetTarget) string {
	base := target.Domain
	if base == "" {
		base = target.IP
	}
	if base == "" {
		base = target.Host
	}
	if target.Port > 0 {
		base += ":" + strconv.Itoa(target.Port)
	}
	if target.Protocol == "http" || target.Protocol == "https" {
		return target.Protocol + "://" + base
	}
	return base
}

func piRoleAllowsTool(roleTools []string, toolName string) bool {
	if len(roleTools) == 0 {
		return true
	}
	for _, item := range roleTools {
		if strings.TrimSpace(item) == toolName {
			return true
		}
	}
	return false
}

func parsePiFinalResult(raw string) (piFinalResult, bool) {
	block := extractPiFinalBlock(raw)
	if block == "" {
		return piFinalResult{}, false
	}
	var result piFinalResult
	if err := json.Unmarshal([]byte(stripJSONFence(block)), &result); err != nil {
		return piFinalResult{}, false
	}
	return result, true
}

func piVulnerabilityIDFromResult(result string) string {
	match := regexp.MustCompile(`(?i)(?:漏洞ID|vulnerability\s*id)\s*[:：]\s*([A-Za-z0-9_-]+)`).FindStringSubmatch(result)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func piAssetIDFromResult(result string) string {
	var payload struct {
		Asset struct {
			ID string `json:"id"`
		} `json:"asset"`
	}
	if json.Unmarshal([]byte(result), &payload) == nil {
		return strings.TrimSpace(payload.Asset.ID)
	}
	return ""
}

func firstPiText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return safePiDisplayText(value, 4000)
		}
	}
	return ""
}

func normalizePiSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "严重", "严重程度", "致命":
		return "critical"
	case "high", "高", "高危":
		return "high"
	case "medium", "中", "中危":
		return "medium"
	case "low", "低", "低危":
		return "low"
	case "info", "informational", "信息", "提示":
		return "info"
	default:
		return ""
	}
}

func inferPiVulnerabilityType(title, description string) string {
	text := strings.ToLower(title + " " + description)
	for _, item := range []struct{ key, value string }{
		{"sql", "SQL 注入"}, {"注入", "注入"}, {"xss", "跨站脚本（XSS）"}, {"跨站脚本", "跨站脚本（XSS）"},
		{"ssrf", "服务端请求伪造（SSRF）"}, {"越权", "越权访问"}, {"idor", "对象级授权缺失（IDOR）"},
		{"文件上传", "文件上传"}, {"命令执行", "命令注入"}, {"命令注入", "命令注入"}, {"路径遍历", "路径遍历"},
	} {
		if strings.Contains(text, item.key) {
			return item.value
		}
	}
	return "安全漏洞"
}

func piStableFactKey(prefix, seed string) string {
	seed = strings.TrimSpace(seed)
	slug := strings.ToLower(seed)
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "item"
	}
	if len(slug) > 70 {
		slug = slug[:70]
	}
	hash := sha256.Sum256([]byte(seed))
	return prefix + "/" + slug + "-" + hex.EncodeToString(hash[:])[:10]
}

func piSummary(value string, max int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	return safePiDisplayText(value, max)
}

func uniquePiStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func appendUniquePiVulnerability(items []piLinkedVulnerability, item piLinkedVulnerability) []piLinkedVulnerability {
	for _, existing := range items {
		if existing.ID == item.ID {
			return items
		}
	}
	return append(items, item)
}

func appendUniquePiAsset(items []piLinkedAsset, item piLinkedAsset) []piLinkedAsset {
	for _, existing := range items {
		if existing.ID == item.ID {
			return items
		}
	}
	return append(items, item)
}

func appendUniquePiFact(items []piLinkedFact, item piLinkedFact) []piLinkedFact {
	for _, existing := range items {
		if existing.ProjectID == item.ProjectID && existing.FactKey == item.FactKey {
			return items
		}
	}
	return append(items, item)
}

// appendPiResultLinks adds only navigable UI links to the already-normalized
// response. Raw IDs remain hidden in prose and are used only in hash routes.
func appendPiResultLinks(rendered string, links piResultLinks) string {
	if len(links.Vulnerabilities) == 0 && len(links.Assets) == 0 && len(links.Facts) == 0 {
		return rendered
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(rendered))
	b.WriteString("\n\n## 关联资源\n\n")
	for _, item := range links.Vulnerabilities {
		fmt.Fprintf(&b, "- 漏洞：[%s](#vulnerabilities?id=%s)\n", safePiDisplayText(item.Title, 220), url.QueryEscape(item.ID))
	}
	for _, item := range links.Assets {
		fmt.Fprintf(&b, "- 资产：[%s](#asset-library?q=%s)\n", safePiDisplayText(item.Target, 220), url.QueryEscape(item.Target))
	}
	for _, item := range links.Facts {
		fmt.Fprintf(&b, "- 项目事实：[%s](#projects?id=%s&fact_key=%s)\n", safePiDisplayText(item.Summary, 220), url.QueryEscape(item.ProjectID), url.QueryEscape(item.FactKey))
	}
	return strings.TrimSpace(b.String())
}
