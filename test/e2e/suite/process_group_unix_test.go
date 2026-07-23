//go:build e2e && (aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package suite

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNodeProcessStopReapsTermIgnoringDescendant(t *testing.T) {
	rootDir := t.TempDir()
	descendantPIDPath := filepath.Join(rootDir, "descendant.pid")
	readyPath := filepath.Join(rootDir, "descendant.ready")
	cmd := exec.Command("/bin/sh", "-c", `
/bin/sh -c 'trap "" TERM; echo $$ > "$DESCENDANT_PID_PATH"; : > "$DESCENDANT_READY_PATH"; exec sleep 30' &
while [ ! -f "$DESCENDANT_READY_PATH" ]; do :; done
exit 0
`)
	cmd.Env = append(os.Environ(),
		"DESCENDANT_PID_PATH="+descendantPIDPath,
		"DESCENDANT_READY_PATH="+readyPath,
	)
	process := newCommandNodeProcess(t, cmd)
	process.StopTimeout = 100 * time.Millisecond
	require.NoError(t, process.Start())
	require.Eventually(t, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, process.Wait())
	require.False(t, process.Running())

	descendantPIDData, err := os.ReadFile(descendantPIDPath)
	require.NoError(t, err)
	descendantPID, err := strconv.Atoi(strings.TrimSpace(string(descendantPIDData)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = syscall.Kill(descendantPID, syscall.SIGKILL)
	})
	require.NoError(t, process.Stop())
	require.Eventually(t, func() bool {
		err := syscall.Kill(descendantPID, 0)
		return errors.Is(err, syscall.ESRCH)
	}, time.Second, 10*time.Millisecond, "TERM-ignoring descendant pid %d survived NodeProcess.Stop", descendantPID)
}

func TestStartedClusterStartStoppedNodeWaitsForPreviousProcessTreeCleanup(t *testing.T) {
	rootDir := t.TempDir()
	descendantPIDPath := filepath.Join(rootDir, "descendant.pid")
	readyPath := filepath.Join(rootDir, "descendant.ready")
	startedPath := filepath.Join(rootDir, "replacement.started")
	oldCommand := exec.Command("/bin/sh", "-c", `
/bin/sh -c 'trap "" TERM; echo $$ > "$DESCENDANT_PID_PATH"; : > "$DESCENDANT_READY_PATH"; exec sleep 30' &
while [ ! -f "$DESCENDANT_READY_PATH" ]; do :; done
exit 0
`)
	oldCommand.Env = append(os.Environ(),
		"DESCENDANT_PID_PATH="+descendantPIDPath,
		"DESCENDANT_READY_PATH="+readyPath,
	)
	oldProcess := newCommandNodeProcess(t, oldCommand)
	oldProcess.Spec.ID = 1
	oldProcess.Spec.Env = []string{"START_MARKER=" + startedPath}
	oldProcess.StopTimeout = 300 * time.Millisecond
	replacementBinary := filepath.Join(rootDir, "replacement.sh")
	require.NoError(t, os.WriteFile(replacementBinary, []byte(`#!/bin/sh
: > "$START_MARKER"
trap 'exit 0' TERM
while :; do sleep 1; done
`), 0o755))

	require.NoError(t, oldProcess.Start())
	require.Eventually(t, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	<-oldProcess.Done()
	select {
	case <-oldProcess.cleanupDone:
		t.Fatal("old process tree cleanup completed before the restart race was exercised")
	default:
	}
	descendantPIDData, err := os.ReadFile(descendantPIDPath)
	require.NoError(t, err)
	descendantPID, err := strconv.Atoi(strings.TrimSpace(string(descendantPIDData)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) })

	cluster := &StartedCluster{
		Nodes:      []StartedNode{{Spec: oldProcess.Spec, Process: oldProcess}},
		binaryPath: replacementBinary,
	}
	startResult := make(chan error, 1)
	go func() { startResult <- cluster.StartStoppedNode(1) }()
	t.Cleanup(func() { _ = cluster.MustNode(1).Stop() })

	select {
	case <-oldProcess.cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("old process tree cleanup did not finish")
	case <-time.After(50 * time.Millisecond):
		if _, err := os.Stat(startedPath); !os.IsNotExist(err) {
			t.Fatalf("replacement started before old process tree cleanup: %v", err)
		}
		select {
		case <-oldProcess.cleanupDone:
		case <-time.After(time.Second):
			t.Fatal("old process tree cleanup did not finish")
		}
	}
	select {
	case err := <-startResult:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("replacement did not start after old process tree cleanup")
	}
	require.Eventually(t, func() bool {
		_, err := os.Stat(startedPath)
		return err == nil
	}, time.Second, 10*time.Millisecond)
}
