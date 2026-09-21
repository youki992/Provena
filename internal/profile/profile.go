// Package profile describes the capability profiles a Provena run can select.
//
// A profile is data, not a branch. Before this package a profile was a magic
// string in config.Profile compared against literals in four separate places,
// and shipping a second capability set meant pointing the whole config file at
// a different tools directory. Adding a profile should now mean adding one entry
// here, not editing the agent.
//
// Only dimensions that a call site actually enforces are modelled. Add a field
// when something reads it.
package profile

import (
	"os"
	"path/filepath"
	"strings"
)

// Profile is one selectable capability set.
type Profile struct {
	// Name is the canonical identifier, as written in config.Profile.
	Name string

	// Skills lists the skill directories a run may load, in preference order.
	// The first one present on disk wins. An empty list keeps the run
	// skill-free, which is the behaviour of every profile except v3-minimal.
	Skills []string

	// Tools lists the tool recipe names (the `name:` field of tools/*.yaml) a
	// run may register. An empty list means no restriction: every recipe under
	// tools_dir loads.
	Tools []string

	// PiActivityTools are extra Pi-native tool names appended to an FGS
	// activity's tool set. Empty means Pi keeps whatever the server exposed.
	PiActivityTools []string

	// SkillsRootFallback is the skills directory used when skills_dir is unset.
	SkillsRootFallback string

	// PlaybookRel is the FGS playbook directory, relative to the resolved skills
	// root.
	PlaybookRel string

	// HonoursSkillsDir reports whether skills_dir overrides SkillsRootFallback
	// when resolving the playbook directory. The default profile has always
	// read a fixed path and ignored skills_dir, so this stays false for it.
	HonoursSkillsDir bool
}

// defaultProfile is what an empty, unknown or misspelled profile resolves to.
// Staying permissive is deliberate: a typo in the config file must not silently
// strip a user's tools.
var defaultProfile = Profile{
	Name:               "default",
	SkillsRootFallback: "skills",
	PlaybookRel:        "src-hunter/references/playbooks",
}

// v3Minimal is the low-impact capability set: request, search and read work
// only, no third-party CLI scanners, one bundled skill for world tasks, and the
// read-only Pi file tools instead of a shell.
var v3Minimal = Profile{
	Name:   "v3-minimal",
	Skills: []string{"src-6k-skill"},
	Tools: []string{
		"http-framework-test",
		"web-search",
		"query-execution-result",
	},
	PiActivityTools:    []string{"read", "grep", "find", "ls"},
	SkillsRootFallback: "skills-v3",
	PlaybookRel:        "src-6k-skill/知识库",
	HonoursSkillsDir:   true,
}

// For resolves a configured profile name. Matching is case-insensitive, and
// "minimal" is accepted as an alias for "v3-minimal".
func For(name string) Profile {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "v3-minimal", "minimal":
		return v3Minimal
	default:
		return defaultProfile
	}
}

// IsDefault reports whether this profile is the unrestricted one.
func (p Profile) IsDefault() bool { return p.Name == defaultProfile.Name }

// RestrictsTools reports whether a tool allowlist applies.
func (p Profile) RestrictsTools() bool { return len(p.Tools) > 0 }

// AllowsTool reports whether a recipe may be registered. The default profile
// allows everything; matching is case-insensitive.
func (p Profile) AllowsTool(name string) bool {
	if !p.RestrictsTools() {
		return true
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, allowed := range p.Tools {
		if strings.ToLower(strings.TrimSpace(allowed)) == want {
			return true
		}
	}
	return false
}

// SkillPath returns the SKILL.md of the first allowed skill that exists under
// skillsRoot, or "" when this profile loads no skills or none are installed.
func (p Profile) SkillPath(skillsRoot string) string {
	root := strings.TrimSpace(skillsRoot)
	if root == "" {
		return ""
	}
	for _, dir := range p.Skills {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		candidate := filepath.Join(root, dir, "SKILL.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// PlaybookDir resolves the directory the FGS playbooks are read from.
func (p Profile) PlaybookDir(configuredSkillsDir, cwd string) string {
	if strings.TrimSpace(p.PlaybookRel) == "" {
		return ""
	}
	root := strings.TrimSpace(p.SkillsRootFallback)
	if root == "" {
		root = "skills"
	}
	if p.HonoursSkillsDir {
		if configured := strings.TrimSpace(configuredSkillsDir); configured != "" {
			root = configured
		}
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(cwd, root)
	}
	return filepath.Join(root, filepath.FromSlash(p.PlaybookRel))
}
