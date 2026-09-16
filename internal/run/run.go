package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/piagent"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	defaultMaxActivities = 6
	defaultActivitySecs  = 180
	goalCompleteMarker   = "GOAL_COMPLETE"
	defaultRunsDir       = "data/runs"
)

// Options configures one headless run.
type Options struct {
	Target        string
	Objective     string
	Scope         []string
	MaxActivities int
	// ActivityTimeout overrides pi_agent.activity_timeout_seconds for this run.
	// It is the idle window in seconds: a round is only cut after that long with
	// no Pi event. Zero keeps the configured value.
	ActivityTimeout int
	// ActivityMaxTimeout is an optional hard ceiling in seconds on a single
	// activity, applied on top of the idle window. Zero means no ceiling, so a
	// round that keeps producing evidence may run as long as it needs.
	ActivityMaxTimeout int
	OutDir             string
	Format             string
	DryRun             bool
	As                 string
	ConfigPath         string
	Verbose            bool
	// Plain keeps the line-oriented output: no streamed reasoning, no panels,
	// no colour. Use it for pipes, logs and CI.
	Plain bool
	// ShowThinking streams the model's reasoning blocks as they arrive.
	ShowThinking bool
	// GraphMode controls FGS graph printing: "off", "activity" or "live".
	GraphMode string
}

// Result is the durable outcome of a run.
type Result struct {
	RunID          string
	Target         string
	Objective      string
	Scope          []string
	Status         string
	Activities     int
	StartedAt      time.Time
	EndedAt        time.Time
	GraphPath      string
	ReportPath     string
	ConversationID string
}

// Deps are the already wired-up components the runner drives. Passing them in
// (rather than constructing them here) keeps the runner usable with an embedded
// application core and avoids duplicating the tool registration in internal/app.
type Deps struct {
	Agent  *agent.Agent
	DB     *database.DB
	Config *config.Config
	Logger *zap.Logger
}

type Runner struct {
	deps Deps
	opts Options
	con  *console
}

// console returns the renderer for this run, building it on first use so tests
// that construct a Runner literal still get a working (plain) console.
func (r *Runner) console() *console {
	if r.con == nil {
		r.con = newConsole(os.Stdout, r.opts)
	}
	return r.con
}

// errAborted marks a round that stopped because the operator interrupted the
// run, as opposed to an activity that failed on its own.
var errAborted = errors.New("aborted by operator")

func isAborted(err error) bool {
	return errors.Is(err, errAborted)
}

// New validates the options and prepares a runner.
func New(deps Deps, opts Options) (*Runner, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("runner requires a configuration")
	}
	if deps.Agent == nil {
		return nil, fmt.Errorf("runner requires an initialized agent")
	}
	if strings.TrimSpace(opts.Target) == "" {
		return nil, fmt.Errorf("a target is required (use -t)")
	}
	if opts.MaxActivities <= 0 {
		opts.MaxActivities = defaultMaxActivities
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		opts.OutDir = defaultRunsDir
	}
	if strings.TrimSpace(opts.As) == "" {
		opts.As = "admin"
	}
	opts.Format = normalizeFormat(opts.Format)
	return &Runner{deps: deps, opts: opts}, nil
}

