package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// Scanner discovers local plugin process specs from a configured plugin directory.
type Scanner func(dir string) ([]ProcessSpec, error)

// ProcessController starts and stops node-local plugin processes.
type ProcessController interface {
	Start(context.Context, ProcessSpec) (*ProcessHandle, error)
	Stop(context.Context, *ProcessHandle, StopFunc) error
}

// DirectoryWatcher starts and stops hot reload watching for local plugin binaries.
type DirectoryWatcher interface {
	Start(context.Context) error
	Stop()
}

// RuntimeOptions configure the node-local plugin process runtime.
type RuntimeOptions struct {
	// Enable controls whether the runtime starts local plugin processes.
	Enable bool
	// HotReload controls whether the runtime watches Dir for .wkp changes.
	HotReload bool
	// Dir contains node-local .wkp plugin binaries.
	Dir string
	// SocketPath is the Unix socket path passed to plugin processes.
	SocketPath string
	// SandboxDir is the per-plugin writable sandbox root.
	SandboxDir string
	// StateDir stores node-local desired plugin config and enable state.
	StateDir string
	// Timeout bounds plugin stop RPCs and graceful process shutdown.
	Timeout time.Duration
	// Registry stores live node-local plugin observations.
	Registry *Registry
	// Store persists desired node-local plugin state.
	Store *Store
	// Socket serves PDK-compatible plugin host RPC traffic.
	Socket SocketServer
	// Invoker asks connected plugins to stop before process termination.
	Invoker *Invoker
	// ProcessManager starts and stops plugin processes.
	ProcessManager ProcessController
	// Watcher emits hot reload restart requests when enabled.
	Watcher DirectoryWatcher
	// Scanner discovers local plugin binaries.
	Scanner Scanner
	// Now supplies timestamps for runtime observations and desired state.
	Now func() time.Time
	// Logger receives structured diagnostics from the legacy wkrpc socket dependency.
	Logger wklog.Logger
}

// Runtime orchestrates node-local plugin sockets, processes, watcher, and state.
type Runtime struct {
	enable     bool
	hotReload  bool
	dir        string
	socketPath string
	sandboxDir string
	stateDir   string
	registry   *Registry
	store      *Store
	socket     SocketServer
	invoker    *Invoker
	processes  ProcessController
	watcher    DirectoryWatcher
	scanner    Scanner
	now        func() time.Time

	mu      sync.Mutex
	started bool
	handles map[string]*ProcessHandle
}

