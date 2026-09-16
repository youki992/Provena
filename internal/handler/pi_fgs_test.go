package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/mcp/builtin"
)

func TestPiFGSBridgeReadsAppliesAndSubmitsFacts(t *testing.T) {
	store, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "完成一个目标")
	if err != nil {
		t.Fatal(err)
	}
	b := &piBridge{
		token:   "expected",
		tools:   map[string]struct{}{"fgs_read": {}, "fgs_apply": {}, "submit_fact": {}},
		baseCtx: context.Background(),
		fgs:     store,
	}

	status, body := callPiFGSTestTool(t, b, map[string]interface{}{
		"toolName": "fgs_apply",
		"args": map[string]interface{}{
			"mutations": []interface{}{
				map[string]interface{}{"op": "add_node", "id": "step-1", "kind": "step", "label": "观察"},
				map[string]interface{}{"op": "add_edge", "from": "goal", "to": "step-1", "relation": "next"},
			},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("fgs_apply status = %d, body = %s", status, body)
	}

	status, body = callPiFGSTestTool(t, b, map[string]interface{}{
		"toolName": "submit_fact",
		"args": map[string]interface{}{
			"label":    "观察结果",
			"content":  "观察到一个可复核事实",
			"stepId":   "step-1",
			"evidence": []interface{}{"证据片段"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("submit_fact status = %d, body = %s", status, body)
	}

	snapshot := store.Snapshot()
	if len(snapshot.Nodes) != 4 || len(snapshot.Edges) != 3 {
		t.Fatalf("snapshot after bridge calls = %+v", snapshot)
	}

	status, body = callPiFGSTestTool(t, b, map[string]interface{}{"toolName": "fgs_read", "args": map[string]interface{}{}})
	if status != http.StatusOK {
		t.Fatalf("fgs_read status = %d, body = %s", status, body)
	}
	var response piBridgeCallResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode fgs_read response: %v", err)
	}
	var readSnapshot fgs.Snapshot
	if err := json.Unmarshal([]byte(response.Result), &readSnapshot); err != nil {
		t.Fatalf("decode fgs_read result: %v", err)
	}
	if readSnapshot.Version != snapshot.Version {
		t.Fatalf("read version = %d, want %d", readSnapshot.Version, snapshot.Version)
	}
	reopened, err := fgs.Open(store.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	reopenedSnapshot := reopened.Snapshot()
	if len(reopenedSnapshot.Nodes) != len(snapshot.Nodes) || reopenedSnapshot.Version != snapshot.Version {
		t.Fatalf("reopened FGS lost submitted fact: reopened=%+v original=%+v", reopenedSnapshot, snapshot)
	}
	var foundFact bool
	for _, node := range reopenedSnapshot.Nodes {
		if node.Kind == fgs.KindFact && node.Label == "观察结果" && node.Content == "观察到一个可复核事实" {
			foundFact = true
			break
		}
	}
	if !foundFact {
		t.Fatalf("reopened FGS does not contain submitted fact: %+v", reopenedSnapshot.Nodes)
	}
}

func TestPiFGSExecuteBridgeCannotApplyGraphMutations(t *testing.T) {
	store, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "完成一个目标")
	if err != nil {
		t.Fatal(err)
	}
	before := store.Version()
	b := &piBridge{
		token:   "expected",
		tools:   map[string]struct{}{"fgs_read": {}, "submit_fact": {}},
		baseCtx: context.Background(),
		fgs:     store,
	}
	status, body := callPiFGSTestTool(t, b, map[string]interface{}{
		"toolName": "fgs_apply",
		"args":     map[string]interface{}{"mutations": []interface{}{}},
	})
	if status != http.StatusForbidden {
		t.Fatalf("execute fgs_apply status = %d, body = %s", status, body)
	}
	if store.Version() != before {
		t.Fatalf("forbidden fgs_apply changed graph version from %d to %d", before, store.Version())
	}
}

func TestPiFGSRunWorkspaceIsolatedPerRequest(t *testing.T) {
	base := t.TempDir()
	first, firstID, err := ensurePiFGSRunWorkspace(base, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	second, secondID, err := ensurePiFGSRunWorkspace(base, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || firstID == secondID {
		t.Fatalf("FGS runs reused workspace: first=%q/%q second=%q/%q", first, firstID, second, secondID)
	}
	if filepath.Base(first) != firstID || filepath.Base(second) != secondID {
		t.Fatalf("run directory does not identify its run: %q, %q", first, second)
	}
}

func TestPiFGSConversationGraphResumesUnfinishedRun(t *testing.T) {
	base := t.TempDir()
	runDir, runID, err := ensurePiFGSRunWorkspace(base, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	store, err := fgs.Open(filepath.Join(runDir, "graph.jsonl"), "检查 https://target.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-1", Kind: fgs.KindStep, Status: fgs.StatusActive}}); err != nil {
		t.Fatal(err)
	}

	workingDir, gotRunID, errStore, resumed, err := openPiFGSConversationGraph(base, "conversation-1", "继续检查 https://target.test")
	if err != nil {
		t.Fatal(err)
	}
	if !resumed || gotRunID != runID || filepath.Clean(workingDir) != filepath.Clean(runDir) || errStore.Version() != store.Version() {
		t.Fatalf("unfinished graph was not resumed: dir=%q run=%q resumed=%v version=%d want=%q/%q/%d", workingDir, gotRunID, resumed, errStore.Version(), runDir, runID, store.Version())
	}
}

func TestPiFGSConversationHasResumableGraphForBareContinuation(t *testing.T) {
	base := t.TempDir()
	runDir, _, err := ensurePiFGSRunWorkspace(base, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	store, err := fgs.Open(filepath.Join(runDir, "graph.jsonl"), "检查 https://target.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-1", Kind: fgs.KindStep, Status: fgs.StatusActive}}); err != nil {
		t.Fatal(err)
	}
	found, err := piFGSConversationHasResumableGraph(base, "conversation-1", "继续")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("bare continuation did not find unfinished FGS graph")
	}
}

func TestPiFGSConversationHasNoResumableGraphForUnrelatedQuestion(t *testing.T) {
	base := t.TempDir()
	runDir, _, err := ensurePiFGSRunWorkspace(base, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	store, err := fgs.Open(filepath.Join(runDir, "graph.jsonl"), "检查 https://target.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-1", Kind: fgs.KindStep, Status: fgs.StatusActive}}); err != nil {
		t.Fatal(err)
	}
	found, err := piFGSConversationHasResumableGraph(base, "conversation-1", "你是谁")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("unrelated question unexpectedly found resumable FGS graph")
	}
}