// Run executes the bounded activity loop and writes the report.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	started := time.Now()
	res := Result{
		RunID:      uuid.NewString(),
		Target:     strings.TrimSpace(r.opts.Target),
		Objective:  strings.TrimSpace(r.opts.Objective),
		Scope:      r.opts.Scope,
		StartedAt:  started,
		Status:     "running",
		Activities: 0,
	}

	runDir := filepath.Join(r.opts.OutDir, res.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return res, fmt.Errorf("cannot create run directory: %w", err)
	}
	res.GraphPath = filepath.Join(runDir, "graph.jsonl")

	goal := res.Target
	if res.Objective != "" {
		goal = res.Target + " — " + res.Objective
	}

	// The provider must be resolvable before anything else: without a model the
	// activity loop would fail only after opening state and spawning processes.
	oa, channelID, found := r.deps.Config.ResolveAIChannel("")
	if !found {
		return res, fmt.Errorf("no usable AI channel; configure ai.default_channel and ai.channels")
	}
	if strings.TrimSpace(oa.APIKey) == "" || strings.TrimSpace(oa.Model) == "" {
		return res, fmt.Errorf("AI channel %q is incomplete: api_key and model are required", channelID)
	}

	graph, err := fgs.Open(res.GraphPath, goal)
	if err != nil {
		return res, fmt.Errorf("cannot open FGS graph: %w", err)
	}
	if err := bootstrapGraph(graph, res); err != nil {
		return res, err
	}

	tools := append(fgsToolDefs(), worldBridgeTools(r.deps.Agent)...)

	if r.opts.DryRun {
		res.Status = "dry_run"
		res.EndedAt = time.Now()
		fmt.Printf("dry run: wiring resolved successfully\n")
		fmt.Printf("  channel     : %s\n", channelID)
		fmt.Printf("  model       : %s\n", oa.Model)
		fmt.Printf("  base_url    : %s\n", oa.BaseURL)
		fmt.Printf("  pi command  : %s\n", r.deps.Config.PiAgent.CommandEffective())
		fmt.Printf("  tools       : %d bridge tool(s)\n", len(tools))
		fmt.Printf("  graph       : %s\n", res.GraphPath)
		fmt.Printf("  activities  : up to %d\n", r.opts.MaxActivities)
		fmt.Printf("  idle window : %ds per activity (resets on every Pi event)\n", r.activityTimeoutSeconds())
		if r.opts.ActivityMaxTimeout > 0 {
			fmt.Printf("  ceiling     : %ds per activity\n", r.opts.ActivityMaxTimeout)
		} else {
			fmt.Printf("  ceiling     : none (productive rounds are not cut)\n")
		}
		mode := "rich"
		if r.opts.Plain {
			mode = "plain"
		}
		fmt.Printf("  console     : %s, graph=%s, thinking=%t\n", mode, normalizeGraphMode(r.opts.GraphMode), r.opts.ShowThinking)
		return res, nil
	}

	principal, conversationID, err := r.prepareIdentity(ctx)
	if err != nil {
		return res, err
	}
	res.ConversationID = conversationID
	runCtx := authctx.WithPrincipal(ctx, principal)

	bridge, err := startBridge(runCtx, graph, r.deps.Agent, conversationID, tools, r.deps.Logger)
	if err != nil {
		return res, err
	}
	defer func() { _ = bridge.Close() }()

	piCfg := r.piConfig(oa, bridge, tools, runDir)
	con := r.console()
	con.header(res.Target, res.Objective, res.Scope, r.opts.MaxActivities)

	stalled := 0
	lastVersion := graph.Version()
	printedVersion := -1
	status := "max_activities"

	for activity := 1; activity <= r.opts.MaxActivities; activity++ {
		if err := runCtx.Err(); err != nil {
			status = "cancelled"
			break
		}

		snapshotJSON, err := json.Marshal(graph.Snapshot())
		if err != nil {
			return res, fmt.Errorf("cannot serialize graph: %w", err)
		}

		activityResult, runErr := r.runActivity(runCtx, piCfg, graph, snapshotJSON, activity)
		res.Activities = activity
		if runErr != nil {
			if isAborted(runErr) {
				// Operator interrupt: stop cleanly and keep the partial evidence.
				status = "cancelled"
				break
			}
			// A failed activity is data: the graph keeps whatever it recorded and
			// the next activity can pick a different approach.
			if r.deps.Logger != nil {
				r.deps.Logger.Warn("activity failed", zap.Int("activity", activity), zap.Error(runErr))
			}
		}

		if version := graph.Version(); version != printedVersion {
			con.graph(graph.Snapshot(), triggerActivity)
			printedVersion = version
		}

		if activityResult.GoalComplete {
			status = "goal_complete"
			break
		}

		if version := graph.Version(); version == lastVersion {
			stalled++
			if stalled >= 2 {
				status = "no_progress"
				break
			}
		} else {
			stalled = 0
			lastVersion = version
		}
	}

	res.Status = status
	res.EndedAt = time.Now()

	// Always close with the graph: it is the run's actual output, and reading it
	// should not require opening graph.jsonl.
	con.graph(graph.Snapshot(), triggerFinal)

	vulns := r.conversationVulnerabilities(res.ConversationID)
	if err := r.writeReports(res, graph.Snapshot(), vulns); err != nil {
		return res, err
	}
	return res, nil
}

