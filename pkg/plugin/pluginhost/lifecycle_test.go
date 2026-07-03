package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/wkrpc"
	"github.com/stretchr/testify/require"
)

func TestRuntimeDisabledDoesNothing(t *testing.T) {
	dir := t.TempDir()
	socket := &recordingRuntimeSocket{}
	manager := &recordingRuntimeProcessManager{}
	watcher := &recordingRuntimeWatcher{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         false,
		Dir:            filepath.Join(dir, "plugins"),
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Socket:         socket,
		ProcessManager: manager,
		Watcher:        watcher,
		Scanner: func(string) ([]ProcessSpec, error) {
			t.Fatal("scanner should not be called when runtime is disabled")
			return nil, nil
		},
	})

	require.NoError(t, runtime.Start(context.Background()))
	require.NoError(t, runtime.Stop(context.Background()))
	require.False(t, socket.started)
	require.Empty(t, manager.started)
	require.False(t, watcher.started)
}

func TestRuntimeStartCreatesDirsStartsSocketBeforeProcessesAndWatcher(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	sandboxDir := filepath.Join(dir, "sandbox")
	stateDir := filepath.Join(dir, "state")
	socketPath := filepath.Join(dir, "run", "plugin.sock")
	order := &runtimeOrderRecorder{}
	socket := &recordingRuntimeSocket{order: order}
	manager := &recordingRuntimeProcessManager{order: order}
	watcher := &recordingRuntimeWatcher{order: order}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		HotReload:      true,
		Dir:            pluginDir,
		SocketPath:     socketPath,
		SandboxDir:     sandboxDir,
		StateDir:       stateDir,
		Socket:         socket,
		ProcessManager: manager,
		Watcher:        watcher,
		Scanner: func(gotDir string) ([]ProcessSpec, error) {
			require.Equal(t, pluginDir, gotDir)
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")}}, nil
		},
	})

	require.NoError(t, runtime.Start(context.Background()))

	require.DirExists(t, pluginDir)
	require.DirExists(t, filepath.Dir(socketPath))
	require.DirExists(t, sandboxDir)
	require.DirExists(t, stateDir)
	require.True(t, socket.started)
	require.True(t, watcher.started)
	require.Equal(t, []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")}}, manager.started)
	require.Equal(t, []string{"socket:start", "process:start:alpha", "watcher:start"}, order.events)

	got, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusStarting, got.Status)
	require.True(t, got.Enabled)
	require.Equal(t, 101, got.PID)
}

func TestRuntimeStartSkipsDesiredDisabledPlugin(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	store := NewStore(filepath.Join(dir, "state"))
	require.NoError(t, store.Save(DesiredState{No: "alpha", Enabled: false, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}))
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Store:          store,
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")}}, nil
		},
	})

	require.NoError(t, runtime.Start(context.Background()))

	require.Empty(t, manager.started)
	got, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusDisabled, got.Status)
	require.False(t, got.Enabled)
}

func TestRuntimeStartReturnsErrorOnCorruptDesiredState(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	stateDir := filepath.Join(dir, "state")
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "alpha.json"), []byte(`{"no":`), 0o600))
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       stateDir,
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")}}, nil
		},
	})

	err := runtime.Start(context.Background())

	require.ErrorIs(t, err, ErrCorruptDesiredState)
	require.Empty(t, manager.started)
}

func TestRuntimeStopStopsWatcherProcessesThenSocket(t *testing.T) {
	dir := t.TempDir()
	order := &runtimeOrderRecorder{}
	socket := &recordingRuntimeSocket{order: order}
	manager := &recordingRuntimeProcessManager{order: order}
	watcher := &recordingRuntimeWatcher{order: order}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		HotReload:      true,
		Dir:            filepath.Join(dir, "plugins"),
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Socket:         socket,
		ProcessManager: manager,
		Watcher:        watcher,
		Scanner: func(pluginDir string) ([]ProcessSpec, error) {
			return []ProcessSpec{
				{No: "beta", Path: filepath.Join(pluginDir, "beta.wkp")},
				{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")},
			}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))
	order.clear()

	require.NoError(t, runtime.Stop(context.Background()))

	require.False(t, watcher.started)
	require.False(t, socket.started)
	require.ElementsMatch(t, []string{"alpha", "beta"}, manager.stopped)
	require.Equal(t, []string{"watcher:stop", "process:stop:alpha", "process:stop:beta", "socket:stop"}, order.events)
	for _, no := range []string{"alpha", "beta"} {
		got, ok := runtime.Registry().Get(no)
		require.True(t, ok)
		require.Equal(t, StatusOffline, got.Status)
		require.Zero(t, got.PID)
	}
}

