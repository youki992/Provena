package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/chobits02/provena/internal/app"
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/logger"
	"github.com/chobits02/provena/internal/run"
)

func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to the configuration file")
	target := fs.String("t", "", "Target to test (URL, host or host:port)")
	fs.StringVar(target, "target", "", "Alias for -t")
	objective := fs.String("objective", "", "What the run should achieve against the target")
	scope := fs.String("scope", "", "Comma-separated authorized scope; defaults to the target")
	maxActivities := fs.Int("max-activities", 6, "Maximum number of model activities")
	activityTimeout := fs.Int("activity-timeout", 0, "Idle seconds before a stalled activity is cut; every Pi event resets it (0 = use pi_agent.activity_timeout_seconds)")
	activityMaxTimeout := fs.Int("activity-max-timeout", 0, "Hard ceiling in seconds on a single activity, on top of the idle window (0 = no ceiling)")
	outDir := fs.String("out", "data/runs", "Directory for run state and reports")
	format := fs.String("format", "md", "Report format printed to stdout: md, json or sarif")
	dryRun := fs.Bool("dry-run", false, "Validate wiring without calling the model")
	as := fs.String("as", "admin", "Platform user whose RBAC permissions the run uses")
	verbose := fs.Bool("v", false, "Print tool results as well as tool names")
	plain := fs.Bool("plain", false, "Line-oriented output: no streamed reasoning, no panels, no colour")
	thinking := fs.Bool("thinking", true, "Stream the model's reasoning as it arrives")
	graphMode := fs.String("graph", "activity", "Print the FGS graph to the console: off, activity or live")
	_ = fs.Parse(args)

	if strings.TrimSpace(*target) == "" {
		return fmt.Errorf("a target is required: provena run -t https://example.com")
	}
	if *activityTimeout < 0 {
		return fmt.Errorf("-activity-timeout must not be negative")
	}
	if *activityMaxTimeout < 0 {
		return fmt.Errorf("-activity-max-timeout must not be negative")
	}
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

	// A headless run consumes neither captured traffic nor C2 sessions, so do not
	// start a local capture proxy or a listener as a side effect of `provena run`.
	cfg.PacketCapture.Enabled = false
	c2Disabled := false
	cfg.C2.Enabled = &c2Disabled

	// Build the same application core the console uses. This is what makes the
	// headless path reuse the identical tool registry, MCP wiring and RBAC
	// records instead of duplicating them.
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

	runner, err := run.New(run.Deps{
		Agent:  application.Agent(),
		DB:     application.Database(),
		Config: cfg,
		Logger: log.Logger,
	}, run.Options{
		Target:             *target,
		Objective:          *objective,
		Scope:              scopeList,
		MaxActivities:      *maxActivities,
		ActivityTimeout:    *activityTimeout,
		ActivityMaxTimeout: *activityMaxTimeout,
		OutDir:             *outDir,
		Format:             *format,
		DryRun:             *dryRun,
		As:                 *as,
		ConfigPath:         cp,
		Verbose:            *verbose,
		Plain:              *plain,
		ShowThinking:       *thinking,
		GraphMode:          *graphMode,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// First Ctrl+C aborts the running activity and lets the partial report be
	// written; a second one exits immediately if something is wedged.
	var interrupts atomic.Int32
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sigCh {
			if interrupts.Add(1) == 1 {
				fmt.Fprintln(os.Stderr, "\nstopping now — aborting the running activity...")
				cancel()
				continue
			}
			fmt.Fprintln(os.Stderr, "forced exit")
			os.Exit(130)
		}
	}()

	result, err := runner.Run(ctx)
	printRunSummary(result)
	if err != nil {
		return err
	}
	return nil
}

func printRunSummary(result run.Result) {
	fmt.Println()
	fmt.Printf("run id      : %s\n", result.RunID)
	fmt.Printf("target      : %s\n", result.Target)
	fmt.Printf("status      : %s\n", result.Status)
	fmt.Printf("activities  : %d\n", result.Activities)
	if result.GraphPath != "" {
		fmt.Printf("graph       : %s\n", result.GraphPath)
	}
	if result.ReportPath != "" {
		fmt.Printf("report      : %s\n", result.ReportPath)
	}
	fmt.Println()
	fmt.Println("Findings are evidence-backed observations; confirm before acting on them.")
}
