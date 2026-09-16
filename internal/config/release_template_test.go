package config

import (
	"path/filepath"
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
	if cfg.Server.Port != 8080 {
		t.Fatalf("release template server port = %d, want 8080", cfg.Server.Port)
	}
}
