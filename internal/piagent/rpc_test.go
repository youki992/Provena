package piagent

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestPrepareAgentDirWritesBridgeExtension(t *testing.T) {
	cleanup, dir, err := prepareAgentDir(Config{
		BridgeURL:   "http://127.0.0.1:43123",
		BridgeToken: "secret-token",
		BridgeTools: []BridgeTool{{
			Name:        "query_assets",
			Description: "query assets",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		}},
	})
	if err != nil {
		t.Fatalf("prepareAgentDir: %v", err)
	}
	defer cleanup()
	if dir == "" {
		t.Fatal("expected temporary runtime directory")
	}
	path := dir + string(os.PathSeparator) + "bridge.ts"
	if err := writeBridgeExtension(path, "http://127.0.0.1:43123", "secret-token", []BridgeTool{{
		Name:        "query_assets",
		Description: "query assets",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
	}}, false); err != nil {
		t.Fatalf("writeBridgeExtension: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bridge extension: %v", err)
	}
	script := string(data)
	for _, expected := range []string{"127.0.0.1:43123", "secret-token", "query_assets", "pi.registerTool"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("bridge extension missing %q: %s", expected, script)
		}
	}
}

func TestWriteBridgeExtensionSanitizesGeminiIncompatibleSchema(t *testing.T) {
	path := t.TempDir() + string(os.PathSeparator) + "bridge.ts"
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"options": map[string]interface{}{
				"anyOf": []interface{}{
					map[string]interface{}{"type": "object", "additionalProperties": true},
					map[string]interface{}{"type": "null"},
				},
				"default": nil,
			},
			"name": map[string]interface{}{
				"oneOf": []interface{}{
					map[string]interface{}{"type": "string"},
					map[string]interface{}{"type": "null"},
				},
			},
		},
		"required": []interface{}{"options"},
	}
	if err := writeBridgeExtension(path, "http://127.0.0.1:43123", "secret-token", []BridgeTool{{
		Name: "arl_submit_scan", Parameters: schema,
	}}, true); err != nil {
		t.Fatalf("writeBridgeExtension: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bridge extension: %v", err)
	}
	script := string(data)
	for _, forbidden := range []string{"additionalProperties", "anyOf", "oneOf", "\"default\""} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("provider bridge still contains unsupported schema key %q:\n%s", forbidden, script)
		}
	}
	for _, expected := range []string{"arl_submit_scan", "\"options\":{\"type\":\"object\"}", "\"name\":{\"type\":\"string\"}"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("provider bridge lost useful schema field %q:\n%s", expected, script)
		}
	}
	// The provider-facing copy must not mutate the original MCP schema.
	if _, ok := schema["properties"].(map[string]interface{})["options"].(map[string]interface{})["anyOf"]; !ok {
		t.Fatal("source MCP schema was mutated")
	}
}

func TestGeminiDetectionDoesNotMatchOtherModels(t *testing.T) {
	if !isGeminiProvider("openai_compatible", "gemini-3.7-flash", "https://api.example.test/v1") {
		t.Fatal("Gemini model should enable compatibility mode")
	}
	if isGeminiProvider("openai", "gpt-5", "https://api.openai.com/v1") {
		t.Fatal("GPT must receive the original schema")
	}
	if isGeminiProvider("anthropic", "claude-sonnet", "https://api.anthropic.com") {
		t.Fatal("Claude must receive the original schema")
	}
}

func TestPrepareAgentDirWithoutRuntimeInputsDoesNotCreateDirectory(t *testing.T) {
	cleanup, dir, err := prepareAgentDir(Config{})
	if err != nil {
		t.Fatalf("prepareAgentDir: %v", err)
	}
	cleanup()
	if dir != "" {
		t.Fatalf("expected no runtime directory, got %q", dir)
	}
}

func TestConfigPreservesNoTools(t *testing.T) {
	cfg := Config{NoTools: true}
	if !cfg.NoTools {
		t.Fatal("expected NoTools to be preserved in piagent.Config")
	}
}

func TestRuntimeProviderRequiresCompleteModelConfiguration(t *testing.T) {
	if runtimeModelConfigured(Config{BaseURL: "https://example.test", APIKey: "key"}) {
		t.Fatal("incomplete runtime model configuration must not select provena")
	}
	if !runtimeModelConfigured(Config{BaseURL: "https://example.test", APIKey: "key", Model: "model"}) {
		t.Fatal("complete runtime model configuration should select provena")
	}
}

func TestRuntimeProviderRequiresGeneratedModelsFile(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{BaseURL: "https://example.test", APIKey: "key", Model: "model"}
	if runtimeProviderAvailable(dir, cfg) {
		t.Fatal("provider must not be selected before models.json is generated")
	}
	if err := os.WriteFile(dir+string(os.PathSeparator)+"models.json", []byte(`{"providers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !runtimeProviderAvailable(dir, cfg) {
		t.Fatal("provider should be selected after models.json is generated")
	}
}

func TestPrepareAppendSystemPromptUsesFileForLongPrompt(t *testing.T) {
	prompt := strings.Repeat("安全测试上下文 ", 1000)
	value, cleanup, err := prepareAppendSystemPrompt(prompt, "")
	if err != nil {
		t.Fatalf("prepareAppendSystemPrompt: %v", err)
	}
	defer cleanup()
	if value == "" || value == prompt {
		t.Fatalf("long prompt should be passed as a file path, got %q", value)
	}
	data, err := os.ReadFile(value)
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	if string(data) != strings.TrimSpace(prompt) {
		t.Fatal("prompt file content changed")
	}
}

func TestNormalizePiProcessOutputDecodesGBK(t *testing.T) {
	raw, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte("命令行参数太长"))
	if err != nil {
		t.Fatalf("encode GBK: %v", err)
	}
	if got := normalizePiProcessOutput(raw); got != "命令行参数太长" {
		t.Fatalf("decoded stderr = %q", got)
	}
}
