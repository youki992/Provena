package handler

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/piagent"
)

func TestPlanPiFGSARLBootstrapBuildsExplicitAssetScan(t *testing.T) {
	request := "已授权。使用 ARL mcp 对 tuhu.cn 做完整资产收集：启用大域名字典爆破、端口 Top1000、服务识别、证书收集、DNS 插件、历史资产查询"
	plan, err := planPiFGSARLBootstrap(request, []piagent.BridgeTool{
		{Name: "arl__arl_health"}, {Name: "arl__arl_submit_scan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "tuhu.cn" || plan.HealthTool != "arl__arl_health" || plan.ScanTool != "arl__arl_submit_scan" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	want := map[string]interface{}{
		"enable_dict_bruteforce": true, "port_scan_top": 1000, "service_detection": true,
		"cert_collection": true, "dns_plugins": true, "historical_assets": true,
	}
	if !reflect.DeepEqual(plan.Options, want) {
		t.Fatalf("options = %#v, want %#v", plan.Options, want)
	}
}

func TestPlanPiFGSARLBootstrapRequiresExplicitAuthorization(t *testing.T) {
	plan, err := planPiFGSARLBootstrap("使用 ARL mcp 对 example.test 做资产收集", []piagent.BridgeTool{{Name: "arl__arl_submit_scan"}})
	if err != nil || plan.ScanTool != "" {
		t.Fatalf("unauthorized-looking request unexpectedly planned: plan=%+v err=%v", plan, err)
	}
}

func TestPiFGSARLBootstrapRequestResumesExplicitScanGoal(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "已授权。使用 ARL mcp 对 example.test 做资产收集并提交扫描")
	if err != nil {
		t.Fatal(err)
	}
	got := piFGSARLBootstrapRequest("继续", graph)
	if got != graph.Snapshot().Goal {
		t.Fatalf("continuation request = %q, want original goal %q", got, graph.Snapshot().Goal)
	}
}

func TestExecutePiFGSARLBootstrapRecordsHealthAndSubmission(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "已授权的资产收集")
	if err != nil {
		t.Fatal(err)
	}
	plan := piFGSARLBootstrapPlan{
		Target: "example.test", Name: "example.test 资产收集", HealthTool: "arl__arl_health", ScanTool: "arl__arl_submit_scan",
		Options: map[string]interface{}{"service_detection": true},
	}
	var calls []string
	result := executePiFGSARLBootstrap(context.Background(), graph, plan, func(_ context.Context, tool string, args map[string]interface{}) (*agent.ToolExecutionResult, error) {
		calls = append(calls, tool)
		if tool == "arl__arl_submit_scan" {
			if args["target"] != "example.test" {
				t.Fatalf("scan target = %#v", args["target"])
			}
			return &agent.ToolExecutionResult{ExecutionID: "scan-1", Result: `{"task_id":"scan-1"}`}, nil
		}
		return &agent.ToolExecutionResult{Result: `{"ok":true}`}, nil
	})
	if !result.Handled || !result.Started {
		t.Fatalf("bootstrap result = %+v", result)
	}
	if !reflect.DeepEqual(calls, []string{"arl__arl_health", "arl__arl_submit_scan"}) {
		t.Fatalf("calls = %#v", calls)
	}

	var healthFact, scanFact, completedStep bool
	for _, node := range graph.Snapshot().Nodes {
		if node.Kind == fgs.KindFact && node.Label == "ARL 结果 · arl__arl_health" {
			healthFact = true
		}
		if node.Kind == fgs.KindFact && node.Label == "ARL 结果 · arl__arl_submit_scan" {
			scanFact = true
		}
		if node.Kind == fgs.KindStep && node.Label == "ARL MCP 资产扫描" && node.Status == fgs.StatusCompleted {
			completedStep = true
		}
	}
	if !healthFact || !scanFact || !completedStep {
		t.Fatalf("FGS did not preserve ARL bootstrap evidence: %+v", graph.Snapshot())
	}
}

func TestExecutePiFGSARLBootstrapDoesNotSubmitDuplicate(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "已授权的资产收集")
	if err != nil {
		t.Fatal(err)
	}
	if err := recordARLResultToFGS(graph, "", "arl__arl_submit_scan", map[string]interface{}{"target": "example.test"}, &agent.ToolExecutionResult{Result: `{"task_id":"old"}`}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result := executePiFGSARLBootstrap(context.Background(), graph, piFGSARLBootstrapPlan{Target: "example.test", ScanTool: "arl__arl_submit_scan"}, func(context.Context, string, map[string]interface{}) (*agent.ToolExecutionResult, error) {
		calls++
		return nil, nil
	})
	if !result.Handled || !result.Started || calls != 0 {
		t.Fatalf("duplicate bootstrap = %+v, calls=%d", result, calls)
	}
}
