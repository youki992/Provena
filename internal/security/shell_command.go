package security

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// agentShellPath selects a shell that can execute the Unix-style command
// wrappers used by the execute tool. Git Bash is the Windows-compatible option
// because the wrappers use export and /dev/null.
func agentShellPath() (string, error) {
	if runtime.GOOS != "windows" {
		return "/bin/sh", nil
	}

	if configured := strings.TrimSpace(os.Getenv("PROVENA_SHELL")); configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured, nil
		}
		return "", fmt.Errorf("PROVENA_SHELL does not exist: %s", configured)
	}

	if found, err := exec.LookPath("sh.exe"); err == nil {
		return found, nil
	}
	for _, candidate := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "sh.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "usr", "bin", "sh.exe"),
	} {
		if candidate == "\\Git\\usr\\bin\\sh.exe" || candidate == "Git\\usr\\bin\\sh.exe" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no compatible shell found on Windows; install Git for Windows or set PROVENA_SHELL to sh.exe")
}

func newAgentShellCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	shell, err := agentShellPath()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, shell, "-c", command), nil
}

func resolveAgentShell(shell string) (string, error) {
	trimmed := strings.TrimSpace(shell)
	if trimmed == "" || trimmed == "sh" || trimmed == "/bin/sh" || trimmed == "sh.exe" {
		return agentShellPath()
	}
	return trimmed, nil
}
