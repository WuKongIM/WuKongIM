//go:build e2e && !(aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package suite

import (
	"os"
	"os/exec"
)

func configureProcessTree(_ *exec.Cmd) {}

func terminateProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

func killProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

func processTreeAliveAfterParentExit(_ *os.Process) (bool, error) {
	// Platforms without process-group support cannot have descendants that are
	// addressable through the reaped parent. Treat direct-child completion as
	// complete tree cleanup so the shared reaper does not report a false leak.
	return false, nil
}
