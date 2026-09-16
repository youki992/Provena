package piagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	systemprompt "github.com/chobits02/provena/internal/prompt"
	"github.com/chobits02/provena/internal/security"
)

// launch is a fully resolved way to start one Pi process: the command, its
// arguments, the environment that carries the provider credentials, and the
// cleanup for the temporary agent directory holding models.json and the bridge
// extension. Both the one-shot Run and the long-lived Session start from it, so
// the flag mapping cannot drift between the two.
type launch struct {
	command string
	args    []string
	env     []string
	dir     string
	cleanup func()
}

func prepareLaunch(cfg Config) (*launch, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		command = "pi"
	}
	args := []string{"--mode", "rpc"}
	cleanupAgentDir, agentDir, err := prepareAgentDir(cfg)
	if err != nil {
		return nil, err
	}
	cleanup := cleanupAgentDir
	// fail releases whatever has already been allocated. Every early return in
	// this function goes through it; the caller only owns the cleanup once the
	// launch has been built.
	fail := func(err error) (*launch, error) {
		cleanup()
		return nil, err
	}

	if cfg.NoSession {
		args = append(args, "--no-session")
	}
	if strings.TrimSpace(cfg.SessionDir) != "" {
		args = append(args, "--session-dir", strings.TrimSpace(cfg.SessionDir))
	}
	if cfg.ContinueSession {
		args = append(args, "--continue")
	}
	provider := strings.TrimSpace(cfg.Provider)
	// A Bridge also needs a temporary Pi directory, but that directory does
	// not necessarily contain models.json. Only select the synthetic provider
	// when the runtime model catalog was actually configured.
	if runtimeProviderAvailable(agentDir, cfg) {
		provider = "provena"
	}
	if provider != "" {
		args = append(args, "--provider", provider)
	}
	if strings.TrimSpace(cfg.Model) != "" {
		args = append(args, "--model", strings.TrimSpace(cfg.Model))
	}
	if strings.TrimSpace(cfg.Thinking) != "" {
		args = append(args, "--thinking", strings.TrimSpace(cfg.Thinking))
	}
	appendSystemPrompt := systemprompt.PrependSystemPrompt(cfg.GlobalSystemPrompt, cfg.AppendSystemPrompt)
	if appendPrompt, cleanupPrompt, promptErr := prepareAppendSystemPrompt(appendSystemPrompt, agentDir); promptErr != nil {
		return fail(promptErr)
	} else if appendPrompt != "" {
		previous := cleanup
		cleanup = func() { cleanupPrompt(); previous() }
		args = append(args, "--append-system-prompt", appendPrompt)
	}
	toolAllowlist := append([]string(nil), cfg.Tools...)
	if len(cfg.BridgeTools) > 0 {
		seen := make(map[string]struct{}, len(toolAllowlist)+len(cfg.BridgeTools))
		for _, name := range toolAllowlist {
			if name = strings.TrimSpace(name); name != "" {
				seen[name] = struct{}{}
			}
		}
		for _, bridgeTool := range cfg.BridgeTools {
			if name := strings.TrimSpace(bridgeTool.Name); name != "" {
				if _, ok := seen[name]; !ok {
					toolAllowlist = append(toolAllowlist, name)
					seen[name] = struct{}{}
				}
			}
		}
	}
	if len(toolAllowlist) > 0 {
		args = append(args, "--tools", strings.Join(toolAllowlist, ","))
	}
	if cfg.NoContextFiles {
		args = append(args, "--no-context-files")
	}
	if cfg.NoSkills {
		args = append(args, "--no-skills")
	}
	if cfg.NoPromptTemplates {
		args = append(args, "--no-prompt-templates")
	}
	if cfg.NoExtensions {
		args = append(args, "--no-extensions")
	}
	if cfg.NoTools {
		args = append(args, "--no-tools")
	}
	if strings.TrimSpace(cfg.BridgeURL) != "" && len(cfg.BridgeTools) > 0 {
		extensionPath := fmt.Sprintf("%s%cbridge.ts", agentDir, os.PathSeparator)
		if agentDir == "" {
			return fail(fmt.Errorf("Pi Bridge 需要临时运行目录"))
		}
		geminiCompat := cfg.GeminiSchemaCompat || isGeminiProvider(cfg.Provider, cfg.Model, cfg.BaseURL)
		if err := writeBridgeExtension(extensionPath, cfg.BridgeURL, cfg.BridgeToken, cfg.BridgeTools, geminiCompat); err != nil {
			return fail(err)
		}
		args = append(args, "--extension", extensionPath)
	}
	for _, skillPath := range cfg.SkillPaths {
		if skillPath = strings.TrimSpace(skillPath); skillPath != "" {
			args = append(args, "--skill", skillPath)
		}
	}

	env := os.Environ()
	if agentDir != "" {
		env = append(env, "PI_CODING_AGENT_DIR="+agentDir)
		env = append(env, "PROVENA_PI_API_KEY="+cfg.APIKey)
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		env = append(env, "OPENAI_API_KEY="+cfg.APIKey)
		env = append(env, "ANTHROPIC_API_KEY="+cfg.APIKey)
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		env = append(env, "OPENAI_BASE_URL="+cfg.BaseURL)
		env = append(env, "ANTHROPIC_BASE_URL="+cfg.BaseURL)
	}

	return &launch{
		command: command,
		args:    args,
		env:     env,
		dir:     strings.TrimSpace(cfg.WorkingDir),
		cleanup: cleanup,
	}, nil
}