func TestPiFGSTargetScopeOverlapIncludesSubdomains(t *testing.T) {
	if !piFGSTargetsOverlap("检查 https://api.target.test", "继续检查 https://admin.target.test") {
		t.Fatal("same registered target scope should resume across subdomains")
	}
	if piFGSTargetsOverlap("检查 https://target.test", "检查 https://other.test") {
		t.Fatal("different target scopes should not overlap")
	}
}

func TestPiFGSDirectionChangeResumesExistingGraph(t *testing.T) {
	store, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "测试 https://target.test")
	if err != nil {
		t.Fatal(err)
	}
	if !shouldResumePiFGSGraph(store.Snapshot(), "除了 GraphQL，都可以测挖洞") {
		t.Fatal("direction change with an explicit exclusion did not resume the existing graph")
	}
}

func TestPiFGSConversationGraphStartsFreshForTerminalOrUnrelatedRun(t *testing.T) {
	base := t.TempDir()
	runDir, oldRunID, err := ensurePiFGSRunWorkspace(base, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	store, err := fgs.Open(filepath.Join(runDir, "graph.jsonl"), "检查 https://target.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]fgs.Mutation{{Op: "set_status", ID: "goal", Status: fgs.StatusCompleted}}); err != nil {
		t.Fatal(err)
	}
	_, newRunID, newStore, resumed, err := openPiFGSConversationGraph(base, "conversation-1", "你是谁")
	if err != nil {
		t.Fatal(err)
	}
	if resumed || newRunID == oldRunID || newStore.Snapshot().Goal != "你是谁" {
		t.Fatalf("expected fresh graph for unrelated request: run=%q old=%q resumed=%v goal=%q", newRunID, oldRunID, resumed, newStore.Snapshot().Goal)
	}
}

func TestPiFGSDecidePromptSupportsNonWorldGoals(t *testing.T) {
	prompt := buildPiFGSSystemPrompt(fgsActivityDecide)
	for _, phrase := range []string{"conversational", "without changing the world", "mark the Goal completed"} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("decide prompt missing %q", phrase)
		}
	}
}

