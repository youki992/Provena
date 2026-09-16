package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/piagent"
)

// piFGSARLBootstrapPlan is the small deterministic part of an explicitly
// requested ARL asset-discovery run.  It exists so a model refusing an
// exposed active tool cannot silently turn an authorized scan request into an
// empty FGS round.
type piFGSARLBootstrapPlan struct {
	Target     string
	Name       string
	Options    map[string]interface{}
	HealthTool string
	ScanTool   string
}

type piFGSARLBootstrapResult struct {
	Handled bool
	Started bool
	Message string
}

// piFGSRequestsExplicitARLAssetScan deliberately has a narrow trigger.  This
// path is not a replacement for normal FGS tool selection: it applies only
// when the user both declares authorization and explicitly asks ARL to do
// asset collection or scanning.
func piFGSRequestsExplicitARLAssetScan(request string) bool {
	lower := strings.ToLower(strings.TrimSpace(request))
	if lower == "" || !strings.Contains(lower, "arl") {
		return false
	}
	if !strings.Contains(lower, "已授权") && !strings.Contains(lower, "授权") {
		return false
	}
	return containsAnyString(lower,
		"资产收集", "资产发现", "资产侦察", "资产测绘", "资产扫描", "完整资产", "扫描目标", "提交扫描",
	)
}

// piFGSARLBootstrapRequest also recognizes a bare continuation of a pending
// graph whose original goal was an explicit ARL scan.  Without this, the user
// would need to paste the full authorization sentence again after an earlier
// no-op run instead of simply sending “继续”.
func piFGSARLBootstrapRequest(request string, graph *fgs.Store) string {
	if piFGSRequestsExplicitARLAssetScan(request) {
		return request
	}
	if graph == nil || !isPiFGSContinuationRequest(request) {
		return ""
	}
	if goal := strings.TrimSpace(graph.Snapshot().Goal); piFGSRequestsExplicitARLAssetScan(goal) {
		return goal
	}
	return ""
}

func planPiFGSARLBootstrap(request string, tools []piagent.BridgeTool) (piFGSARLBootstrapPlan, error) {
	if !piFGSRequestsExplicitARLAssetScan(request) {
		return piFGSARLBootstrapPlan{}, nil
	}
	plan := piFGSARLBootstrapPlan{}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		switch strings.ToLower(name) {
		case "arl__arl_health", "arl::arl_health":
			plan.HealthTool = name
		case "arl__arl_submit_scan", "arl::arl_submit_scan":
			plan.ScanTool = name
		}
	}
	if plan.ScanTool == "" {
		return piFGSARLBootstrapPlan{}, fmt.Errorf("ARL MCP 未提供已启用的 arl_submit_scan 工具")
	}

	targets := make([]string, 0, 1)
	seen := make(map[string]struct{})
	for _, raw := range piScopeTargets(request) {
		target, ok := parsePiAssetTarget(raw)
		if !ok || piIsLocalAssetTarget(target) {
			continue
		}
		candidate := strings.TrimSpace(target.Domain)
		if candidate == "" {
			candidate = strings.TrimSpace(target.IP)
		}
		candidate = strings.ToLower(strings.TrimSuffix(candidate, "."))
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		targets = append(targets, candidate)
	}
	if len(targets) != 1 {
		return piFGSARLBootstrapPlan{}, fmt.Errorf("明确 ARL 扫描需要且只能包含一个非本机目标，当前识别到 %d 个", len(targets))
	}

	lower := strings.ToLower(request)
	options := make(map[string]interface{})
	if containsAnyString(lower, "大域名字典", "字典爆破", "字典枚举") {
		options["enable_dict_bruteforce"] = true
	}
	if containsAnyString(lower, "top1000", "top 1000", "端口1000", "端口 1000") {
		options["port_scan_top"] = 1000
	}
	if containsAnyString(lower, "服务识别", "服务探测") {
		options["service_detection"] = true
	}
	if containsAnyString(lower, "证书收集", "证书枚举", "证书查询") {
		options["cert_collection"] = true
	}
	if containsAnyString(lower, "dns 插件", "dns插件") {
		options["dns_plugins"] = true
	}
	if containsAnyString(lower, "历史资产", "历史查询") {
		options["historical_assets"] = true
	}
	plan.Target = targets[0]
	plan.Name = plan.Target + " 资产收集"
	plan.Options = options
	return plan, nil
}

