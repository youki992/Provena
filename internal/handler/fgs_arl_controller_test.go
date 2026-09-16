package handler

import (
	"path/filepath"
	"testing"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/fgs"
)

func TestARLResultIsDurablyRecordedOnce(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-arl", Kind: fgs.KindStep, Status: fgs.StatusActive}}); err != nil {
		t.Fatal(err)
	}
	result := &agent.ToolExecutionResult{ExecutionID: "exec-1", Result: `{"assets":["api.example.test"]}`}
	args := map[string]interface{}{"target": "example.test"}
	if err := recordARLResultToFGS(graph, "step-arl", "arl__arl_submit_scan", args, result); err != nil {
		t.Fatal(err)
	}
	if err := recordARLResultToFGS(graph, "step-arl", "arl__arl_submit_scan", args, result); err != nil {
		t.Fatal(err)
	}
	var arlFacts int
	for _, node := range graph.Snapshot().Nodes {
		for _, evidence := range node.Evidence {
			if evidence == "source=arl" {
				arlFacts++
			}
		}
	}
	if arlFacts != 1 {
		t.Fatalf("ARL result facts = %d, want one idempotent Fact", arlFacts)
	}
}

func TestControllerBlocksCompletedNoOpStep(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-noop", Kind: fgs.KindStep, Status: fgs.StatusCompleted}}); err != nil {
		t.Fatal(err)
	}
	if err := recordPiFGSNoOpFact(graph, "step-noop"); err != nil {
		t.Fatal(err)
	}
	_ = auditPiFGSRound(graph, piFGSActivityResult{WorkerCount: 1, AssignedStepIDs: []string{"step-noop"}})
	for _, node := range graph.Snapshot().Nodes {
		if node.ID == "step-noop" && node.Status != fgs.StatusBlocked {
			t.Fatalf("no-op step status = %s, want blocked", node.Status)
		}
	}
}

func TestControllerBlocksTimedOutWorkerStep(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-timeout", Kind: fgs.KindStep, Status: fgs.StatusActive}}); err != nil {
		t.Fatal(err)
	}
	if err := recordPiFGSTimeoutForStep(graph, "step-timeout", 7); err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	var foundFact bool
	for _, node := range snapshot.Nodes {
		if node.ID == "step-timeout" && node.Status != fgs.StatusBlocked {
			t.Fatalf("timed out step status = %s, want blocked", node.Status)
		}
		if node.Kind == fgs.KindFact && node.Label == "Execute 超时：Pi Worker 未在单轮时限内返回" {
			foundFact = true
			if len(node.Evidence) == 0 {
				t.Fatal("timeout fact has no evidence")
			}
		}
	}
	if !foundFact {
		t.Fatal("timeout observation fact was not recorded")
	}
}

func TestControllerAcceptsARLEvidenceForCompletedStep(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-ok", Kind: fgs.KindStep, Status: fgs.StatusCompleted}}); err != nil {
		t.Fatal(err)
	}
	if err := recordARLResultToFGS(graph, "step-ok", "arl__arl_health", nil, &agent.ToolExecutionResult{Result: `{"ok":true}`}); err != nil {
		t.Fatal(err)
	}
	if err := auditPiFGSRound(graph, piFGSActivityResult{WorkerCount: 1, AssignedStepIDs: []string{"step-ok"}, WorldToolCalls: 1}); err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Snapshot().Nodes {
		if node.ID == "step-ok" && node.Status != fgs.StatusCompleted {
			t.Fatalf("evidence-backed step status = %s, want completed", node.Status)
		}
	}
}

func TestEvaluatorCannotPromoteUnknownFact(t *testing.T) {
	snapshot := fgs.Snapshot{Nodes: []fgs.Node{{ID: "fact-1", Kind: fgs.KindFact, Content: "evidence", Evidence: []string{"ref"}}}}
	d := piSupervisorDecision{Action: "continue", EvidenceVerdict: "accepted", PromotableFactIDs: []string{"missing"}}
	if !validateFGSEvaluatorDecision(&d, snapshot) {
		t.Fatal("validator should sanitize instead of failing the round")
	}
	if d.EvidenceVerdict != "insufficient" || len(d.PromotableFactIDs) != 0 {
		t.Fatalf("unexpected sanitized evaluator decision: %+v", d)
	}
}

func TestEvaluatorCannotPromoteFailedARLResult(t *testing.T) {
	snapshot := fgs.Snapshot{Nodes: []fgs.Node{{ID: "fact-error", Kind: fgs.KindFact, Content: "是否错误：true\n结果：timeout", Evidence: []string{"source=arl", "execution_id=failed"}}}}
	d := piSupervisorDecision{Action: "continue", EvidenceVerdict: "accepted", PromotableFactIDs: []string{"fact-error"}}
	if !validateFGSEvaluatorDecision(&d, snapshot) {
		t.Fatal("validator should sanitize failed ARL evidence")
	}
	if d.EvidenceVerdict != "insufficient" || len(d.PromotableFactIDs) != 0 {
		t.Fatalf("failed ARL result became promotable: %+v", d)
	}
}