// piProcess is one running `pi --mode rpc` child and the machinery that keeps it
// from leaking descendants: a job object on Windows, a process-group kill
// elsewhere, and pipe closure so a reader can never block on a surviving
// grandchild.
type piProcess struct {
	launch  *launch
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	tree    *security.ProcessTree
	stderr  *bytes.Buffer
	done    chan struct{}
	stopped bool
	stopMu  sync.Mutex
	waitErr error
}

func startPiProcess(ctx context.Context, l *launch) (*piProcess, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// A job object owns the whole tree on Windows: taskkill /T walks the
	// parent-child chain and loses descendants once an intermediate process
	// exits, whereas job membership survives reparenting. Failure is not fatal —
	// the taskkill path below still applies.
	processTree, treeErr := security.NewProcessTree()
	if treeErr != nil && os.Getenv("PROVENA_PI_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[pi-debug] job object unavailable, falling back to taskkill: %v\n", treeErr)
	}

	cmd := exec.CommandContext(ctx, l.command, l.args...)
	// CommandContext's default cancel path kills only the direct child on
	// Windows. Pi forks shells and scanners underneath itself, so terminate the
	// whole tree instead — and do it from Cancel so the tree is walked while the
	// parent is still alive. Racing the default Process.Kill() with a separate
	// taskkill /T means taskkill often runs after the parent is gone and can no
	// longer enumerate the descendants.
	cmd.Cancel = func() error {
		processTree.Terminate()
		security.TerminateCommandTree(cmd)
		return nil
	}
	cmd.WaitDelay = piWaitDelay
	security.PrepareCommandForProcessTree(cmd)
	if l.dir != "" {
		cmd.Dir = l.dir
	}
	cmd.Env = l.env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 Pi stdin 失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("创建 Pi stdout 失败: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("创建 Pi stderr 失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Pi 失败（请确认已安装 pi 并在 PATH 中）: %w", err)
	}
	// Assign immediately after Start: a descendant spawned before this call
	// would sit outside the job. Release closes the handle, and kill-on-close
	// then sweeps whatever is still alive — the turn's remnants included.
	_ = processTree.Attach(cmd)

	p := &piProcess{
		launch: l,
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		tree:   processTree,
		stderr: &bytes.Buffer{},
		done:   make(chan struct{}),
	}
	go func() {
		_, _ = io.Copy(p.stderr, stderrPipe)
	}()
	// Cancelling the caller's context must both reap the tree and unblock the
	// reader. Closing the pipes is the part that actually bounds wall time: if
	// any descendant survives the tree kill it still holds the write end, and
	// the read loop would otherwise block until that process exits by itself.
	go func() {
		select {
		case <-ctx.Done():
			p.terminate()
		case <-p.done:
		}
	}()
	return p, nil
}

// terminate reaps the tree and closes the pipes so readers return promptly. It
// is idempotent and safe to call from the cancel watcher and from stop.
func (p *piProcess) terminate() {
	p.stopMu.Lock()
	already := p.stopped
	p.stopMu.Unlock()
	if already {
		return
	}
	p.tree.Terminate()
	security.TerminateCommandTree(p.cmd)
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.stdout.Close()
}

// writeLine sends one JSONL command to Pi.
func (p *piProcess) writeLine(payload []byte) error {
	if _, err := p.stdin.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func (p *piProcess) wait() error {
	p.waitErr = p.cmd.Wait()
	return p.waitErr
}

func (p *piProcess) stderrText() string {
	return strings.TrimSpace(normalizePiProcessOutput(p.stderr.Bytes()))
}

// stop terminates the process, waits for it and releases the job handle. Calling
// it twice is harmless.
func (p *piProcess) stop() {
	p.stopMu.Lock()
	if p.stopped {
		p.stopMu.Unlock()
		return
	}
	p.stopped = true
	p.stopMu.Unlock()

	p.terminate()
	_ = p.stdin.Close()
	_ = p.wait()
	close(p.done)
	p.tree.Release()
}

// isAbort reports whether a read failure is the expected consequence of stopping
// the process rather than a protocol error.
func (p *piProcess) isAbort(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	select {
	case <-p.done:
		return true
	default:
	}
	return false
}

// commandLine renders the command line for diagnostics.
func (l *launch) commandLine() string {
	return l.command + " " + strings.Join(l.args, " ")
}
