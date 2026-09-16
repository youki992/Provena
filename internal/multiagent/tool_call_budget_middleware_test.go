package multiagent

import (
	"testing"

	"github.com/chobits02/provena/internal/config"
)

func TestToolCallBudgetBlocksDuplicateAndPerToolLoops(t *testing.T) {
	mw := &config.MultiAgentEinoMiddlewareConfig{
		MaxToolCalls:          3,
		MaxToolCallsPerTool:   2,
		MaxDuplicateToolCalls: 1,
		MaxToolSearchCalls:    1,
	}
	b := newToolCallBudget(mw)
	if ok, _ := b.admit("nmap", `{"target":"a"}`); !ok {
		t.Fatal("first call should be allowed")
	}
	if ok, reason := b.admit("nmap", `{"target":"a"}`); ok || reason == "" {
		t.Fatal("identical call should be blocked with a reason")
	}
	if ok, _ := b.admit("nmap", `{"target":"b"}`); !ok {
		t.Fatal("different call for the same tool should be allowed")
	}
	if ok, _ := b.admit("nmap", `{"target":"c"}`); ok {
		t.Fatal("per-tool budget should block the third call")
	}
}

func TestToolCallBudgetNormalizesJSONArguments(t *testing.T) {
	b := newToolCallBudget(&config.MultiAgentEinoMiddlewareConfig{MaxDuplicateToolCalls: 1})
	if ok, _ := b.admit("httpx", `{"url":"https://example.test","ports":[80,443]}`); !ok {
		t.Fatal("first call should be allowed")
	}
	if ok, reason := b.admit(" HTTPX ", "{\n  \"ports\": [80, 443], \"url\": \"https://example.test\"\n}"); ok || reason == "" {
		t.Fatal("JSON formatting differences should still identify the duplicate call")
	}
}

func TestToolCallBudgetAllowsUnlimitedDimensions(t *testing.T) {
	b := newToolCallBudget(&config.MultiAgentEinoMiddlewareConfig{
		MaxToolCalls:          -1,
		MaxToolCallsPerTool:   -1,
		MaxDuplicateToolCalls: -1,
		MaxToolSearchCalls:    -1,
	})
	for i := 0; i < 200; i++ {
		if ok, reason := b.admit("http-framework-test", `{"target":"same"}`); !ok {
			t.Fatalf("unlimited dimensions blocked call %d: %s", i, reason)
		}
	}
}

func TestToolCallBudgetDefaultsToUnlimitedDimensions(t *testing.T) {
	b := newToolCallBudget(&config.MultiAgentEinoMiddlewareConfig{})
	for i := 0; i < 200; i++ {
		if ok, reason := b.admit("http-framework-test", `{"target":"same"}`); !ok {
			t.Fatalf("default tool budget blocked call %d: %s", i, reason)
		}
	}
}
