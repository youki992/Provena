package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/chobits02/provena/internal/app"
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/logger"
	"github.com/chobits02/provena/internal/run"
)

// runChat starts the interactive session. Unlike runRun it installs no signal
// handler of its own: the chat loop needs to distinguish "interrupt this turn"
// from "leave the session", which only it can do.
func runChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to the configuration file")
	target := fs.String("t", "", "Authorized target; optional, it seeds the session scope")
	fs.StringVar(target, "target", "", "Alias for -t")
	objective := fs.String("objective", "", "What this session is about")
	scope := fs.String("scope", "", "Comma-separated authorized scope; defaults to the target")
	as := fs.String("as", "admin", "Platform user whose RBAC permissions the session uses")
	sessionsDir := fs.String("sessions", "data/sessions", "Directory holding one state directory per session")
	cont := fs.Bool("continue", false, "Resume the most recent session in -sessions instead of starting a new one")
	verbose := fs.Bool("v", false, "Print tool results as well as tool names")
	plain := fs.Bool("plain", false, "Line-oriented output: no streamed reasoning, no panels, no colour")
	thinking := fs.Bool("thinking", true, "Stream the model's reasoning as it arrives")
	graphMode := fs.String("graph", "activity", "Print the FGS graph to the console: off, activity or live")
	_ = fs.Parse(args)

	switch strings.ToLower(strings.TrimSpace(*graphMode)) {
	case "off", "activity", "live":
	default:
		return fmt.Errorf(`-graph must be "off", "activity" or "live"`)
	}

	cp := strings.TrimSpace(*configPath)
	if cp == "" {
		cp = defaultConfigPath
	}

	cfg, err := config.Load(cp)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Output)

	// An interactive session consumes neither captured traffic nor C2 sessions.
	cfg.PacketCapture.Enabled = false
	c2Disabled := false
	cfg.C2.Enabled = &c2Disabled

	application, err := app.New(cfg, log, cp)
	if err != nil {
		return fmt.Errorf("application init failed: %w", err)
	}
	defer application.Shutdown()

	var scopeList []string
	for _, part := range strings.Split(*scope, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			scopeList = append(scopeList, trimmed)
		}
	}

	chatter, err := run.NewChat(run.Deps{
		Agent:  application.Agent(),
		DB:     application.Database(),
		Config: cfg,
		Logger: log.Logger,
	}, run.ChatOptions{
		Target:       *target,
		Objective:    *objective,
		Scope:        scopeList,
		As:           *as,
		ConfigPath:   cp,
		OutDir:       *sessionsDir,
		Continue:     *cont,
		Verbose:      *verbose,
		Plain:        *plain,
		ShowThinking: *thinking,
		GraphMode:    *graphMode,
	})
	if err != nil {
		return err
	}

	return chatter.Run(context.Background())
}
