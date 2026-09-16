package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/fgs"
)

// auditPiFGSRound is the external controller boundary for an Execute round.
// A Step cannot be accepted as completed merely because the model returned;
// it must have a linked, non-empty Fact/Finding with evidence. This check is
// deliberately independent of Pi's prompt and therefore remains effective
// when the model emits prose, omits stepId, or records an empty/no-op result.
func auditPiFGSRound(graph *fgs.Store, result piFGSActivityResult) error {
	if graph == nil {
		return fmt.Errorf("FGS store 未初始化")
	}
	snapshot := graph.Snapshot()
	nodes := make(map[string]fgs.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.ID] = node
	}
	issues := make([]string, 0)
	for _, stepID := range result.AssignedStepIDs {
		stepID = strings.TrimSpace(stepID)
		if stepID == "" {
			continue
		}
		step, ok := nodes[stepID]
		if !ok || step.Kind != fgs.KindStep {
			continue
		}
		if step.Status != fgs.StatusCompleted {
			continue
		}
		validEvidence := false
		noOpOnly := true
		for _, edge := range snapshot.Edges {
			if edge.From != stepID {
				continue
			}
			target, exists := nodes[edge.To]
			if !exists || (target.Kind != fgs.KindFact && target.Kind != fgs.KindFinding) {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(target.Label), "Execute 空跑") {
				continue
			}
			noOpOnly = false
			if strings.TrimSpace(target.Content) != "" && len(target.Evidence) > 0 {
				validEvidence = true
				break
			}
		}
		if validEvidence {
			continue
		}

		// Reject the status written by the model/harness and preserve the reason
		// as a controller Fact. Blocked is intentional: it prevents Decide from
		// mistaking an unverified completion for successful progress.
		if _, err := graph.Apply([]fgs.Mutation{{Op: "set_status", ID: stepID, Status: fgs.StatusBlocked}}); err != nil {
			issues = append(issues, stepID+": 无法阻止无证据完成")
			continue
		}
		label := "Controller 审计：Step 缺少可验证证据"
		if noOpOnly {
			label = "Controller 审计：Step 仅产生空跑记录"
		}
		content := fmt.Sprintf("外部证据控制器拒绝 Step %s 的 completed 状态：必须存在由该 Step 产生、内容非空且带 evidence 的 Fact/Finding。当前结果未满足验证条件；请在 Decide 中改用可执行路径。", stepID)
		_, _ = graph.SubmitFact(label, content, stepID, []string{"controller=fgs_round_audit", "evidence_required=true"})
		issues = append(issues, stepID+": completed 被降级为 blocked")
	}
	if result.WorkerCount > 0 && result.WorldToolCalls == 0 {
		issues = append(issues, "本轮没有世界工具调用")
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(issues, "；"))
}

// isARLBridgeTool identifies the v3 ARL MCP namespace after the OpenAI-safe
// name conversion (arl::tool -> arl__tool). Keeping this check local to the
// controller prevents unrelated MCP observations from being misclassified.
func isARLBridgeTool(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	return strings.HasPrefix(name, "arl__") || strings.HasPrefix(name, "arl::")
}

// blockPiFGSStepsForUnavailableARL prevents a transient MCP discovery outage
// from being treated as a successful model execution. The event log records
// the reason and leaves all prior evidence intact for a later resumed run.
func blockPiFGSStepsForUnavailableARL(graph *fgs.Store, reason string) int {
	if graph == nil {
		return 0
	}
	blocked := 0
	for _, node := range graph.Snapshot().Nodes {
		if node.Kind != fgs.KindStep || (node.Status != fgs.StatusPending && node.Status != fgs.StatusActive) {
			continue
		}
		if _, err := graph.Apply([]fgs.Mutation{{Op: "set_status", ID: node.ID, Status: fgs.StatusBlocked}}); err != nil {
			continue
		}
		_, _ = graph.SubmitFact("Controller：ARL MCP 不可用", reason, node.ID, []string{"source=controller", "arl_tools=0", "retry_required=true"})
		blocked++
	}
	return blocked
}

// recordARLResultToFGS turns one completed ARL MCP call into one durable FGS
// Fact. The fact is an observation of the tool boundary, not a vulnerability
// claim. A content hash makes retries idempotent while preserving the full
// append-only event history.
func recordARLResultToFGS(graph *fgs.Store, stepID, toolName string, args map[string]interface{}, result *agent.ToolExecutionResult) error {
	if graph == nil {
		return fmt.Errorf("FGS store 未初始化")
	}
	toolName = strings.TrimSpace(toolName)
	if !isARLBridgeTool(toolName) {
		return nil
	}
	if result == nil {
		result = &agent.ToolExecutionResult{Result: "（工具无输出）"}
	}
	argsJSON, _ := json.Marshal(args)
	if len(argsJSON) == 0 {
		argsJSON = []byte("{}")
	}
	resultText := strings.TrimSpace(result.Result)
	if resultText == "" {
		resultText = "（工具无输出）"
	}
	if len([]rune(resultText)) > 12000 {
		resultText = string([]rune(resultText)[:12000]) + "…"
	}
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte("\n"))
	h.Write(argsJSON)
	h.Write([]byte("\n"))
	h.Write([]byte(resultText))
	h.Write([]byte(fmt.Sprintf("\nerror=%t", result.IsError)))
	hash := hex.EncodeToString(h.Sum(nil))[:20]

	for _, node := range graph.Snapshot().Nodes {
		for _, evidence := range node.Evidence {
			if evidence == "arl_result_hash:"+hash {
				return nil
			}
		}
	}

	label := "ARL 结果 · " + toolName
	content := fmt.Sprintf("来源：ARL MCP\n工具：%s\n参数：%s\n是否错误：%t\n结果：\n%s\n\n结果哈希：%s", toolName, string(argsJSON), result.IsError, resultText, hash)
	evidence := []string{"source=arl", "tool=" + toolName, "arl_result_hash:" + hash}
	if result.ExecutionID != "" {
		evidence = append(evidence, "execution_id="+result.ExecutionID)
	}
	_, err := graph.SubmitFact(label, content, strings.TrimSpace(stepID), evidence)
	return err
}