// NewRuntime creates a node-local plugin runtime from explicit dependencies.
func NewRuntime(opts RuntimeOptions) *Runtime {
	registry := opts.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	store := opts.Store
	if store == nil && opts.StateDir != "" {
		store = NewStore(opts.StateDir)
	}
	socket := opts.Socket
	if socket == nil && opts.SocketPath != "" {
		socket = NewSocketServerWithLogger(opts.SocketPath, opts.Logger)
	}
	invoker := opts.Invoker
	if invoker == nil && socket != nil {
		invoker = NewInvoker(socket, WithTimeout(opts.Timeout))
	}
	processes := opts.ProcessManager
	if processes == nil {
		processes = NewProcessManager(ProcessOptions{SocketPath: opts.SocketPath, SandboxDir: opts.SandboxDir, StopTimeout: opts.Timeout})
	}
	scanner := opts.Scanner
	if scanner == nil {
		scanner = ScanPlugins
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	r := &Runtime{
		enable:     opts.Enable,
		hotReload:  opts.HotReload,
		dir:        opts.Dir,
		socketPath: opts.SocketPath,
		sandboxDir: opts.SandboxDir,
		stateDir:   opts.StateDir,
		registry:   registry,
		store:      store,
		socket:     socket,
		invoker:    invoker,
		processes:  processes,
		watcher:    opts.Watcher,
		scanner:    scanner,
		now:        now,
		handles:    make(map[string]*ProcessHandle),
	}
	if r.watcher == nil && opts.HotReload {
		r.watcher = NewWatcher(WatcherOptions{Dir: opts.Dir, OnRestart: func(ctx context.Context, no string) {
			_ = r.Restart(ctx, no)
		}})
	}
	return r
}

// Registry returns the runtime's live plugin observation registry.
func (r *Runtime) Registry() *Registry {
	return r.registry
}

// Store returns the runtime's node-local desired state store.
func (r *Runtime) Store() *Store {
	return r.store
}

// Socket returns the runtime's PDK-compatible host RPC socket server.
func (r *Runtime) Socket() SocketServer {
	return r.socket
}

// Start creates runtime directories, starts the socket, scans plugins, and starts enabled processes.
func (r *Runtime) Start(ctx context.Context) error {
	if !r.enable {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}

	if err := r.ensureDirs(); err != nil {
		return err
	}
	if r.socket != nil {
		if err := r.socket.Start(); err != nil {
			return err
		}
	}

	specs, err := r.scanner(r.dir)
	if err != nil {
		if r.socket != nil {
			r.socket.Stop()
		}
		return err
	}
	for _, spec := range specs {
		shouldStart, err := r.shouldStartSpecLocked(spec)
		if err != nil {
			if r.watcher != nil && r.hotReload {
				r.watcher.Stop()
			}
			r.stopProcessesLocked(ctx)
			if r.socket != nil {
				r.socket.Stop()
			}
			return err
		}
		if !shouldStart {
			r.upsertStatus(spec.No, StatusDisabled, false, 0, "")
			continue
		}
		if err := r.startSpecLocked(ctx, spec); err != nil {
			if r.watcher != nil && r.hotReload {
				r.watcher.Stop()
			}
			r.stopProcessesLocked(ctx)
			if r.socket != nil {
				r.socket.Stop()
			}
			return err
		}
	}
	if r.hotReload && r.watcher != nil {
		if err := r.watcher.Start(ctx); err != nil {
			r.stopProcessesLocked(ctx)
			if r.socket != nil {
				r.socket.Stop()
			}
			return err
		}
	}
	r.started = true
	return nil
}

// Stop shuts down watcher, plugin processes, and socket in host-safe order.
func (r *Runtime) Stop(ctx context.Context) error {
	if !r.enable {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil
	}
	if r.watcher != nil {
		r.watcher.Stop()
	}
	err := r.stopProcessesLocked(ctx)
	if r.socket != nil {
		r.socket.Stop()
	}
	r.started = false
	return err
}

// Restart stops one plugin process and starts the currently discovered spec for that plugin.
func (r *Runtime) Restart(ctx context.Context, pluginNo string) error {
	if !r.enable {
		return nil
	}
	if err := validatePluginNo(pluginNo); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil
	}

	specs, err := r.scanner(r.dir)
	if err != nil {
		return err
	}
	spec, ok := findSpec(specs, pluginNo)
	if !ok {
		return fmt.Errorf("plugin %q not found in %q", pluginNo, r.dir)
	}
	shouldStart, err := r.shouldStartSpecLocked(spec)
	if err != nil {
		return err
	}
	if err := r.stopProcessLocked(ctx, pluginNo); err != nil {
		return err
	}
	if !shouldStart {
		r.upsertStatus(pluginNo, StatusDisabled, false, 0, "")
		return nil
	}
	return r.startSpecLocked(ctx, spec)
}