// prepareIdentity creates the conversation that scopes tool execution and the
// principal the MCP authorizer requires.
//
// The principal is derived from the same RBAC records the console uses, but
// without a password prompt: the operator already has local filesystem access
// to the database, so this is not a privilege boundary. The account is still
// validated so a disabled or unknown -as value fails loudly.
func (r *Runner) prepareIdentity(_ context.Context) (authctx.Principal, string, error) {
	return prepareIdentity(r.deps, r.opts.As, "provena run: "+r.opts.Target)
}

// prepareIdentity is shared by the one-shot run and the interactive chat so both
// resolve the same account and open a conversation their tools can be audited
// against.
func prepareIdentity(deps Deps, as, title string) (authctx.Principal, string, error) {
	if deps.DB == nil {
		return authctx.Principal{}, "", fmt.Errorf("runner requires a database")
	}
	username := strings.ToLower(strings.TrimSpace(as))
	if username == "" {
		username = "admin"
	}
	user, err := deps.DB.GetRBACUserByUsername(username)
	if err != nil {
		return authctx.Principal{}, "", fmt.Errorf("cannot load user %q: %w", username, err)
	}
	if !user.Enabled {
		return authctx.Principal{}, "", fmt.Errorf("user %q is disabled", username)
	}
	access, err := deps.DB.ResolveRBACAccess(user.ID)
	if err != nil {
		return authctx.Principal{}, "", fmt.Errorf("cannot resolve RBAC access for %q: %w", username, err)
	}
	principal := authctx.NewPrincipalWithScopes(user.ID, user.Username, access.Scope, access.Permissions, access.PermissionScopes)

	conversation, err := deps.DB.CreateConversation(title, database.ConversationCreateMeta{})
	if err != nil {
		return authctx.Principal{}, "", fmt.Errorf("cannot create run conversation: %w", err)
	}
	return principal, conversation.ID, nil
}

// piConfig mirrors the interactive harness so a headless activity behaves the
// same as a console activity.
func (r *Runner) piConfig(oa config.OpenAIConfig, bridge *bridge, tools []piagent.BridgeTool, runDir string) piagent.Config {
	cfg := r.deps.Config
	pi := piagent.Config{
		Command:            cfg.PiAgent.CommandEffective(),
		Provider:           oa.Provider,
		Protocol:           "openai-completions",
		Model:              oa.Model,
		Thinking:           cfg.PiAgent.Thinking,
		AppendSystemPrompt: headlessContract,
		GlobalSystemPrompt: oa.GlobalSystemPrompt,
		ContextWindow:      oa.MaxTotalTokens,
		MaxTokens:          oa.MaxCompletionTokens,
		APIKey:             oa.APIKey,
		BaseURL:            oa.BaseURL,
		WorkingDir:         runDir,
		NoSession:          true,
		NoContextFiles:     true,
		NoSkills:           true,
		NoPromptTemplates:  true,
		NoExtensions:       true,
		Tools:              []string{"read", "grep", "find", "ls"},
		BridgeURL:          bridge.URL(),
		BridgeToken:        bridge.Token(),
		BridgeTools:        tools,
	}
	return pi
}

type activityResult struct {
	GoalComplete bool
	ToolCalls    int
}

