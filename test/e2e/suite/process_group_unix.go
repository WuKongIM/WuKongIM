//go:build e2e && (aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package suite

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
}

func terminateProcessTree(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGTERM)
}

func killProcessTree(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGKILL)
}

func processTreeAliveAfterParentExit(process *os.Process) (bool, error) {
	if process == nil {
		return false, nil
	}
	err := syscall.Kill(-process.Pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func signalProcessGroup(process *os.Process, signal syscall.Signal) error {
	if process == nil {
		return nil
	}
	err := syscall.Kill(-process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