func TestPiFGSConvergenceDirectiveReportsGraphBalance(t *testing.T) {
	snapshot := fgs.Snapshot{Nodes: []fgs.Node{
		{Kind: fgs.KindFact, Status: fgs.StatusConfirmed},
		{Kind: fgs.KindFact, Status: fgs.StatusConfirmed},
		{Kind: fgs.KindFinding, Status: fgs.StatusConfirmed},
		{Kind: fgs.KindStep, Status: fgs.StatusActive},
		{Kind: fgs.KindStep, Status: fgs.StatusCompleted},
		{Kind: fgs.KindStep, Status: fgs.StatusBlocked},
	}}
	directive := piFGSConvergenceDirective(snapshot)
	for _, want := range []string{"Facts=2", "Findings=1", "待执行 Steps=1", "已完成 Steps=1", "已阻塞/废弃 Steps=1"} {
		if !strings.Contains(directive, want) {
			t.Fatalf("convergence directive missing %q: %s", want, directive)
		}
	}
}

func TestClassifyPiFGSToolOutcome(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"exec: \"bash\": executable file not found in %PATH%", "tool_environment_missing_executable"},
		{"UnicodeEncodeError: 'gbk' codec can't encode", "tool_output_encoding_failure"},
		{"context deadline exceeded", "network_or_tool_timeout"},
		{"HTTP/1.1 500 INTERNAL SERVER ERROR", "target_server_error"},
		{"plain response body", "unclassified_world_observation"},
	}
	for _, tt := range tests {
		got, _ := classifyPiFGSToolOutcome(tt.input)
		if got != tt.want {
			t.Fatalf("classifyPiFGSToolOutcome(%q)=%q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestReadPiFGSPlaybookIsEvidenceGatedAndRecordsHint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sqli.md"), []byte("# SQLi reference\nUse controlled differential validation."), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "test goal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readPiFGSPlaybook(store, root, map[string]interface{}{"path": "sqli.md"}); err == nil {
		t.Fatal("expected playbook read to require evidence")
	}
	mutations := make([]fgs.Mutation, 0, 3)
	for i := 0; i < 3; i++ {
		mutations = append(mutations, fgs.Mutation{Op: "add_node", ID: fmt.Sprintf("fact-%d", i), Kind: fgs.KindFact, Label: "fact", Content: "observed", Status: fgs.StatusConfirmed})
	}
	if _, err := store.Apply(mutations); err != nil {
		t.Fatal(err)
	}
	result, err := readPiFGSPlaybook(store, root, map[string]interface{}{"path": "sqli.md"})
	if err != nil || !strings.Contains(result, "SQLi reference") {
		t.Fatalf("readPiFGSPlaybook result=%q err=%v", result, err)
	}
	foundHint := false
	for _, node := range store.Snapshot().Nodes {
		if node.Kind == fgs.KindHint && strings.Contains(node.Label, "sqli.md") {
			foundHint = true
		}
	}
	if !foundHint {
		t.Fatal("playbook read did not append an FGS hint")
	}
	if _, err := readPiFGSPlaybook(store, root, map[string]interface{}{"path": "../sqli.md"}); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestListPiPlaybooksSearchesPathAndContent(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"info-disclosure.md":               "# Information Disclosure\nOSS S3 object storage and directory listing.",
		"ssrf-cache-host/11-cloud.md":      "# Cloud payloads\nS3 storage, IAM and cloud metadata.",
		"ssrf-cache-host/12-cache.md":      "# Host Header and reverse proxy cache poisoning",
		"logic-flaws/11-business-logic.md": "# Business logic",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	matches, err := listPiPlaybooks(root, "object storage")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(matches, "info-disclosure.md") || !containsString(matches, "ssrf-cache-host/11-cloud.md") {
		t.Fatalf("content search matches=%v", matches)
	}

	matches, err = listPiPlaybooks(root, "Host-Header reverse proxy")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 || matches[0] != "ssrf-cache-host/12-cache.md" {
		t.Fatalf("path/content relevance matches=%v", matches)
	}

	matches, err = listPiPlaybooks(root, "not-a-real-playbook")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %v", matches)
	}
	categories, err := listPiPlaybookCategories(root)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(categories, "ssrf-cache-host") || !containsString(categories, "根目录") {
		t.Fatalf("categories=%v", categories)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestPiFGSRequiresExecutableStepForWorldExecution(t *testing.T) {
	store, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "观察外部目标")
	if err != nil {
		t.Fatal(err)
	}
	if fgsHasExecutableStep(store.Snapshot()) {
		t.Fatal("initial FGS graph unexpectedly has an executable step")
	}
	if !strings.Contains(buildPiFGSSystemPrompt(fgsActivityDecide), "leave at least one pending or active executable Step") {
		t.Fatal("decide prompt does not require an executable step")
	}
	if !strings.Contains(buildPiFGSActivityPrompt("观察外部目标", fgsActivityDecide), "至少一个 pending 或 active 的 Step") {
		t.Fatal("decide activity prompt does not require an executable step")
	}
	_, err = store.Apply([]fgs.Mutation{{Op: "add_node", ID: "step-1", Kind: fgs.KindStep, Status: fgs.StatusActive}})
	if err != nil {
		t.Fatal(err)
	}
	if !fgsHasExecutableStep(store.Snapshot()) {
		t.Fatal("active step was not recognized as executable")
	}
}

func TestPiFGSWorldToolsExcludeMemoryAndCoordinatorTools(t *testing.T) {
	makeTool := func(name string) agent.Tool {
		return agent.Tool{Function: agent.FunctionDefinition{Name: name}}
	}
	tools := filterFGSWorldTools([]agent.Tool{
		makeTool("bash"),
		makeTool(builtin.ToolSearchKnowledgeBase),
		makeTool(builtin.ToolSearchProjectFacts),
		makeTool(builtin.ToolBatchTaskStart),
		makeTool(builtin.ToolGetToolExecution),
		makeTool("external_world_action"),
	})
	if len(tools) != 2 || tools[0].Function.Name != "bash" || tools[1].Function.Name != "external_world_action" {
		t.Fatalf("filtered FGS world tools = %+v", tools)
	}
}

func TestPiFGSActivityResultMergeUsesWorkerLocalCounters(t *testing.T) {
	merged := mergePiFGSActivityResults(
		piFGSActivityResult{Text: "worker-a", WorldToolCalls: 1, FGSSubmissions: 1, LastWorldToolResult: "a", WorkerCount: 1, NoOpWorkers: 1},
		piFGSActivityResult{Text: "worker-b", WorldToolCalls: 0, FGSSubmissions: 0, LastWorldToolResult: "", WorkerCount: 1, TimedOutWorkers: 1},
	)
	if merged.WorldToolCalls != 1 || merged.FGSSubmissions != 1 || !strings.Contains(merged.Text, "worker-a") || !strings.Contains(merged.Text, "worker-b") {
		t.Fatalf("merged worker-local activity result = %+v", merged)
	}
	if merged.WorkerCount != 2 || merged.NoOpWorkers != 1 || merged.TimedOutWorkers != 1 {
		t.Fatalf("merged worker counters = %+v", merged)
	}
}

func TestCollapsePiFGSDuplicateBootstrapSteps(t *testing.T) {
	store, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "测试目标")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]fgs.Mutation{
		{Op: "add_node", ID: "bootstrap-old", Kind: fgs.KindStep, Label: "执行最小必要动作", Status: fgs.StatusPending, Priority: 100},
		{Op: "add_node", ID: "other", Kind: fgs.KindStep, Label: "其他方向", Status: fgs.StatusPending, Priority: 90},
		{Op: "add_node", ID: "bootstrap-new", Kind: fgs.KindStep, Label: "执行最小必要动作", Status: fgs.StatusActive, Priority: 100},
	}); err != nil {
		t.Fatal(err)
	}
	if err := collapsePiFGSDuplicateBootstrapSteps(store); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	activeBootstrap := 0
	for _, node := range snapshot.Nodes {
		if node.Label != "执行最小必要动作" {
			continue
		}
		if node.Status == fgs.StatusPending || node.Status == fgs.StatusActive {
			activeBootstrap++
		}
		if node.ID == "bootstrap-old" && node.Status != fgs.StatusAbandoned {
			t.Fatalf("old bootstrap status = %q, want abandoned", node.Status)
		}
	}
	if activeBootstrap != 1 {
		t.Fatalf("active bootstrap count = %d, want 1", activeBootstrap)
	}
}

