package run

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/piagent"
)

// testPrincipal is the identity the headless bridge must carry into every tool
// call so the MCP authorizer can authorize it.
func testPrincipal() authctx.Principal {
	return authctx.NewPrincipal("user-test", "admin", "all", map[string]bool{"mcp:tool:execute": true})
}

// recordingExecutor captures what the bridge hands to the agent so tests can
// assert the principal survived the loopback hop.
type recordingExecutor struct {
	calls      int
	convID     string
	toolName   string
	principal  authctx.Principal
	authorized bool
}

func (r *recordingExecutor) ExecuteMCPToolForConversation(ctx context.Context, conversationID, toolName string, args map[string]interface{}) (*agent.ToolExecutionResult, error) {
	r.calls++
	r.convID = conversationID
	r.toolName = toolName
	r.principal, r.authorized = authctx.PrincipalFromContext(ctx)
	return &agent.ToolExecutionResult{Result: "executed"}, nil
}

func newTestBridgeWithExecutor(t *testing.T, tools []piagent.BridgeTool) (*bridge, *fgs.Store, *recordingExecutor) {
	t.Helper()
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "test goal")
	if err != nil {
		t.Fatalf("fgs.Open: %v", err)
	}
	executor := &recordingExecutor{}
	ctx := authctx.WithPrincipal(context.Background(), testPrincipal())
	b, err := startBridge(ctx, graph, executor, "conv-test", tools, nil)
	if err != nil {
		t.Fatalf("startBridge: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, graph, executor
}

func newTestBridge(t *testing.T) (*bridge, *fgs.Store) {
	t.Helper()
	b, graph, _ := newTestBridgeWithExecutor(t, fgsToolDefs())
	return b, graph
}

func callTool(t *testing.T, b *bridge, token, tool string, args map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"toolName": tool, "args": args})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, b.URL()+"/call", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(bridgeTokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bridge call: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	payload := map[string]interface{}{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode response %q: %v", string(raw), err)
		}
	}
	return resp.StatusCode, payload
}

func TestBridgeSubmitFactAppendsToGraph(t *testing.T) {
	b, graph := newTestBridge(t)

	status, payload := callTool(t, b, b.Token(), "submit_fact", map[string]interface{}{
		"label":   "login endpoint responds",
		"content": "POST /login returns 200 for valid credentials",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, payload = %v", status, payload)
	}

	facts := factsOf(graph.Snapshot())
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(facts))
	}
	if facts[0].Label != "login endpoint responds" {
		t.Errorf("label = %q", facts[0].Label)
	}
}

func TestBridgeSubmitFindingRequiresContent(t *testing.T) {
	b, _ := newTestBridge(t)

	status, _ := callTool(t, b, b.Token(), "submit_finding", map[string]interface{}{
		"label": "only a label",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestBridgeApplyMutationsAndRead(t *testing.T) {
	b, graph := newTestBridge(t)

	status, _ := callTool(t, b, b.Token(), "fgs_apply", map[string]interface{}{
		"mutations": []map[string]interface{}{
			{"op": "add_node", "id": "step-1", "kind": "step", "label": "probe root", "priority": 5},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("fgs_apply status = %d", status)
	}

	status, payload := callTool(t, b, b.Token(), "fgs_read", map[string]interface{}{})
	if status != http.StatusOK {
		t.Fatalf("fgs_read status = %d", status)
	}
	snapshot, _ := payload["result"].(string)
	if snapshot == "" {
		t.Fatal("fgs_read returned an empty snapshot")
	}
	if !bytes.Contains([]byte(snapshot), []byte("step-1")) {
		t.Errorf("snapshot does not contain the new step: %s", snapshot)
	}
	if graph.Version() == 0 {
		t.Error("graph version did not advance")
	}
}

func TestBridgeRejectsMissingOrWrongToken(t *testing.T) {
	b, _ := newTestBridge(t)

	if status, _ := callTool(t, b, "", "fgs_read", nil); status != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", status)
	}
	if status, _ := callTool(t, b, "not-the-token", "fgs_read", nil); status != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", status)
	}
}

func TestBridgeRejectsToolOutsideAllowlist(t *testing.T) {
	b, _ := newTestBridge(t)

	// Only the FGS tools are enabled for this bridge; anything else must be
	// refused before it can reach a tool executor.
	if status, _ := callTool(t, b, b.Token(), "read_file", map[string]interface{}{"path": "/etc/passwd"}); status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
}

func TestBridgeDoesNotExposeCoordinatorTools(t *testing.T) {
	// The world-tool filter is a security boundary: memory/orchestration tools
	// must never be handed to a model activity.
	for _, name := range []string{
		"upsert_project_fact",
		"search_project_facts",
		"search_knowledge_base",
		"batch_task_create",
	} {
		if !isStateOrCoordinatorTool(name) {
			t.Errorf("%s should be filtered out of the world tool set", name)
		}
	}
	if isStateOrCoordinatorTool("http_request") {
		t.Error("http_request must remain available as a world action")
	}
}

func TestBridgeMethodAndHealth(t *testing.T) {
	b, _ := newTestBridge(t)

	resp, err := http.Get(b.URL() + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}

	getResp, err := http.Get(b.URL() + "/call")
	if err != nil {
		t.Fatalf("GET /call: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /call status = %d, want 405", getResp.StatusCode)
	}
}

func TestStartBridgeRequiresPrincipal(t *testing.T) {
	graph, err := fgs.Open(filepath.Join(t.TempDir(), "graph.jsonl"), "test goal")
	if err != nil {
		t.Fatalf("fgs.Open: %v", err)
	}
	if _, err := startBridge(context.Background(), graph, nil, "conv-test", fgsToolDefs(), nil); err == nil {
		t.Fatal("startBridge accepted a context without a principal")
	}
}

func TestBridgeAttachesPrincipalToToolCalls(t *testing.T) {
	// Regression: the headless bridge answers its own loopback HTTP requests, so
	// the request context does not inherit the run context. Dropping the
	// principal there makes the MCP authorizer reject every tool call with
	// "missing authenticated principal", which silently disables the whole
	// world-tool surface of a run.
	tools := append(fgsToolDefs(), piagent.BridgeTool{Name: "http-framework-test"})
	b, _, executor := newTestBridgeWithExecutor(t, tools)

	status, payload := callTool(t, b, b.Token(), "http-framework-test", map[string]interface{}{
		"url": "http://127.0.0.1:8080/",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, payload = %v", status, payload)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if !executor.authorized {
		t.Fatal("no principal reached the tool call: context was not propagated")
	}
	if executor.principal.UserID != "user-test" {
		t.Errorf("principal user = %q, want %q", executor.principal.UserID, "user-test")
	}
	if executor.convID != "conv-test" {
		t.Errorf("conversation id = %q, want %q", executor.convID, "conv-test")
	}
	if executor.toolName != "http-framework-test" {
		t.Errorf("tool = %q, want %q", executor.toolName, "http-framework-test")
	}
}