// activityTimeoutSeconds resolves the idle window for one activity. A per-run
// override wins over the configuration file.
func (r *Runner) activityTimeoutSeconds() int {
	if r.opts.ActivityTimeout > 0 {
		return r.opts.ActivityTimeout
	}
	if r.deps.Config != nil {
		if timeout := r.deps.Config.PiAgent.ActivityTimeoutSecondsEffective(); timeout > 0 {
			return timeout
		}
	}
	return defaultActivitySecs
}

// runActivity performs exactly one stateless activity against the graph.
//
// The activity is bounded by an idle window, not by a fixed wall clock.
// activity_timeout_seconds is both the floor and the silence budget: a round
// always gets at least that long, and every Pi event pushes the deadline out.
// A round is therefore only cut when it stops producing anything — which is what
// the setting actually guards against — while a round that keeps gathering
// evidence is free to run as long as it needs. A stalled round still dies no
// later than it did under the old fixed deadline, so this is a strict widening.
//
// ActivityMaxTimeout, when set, puts a hard ceiling back on top for callers who
// want a predictable total cost rather than maximum depth.
func (r *Runner) runActivity(ctx context.Context, piCfg piagent.Config, graph *fgs.Store, snapshotJSON []byte, activity int) (activityResult, error) {
	timeout := r.activityTimeoutSeconds()
	idle := time.Duration(timeout) * time.Second

	activityCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var maxTotal time.Duration
	if r.opts.ActivityMaxTimeout > 0 {
		maxTotal = time.Duration(r.opts.ActivityMaxTimeout) * time.Second
	}

	var (
		deadlineMu sync.Mutex
		startedAt  = time.Now()
	)
	// clamp keeps the extendable deadline inside the optional hard ceiling.
	clamp := func(at time.Time) time.Time {
		if maxTotal > 0 {
			if limit := startedAt.Add(maxTotal); at.After(limit) {
				return limit
			}
		}
		return at
	}
	deadline := clamp(startedAt.Add(idle))

	var idleFired, capFired atomic.Bool
	// touch pushes the deadline out. It only ever extends, so a burst of events
	// can never shorten the guaranteed floor.
	touch := func() {
		deadlineMu.Lock()
		if next := clamp(time.Now().Add(idle)); next.After(deadline) {
			deadline = next
		}
		deadlineMu.Unlock()
	}

	watchdogDone := make(chan struct{})
	defer close(watchdogDone)
	go func() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-activityCtx.Done():
				return
			case <-timer.C:
				deadlineMu.Lock()
				remaining := time.Until(deadline)
				deadlineMu.Unlock()
				if remaining > 0 {
					timer.Reset(remaining)
					continue
				}
				if maxTotal > 0 && time.Since(startedAt) >= maxTotal {
					capFired.Store(true)
				} else {
					idleFired.Store(true)
				}
				cancel()
				return
			}
		}
	}()

	prompt := buildActivityPrompt(r.opts, piCfg.WorkingDir, string(snapshotJSON), activity, r.opts.MaxActivities)

	out := activityResult{}
	con := r.console()
	con.activityStart(activity, r.opts.MaxActivities)
	activityStart := time.Now()

	err := piagent.Run(activityCtx, piCfg, prompt, func(ev piagent.Event) {
		touch()
		con.event(ev)
		switch ev.Type {
		case "tool_execution_start":
			out.ToolCalls++
		case "tool_execution_end":
			if isGraphMutation(rawString(ev.Raw, "toolName", "name")) {
				con.graph(graph.Snapshot(), triggerMutation)
			}
		case "message_end", "agent_end", "turn_end", "response":
			if text := finalText(ev.Raw); text != "" && strings.Contains(text, goalCompleteMarker) {
				out.GoalComplete = true
			}
		}
	})
	// Only annotate a real failure: a round that completed just as the watchdog
	// fired still counts as completed.
	if err != nil {
		switch {
		case capFired.Load():
			// Stay recognisable as a timeout while saying what actually happened.
			err = fmt.Errorf("activity hit the %ds ceiling: %w", r.opts.ActivityMaxTimeout, context.DeadlineExceeded)
		case idleFired.Load():
			err = fmt.Errorf("activity stalled: no Pi event for %ds: %w", timeout, context.DeadlineExceeded)
		case ctx.Err() != nil:
			// The parent context was cancelled: the operator stopped the run, so
			// this is not an activity failure. Wrap both so callers can test for
			// the abort or for the underlying context error.
			err = fmt.Errorf("%w: %w", errAborted, err)
		}
	}
	con.activityEnd(out.ToolCalls, time.Since(activityStart), err)
	return out, err
}

