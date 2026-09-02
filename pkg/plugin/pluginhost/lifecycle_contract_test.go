package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeDefaultCompositionExposesOwnedComponents(t *testing.T) {
	dir := t.TempDir()
	runtime := NewRuntime(RuntimeOptions{
		HotReload:  true,
		Dir:        filepath.Join(dir, "plugins"),
		SocketPath: filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir: filepath.Join(dir, "sandbox"),
		StateDir:   filepath.Join(dir, "state"),
		Timeout:    3 * time.Second,
	})

	require.Same(t, runtime.registry, runtime.Registry())
	require.Same(t, runtime.store, runtime.Store())
	require.Same(t, runtime.socket, runtime.Socket())
	require.IsType(t, &WKRPCSocketServer{}, runtime.Socket())
	require.IsType(t, &ProcessManager{}, runtime.processes)
	require.IsType(t, &Watcher{}, runtime.watcher)
	require.NotNil(t, runtime.invoker)
	require.NotNil(t, runtime.scanner)
	require.Equal(t, time.UTC, runtime.now().Location())
}

func TestRuntimeStartIsIdempotentAndStopBeforeStartIsSafe(t *testing.T) {
	dir := t.TempDir()
	socket := &recordingRuntimeSocket{}
	manager := &recordingRuntimeProcessManager{}
	scans := 0
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            filepath.Join(dir, "plugins"),
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Socket:         socket,
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			scans++
			return nil, nil
		},
	})

	require.NoError(t, runtime.Stop(context.Background()))
	require.NoError(t, runtime.Start(context.Background()))
	require.NoError(t, runtime.Start(context.Background()))
	require.Equal(t, 1, scans)
	require.NoError(t, runtime.Stop(context.Background()))
	require.NoError(t, runtime.Stop(context.Background()))
	require.Equal(t, 1, scans)
}

func TestRuntimeStartFailsBeforeStartingSocketWhenDirectoryCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	blockingFile := filepath.Join(dir, "not-a-directory")
	require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0o600))
	socket := &recordingRuntimeSocket{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            filepath.Join(blockingFile, "plugins"),
		Socket:         socket,
		ProcessManager: &recordingRuntimeProcessManager{},
		Scanner:        func(string) ([]ProcessSpec, error) { return nil, nil },
	})

	err := runtime.Start(context.Background())

	require.ErrorContains(t, err, "create plugin runtime dir")
	require.False(t, socket.started)
}

func TestRuntimeStartReturnsSocketFailureWithoutScanning(t *testing.T) {
	expected := errors.New("socket unavailable")
	socket := &recordingRuntimeSocket{startErr: expected}
	scanned := false
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            t.TempDir(),
		Socket:         socket,
		ProcessManager: &recordingRuntimeProcessManager{},
		Scanner: func(string) ([]ProcessSpec, error) {
			scanned = true
			return nil, nil
		},
	})

	err := runtime.Start(context.Background())

	require.ErrorIs(t, err, expected)
	require.False(t, scanned)
}

func TestRuntimeStartRollsBackSocketWhenScanFails(t *testing.T) {
	expected := errors.New("plugin directory unreadable")
	socket := &recordingRuntimeSocket{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            t.TempDir(),
		Socket:         socket,
		ProcessManager: &recordingRuntimeProcessManager{},
		Scanner:        func(string) ([]ProcessSpec, error) { return nil, expected },
	})

	err := runtime.Start(context.Background())

	require.ErrorIs(t, err, expected)
	require.False(t, socket.started)
}

func TestRuntimeStartRollsBackEarlierProcessWhenLaterStartFails(t *testing.T) {
	dir := t.TempDir()
	expected := errors.New("beta failed to start")
	manager := &selectiveRuntimeProcessManager{startErrFor: "beta", startErr: expected}
	socket := &recordingRuntimeSocket{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		HotReload:      true,
		Dir:            dir,
		Socket:         socket,
		ProcessManager: manager,
		Watcher:        &recordingRuntimeWatcher{},
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{
				{No: "alpha", Path: filepath.Join(dir, "alpha.wkp")},
				{No: "beta", Path: filepath.Join(dir, "beta.wkp")},
			}, nil
		},
	})

	err := runtime.Start(context.Background())

	require.ErrorIs(t, err, expected)
	require.Equal(t, []string{"alpha"}, manager.stopped)
	require.False(t, socket.started)
	alpha, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusOffline, alpha.Status)
	beta, ok := runtime.Registry().Get("beta")
	require.True(t, ok)
	require.Equal(t, StatusError, beta.Status)
	require.Contains(t, beta.LastError, expected.Error())
}