func TestRuntimeRestartAfterStopDoesNotStartProcess(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))
	require.NoError(t, runtime.Stop(context.Background()))
	manager.started = nil

	require.NoError(t, runtime.Restart(context.Background(), "alpha"))

	require.Empty(t, manager.started)
}

func TestRuntimeRestartStopsExistingProcessAndStartsDiscoveredSpec(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	order := &runtimeOrderRecorder{}
	manager := &recordingRuntimeProcessManager{order: order}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Socket:         &recordingRuntimeSocket{order: order},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha-v2.wkp")}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))
	order.clear()

	require.NoError(t, runtime.Restart(context.Background(), "alpha"))

	require.Equal(t, []string{"process:stop:alpha", "process:start:alpha"}, order.events)
	require.Equal(t, []string{"alpha"}, manager.stopped)
	require.Len(t, manager.started, 2)
	require.Equal(t, filepath.Join(pluginDir, "alpha-v2.wkp"), manager.started[1].Path)
}

func TestRuntimeRestartDisabledPluginStopsExistingProcess(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	stateDir := filepath.Join(dir, "state")
	store := NewStore(stateDir)
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       stateDir,
		Store:          store,
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))
	require.Len(t, manager.started, 1)
	require.NoError(t, store.Save(DesiredState{No: "alpha", Enabled: false, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}))

	require.NoError(t, runtime.Restart(context.Background(), "alpha"))

	require.Equal(t, []string{"alpha"}, manager.stopped)
	require.Len(t, manager.started, 1)
	got, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusDisabled, got.Status)
	require.False(t, got.Enabled)
}

func TestRuntimeRestartMarksErrorWhenProcessStartFails(t *testing.T) {
	dir := t.TempDir()
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            filepath.Join(dir, "plugins"),
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(pluginDir string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: filepath.Join(pluginDir, "alpha.wkp")}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))
	manager.startErr = errors.New("boom")

	err := runtime.Restart(context.Background(), "alpha")

	require.ErrorContains(t, err, "boom")
	got, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusError, got.Status)
	require.Contains(t, got.LastError, "boom")
}

func TestRuntimeDisabledUninstallDoesNothing(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	stateDir := filepath.Join(dir, "state")
	pluginPath := filepath.Join(pluginDir, "alpha.wkp")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))
	writeExecutablePlugin(t, pluginPath)
	store := NewStore(stateDir)
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         false,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       stateDir,
		Store:          store,
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			t.Fatal("scanner should not be called when runtime is disabled")
			return nil, nil
		},
	})

	require.NoError(t, runtime.Uninstall(context.Background(), "alpha"))

	require.Empty(t, manager.stopped)
	require.FileExists(t, pluginPath)
	_, err := store.Load("alpha")
	require.ErrorIs(t, err, ErrDesiredStateNotFound)
}

func TestRuntimeUninstallStopsProcessDisablesStateAndRemovesLocalBinary(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	stateDir := filepath.Join(dir, "state")
	pluginPath := filepath.Join(pluginDir, "alpha.wkp")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))
	writeExecutablePlugin(t, pluginPath)
	manager := &recordingRuntimeProcessManager{}
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       stateDir,
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: manager,
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: pluginPath}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))

	require.NoError(t, runtime.Uninstall(context.Background(), "alpha"))

	require.Equal(t, []string{"alpha"}, manager.stopped)
	_, err := os.Stat(pluginPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	state, err := NewStore(stateDir).Load("alpha")
	require.NoError(t, err)
	require.False(t, state.Enabled)
	got, ok := runtime.Registry().Get("alpha")
	require.True(t, ok)
	require.Equal(t, StatusDisabled, got.Status)
	require.False(t, got.Enabled)
}