func bootstrapGraph(graph *fgs.Store, res Result) error {
	if graph == nil {
		return fmt.Errorf("graph is required")
	}
	if graph.Version() > 0 {
		return nil
	}
	mutations := []fgs.Mutation{{
		Op:      "add_node",
		ID:      "scope",
		Kind:    fgs.KindFact,
		Label:   "authorized scope",
		Content: scopeDescription(res),
		Status:  fgs.StatusConfirmed,
	}}
	if _, err := graph.Apply(mutations); err != nil {
		return fmt.Errorf("cannot seed graph scope: %w", err)
	}
	return nil
}

func scopeDescription(res Result) string {
	if len(res.Scope) > 0 {
		return "Authorized scope: " + strings.Join(res.Scope, ", ")
	}
	return "Authorized scope: " + res.Target
}

func (r *Runner) conversationVulnerabilities(conversationID string) []*database.Vulnerability {
	if r.deps.DB == nil || strings.TrimSpace(conversationID) == "" {
		return nil
	}
	vulns, err := r.deps.DB.ListVulnerabilities(1000, 0, database.VulnerabilityListFilter{ConversationID: conversationID})
	if err != nil {
		if r.deps.Logger != nil {
			r.deps.Logger.Warn("cannot list run vulnerabilities", zap.Error(err))
		}
		return nil
	}
	return vulns
}

func (r *Runner) writeReports(res Result, snapshot fgs.Snapshot, vulns []*database.Vulnerability) error {
	runDir := filepath.Dir(res.GraphPath)

	markdown := buildMarkdown(res, snapshot, vulns)
	mdPath := filepath.Join(runDir, "report.md")
	if err := os.WriteFile(mdPath, []byte(markdown), 0o600); err != nil {
		return fmt.Errorf("cannot write markdown report: %w", err)
	}

	jsonData, err := buildJSON(res, snapshot, vulns)
	if err != nil {
		return fmt.Errorf("cannot build JSON report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "report.json"), jsonData, 0o600); err != nil {
		return fmt.Errorf("cannot write JSON report: %w", err)
	}

	sarif, err := buildSARIF(res, snapshot, vulns)
	if err != nil {
		return fmt.Errorf("cannot build SARIF report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "report.sarif"), sarif, 0o600); err != nil {
		return fmt.Errorf("cannot write SARIF report: %w", err)
	}

	// The requested format is what the operator sees on stdout.
	switch r.opts.Format {
	case formatJSON:
		fmt.Printf("\n%s\n", jsonData)
	case formatSARIF:
		fmt.Printf("\n%s\n", sarif)
	default:
		res.ReportPath = mdPath
	}
	if res.ReportPath == "" {
		res.ReportPath = mdPath
	}
	return nil
}

func rawValue(raw map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func rawString(raw map[string]interface{}, keys ...string) string {
	value, ok := rawValue(raw, keys...)
	if !ok {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func rawValueString(raw map[string]interface{}, keys ...string) string {
	value, ok := rawValue(raw, keys...)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(data)
	}
}

// finalText extracts assistant text from a terminal Pi event.
func finalText(raw map[string]interface{}) string {
	for _, key := range []string{"text", "content", "message"} {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		case map[string]interface{}:
			if text, ok := typed["content"].(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
			if text, ok := typed["text"].(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}
