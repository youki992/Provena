//go:build !windows

package security

import (
	"os/exec"
	"syscall"
)

// ProcessTree is the Windows job-object mechanism. Unix already reaps a whole
// session through Setsid + kill(-pgid), so this is an inert counterpart that
// keeps the call sites platform-neutral.
type ProcessTree struct{}

func NewProcessTree() (*ProcessTree, error) { return &ProcessTree{}, nil }

func (t *ProcessTree) Attach(cmd *exec.Cmd) error { return nil }

func (t *ProcessTree) Terminate() {}

func (t *ProcessTree) Release() {}

// prepareShellCmdSession 让 shell 子进程在独立会话中运行，便于超时/取消时整组 SIGKILL（含子进程）。
func prepareShellCmdSession(cmd *exec.Cmd) error {
	if cmd == nil {
		return nil
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	return nil
}

// terminateProcessGroup 对 rootPID 对应进程组发 SIGKILL；rootPID 为 0 时回退到 cmd.Process.Pid。
func terminateProcessGroup(rootPID int, cmd *exec.Cmd) {
	pid := rootPID
	if pid <= 0 && cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

// terminateCmdTree 尽力终止 cmd 及其进程组（Unix 下 Setsid 后 PGID == 首进程 PID）。
func terminateCmdTree(cmd *exec.Cmd) {
	terminateProcessGroup(0, cmd)
}
