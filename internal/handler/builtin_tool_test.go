package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/mcp"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func setupBuiltinToolTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	server := mcp.NewServer(zap.NewNop())
	server.RegisterTool(mcp.Tool{
		Name:        "test_echo",
		Description: "test builtin tool",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(_ context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		value, _ := args["value"].(string)
		return &mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: value}}}, nil
	})

	h := NewConfigHandler("", &config.Config{}, server, nil, nil, nil, nil, zap.NewNop())
	router := gin.New()
	router.POST("/api/config/tools/:name/test", h.TestBuiltinTool)
	return router
}

func TestTestBuiltinToolReturnsExecutionAndResult(t *testing.T) {
	router := setupBuiltinToolTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/config/tools/test_echo/test", bytes.NewBufferString(`{"arguments":{"value":"ok"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var response struct {
		OK          bool   `json:"ok"`
		Tool        string `json:"tool"`
		ExecutionID string `json:"execution_id"`
		Result      struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.OK || response.Tool != "test_echo" || response.ExecutionID == "" {
		t.Fatalf("response = %#v, want successful tool execution", response)
	}
	if len(response.Result.Content) != 1 || response.Result.Content[0].Text != "ok" {
		t.Fatalf("result = %#v, want text ok", response.Result)
	}
}

func TestTestBuiltinToolRejectsInvalidOrUnregisteredTool(t *testing.T) {
	router := setupBuiltinToolTestRouter()
	tests := []struct {
		name       string
		path       string
		body       string
		statusCode int
	}{
		{name: "invalid json", path: "/api/config/tools/test_echo/test", body: `{`, statusCode: http.StatusBadRequest},
		{name: "external name", path: "/api/config/tools/server::tool/test", body: `{}`, statusCode: http.StatusBadRequest},
		{name: "unregistered", path: "/api/config/tools/missing/test", body: `{}`, statusCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.statusCode, w.Body.String())
			}
		})
	}
}