// Uninstall stops a local plugin, disables desired state, and removes binaries under Dir.
func (r *Runtime) Uninstall(ctx context.Context, pluginNo string) error {
	if !r.enable {
		return nil
	}
	if err := validatePluginNo(pluginNo); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	if err := r.stopProcessLocked(ctx, pluginNo); err != nil {
		errs = append(errs, err)
	}
	if r.store != nil {
		state := DesiredState{No: pluginNo, Enabled: false, CreatedAt: r.now(), UpdatedAt: r.now()}
		if existing, err := r.store.Load(pluginNo); err == nil {
			state = existing
			state.Enabled = false
			state.UpdatedAt = r.now()
		} else if !errors.Is(err, ErrDesiredStateNotFound) {
			errs = append(errs, err)
		}
		if err := r.store.Save(state); err != nil {
			errs = append(errs, err)
		}
	}
	if spec, ok := r.currentSpecLocked(pluginNo); ok {
		if removed, err := removeIfUnderDir(r.dir, spec.Path); err != nil {
			errs = append(errs, err)
		} else if removed {
			// Keep registry state even after local binary removal so managers can show disabled state.
		}
	}
	r.upsertStatus(pluginNo, StatusDisabled, false, 0, "")
	return errors.Join(errs...)
}

func (r *Runtime) ensureDirs() error {
	for _, dir := range []string{r.dir, filepath.Dir(r.socketPath), r.sandboxDir, r.stateDir} {
		if dir == "" || dir == "." {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create plugin runtime dir %q: %w", dir, err)
		}
	}
	return nil
}

func (r *Runtime) shouldStartSpecLocked(spec ProcessSpec) (bool, error) {
	if r.store == nil {
		return true, nil
	}
	state, err := r.store.Load(spec.No)
	if errors.Is(err, ErrDesiredStateNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return state.Enabled, nil
}

func (r *Runtime) startSpecLocked(ctx context.Context, spec ProcessSpec) error {
	if err := validatePluginNo(spec.No); err != nil {
		return err
	}
	handle, err := r.processes.Start(ctx, spec)
	if err != nil {
		r.upsertStatus(spec.No, StatusError, true, 0, err.Error())
		return err
	}
	r.handles[spec.No] = handle
	r.upsertStatus(spec.No, StatusStarting, true, handle.PID, "")
	return nil
}

func (r *Runtime) stopProcessesLocked(ctx context.Context) error {
	nos := make([]string, 0, len(r.handles))
	for no := range r.handles {
		nos = append(nos, no)
	}
	sort.Strings(nos)
	var errs []error
	for _, no := range nos {
		if err := r.stopProcessLocked(ctx, no); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Runtime) stopProcessLocked(ctx context.Context, no string) error {
	handle := r.handles[no]
	if handle == nil {
		return nil
	}
	delete(r.handles, no)
	var stop StopFunc
	if r.invoker != nil {
		stop = r.invoker.Stop
	}
	if err := r.processes.Stop(ctx, handle, stop); err != nil {
		r.upsertStatus(no, StatusError, true, 0, err.Error())
		return err
	}
	r.upsertStatus(no, StatusOffline, true, 0, "")
	return nil
}

func (r *Runtime) currentSpecLocked(pluginNo string) (ProcessSpec, bool) {
	if handle := r.handles[pluginNo]; handle != nil {
		return handle.Spec, true
	}
	specs, err := r.scanner(r.dir)
	if err != nil {
		return ProcessSpec{}, false
	}
	return findSpec(specs, pluginNo)
}

func (r *Runtime) upsertStatus(no string, status Status, enabled bool, pid int, lastErr string) {
	plugin, _ := r.registry.Get(no)
	plugin.No = no
	plugin.Status = status
	plugin.Enabled = enabled
	plugin.PID = pid
	plugin.LastSeenAt = r.now()
	plugin.LastError = lastErr
	r.registry.Upsert(plugin)
}

func findSpec(specs []ProcessSpec, pluginNo string) (ProcessSpec, bool) {
	for _, spec := range specs {
		if spec.No == pluginNo {
			return spec, true
		}
	}
	return ProcessSpec{}, false
}

func removeIfUnderDir(dir, path string) (bool, error) {
	resolvedDir, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		resolvedDir = filepath.Clean(dir)
	}
	resolvedPath, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		resolvedPath = filepath.Clean(path)
	}
	if err := ensurePathUnderDir(resolvedDir, resolvedPath); err != nil {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("remove plugin binary %q: %w", path, err)
	}
	return true, nil
}
