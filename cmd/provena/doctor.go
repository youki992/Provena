package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/chobits02/provena/internal/config"
)

// doctorReport tallies checks so the command can exit non-zero on failure.
type doctorReport struct {
	failures int
	warnings int
}

func (d *doctorReport) ok(format string, a ...interface{}) {
	fmt.Printf("  ok    %s\n", fmt.Sprintf(format, a...))
}

func (d *doctorReport) warn(format string, a ...interface{}) {
	d.warnings++
	fmt.Printf("  warn  %s\n", fmt.Sprintf(format, a...))
}

func (d *doctorReport) fail(format string, a ...interface{}) {
	d.failures++
	fmt.Printf("  FAIL  %s\n", fmt.Sprintf(format, a...))
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to the configuration file")
	_ = fs.Parse(args)

	cp := strings.TrimSpace(*configPath)
	if cp == "" {
		cp = defaultConfigPath
	}

	rep := &doctorReport{}

	fmt.Printf("Provena doctor (%s/%s)\n\n", runtime.GOOS, runtime.GOARCH)

	// ---- configuration -------------------------------------------------------
	fmt.Println("Configuration")
	cfg, err := config.Load(cp)
	if err != nil {
		rep.fail("cannot load %s: %v", cp, err)
		fmt.Printf("\n%d failure(s), %d warning(s)\n", rep.failures, rep.warnings)
		return fmt.Errorf("configuration could not be loaded")
	}
	rep.ok("%s loaded", cp)
	if v := strings.TrimSpace(cfg.Version); v != "" {
		rep.ok("version %s", v)
	}
	if p := strings.TrimSpace(cfg.Profile); p != "" {
		rep.ok("profile %q", p)
	} else {
		rep.ok("profile: default (full capability set)")
	}

	// ---- model provider ------------------------------------------------------
	fmt.Println("\nModel provider")
	if oa, channelID, found := cfg.ResolveAIChannel(""); found {
		switch {
		case strings.TrimSpace(oa.APIKey) == "":
			rep.fail("channel %q resolved but api_key is empty; set it in the config or the referenced env var", channelID)
		case strings.Contains(oa.APIKey, "${"):
			rep.fail("channel %q api_key still contains an unexpanded ${VAR} reference", channelID)
		default:
			rep.ok("channel %q api_key is set", channelID)
		}
		if strings.TrimSpace(oa.BaseURL) == "" {
			rep.warn("channel %q has no base_url", channelID)
		} else {
			rep.ok("base_url %s", oa.BaseURL)
		}
		if strings.TrimSpace(oa.Model) == "" {
			rep.warn("channel %q has no model", channelID)
		} else {
			rep.ok("model %s", oa.Model)
		}
	} else {
		rep.fail("no usable AI channel; configure ai.channels and ai.default_channel")
	}

	// ---- agent runtime --------------------------------------------------------
	fmt.Println("\nAgent runtime")
	if !cfg.PiAgent.Enabled {
		rep.warn("pi_agent.enabled is false; agent tasks will not run")
	} else {
		cmdName := strings.TrimSpace(cfg.PiAgent.Command)
		if cmdName == "" {
			cmdName = "pi"
		}
		if p, err := exec.LookPath(cmdName); err != nil {
			rep.fail("pi runtime %q not found on PATH; install it or set pi_agent.command", cmdName)
		} else {
			rep.ok("pi runtime %s", p)
		}
		mode := strings.TrimSpace(cfg.PiAgent.HarnessMode)
		if mode == "" {
			mode = "legacy"
		}
		rep.ok("harness mode %q", mode)
	}

	// ---- python (used by the tool recipes) ------------------------------------
	fmt.Println("\nPython")
	if py, err := lookPathAny("python3", "python"); err != nil {
		rep.warn("no python interpreter on PATH; python-based tool recipes will be skipped")
	} else {
		rep.ok("interpreter %s", py)
	}

	// ---- external MCP servers -------------------------------------------------
	fmt.Println("\nExternal MCP servers")
	if len(cfg.ExternalMCP.Servers) == 0 {
		rep.ok("none configured; Provena runs with built-in tools only")
	} else {
		names := make([]string, 0, len(cfg.ExternalMCP.Servers))
		for name := range cfg.ExternalMCP.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
		enabled := 0
		for _, name := range names {
			srv := cfg.ExternalMCP.Servers[name]
			if srv.Disabled {
				rep.ok("%s (disabled)", name)
				continue
			}
			enabled++
			target := strings.TrimSpace(srv.URL)
			if target == "" {
				target = strings.TrimSpace(srv.Command)
			}
			if target == "" {
				rep.warn("%s has neither url nor command", name)
				continue
			}
			rep.ok("%s -> %s", name, target)
		}
		if enabled == 0 {
			rep.warn("all configured MCP servers are disabled")
		}
	}

	// ---- local content directories --------------------------------------------
	fmt.Println("\nContent directories")
	checkDir := func(label, dir string) {
		if strings.TrimSpace(dir) == "" {
			rep.warn("%s not set; the built-in default is used", label)
			return
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			rep.warn("%s %s does not exist", label, dir)
			return
		}
		rep.ok("%s %s", label, dir)
	}
	checkDir("tools_dir", cfg.Security.ToolsDir)
	checkDir("skills_dir", cfg.SkillsDir)
	checkDir("agents_dir", cfg.AgentsDir)

	// ---- writable state --------------------------------------------------------
	fmt.Println("\nState")
	dbPath := strings.TrimSpace(cfg.Database.Path)
	if dbPath == "" {
		dbPath = "data/conversations.db"
	}
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		rep.fail("cannot create database directory %s: %v", dbDir, err)
	} else {
		rep.ok("database directory %s", dbDir)
	}
	if st, err := os.Stat(dbPath); err == nil && !st.IsDir() {
		rep.ok("database %s", dbPath)
	} else {
		rep.warn("database %s does not exist yet; it is created on first serve", dbPath)
	}

	// ---- summary ---------------------------------------------------------------
	fmt.Printf("\n%d failure(s), %d warning(s)\n", rep.failures, rep.warnings)
	if rep.failures > 0 {
		return fmt.Errorf("doctor found %d problem(s)", rep.failures)
	}
	return nil
}

func lookPathAny(names ...string) (string, error) {
	var lastErr error
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("not found")
	}
	return "", lastErr
}