func TestRuntimeUninstallDoesNotRemoveBinaryOutsidePluginDir(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	outsideDir := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))
	outsidePath := filepath.Join(outsideDir, "alpha.wkp")
	writeExecutablePlugin(t, outsidePath)
	runtime := NewRuntime(RuntimeOptions{
		Enable:         true,
		Dir:            pluginDir,
		SocketPath:     filepath.Join(dir, "run", "plugin.sock"),
		SandboxDir:     filepath.Join(dir, "sandbox"),
		StateDir:       filepath.Join(dir, "state"),
		Socket:         &recordingRuntimeSocket{},
		ProcessManager: &recordingRuntimeProcessManager{},
		Scanner: func(string) ([]ProcessSpec, error) {
			return []ProcessSpec{{No: "alpha", Path: outsidePath}}, nil
		},
	})
	require.NoError(t, runtime.Start(context.Background()))

	require.NoError(t, runtime.Uninstall(context.Background(), "alpha"))

	require.FileExists(t, outsidePath)
}

type runtimeOrderRecorder struct {
	events []string
}

func (r *runtimeOrderRecorder) add(event string) {
	r.events = append(r.events, event)
}

func (r *runtimeOrderRecorder) clear() {
	r.events = nil
}

type recordingRuntimeSocket struct {
	order     *runtimeOrderRecorder
	started   bool
	startErr  error
	requests  []string
	requestOK []byte
}

func (s *recordingRuntimeSocket) Start() error {
	if s.order != nil {
		s.order.add("socket:start")
	}
	if s.startErr != nil {
		return s.startErr
	}
	s.started = true
	return nil
}

func (s *recordingRuntimeSocket) Stop() {
	if s.order != nil {
		s.order.add("socket:stop")
	}
	s.started = false
}

func (s *recordingRuntimeSocket) RequestWithContext(ctx context.Context, uid, path string, body []byte) ([]byte, error) {
	s.requests = append(s.requests, uid+":"+path)
	return append([]byte(nil), s.requestOK...), nil
}

func (s *recordingRuntimeSocket) Request(ctx context.Context, uid, path string, body []byte) ([]byte, error) {
	return s.RequestWithContext(ctx, uid, path, body)
}

func (s *recordingRuntimeSocket) Send(uid string, msgType uint32, body []byte) error { return nil }

func (s *recordingRuntimeSocket) Route(path string, handler wkrpc.Handler) {}

type recordingRuntimeProcessManager struct {
	order    *runtimeOrderRecorder
	started  []ProcessSpec
	stopped  []string
	startErr error
	stopErr  error
}

func (m *recordingRuntimeProcessManager) Start(ctx context.Context, spec ProcessSpec) (*ProcessHandle, error) {
	if m.order != nil {
		m.order.add("process:start:" + spec.No)
	}
	if m.startErr != nil {
		return nil, m.startErr
	}
	m.started = append(m.started, spec)
	return &ProcessHandle{Spec: spec, PID: 100 + len(m.started), StartedAt: time.Now().UTC()}, nil
}

func (m *recordingRuntimeProcessManager) Stop(ctx context.Context, handle *ProcessHandle, stop StopFunc) error {
	if m.order != nil {
		m.order.add("process:stop:" + handle.Spec.No)
	}
	if stop != nil {
		_ = stop(ctx, handle.Spec.No)
	}
	m.stopped = append(m.stopped, handle.Spec.No)
	if m.stopErr != nil {
		return m.stopErr
	}
	return nil
}

type recordingRuntimeWatcher struct {
	order   *runtimeOrderRecorder
	started bool
	startFn func(context.Context) error
}

func (w *recordingRuntimeWatcher) Start(ctx context.Context) error {
	if w.order != nil {
		w.order.add("watcher:start")
	}
	if w.startFn != nil {
		return w.startFn(ctx)
	}
	w.started = true
	return nil
}

func (w *recordingRuntimeWatcher) Stop() {
	if w.order != nil {
		w.order.add("watcher:stop")
	}
	w.started = false
}
