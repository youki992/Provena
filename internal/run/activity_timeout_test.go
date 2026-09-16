package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/fgs"
)

// fakePi writes a stand-in pi executable. The batch-file shape matters: it
// reproduces the Windows process layout that made a fixed deadline ineffective,
// where the timeout kills the direct child (cmd.exe) while a grandchild (ping)
// keeps holding the inherited stdout pipe open.
func fakePi(t *testing.T, dir, name, script string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the fake pi shims are Windows batch files")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write the fake pi shim: %v", err)
	}
	return path
}

// silentPi sleeps far longer than any window used below and emits nothing.
const silentPi = "@echo off\r\nping -n 90 127.0.0.1 > NUL\r\n"

// chattyPi emits one event every ~2s for ~16s, standing in for a model that is
// streaming and calling tools.
const chattyPi = "@echo off\r\n" +
	"for /l %%i in (1,1,8) do (\r\n" +
	"  echo {\"type\":\"message_end\",\"id\":\"%%i\"}\r\n" +
	"  ping -n 3 127.0.0.1 > NUL\r\n" +
	")\r\n"

func runActivityWithFakePi(t *testing.T, script string, opts Options) (time.Duration, error) {
	t.Helper()
	dir := t.TempDir()
	shim := fakePi(t, dir, "fake-pi.cmd", script)

	graph, err := fgs.Open(filepath.Join(dir, "graph.jsonl"), "goal")
	if err != nil {
		t.Fatalf("fgs.Open: %v", err)
	}

	cfg := &config.Config{}
	cfg.PiAgent.Command = shim
	cfg.PiAgent.ActivityTimeoutSeconds = opts.ActivityTimeout
	if opts.ActivityTimeout == 0 {
		cfg.PiAgent.ActivityTimeoutSeconds = 3
	}

	opts.MaxActivities = 1
	r := &Runner{deps: Deps{Config: cfg}, opts: opts}

	piCfg := r.piConfig(config.OpenAIConfig{}, nil, nil, dir)
	piCfg.Command = shim

	start := time.Now()
	_, runErr := r.runActivity(context.Background(), piCfg, graph, []byte("{}"), 1)
	elapsed := time.Since(start)
	t.Logf("elapsed=%v err=%v", elapsed, runErr)
	return elapsed, runErr
}

// TestRunActivityHonoursActivityTimeout guards the documented protection: a
// silent, stalled round must be cut, and the cut must actually bound wall time
// rather than merely be reported after the fact.
func TestRunActivityHonoursActivityTimeout(t *testing.T) {
	elapsed, runErr := runActivityWithFakePi(t, silentPi, Options{
		Target: "http://127.0.0.1:1/", ActivityTimeout: 3,
	})

	if runErr == nil {
		t.Fatal("the activity was not stopped by the timeout")
	}
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("activity stopped for the wrong reason: %v (a start failure would mask a broken timeout)", runErr)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("activity ran %v despite a 3s timeout: the process tree was not reaped", elapsed)
	}
}

// TestRunActivityExtendsWhilePiKeepsWorking is the counterpart to the test above:
// the idle window must not truncate a round that is still producing events.
//
// A single fixed wall-clock budget cannot serve both ends — a round that needs
// ten minutes of evidence gathering gets guillotined at three, which is exactly
// the trade-off this test pins down. Under a 4s window a fixed-deadline
// implementation cuts at 4s; the idle window lets this round run to completion.
func TestRunActivityExtendsWhilePiKeepsWorking(t *testing.T) {
	elapsed, runErr := runActivityWithFakePi(t, chattyPi, Options{
		Target: "http://127.0.0.1:1/", ActivityTimeout: 4,
	})

	if runErr != nil {
		t.Fatalf("a progressing activity was cut short: %v", runErr)
	}
	if elapsed < 9*time.Second {
		t.Fatalf("activity ended after %v: the idle window was not extended by Pi events", elapsed)
	}
}

// TestRunActivityHonoursActivityCeiling covers the third case: a caller who
// wants a predictable cost can put a hard ceiling back on top of the idle
// window, and it must bite even while the round is still busy.
func TestRunActivityHonoursActivityCeiling(t *testing.T) {
	elapsed, runErr := runActivityWithFakePi(t, chattyPi, Options{
		Target: "http://127.0.0.1:1/", ActivityTimeout: 60, ActivityMaxTimeout: 4,
	})

	if runErr == nil {
		t.Fatal("the ceiling did not stop the activity")
	}
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("activity stopped for the wrong reason: %v", runErr)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("activity ran %v despite a 4s ceiling", elapsed)
	}
}

// TestRunActivityReportsOperatorInterrupt pins the difference between "this
// round failed" and "the operator stopped the run". Cancelling the parent
// context must surface as an abort so Run ends cleanly with status cancelled
// and still writes the partial report, instead of logging a phantom activity
// failure.
func TestRunActivityReportsOperatorInterrupt(t *testing.T) {
	// A best-effort directory rather than t.TempDir: cancelling mid-start kills
	// the fake-pi tree asynchronously, and on Windows a descendant that has
	// already been reparented can keep holding the working directory. Failing
	// the test over that would hide the semantics it actually asserts.
	dir, err := os.MkdirTemp("", "provena-abort-test-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	shim := fakePi(t, dir, "fake-pi.cmd", silentPi)

	graph, err := fgs.Open(filepath.Join(dir, "graph.jsonl"), "goal")
	if err != nil {
		t.Fatalf("fgs.Open: %v", err)
	}

	cfg := &config.Config{}
	cfg.PiAgent.Command = shim
	cfg.PiAgent.ActivityTimeoutSeconds = 120

	r := &Runner{
		deps: Deps{Config: cfg},
		opts: Options{Target: "http://127.0.0.1:1/", MaxActivities: 1},
	}
	piCfg := r.piConfig(config.OpenAIConfig{}, nil, nil, dir)
	piCfg.Command = shim

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, runErr := r.runActivity(ctx, piCfg, graph, []byte("{}"), 1)
	elapsed := time.Since(start)
	t.Logf("elapsed=%v err=%v", elapsed, runErr)

	if !isAborted(runErr) {
		t.Fatalf("an operator interrupt was not classified as an abort: %v", runErr)
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("abort lost the context error: %v", runErr)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("abort took %v: the activity kept running after the interrupt", elapsed)
	}
}
