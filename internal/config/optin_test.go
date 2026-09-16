package config

import (
	"path/filepath"
	"testing"
)

// C2 and WebShell are high-risk capabilities aimed at systems you own or are
// explicitly authorized to test. In a general-purpose build they must be opt-in:
// omitting the config section has to mean "off", and only an explicit true may
// turn them on.
func TestHighRiskCapabilitiesDefaultToDisabled(t *testing.T) {
	t.Run("C2 omitted means disabled", func(t *testing.T) {
		if (C2Config{}).EnabledEffective() {
			t.Error("C2 must be disabled when the section is omitted")
		}
	})

	t.Run("WebShell omitted means disabled", func(t *testing.T) {
		if (WebShellConfig{}).EnabledEffective() {
			t.Error("WebShell must be disabled when the section is omitted")
		}
	})

	disabled := false
	if (C2Config{Enabled: &disabled}).EnabledEffective() {
		t.Error("explicit false must stay disabled")
	}
	if (WebShellConfig{Enabled: &disabled}).EnabledEffective() {
		t.Error("explicit false must stay disabled")
	}

	enabled := true
	if !(C2Config{Enabled: &enabled}).EnabledEffective() {
		t.Error("explicit true must enable C2")
	}
	if !(WebShellConfig{Enabled: &enabled}).EnabledEffective() {
		t.Error("explicit true must enable WebShell")
	}
}

// The shipped template is what every fresh install inherits, so the defaults it
// writes matter more than the zero values above.
func TestExampleConfigKeepsHighRiskCapabilitiesOff(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("cannot load config.example.yaml: %v", err)
	}
	if cfg.C2.EnabledEffective() {
		t.Error("config.example.yaml must ship with c2 disabled")
	}
	if cfg.WebShell.EnabledEffective() {
		t.Error("config.example.yaml must ship with webshell disabled")
	}
}
