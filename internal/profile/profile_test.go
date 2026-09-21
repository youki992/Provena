package profile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestForUnrestrictedByDefault(t *testing.T) {
	for _, name := range []string{"", "  ", "default", "staging", "v9-typo"} {
		p := For(name)
		if !p.IsDefault() {
			t.Errorf("For(%q).IsDefault() = false, want true", name)
		}
		if p.RestrictsTools() {
			t.Errorf("For(%q).RestrictsTools() = true, want false", name)
		}
		// An unknown profile must not strip tools.
		if !p.AllowsTool("nmap") {
			t.Errorf("For(%q).AllowsTool(nmap) = false, want true", name)
		}
	}
}

func TestForV3MinimalIsCaseInsensitiveAndAliased(t *testing.T) {
	for _, name := range []string{"v3-minimal", "V3-MINIMAL", " v3-minimal ", "minimal", "MINIMAL"} {
		p := For(name)
		if p.IsDefault() {
			t.Fatalf("For(%q) resolved to the default profile", name)
		}
		if p.Name != "v3-minimal" {
			t.Errorf("For(%q).Name = %q, want v3-minimal", name, p.Name)
		}
	}
}

func TestV3MinimalToolAllowlist(t *testing.T) {
	p := For("v3-minimal")

	for _, allowed := range []string{"web-search", "Web-Search", "http-framework-test", "query-execution-result"} {
		if !p.AllowsTool(allowed) {
			t.Errorf("AllowsTool(%q) = false, want true", allowed)
		}
	}
	for _, denied := range []string{"nmap", "ffuf", "fofa_search", "metasploit", ""} {
		if p.AllowsTool(denied) {
			t.Errorf("AllowsTool(%q) = true, want false", denied)
		}
	}
}

func TestSkillPathPicksFirstInstalledSkill(t *testing.T) {
	root := t.TempDir()

	// Nothing installed yet: no skill, and the run stays skill-free.
	if got := v3Minimal.SkillPath(root); got != "" {
		t.Errorf("SkillPath() = %q, want empty when nothing is installed", got)
	}

	// Empty root is always empty rather than a relative path into the cwd.
	if got := v3Minimal.SkillPath("  "); got != "" {
		t.Errorf("SkillPath(blank) = %q, want empty", got)
	}

	want := filepath.Join(root, "src-6k-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := v3Minimal.SkillPath(root); got != want {
		t.Errorf("SkillPath() = %q, want %q", got, want)
	}
	// A directory without SKILL.md is not a skill.
	if got := v3Minimal.SkillPath(filepath.Join(root, "src-6k-skill")); got != "" {
		t.Errorf("SkillPath(no SKILL.md) = %q, want empty", got)
	}
}

func TestDefaultProfileLoadsNoSkills(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "src-6k-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The file exists, but an unrestricted profile does not opt into skills.
	if got := For("").SkillPath(root); got != "" {
		t.Errorf("default SkillPath() = %q, want empty", got)
	}
}

func TestPiActivityTools(t *testing.T) {
	if got := For("").PiActivityTools; len(got) != 0 {
		t.Errorf("default PiActivityTools = %v, want none", got)
	}
	want := []string{"read", "grep", "find", "ls"}
	if got := For("v3-minimal").PiActivityTools; !slices.Equal(got, want) {
		t.Errorf("v3-minimal PiActivityTools = %v, want %v", got, want)
	}
}

func TestPlaybookDir(t *testing.T) {
	cwd := filepath.Join(string(filepath.Separator)+"tmp", "run")

	// v3-minimal honours skills_dir when it is set...
	got := For("v3-minimal").PlaybookDir("custom-skills", cwd)
	if want := filepath.Join(cwd, "custom-skills", "src-6k-skill", "知识库"); got != want {
		t.Errorf("v3-minimal PlaybookDir(configured) = %q, want %q", got, want)
	}
	// ...and falls back to skills-v3 when it is not.
	got = For("v3-minimal").PlaybookDir("", cwd)
	if want := filepath.Join(cwd, "skills-v3", "src-6k-skill", "知识库"); got != want {
		t.Errorf("v3-minimal PlaybookDir(empty) = %q, want %q", got, want)
	}

	// The default profile reads a fixed path and ignores skills_dir, which is
	// the behaviour it has always had.
	got = For("").PlaybookDir("custom-skills", cwd)
	if want := filepath.Join(cwd, "skills", "src-hunter", "references", "playbooks"); got != want {
		t.Errorf("default PlaybookDir() = %q, want %q", got, want)
	}
}