func TestRuntimeStartRollsBackWhenWatcherFails(t *testing.T) {
	dir := t.TempDir()
	expected := errors.New("watcher quota exhausted")
	manager := &recordingRuntimeProcessManager{}
	socket := &recordingRuntimeSocket{}
	watcher := &recordingRuntimeWatcher{startFn: func(context.Context) error { return expected }}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		HotReload:      true,
		Dir:            dir,
		Socket:         socket,
		ProcessManager: manager,
		Watcher:        watcher,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(dir, "alpha.wkp")}}, nil
		},
	})

	err := runtime.Start(context.Background())

	require.ErrorIs(t, err, expected)
	require.Equal(t, []string{"alpha"}, manager.stopped)
	require.False(t, socket.started)
	require.False(t, runtime.started)
}

func TestRuntimeStopReportsProcessFailureAndStillStopsSocket(t *testing.T) {
	dir := t.TempDir()
	expected := errors.New("process did not stop")
	manager := &recordingRuntimeProcessManager{stopErr: expected}
	socket := &recordingRuntimeSocket{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            dir,
		Socket:         socket,
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(dir, "alpha.wkp")}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))

	err := runtime.Stop(context.Background())

	require.ErrorIs(t, err, expected)
	require.False(t, socket.started)
	plugin, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusError, plugin.Status)
	require.Contains(t, plugin.LastError, expected.Error())
}

func TestRuntimeRestartValidatesDiscoveryBeforeStoppingCurrentProcess(t *testing.T) {
	dir := t.TempDir()
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            dir,
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(dir, "alpha.wkp")}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))

	require.ErrorIs(t, runtime.Restart(context.Background(), "../alpha"), ErrInvalidPluginNo)
	expected := errors.New("scan failed")
	runtime.scanner = func(string) ([]ProcessSpec, error) { return nil, expected }
	require.ErrorIs(t, runtime.Restart(context.Background(), "alpha"), expected)
	runtime.scanner = func(string) ([]ProcessSpec, error) { return nil, nil }
	require.ErrorContains(t, runtime.Restart(context.Background(), "alpha"), "not found")
	require.Empty(t, manager.stopped)
}

func TestRuntimeRestartReturnsStopFailureWithoutStartingReplacement(t *testing.T) {
	dir := t.TempDir()
	expected := errors.New("stop failed")
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            dir,
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(dir, "alpha.wkp")}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))
	manager.stopErr = expected

	err := runtime.Restart(context.Background(), "alpha")

	require.ErrorIs(t, err, expected)
	require.Len(t, manager.started, 1)
	plugin, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusError, plugin.Status)
}

func TestRuntimeUninstallPreservesCreatedAtAndUpdatesTimestamp(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state"))
	createdAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	updatedAt := createdAt.Add(24 * time.Hour)
	require.NoError(t, store.Save(DesiredState{No: "alpha", Enabled: true, CreatedAt: createdAt, UpdatedAt: createdAt}))
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            filepath.Join(dir, "plugins"),
		Store:          store,
		ProcessManager: &recordingRuntimeProcessManager{},
		Scanner:        func(string) ([]ProcessSpec, error) { return nil, nil },
		Now:            func() time.Time { return updatedAt },
	})

	require.NoError(t, runtime.Uninstall(context.Background(), "alpha"))

	state, err := store.Load("alpha")
	require.NoError(t, err)
	require.False(t, state.Enabled)
	require.Equal(t, createdAt, state.CreatedAt)
	require.Equal(t, updatedAt, state.UpdatedAt)
	plugin, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, updatedAt, plugin.LastSeenAt)
}

func TestRuntimeUninstallRejectsUnsafePluginNumber(t *testing.T) {
	runtime := NewRuntime(RuntimeOptions{Enable: true})

	err := runtime.Uninstall(context.Background(), "../escape")

	require.ErrorIs(t, err, ErrInvalidPluginNo)
}

func TestRuntimeDisabledRestartIsNoOp(t *testing.T) {
	runtime := NewRuntime(RuntimeOptions{Enable: false})

	require.NoError(t, runtime.Restart(context.Background(), "../ignored"))
}

type selectiveRuntimeProcessManager struct {
	started     []string
	stopped     []string
	startErrFor string
	startErr    error
}

func (m *selectiveRuntimeProcessManager) Start(_ context.Context, spec ProcessSpec) (*ProcessHandle, error) {
	m.started = append(m.started, spec.No)
	if spec.No == m.startErrFor {
		return nil, m.startErr
	}
	return &ProcessHandle{Spec: spec, PID: 100 + len(m.started), StartedAt: time.Now().UTC()}, nil
}

func (m *selectiveRuntimeProcessManager) Stop(_ context.Context, handle *ProcessHandle, _ StopFunc) error {
	m.stopped = append(m.stopped, handle.Spec.No)
	return nil
}
