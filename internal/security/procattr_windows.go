//go:build windows

package security

import (
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessTree owns a Windows job object so a process and every descendant it
// ever creates die together.
//
// taskkill /F /T walks the parent-child chain, so it stops working the moment
// an intermediate process exits and the kernel reparents the survivors — which
// is exactly what happens when a scanner spawns a shell that spawns the real
// tool. A job object is membership-based instead: once a process is assigned,
// its children inherit membership and cannot escape it.
type ProcessTree struct {
	handle windows.Handle
}

// NewProcessTree creates a kill-on-close job object. Callers treat a failure as
// non-fatal: the taskkill fallback still applies, it is just less reliable.
func NewProcessTree() (*ProcessTree, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	runtime.KeepAlive(info)
	return &ProcessTree{handle: handle}, nil
}

// Attach assigns an already started process to the job. Call it immediately
// after Start: a descendant created before the assignment would fall outside
// the job and outlive it.
func (t *ProcessTree) Attach(cmd *exec.Cmd) error {
	if t == nil || t.handle == 0 || cmd == nil || cmd.Process == nil {
		return nil
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(process) }()
	return windows.AssignProcessToJobObject(t.handle, process)
}

// Terminate kills every process currently in the job.
func (t *ProcessTree) Terminate() {
	if t == nil || t.handle == 0 {
		return
	}
	_ = windows.TerminateJobObject(t.handle, 1)
}

// Release closes the job handle. Kill-on-close turns this into a final sweep:
// anything still alive in the job dies here.
func (t *ProcessTree) Release() {
	if t == nil || t.handle == 0 {
		return
	}
	_ = windows.CloseHandle(t.handle)
	t.handle = 0
}

// processCount reports how many processes the job currently holds, or -1 when
// the count is unavailable. Tests use it to prove a tree was actually reaped.
func (t *ProcessTree) processCount() int {
	if t == nil || t.handle == 0 {
		return -1
	}
	buf := make([]byte, 8+1024*8)
	var returned uint32
	if err := windows.QueryInformationJobObject(
		t.handle,
		windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&buf[0])),
		uint32(len(buf)),
		&returned,
	); err != nil {
		return -1
	}
	count := int(*(*uint32)(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	return count
}

func prepareShellCmdSession(cmd *exec.Cmd) error {
	if cmd == nil {
		return nil
	}
	// 独立进程组，便于 taskkill /T 终止整棵子进程树。
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
	return nil
}

// terminateProcessGroup 使用 taskkill /F /T 终止进程及其子进程；rootPID 为 0 时回退到 cmd.Process.Pid。
func terminateProcessGroup(rootPID int, cmd *exec.Cmd) {
	pid := rootPID
	if pid <= 0 && cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if pid <= 0 {
		return
	}
	tk := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	if err := tk.Run(); err != nil {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

// terminateCmdTree 使用 taskkill /F /T 终止进程及其子进程（Windows 上 Process.Kill 无法保证杀掉 python 等孙进程）。
func terminateCmdTree(cmd *exec.Cmd) {
	terminateProcessGroup(0, cmd)
}
