//go:build windows

package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestProcessTreeReapsGrandchildren pins the reason the job object exists.
//
// taskkill /F /T walks the parent-child chain, so a tool that spawns a shell
// which spawns the real scanner can leave a survivor behind as soon as an
// intermediate process exits and the kernel reparents the rest. Job membership
// does not have that failure mode: cmd.exe starts a ping, and closing or
// terminating the job takes the ping with it.
func TestProcessTreeReapsGrandchildren(t *testing.T) {
	tree, err := NewProcessTree()
	if err != nil {
		t.Skipf("job objects unavailable in this environment: %v", err)
	}
	defer tree.Release()

	dir := t.TempDir()
	shim := filepath.Join(dir, "tree.cmd")
	// The leading delay gives Attach time to run before the tree grows, so the
	// long ping is unambiguously a grandchild created after assignment.
	script := "@echo off\r\n" +
		"ping -n 3 127.0.0.1 > NUL\r\n" +
		"ping -n 60 127.0.0.1 > NUL\r\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write the shim: %v", err)
	}

	cmd := exec.Command("cmd", "/c", shim)
	if err := cmd.Start(); err != nil {
		t.Fatalf("cannot start the shim: %v", err)
	}
	if err := tree.Attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		t.Skipf("cannot attach the process to a job here: %v", err)
	}

	waitFor := func(want int) int {
		deadline := time.Now().Add(25 * time.Second)
		count := tree.processCount()
		for time.Now().Before(deadline) {
			count = tree.processCount()
			if count >= want {
				return count
			}
			time.Sleep(100 * time.Millisecond)
		}
		return count
	}

	if got := waitFor(2); got < 2 {
		_ = cmd.Process.Kill()
		t.Skipf("the faked tree never grew to two processes (count=%d)", got)
	}
	// Let the tree settle into its long-running shape: cmd.exe plus a ping.
	time.Sleep(1500 * time.Millisecond)
	t.Logf("job holds %d process(es) before terminate", tree.processCount())

	tree.Terminate()
	if got := waitFor(0); got != 0 {
		t.Fatalf("job still holds %d process(es) after Terminate", got)
	}
	_ = cmd.Wait()
}

// TestProcessTreeReleaseKillsTheRemainder covers the other half of the
// contract: kill-on-close means dropping the handle is itself a cleanup, so a
// turn that finishes while a background scanner is still running does not leak
// the scanner into the next turn.
func TestProcessTreeReleaseKillsTheRemainder(t *testing.T) {
	tree, err := NewProcessTree()
	if err != nil {
		t.Skipf("job objects unavailable in this environment: %v", err)
	}

	dir := t.TempDir()
	shim := filepath.Join(dir, "linger.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\nping -n 60 127.0.0.1 > NUL\r\n"), 0o755); err != nil {
		t.Fatalf("cannot write the shim: %v", err)
	}

	cmd := exec.Command("cmd", "/c", shim)
	if err := cmd.Start(); err != nil {
		t.Fatalf("cannot start the shim: %v", err)
	}
	if err := tree.Attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		t.Skipf("cannot attach the process to a job here: %v", err)
	}

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) && tree.processCount() < 2 {
		time.Sleep(100 * time.Millisecond)
	}
	if tree.processCount() < 2 {
		_ = cmd.Process.Kill()
		t.Skip("the faked tree never grew to two processes")
	}

	tree.Release()
	time.Sleep(600 * time.Millisecond)

	// The job is gone, so re-open the process list indirectly: cmd.Wait must
	// return promptly because nothing is left holding its pipes.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the tree outlived the job handle: Release did not reap it")
	}
}
