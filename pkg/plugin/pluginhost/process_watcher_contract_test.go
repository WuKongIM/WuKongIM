package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessManagerDefaultsStopTimeout(t *testing.T) {
	manager := NewProcessManager(ProcessOptions{})

	require.Equal(t, defaultProcessStopTimeout, manager.stopTimeout)
}

func TestProcessManagerStartRejectsUnsafePluginNumberBeforeCreatingSandbox(t *testing.T) {
	sandbox := filepath.Join(t.TempDir(), "sandbox")
	manager := NewProcessManager(ProcessOptions{SandboxDir: sandbox})

	handle, err := manager.Start(context.Background(), ProcessSpec{No: "../escape", Path: "missing"})

	require.Nil(t, handle)
	require.ErrorIs(t, err, ErrInvalidPluginNo)
	require.NoDirExists(t, sandbox)
}

func TestProcessManagerStartHonorsCanceledContextBeforeExecutingBinary(t *testing.T) {
	sandbox := filepath.Join(t.TempDir(), "sandbox")
	manager := NewProcessManager(ProcessOptions{SandboxDir: sandbox})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handle, err := manager.Start(ctx, ProcessSpec{No: "alpha", Path: "missing"})

	require.Nil(t, handle)
	require.ErrorIs(t, err, context.Canceled)
	require.DirExists(t, filepath.Join(sandbox, "alpha"))
}

func TestProcessManagerStartReportsMissingExecutable(t *testing.T) {
	manager := NewProcessManager(ProcessOptions{SandboxDir: t.TempDir()})

	handle, err := manager.Start(context.Background(), ProcessSpec{No: "alpha", Path: filepath.Join(t.TempDir(), "missing.wkp")})

	require.Nil(t, handle)
	require.ErrorContains(t, err, "start plugin")
}

func TestProcessManagerStopTreatsIncompleteHandlesAsStopped(t *testing.T) {
	manager := NewProcessManager(ProcessOptions{})

	require.NoError(t, manager.Stop(context.Background(), nil, nil))
	require.NoError(t, manager.Stop(context.Background(), &ProcessHandle{}, nil))
}

func TestTryReceiveProcessDoneIsNonBlocking(t *testing.T) {
	pending := &ProcessHandle{done: make(chan error)}
	require.False(t, tryReceiveProcessDone(pending))

	done := &ProcessHandle{done: make(chan error, 1)}
	done.done <- errors.New("exited")
	require.True(t, tryReceiveProcessDone(done))
}

func TestWatcherStartStopLifecycleIsIdempotent(t *testing.T) {
	watcher := NewWatcher(WatcherOptions{Dir: filepath.Join(t.TempDir(), "plugins")})
	watcher.Stop()

	require.NoError(t, watcher.Start(context.Background()))
	require.NoError(t, watcher.Start(context.Background()))
	require.True(t, watcher.started)
	watcher.Stop()
	watcher.Stop()
	require.False(t, watcher.started)
}

func TestWatcherStartReportsDirectoryCreationFailure(t *testing.T) {
	dir := t.TempDir()
	blockingPath := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(blockingPath, []byte("x"), 0o600))
	watcher := NewWatcher(WatcherOptions{Dir: filepath.Join(blockingPath, "plugins")})

	err := watcher.Start(context.Background())

	require.ErrorContains(t, err, "create plugin watch dir")
}

func TestWatcherDebouncerDefaultsAndRejectsInvalidOrLateEvents(t *testing.T) {
	scheduler := &manualDebounceScheduler{}
	debouncer := newPluginDebouncer(0, scheduler, nil)
	require.Equal(t, defaultWatcherDebounce, debouncer.delay)

	debouncer.HandlePath(filepath.Join(t.TempDir(), ".wkp"))
	require.Empty(t, scheduler.funcs)

	debouncer.Stop()
	debouncer.HandlePath(filepath.Join(t.TempDir(), "alpha.wkp"))
	require.Empty(t, scheduler.funcs)
}
