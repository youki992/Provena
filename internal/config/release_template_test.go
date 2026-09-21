package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseTemplateKeepsPortableDefaults catches malformed top-level YAML
// that otherwise silently disables portable-release features.
func TestReleaseTemplateKeepsPortableDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("load release template: %v", err)
	}
	if !cfg.Project.Enabled {
		t.Fatal("release template must enable project blackboard")
	}
	if cfg.MultiAgent.Enabled {
		t.Fatal("release template must keep legacy multi-agent runtime disabled")
	}
	if !cfg.PiAgent.Enabled {
		t.Fatal("release template must enable Pi Agent")
	}
	// The template is CLI-only now: it no longer carries a server section, so
	// assert the defaults a command-line run depends on instead.
	if cfg.Server.Port != 0 || cfg.Server.Host != "" {
		t.Fatalf("release template must not configure a web server, got host=%q port=%d",
			cfg.Server.Host, cfg.Server.Port)
	}
	if strings.TrimSpace(cfg.Security.ToolsDir) == "" {
		t.Fatal("release template must point at a tool recipe directory")
	}
	if strings.TrimSpace(cfg.AI.DefaultChannel) == "" {
		t.Fatal("release template must select a default AI channel")
	}
}