func TestPiToolEventIsError(t *testing.T) {
	if !piToolEventIsError(map[string]interface{}{"isError": true}) {
		t.Fatal("isError=true was not recognized")
	}
	if !piToolEventIsError(map[string]interface{}{"is_error": "true"}) {
		t.Fatal("is_error=true was not recognized")
	}
	if piToolEventIsError(map[string]interface{}{"isError": false}) {
		t.Fatal("isError=false was recognized as an error")
	}
}

func TestPiFGSExcludedTopicsParsesExplicitNegations(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    string
	}{
		{message: "graphql不测了，测其他方向", want: "graphql"},
		{message: "不要测试 SSRF", want: "ssrf"},
		{message: "跳过登录测试", want: "登录"},
	} {
		got := piFGSExcludedTopics(tc.message)
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("piFGSExcludedTopics(%q) = %#v, want [%q]", tc.message, got, tc.want)
		}
	}
	if got := piFGSExcludedTopics("测试认证和访问控制"); len(got) != 0 {
		t.Fatalf("ordinary topic mention was treated as exclusion: %#v", got)
	}
	got := piFGSExcludedTopics("除了graphql，都可以测挖洞")
	if len(got) != 1 || got[0] != "graphql" {
		t.Fatalf("exceptive exclusion was not parsed: %#v", got)
	}
}

