package config

import (
	"os"
	"testing"
)

func TestExpandEnvVar(t *testing.T) {
	os.Setenv("TEST_MCP_VAR", "hello")
	os.Setenv("TEST_MCP_PATH", "/usr/local/bin")
	defer os.Unsetenv("TEST_MCP_VAR")
	defer os.Unsetenv("TEST_MCP_PATH")

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"plain string", "no vars here", "no vars here"},
		{"empty string", "", ""},
		{"simple var", "${TEST_MCP_VAR}", "hello"},
		{"var in middle", "prefix-${TEST_MCP_VAR}-suffix", "prefix-hello-suffix"},
		{"multiple vars", "${TEST_MCP_PATH}/${TEST_MCP_VAR}", "/usr/local/bin/hello"},
		{"missing var empty", "${NONEXISTENT_MCP_VAR_XYZ}", ""},
		{"default value used", "${NONEXISTENT_MCP_VAR_XYZ:-fallback}", "fallback"},
		{"default not used", "${TEST_MCP_VAR:-unused}", "hello"},
		{"default with path", "${NONEXISTENT_MCP_VAR_XYZ:-/tmp/default}", "/tmp/default"},
		{"unclosed brace", "${UNCLOSED", "${UNCLOSED"},
		{"dollar without brace", "$PLAIN", "$PLAIN"},
		{"empty var name", "${}", ""},
		{"default empty var", "${:-default}", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandEnvVar(tt.input)
			if got != tt.expect {
				t.Errorf("expandEnvVar(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestExpandConfigEnv(t *testing.T) {
	os.Setenv("TEST_MCP_CMD", "python3")
	os.Setenv("TEST_MCP_TOKEN", "secret123")
	defer os.Unsetenv("TEST_MCP_CMD")
	defer os.Unsetenv("TEST_MCP_TOKEN")

	cfg := &ExternalMCPServerConfig{
		Command: "${TEST_MCP_CMD}",
		Args:    []string{"--token", "${TEST_MCP_TOKEN}", "${MISSING:-default_arg}"},
		Env:     map[string]string{"API_KEY": "${TEST_MCP_TOKEN}", "LEVEL": "${MISSING:-INFO}"},
		URL:     "https://${MISSING:-example.com}/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${TEST_MCP_TOKEN}"},
	}

	ExpandConfigEnv(cfg)

	if cfg.Command != "python3" {
		t.Errorf("Command = %q, want %q", cfg.Command, "python3")
	}
	if cfg.Args[1] != "secret123" {
		t.Errorf("Args[1] = %q, want %q", cfg.Args[1], "secret123")
	}
	if cfg.Args[2] != "default_arg" {
		t.Errorf("Args[2] = %q, want %q", cfg.Args[2], "default_arg")
	}
	if cfg.Env["API_KEY"] != "secret123" {
		t.Errorf("Env[API_KEY] = %q, want %q", cfg.Env["API_KEY"], "secret123")
	}
	if cfg.Env["LEVEL"] != "INFO" {
		t.Errorf("Env[LEVEL] = %q, want %q", cfg.Env["LEVEL"], "INFO")
	}
	if cfg.URL != "https://example.com/mcp" {
		t.Errorf("URL = %q, want %q", cfg.URL, "https://example.com/mcp")
	}
	if cfg.Headers["Authorization"] != "Bearer secret123" {
		t.Errorf("Headers[Authorization] = %q, want %q", cfg.Headers["Authorization"], "Bearer secret123")
	}
}

func TestAIChannelResolveExpandsEnv(t *testing.T) {
	os.Setenv("TEST_AI_KEY", "sk-resolved")
	os.Setenv("TEST_AI_BASE", "https://example.test/v1")
	defer os.Unsetenv("TEST_AI_KEY")
	defer os.Unsetenv("TEST_AI_BASE")

	ai := AIConfig{
		DefaultChannel: "default",
		Channels: map[string]AIChannelConfig{
			"default": {
				Provider: "openai_compatible",
				APIKey:   "${TEST_AI_KEY}",
				BaseURL:  "${TEST_AI_BASE}",
				Model:    "gpt-test",
			},
			"literal": {
				Provider: "openai_compatible",
				APIKey:   "sk-literal",
				BaseURL:  "https://literal.test/v1",
				Model:    "gpt-test",
			},
		},
	}

	oa, id, ok := ai.ResolveChannel("")
	if !ok {
		t.Fatalf("ResolveChannel() ok = false, want true")
	}
	if id != "default" {
		t.Errorf("id = %q, want %q", id, "default")
	}
	if oa.APIKey != "sk-resolved" {
		t.Errorf("APIKey = %q, want %q", oa.APIKey, "sk-resolved")
	}
	if oa.BaseURL != "https://example.test/v1" {
		t.Errorf("BaseURL = %q, want %q", oa.BaseURL, "https://example.test/v1")
	}

	lit, _, ok := ai.ResolveChannel("literal")
	if !ok {
		t.Fatalf("ResolveChannel(literal) ok = false, want true")
	}
	if lit.APIKey != "sk-literal" {
		t.Errorf("literal APIKey = %q, want %q", lit.APIKey, "sk-literal")
	}

	// The stored channel must keep the placeholder; expanding in place would let a
	// config round-trip write the resolved secret back to disk.
	if got := ai.Channels["default"].APIKey; got != "${TEST_AI_KEY}" {
		t.Errorf("stored APIKey = %q, want placeholder %q", got, "${TEST_AI_KEY}")
	}
}

func TestResolveAIChannelLegacyFallbackExpandsEnv(t *testing.T) {
	os.Setenv("TEST_AI_KEY", "sk-resolved")
	defer os.Unsetenv("TEST_AI_KEY")

	cfg := &Config{
		OpenAI: OpenAIConfig{
			APIKey:  "${TEST_AI_KEY}",
			BaseURL: "https://legacy.test/v1",
			Model:   "gpt-test",
		},
	}

	oa, _, ok := cfg.ResolveAIChannel("")
	if !ok {
		t.Fatalf("ResolveAIChannel() ok = false, want true")
	}
	if oa.APIKey != "sk-resolved" {
		t.Errorf("APIKey = %q, want %q", oa.APIKey, "sk-resolved")
	}
}
