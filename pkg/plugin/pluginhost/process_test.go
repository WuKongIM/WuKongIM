package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if os.Getenv("WK_PLUGIN_PROCESS_HELPER") == "1" {
		if path := os.Getenv("WK_PLUGIN_ARGS_FILE"); path != "" {
			if delay := parsePluginArgsWriteDelay(); delay > 0 {
				_ = os.WriteFile(path, nil, 0o600)
				time.Sleep(delay)
			}
			_ = os.WriteFile(path, []byte(strings.Join(os.Args[1:], "\n")), 0o600)
		}
		select {}
	}
	os.Exit(m.Run())
}

func parsePluginArgsWriteDelay() time.Duration {
	raw := os.Getenv("WK_PLUGIN_ARGS_WRITE_DELAY_MS")
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func TestProcessStartPassesSocketAndPerPluginSandbox(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("WK_PLUGIN_PROCESS_HELPER", "1")
	t.Setenv("WK_PLUGIN_ARGS_FILE", argsFile)
	t.Setenv("WK_PLUGIN_ARGS_WRITE_DELAY_MS", "50")
	manager := NewProcessManager(ProcessOptions{
		SocketPath:  filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:  filepath.Join(dir, "sandbox"),
		StopTimeout: 20 * time.Millisecond,
	})

	handle, err := manager.Start(context.Background(), ProcessSpec{No: "alpha", Path: os.Args[0]})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Stop(context.Background(), handle, nil) })

	wantArgs := strings.Join([]string{
		"--socket", filepath.Join(dir, "run", "plugin.sock"),
		"--sandbox", filepath.Join(dir, "sandbox", "alpha"),
	}, "\n")
	var gotArgs string
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(argsFile)
		if err != nil {
			return false
		}
		gotArgs = string(data)
		return gotArgs == wantArgs
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, wantArgs, gotArgs)
	require.DirExists(t, filepath.Join(dir, "sandbox", "alpha"))
	require.Equal(t, "alpha", handle.Spec.No)
	require.NotZero(t, handle.PID)
	require.False(t, handle.StartedAt.IsZero())
}

func TestProcessStopDoesNotBlockWhenKillFailsAfterProcessAlreadyExited(t *testing.T) {
	manager := NewProcessManager(ProcessOptions{StopTimeout: time.Nanosecond})
	done := make(chan error, 1)
	done <- nil
	handle := &ProcessHandle{
		Spec: ProcessSpec{No: "already-exited"},
		cmd:  &exec.Cmd{Process: &os.Process{}},
		done: done,
	}
	finished := make(chan error, 1)

	go func() { finished <- manager.Stop(context.Background(), handle, nil) }()

	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Stop blocked after consuming an already-finished process notification")
	}
}

func TestProcessStopReturnsKillErrorWithoutBlocking(t *testing.T) {
	manager := NewProcessManager(ProcessOptions{StopTimeout: time.Nanosecond})
	done := make(chan error)
	handle := &ProcessHandle{
		Spec: ProcessSpec{No: "ghost"},
		cmd:  &exec.Cmd{Process: &os.Process{}},
		done: done,
	}

	err := manager.Stop(context.Background(), handle, nil)

	require.Error(t, err)
}

func TestProcessStartDoesNotTieProcessLifetimeToCallerContext(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("WK_PLUGIN_PROCESS_HELPER", "1")
	t.Setenv("WK_PLUGIN_ARGS_FILE", argsFile)
	manager := NewProcessManager(ProcessOptions{
		SocketPath:  filepath.Join(dir, "plugin.sock"),
		SandboxDir:  filepath.Join(dir, "sandbox"),
		StopTimeout: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())

	handle, err := manager.Start(ctx, ProcessSpec{No: "ctx", Path: os.Args[0]})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Stop(context.Background(), handle, nil) })
	cancel()

	require.Never(t, func() bool {
		select {
		case <-handle.done:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond)
}

func TestProcessStopKillsWhenStopFuncBlocks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WK_PLUGIN_PROCESS_HELPER", "1")
	manager := NewProcessManager(ProcessOptions{
		SocketPath:  filepath.Join(dir, "plugin.sock"),
		SandboxDir:  filepath.Join(dir, "sandbox"),
		StopTimeout: 10 * time.Millisecond,
	})
	handle, err := manager.Start(context.Background(), ProcessSpec{No: "blocking-stop", Path: os.Args[0]})
	require.NoError(t, err)
	stopStarted := make(chan struct{})
	finished := make(chan error, 1)

	go func() {
		finished <- manager.Stop(context.Background(), handle, func(ctx context.Context, no string) error {
			close(stopStarted)
			<-ctx.Done()
			return ctx.Err()
		})
	}()

	select {
	case <-stopStarted:
	case <-time.After(time.Second):
		t.Fatal("stop callback was not invoked")
	}
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop did not kill process after timeout while StopFunc blocked")
	}
}

func TestProcessStopInvokesCallbackThenKillsAfterTimeout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WK_PLUGIN_PROCESS_HELPER", "1")
	manager := NewProcessManager(ProcessOptions{
		SocketPath:  filepath.Join(dir, "plugin.sock"),
		SandboxDir:  filepath.Join(dir, "sandbox"),
		StopTimeout: 10 * time.Millisecond,
	})
	handle, err := manager.Start(context.Background(), ProcessSpec{No: "beta", Path: os.Args[0]})
	require.NoError(t, err)

	called := make(chan struct{})
	err = manager.Stop(context.Background(), handle, func(ctx context.Context, no string) error {
		close(called)
		require.Equal(t, "beta", no)
		return nil
	})

	require.NoError(t, err)
	select {
	case <-called:
	default:
		t.Fatal("stop callback was not invoked")
	}
}