func containsAnyString(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func piFGSAlreadySubmittedARLScan(graph *fgs.Store, target string) bool {
	if graph == nil {
		return false
	}
	target = strings.ToLower(strings.TrimSpace(target))
	for _, node := range graph.Snapshot().Nodes {
		isScanResult := false
		for _, evidence := range node.Evidence {
			if strings.EqualFold(strings.TrimSpace(evidence), "tool=arl__arl_submit_scan") || strings.EqualFold(strings.TrimSpace(evidence), "tool=arl::arl_submit_scan") {
				isScanResult = true
				break
			}
		}
		if isScanResult && strings.Contains(strings.ToLower(node.Content), target) {
			return true
		}
	}
	return false
}

// executePiFGSARLBootstrap runs the health check and submission through the
// same Agent MCP path as a Pi bridge.  The executor is injected so the policy
// and graph behavior can be regression-tested without contacting ARL.
func executePiFGSARLBootstrap(
	ctx context.Context,
	graph *fgs.Store,
	plan piFGSARLBootstrapPlan,
	execute func(context.Context, string, map[string]interface{}) (*agent.ToolExecutionResult, error),
) piFGSARLBootstrapResult {
	if graph == nil || execute == nil || strings.TrimSpace(plan.ScanTool) == "" || strings.TrimSpace(plan.Target) == "" {
		return piFGSARLBootstrapResult{}
	}
	if piFGSAlreadySubmittedARLScan(graph, plan.Target) {
		return piFGSARLBootstrapResult{Handled: true, Started: true, Message: "ARL 扫描已在当前 FGS 图中提交；未重复创建任务。发送“继续”可查询进度和结果。"}
	}

	stepID := "step-arl-bootstrap-" + shortRand(12)
	if _, err := graph.Apply([]fgs.Mutation{
		{Op: "add_node", ID: stepID, Kind: fgs.KindStep, Label: "ARL MCP 资产扫描", Content: "服务端执行已授权的 ARL 健康检查与资产扫描提交。", Status: fgs.StatusActive, Priority: 100},
		{Op: "add_edge", From: "goal", To: stepID, Relation: "next"},
	}); err != nil {
		return piFGSARLBootstrapResult{Handled: true, Message: "无法在 FGS 中创建 ARL 扫描步骤：" + err.Error()}
	}

	call := func(tool string, args map[string]interface{}) (*agent.ToolExecutionResult, error) {
		result, err := execute(ctx, tool, args)
		if err != nil {
			result = &agent.ToolExecutionResult{Result: "ARL MCP 调用失败：" + err.Error(), IsError: true}
		}
		if result == nil {
			result = &agent.ToolExecutionResult{Result: "（工具无输出）", IsError: true}
		}
		_ = recordARLResultToFGS(graph, stepID, tool, args, result)
		return result, err
	}

	if plan.HealthTool != "" {
		health, _ := call(plan.HealthTool, map[string]interface{}{})
		if health.IsError {
			_, _ = graph.Apply([]fgs.Mutation{{Op: "set_status", ID: stepID, Status: fgs.StatusBlocked}})
			return piFGSARLBootstrapResult{Handled: true, Message: "ARL MCP 健康检查失败，未提交扫描；详情已记录到 FGS。"}
		}
	}

	args := map[string]interface{}{"target": plan.Target, "name": plan.Name}
	if len(plan.Options) > 0 {
		args["options"] = plan.Options
	}
	result, _ := call(plan.ScanTool, args)
	if result.IsError {
		_, _ = graph.Apply([]fgs.Mutation{{Op: "set_status", ID: stepID, Status: fgs.StatusBlocked}})
		return piFGSARLBootstrapResult{Handled: true, Message: "ARL MCP 已连通但扫描提交失败；详情已记录到 FGS。"}
	}
	_ = completePiFGSStep(graph, stepID)
	return piFGSARLBootstrapResult{Handled: true, Started: true, Message: "ARL MCP 已完成健康检查并提交资产扫描。扫描为异步任务；发送“继续”可拉取状态和结果。"}
}
