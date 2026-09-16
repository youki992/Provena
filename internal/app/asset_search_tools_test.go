package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/handler"
	"github.com/chobits02/provena/internal/mcp"
	"github.com/chobits02/provena/internal/mcp/builtin"

	"go.uber.org/zap"
)

func TestAssetSearchToolUsesConfiguredFOFAAndSendsEmail(t *testing.T) {
	var gotEmail, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = r.URL.Query().Get("email")
		gotKey = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": false, "size": 1, "page": 1, "total": 1,
			"results": [][]interface{}{{"https://example.com", "203.0.113.10", 443}},
		})
	}))
	defer server.Close()

	searcher := handler.NewFofaHandler(&config.Config{FOFA: config.FofaConfig{
		Email: "configured@example.com", APIKey: "configured-key", BaseURL: server.URL,
	}}, zap.NewNop())
	mcpServer := mcp.NewServer(zap.NewNop())
	mcpServer.SetToolAuthorizer(mcpToolAuthorizer(nil))
	registerAssetSearchTools(mcpServer, searcher, zap.NewNop())

	principal := authctx.NewPrincipal("asset-user", "asset-user", "all", map[string]bool{"fofa:execute": true})
	ctx := authctx.WithPrincipal(context.Background(), principal)
	result, _, err := mcpServer.CallTool(ctx, builtin.ToolSearchSpaceAssets, map[string]interface{}{
		"query": `domain="example.com"`, "fields": "host,ip,port", "size": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError {
		t.Fatalf("asset search failed: %#v", result)
	}
	if gotEmail != "configured@example.com" || gotKey != "configured-key" {
		t.Fatalf("FOFA credentials were not sent correctly: email=%q key=%q", gotEmail, gotKey)
	}
	if !strings.Contains(result.Content[0].Text, `"provider": "fofa"`) || !strings.Contains(result.Content[0].Text, "203.0.113.10") {
		t.Fatalf("unexpected normalized result: %s", result.Content[0].Text)
	}
}

func TestAssetSearchToolRequiresFOFAEmail(t *testing.T) {
	t.Setenv("FOFA_EMAIL", "")
	t.Setenv("FOFA_API_KEY", "")
	searcher := handler.NewFofaHandler(&config.Config{FOFA: config.FofaConfig{APIKey: "configured-key"}}, zap.NewNop())
	mcpServer := mcp.NewServer(zap.NewNop())
	mcpServer.SetToolAuthorizer(mcpToolAuthorizer(nil))
	registerAssetSearchTools(mcpServer, searcher, zap.NewNop())
	principal := authctx.NewPrincipal("asset-user", "asset-user", "all", map[string]bool{"fofa:execute": true})
	result, _, err := mcpServer.CallTool(authctx.WithPrincipal(context.Background(), principal), builtin.ToolSearchSpaceAssets, map[string]interface{}{
		"provider": "fofa", "query": `domain="example.com"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Content[0].Text, "fofa.email") {
		t.Fatalf("missing email should be a clear tool error: %#v", result)
	}
}

func TestAssetSearchToolAutoUsesConfiguredZoomEye(t *testing.T) {
	t.Setenv("FOFA_EMAIL", "")
	t.Setenv("FOFA_API_KEY", "")
	t.Setenv("ZOOMEYE_API_KEY", "")

	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("API-KEY")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 60000, "query": "domain=\"example.com\"", "total": 1, "page": 1, "pagesize": 1,
			"data": []map[string]interface{}{{"domain": "example.com", "ip": "203.0.113.10"}},
		})
	}))
	defer server.Close()

	searcher := handler.NewFofaHandler(&config.Config{ZoomEye: config.SpaceSearchConfig{
		APIKey: "zoomeye-key", BaseURL: server.URL,
	}}, zap.NewNop())
	mcpServer := mcp.NewServer(zap.NewNop())
	mcpServer.SetToolAuthorizer(mcpToolAuthorizer(nil))
	registerAssetSearchTools(mcpServer, searcher, zap.NewNop())

	principal := authctx.NewPrincipal("asset-user", "asset-user", "all", map[string]bool{"fofa:execute": true})
	result, _, err := mcpServer.CallTool(authctx.WithPrincipal(context.Background(), principal), builtin.ToolSearchSpaceAssets, map[string]interface{}{
		"query": `domain="example.com"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError {
		t.Fatalf("auto asset search failed: %#v", result)
	}
	if gotKey != "zoomeye-key" {
		t.Fatalf("ZoomEye API key was not sent: %q", gotKey)
	}
	if !strings.Contains(result.Content[0].Text, `"provider": "zoomeye"`) {
		t.Fatalf("auto should select configured ZoomEye: %s", result.Content[0].Text)
	}
}
