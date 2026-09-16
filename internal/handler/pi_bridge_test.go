package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/mcp"
	"github.com/chobits02/provena/internal/mcp/builtin"
	"github.com/chobits02/provena/internal/piagent"

	"go.uber.org/zap"
)

func TestListenPiBridgeRetriesTransientBufferFailure(t *testing.T) {
	attempts := 0
	listener, err := listenPiBridge(context.Background(), func(network, address string) (net.Listener, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("bind: An operation on a socket could not be performed because the system lacked sufficient buffer space or because a queue was full")
		}
		return net.Listen(network, address)
	})
	if err != nil {
		t.Fatalf("listenPiBridge: %v", err)
	}
	defer listener.Close()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestListenPiBridgeDoesNotRetryPermanentFailure(t *testing.T) {
	attempts := 0
	want := errors.New("permanent listen failure")
	_, err := listenPiBridge(context.Background(), func(_, _ string) (net.Listener, error) {
		attempts++
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestPiBridgeRejectsInvalidTokenAndUnknownTool(t *testing.T) {
	b := &piBridge{
		token:   "expected",
		tools:   map[string]struct{}{"query_assets": {}},
		baseCtx: context.Background(),
	}

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/call", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	b.handleCall(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/call", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("X-CyberStrike-Pi-Bridge-Token", "expected")
	response = httptest.NewRecorder()
	b.handleCall(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestPiBridgeCallsInternalMCPTool(t *testing.T) {
	server := mcp.NewServer(zap.NewNop())
	server.RegisterTool(mcp.Tool{
		Name:        "echo_bridge",
		Description: "echo input",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
	}, func(_ context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		value, _ := args["value"].(string)
		return &mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: "echo:" + value}}}, nil
	})
	ag := agent.NewAgent(&config.OpenAIConfig{}, &config.AgentConfig{}, server, nil, zap.NewNop(), 1)
	h := &AgentHandler{agent: ag}
	bridge, err := h.startPiBridge(context.Background(), authctx.Principal{}, "conv-bridge", "测试 https://target.test/", []piagent.BridgeTool{{
		Name:        "echo_bridge",
		Description: "echo input",
		Parameters:  map[string]interface{}{"type": "object"},
	}})
	if err != nil {
		t.Fatalf("startPiBridge: %v", err)
	}
	defer bridge.Close()
	payload, _ := json.Marshal(map[string]interface{}{"toolName": "echo_bridge", "args": map[string]interface{}{"value": "ok"}})
	request, err := http.NewRequest(http.MethodPost, bridge.URL()+"/call", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CyberStrike-Pi-Bridge-Token", bridge.Token())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("bridge call: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bridge status = %d, body = %s", response.StatusCode, body)
	}
	var result piBridgeCallResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode bridge result: %v; body=%s", err, body)
	}
	if strings.TrimSpace(result.Result) != "echo:ok" || result.IsError {
		t.Fatalf("unexpected bridge result: %+v", result)
	}
}

func TestPiBridgeRejectsOutOfScopeAsset(t *testing.T) {
	server := mcp.NewServer(zap.NewNop())
	server.RegisterTool(mcp.Tool{
		Name:        builtin.ToolCreateAsset,
		Description: "create asset",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(_ context.Context, _ map[string]interface{}) (*mcp.ToolResult, error) {
		t.Fatal("out-of-scope create_asset must not reach the MCP handler")
		return nil, nil
	})
	ag := agent.NewAgent(&config.OpenAIConfig{}, &config.AgentConfig{}, server, nil, zap.NewNop(), 1)
	h := &AgentHandler{agent: ag}
	bridge, err := h.startPiBridge(context.Background(), authctx.Principal{}, "conv-bridge", "测试 https://target.test/", []piagent.BridgeTool{{
		Name:        builtin.ToolCreateAsset,
		Description: "create asset",
		Parameters:  map[string]interface{}{"type": "object"},
	}})
	if err != nil {
		t.Fatalf("startPiBridge: %v", err)
	}
	defer bridge.Close()
	payload, _ := json.Marshal(map[string]interface{}{
		"toolName": builtin.ToolCreateAsset,
		"args":     map[string]interface{}{"domain": "github.com"},
	})
	request, err := http.NewRequest(http.MethodPost, bridge.URL()+"/call", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CyberStrike-Pi-Bridge-Token", bridge.Token())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("bridge call: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("out-of-scope asset status = %d, body = %s", response.StatusCode, body)
	}
}

func TestPiBridgeRejectsAssetWithoutTarget(t *testing.T) {
	server := mcp.NewServer(zap.NewNop())
	server.RegisterTool(mcp.Tool{
		Name:        builtin.ToolCreateAsset,
		Description: "create asset",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(_ context.Context, _ map[string]interface{}) (*mcp.ToolResult, error) {
		t.Fatal("asset without a target must not reach the MCP handler")
		return nil, nil
	})
	ag := agent.NewAgent(&config.OpenAIConfig{}, &config.AgentConfig{}, server, nil, zap.NewNop(), 1)
	h := &AgentHandler{agent: ag}
	bridge, err := h.startPiBridge(context.Background(), authctx.Principal{}, "conv-bridge", "测试 https://target.test/", []piagent.BridgeTool{{
		Name:        builtin.ToolCreateAsset,
		Description: "create asset",
		Parameters:  map[string]interface{}{"type": "object"},
	}})
	if err != nil {
		t.Fatalf("startPiBridge: %v", err)
	}
	defer bridge.Close()
	payload, _ := json.Marshal(map[string]interface{}{
		"toolName": builtin.ToolCreateAsset,
		"args":     map[string]interface{}{"title": "missing target"},
	})
	request, err := http.NewRequest(http.MethodPost, bridge.URL()+"/call", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CyberStrike-Pi-Bridge-Token", bridge.Token())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("bridge call: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing target status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
}

func TestPiAssetTargetFromArgsPreservesCompleteHostURL(t *testing.T) {
	if got := piAssetTargetFromArgs(map[string]interface{}{"host": "https://target.test:8443/path"}); got != "https://target.test:8443/path" {
		t.Fatalf("complete host URL was rewritten: %q", got)
	}
	if got := piAssetTargetFromArgs(map[string]interface{}{"ip": "192.0.2.10", "port": 8080}); got != "tcp://192.0.2.10" {
		t.Fatalf("IP target was not normalized: %q", got)
	}
	if got := piAssetTargetFromArgs(map[string]interface{}{"domain": "target.test", "protocol": "http"}); got != "http://target.test" {
		t.Fatalf("domain target was not normalized: %q", got)
	}
}
