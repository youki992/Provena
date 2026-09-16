package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGlobalSystemPromptUsesConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	path := filepath.Join(dir, DefaultGlobalSystemPromptFilename)
	if err := os.WriteFile(path, []byte("\n global rule \n"), 0600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	got, resolved, err := LoadGlobalSystemPrompt(configPath, "", nil)
	if err != nil {
		t.Fatalf("LoadGlobalSystemPrompt: %v", err)
	}
	if got != "global rule" {
		t.Fatalf("prompt = %q, want %q", got, "global rule")
	}
	if resolved != path {
		t.Fatalf("resolved path = %q, want %q", resolved, path)
	}
}

func TestLoadGlobalSystemPromptMissingIsCompatible(t *testing.T) {
	got, _, err := LoadGlobalSystemPrompt(filepath.Join(t.TempDir(), "config.yaml"), "", nil)
	if err != nil {
		t.Fatalf("missing prompt returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("missing prompt = %q, want empty", got)
	}
}

func TestPrependSystemPrompt(t *testing.T) {
	if got := PrependSystemPrompt("global", "local"); got != "global\n\nlocal" {
		t.Fatalf("prepend = %q", got)
	}
	if got := PrependSystemPrompt("", "local"); got != "local" {
		t.Fatalf("empty global = %q", got)
	}
}
