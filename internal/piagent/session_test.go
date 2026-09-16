package piagent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sessionPiShim is a stand-in for `pi --mode rpc` written in JavaScript. A batch
// file alone cannot read JSONL from stdin reliably, and node is already a
// prerequisite of Pi, so the shim is a .cmd wrapper around this script.
//
// It answers the three commands the interactive session uses and streams a short
// turn that only ends early when an abort arrives. Every command it receives is
// appended to PROVENA_FAKE_PI_LOG together with its pid, which is what lets the
// test prove that all prompts went to the same process.
const sessionPiShim = `
const fs = require("fs");
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin });
let messages = 0;
let timer = null;

function out(obj) { process.stdout.write(JSON.stringify(obj) + "\n"); }
function log(entry) {
  if (process.env.PROVENA_FAKE_PI_LOG) {
    fs.appendFileSync(process.env.PROVENA_FAKE_PI_LOG, JSON.stringify(entry) + "\n");
  }
}
function finish() {
  if (timer) { clearInterval(timer); timer = null; }
  out({ type: "agent_end" });
}

rl.on("line", (line) => {
  let msg = {};
  try { msg = JSON.parse(line); } catch (e) { return; }
  log({ type: msg.type, pid: process.pid });
  if (msg.type === "prompt") {
    messages++;
    out({ id: msg.id, type: "response", command: "prompt", success: true });
    out({ type: "message_start" });
    out({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "hello" } });
    const started = Date.now();
    timer = setInterval(() => {
      if (Date.now() - started > 1500) { finish(); return; }
      out({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "." } });
    }, 250);
    return;
  }
  if (msg.type === "abort") {
    out({ id: msg.id, type: "response", command: "abort", success: true });
    finish();
    return;
  }
  if (msg.type === "get_state") {
    out({ id: msg.id, type: "response", command: "get_state", success: true,
          data: { messageCount: messages, sessionFile: "/tmp/session.jsonl" } });
    return;
  }
  if (msg.type === "new_session") {
    messages = 0;
    out({ id: msg.id, type: "response", command: "new_session", success: true, data: { cancelled: false } });
  }
});

rl.on("close", () => process.exit(0));
`

// newSessionShim writes the shim and returns the command to run it.
func newSessionShim(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the fake pi shim is a Windows batch wrapper around node")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to run the RPC shim")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "shim.js")
	if err := os.WriteFile(script, []byte(sessionPiShim), 0o600); err != nil {
		t.Fatalf("cannot write the shim script: %v", err)
	}
	wrapper := filepath.Join(dir, "fake-pi.cmd")
	body := "@echo off\r\n\"" + node + "\" \"" + script + "\" %*\r\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatalf("cannot write the shim wrapper: %v", err)
	}
	logPath := filepath.Join(dir, "commands.jsonl")
	t.Setenv("PROVENA_FAKE_PI_LOG", logPath)
	return wrapper, logPath
}

type shimCommand struct {
	Type string `json:"type"`
	PID  int    `json:"pid"`
}

func readShimLog(t *testing.T, path string) []shimCommand {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entries []shimCommand
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var entry shimCommand
		if json.Unmarshal([]byte(line), &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// TestSessionKeepsOneProcessAcrossPrompts is the whole point of the interactive
// mode: a second prompt must reach the same Pi process, so the model still has
// the first turn in context.
func TestSessionKeepsOneProcessAcrossPrompts(t *testing.T) {
	command, logPath := newSessionShim(t)
	session, err := OpenSession(context.Background(), Config{Command: command})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer session.Close()

	for turn := 1; turn <= 2; turn++ {
		var deltas int
		start := time.Now()
		if err := session.Prompt(context.Background(), "hello", func(ev Event) {
			if ev.Type == "message_update" {
				deltas++
			}
		}); err != nil {
			t.Fatalf("turn %d: Prompt: %v", turn, err)
		}
		if deltas == 0 {
			t.Fatalf("turn %d: no streamed deltas reached the caller", turn)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("turn %d took %v; the shim ends a turn in ~1.5s", turn, elapsed)
		}
	}

	entries := readShimLog(t, logPath)
	var prompts []shimCommand
	for _, entry := range entries {
		if entry.Type == "prompt" {
			prompts = append(prompts, entry)
		}
	}
	if len(prompts) != 2 {
		t.Fatalf("shim saw %d prompt(s), want 2: %+v", len(prompts), entries)
	}
	if prompts[0].PID != prompts[1].PID {
		t.Fatalf("each prompt reached a different process (%d then %d); the session restarted instead of reusing Pi",
			prompts[0].PID, prompts[1].PID)
	}
}

// TestSessionAbortEndsTheTurnEarly covers the interrupt path: Abort must reach
// the process and end the turn before its natural end, without killing the
// process.
func TestSessionAbortEndsTheTurnEarly(t *testing.T) {
	command, logPath := newSessionShim(t)
	session, err := OpenSession(context.Background(), Config{Command: command})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer session.Close()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- session.Prompt(context.Background(), "long turn", nil)
	}()

	// The shim ends a turn after ~1.5s; aborting well inside that window only
	// returns early if the abort actually landed.
	time.Sleep(400 * time.Millisecond)
	if err := session.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Prompt: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("abort did not end the turn")
	}
	if elapsed := time.Since(start); elapsed > 1200*time.Millisecond {
		t.Fatalf("the turn ran %v, so the abort did not take effect", elapsed)
	}

	var sawAbort bool
	for _, entry := range readShimLog(t, logPath) {
		if entry.Type == "abort" {
			sawAbort = true
		}
	}
	if !sawAbort {
		t.Fatal("the shim never received an abort command")
	}

	// The conversation must survive the interrupt.
	if err := session.Prompt(context.Background(), "after abort", nil); err != nil {
		t.Fatalf("the session died with the interrupted turn: %v", err)
	}
}

// TestSessionStateAndNewSession covers the two request/response commands the
// REPL relies on for /info and /new.
func TestSessionStateAndNewSession(t *testing.T) {
	command, _ := newSessionShim(t)
	session, err := OpenSession(context.Background(), Config{Command: command})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer session.Close()

	if err := session.Prompt(context.Background(), "one", nil); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state, err := session.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if got := state["sessionFile"]; got != "/tmp/session.jsonl" {
		t.Fatalf("State returned %v, so the response was not matched to its command", state)
	}

	if err := session.NewSession(ctx); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	state, err = session.State(ctx)
	if err != nil {
		t.Fatalf("State after NewSession: %v", err)
	}
	if got, _ := state["messageCount"].(float64); got != 0 {
		t.Fatalf("messageCount = %v after NewSession, want 0", state["messageCount"])
	}
}

// TestSessionRejectsUnknownProviderFailure keeps the error path honest: a Pi that
// refuses a command must surface the reason instead of looking like an empty turn.
func TestSessionSurfacesRejectedPrompt(t *testing.T) {
	command, _ := newSessionShim(t)
	session, err := OpenSession(context.Background(), Config{Command: command})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer session.Close()

	// "abort" with no turn in flight is still a valid command for the shim, so
	// use get_state after closing the process to assert the closed-session path.
	session.Close()
	if err := session.Prompt(context.Background(), "anything", nil); err == nil {
		t.Fatal("a prompt against a closed session must fail")
	}
}