func TestAbandonPiFGSExcludedNodes(t *testing.T) {
	store, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "测试目标")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]fgs.Mutation{
		{Op: "add_node", ID: "graphql-step", Kind: fgs.KindStep, Label: "GraphQL 字段测试", Status: fgs.StatusActive},
		{Op: "add_node", ID: "auth-step", Kind: fgs.KindStep, Label: "认证测试", Status: fgs.StatusPending},
	}); err != nil {
		t.Fatal(err)
	}
	if got := abandonPiFGSExcludedNodes(store, []string{"graphql"}); got != 1 {
		t.Fatalf("abandoned nodes = %d, want 1", got)
	}
	for _, node := range store.Snapshot().Nodes {
		if node.ID == "graphql-step" && node.Status != fgs.StatusAbandoned {
			t.Fatalf("graphql step status = %q, want abandoned", node.Status)
		}
		if node.ID == "auth-step" && node.Status != fgs.StatusPending {
			t.Fatalf("auth step status = %q, want pending", node.Status)
		}
	}
}

func callPiFGSTestTool(t *testing.T, b *piBridge, payload map[string]interface{}) (int, string) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/call", strings.NewReader(string(data)))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CyberStrike-Pi-Bridge-Token", b.Token())
	response := httptest.NewRecorder()
	b.handleCall(response, request)
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.Code, string(body)
}
