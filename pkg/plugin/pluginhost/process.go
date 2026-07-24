package pluginhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const defaultProcessStopTimeout = 5 * time.Second

// ProcessSpec identifies a local plugin executable discovered for this node.
type ProcessSpec struct {
	// No is the filename-safe plugin number derived from the .wkp filename.
	No string
	// Path is the absolute or configured path to the local plugin executable.
	Path string
}

// ProcessHandle describes a plugin process started by the node-local runtime.
type ProcessHandle struct {
	// Spec is the executable specification used to start the process.
	Spec ProcessSpec
	// PID is the operating-system process ID.
	PID int
	// StartedAt records when the process was successfully started.
	StartedAt time.Time

	cmd  *exec.Cmd
	done chan error
}

// ProcessOptions configure node-local plugin process execution.
type ProcessOptions struct {
	// SocketPath is passed to plugins with --socket.
	SocketPath string
	// SandboxDir is the root for per-plugin writable sandbox directories.
	SandboxDir string
	// StopTimeout bounds graceful shutdown before the process is killed.
	StopTimeout time.Duration
}

// StopFunc asks a plugin to stop before the runtime kills its process.
type StopFunc func(ctx context.Context, no string) error

// ProcessManager starts and stops node-local plugin processes.
type ProcessManager struct {
	socketPath  string
	sandboxDir  string
	stopTimeout time.Duration
}

// NewProcessManager creates a process manager from options.
func NewProcessManager(opts ProcessOptions) *ProcessManager {
	if opts.StopTimeout <= 0 {
		opts.StopTimeout = defaultProcessStopTimeout
	}
	return &ProcessManager{socketPath: opts.SocketPath, sandboxDir: opts.SandboxDir, stopTimeout: opts.StopTimeout}
}

// Start launches a plugin executable with socket and sandbox arguments.
func (m *ProcessManager) Start(ctx context.Context, spec ProcessSpec) (*ProcessHandle, error) {
	if err := validatePluginNo(spec.No); err != nil {
		return nil, err
	}
	sandboxPath := filepath.Join(m.sandboxDir, spec.No)
	if err := os.MkdirAll(sandboxPath, 0o755); err != nil {
		return nil, fmt.Errorf("create plugin sandbox %q: %w", sandboxPath, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(spec.Path, "--socket", m.socketPath, "--sandbox", sandboxPath)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin %q: %w", spec.No, err)
	}
	handle := &ProcessHandle{Spec: spec, PID: cmd.Process.Pid, StartedAt: time.Now().UTC(), cmd: cmd, done: make(chan error, 1)}
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskPluginProcessWait, func() {
		handle.done <- cmd.Wait()
	})
	return handle, nil
}

// Stop asks a plugin to stop, waits briefly, then kills the process if needed.
func (m *ProcessManager) Stop(ctx context.Context, handle *ProcessHandle, stop StopFunc) error {
	if handle == nil || handle.cmd == nil || handle.cmd.Process == nil {
		return nil
	}
	timeout := m.stopTimeout
	if timeout <= 0 {
		timeout = defaultProcessStopTimeout
	}
	stopCtx, stopCancel := context.WithCancel(ctx)
	defer stopCancel()
	if stop != nil {
		stopStarted := make(chan struct{})
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskPluginStopCallback, func() {
			close(stopStarted)
			_ = stop(stopCtx, handle.Spec.No)
		})
		select {
		case <-stopStarted:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-handle.done:
		return nil
	default:
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-handle.done:
		return nil
	case <-ctx.Done():
		stopCancel()
		if err := handle.cmd.Process.Kill(); err == nil {
			<-handle.done
		}
		return ctx.Err()
	case <-timer.C:
		stopCancel()
		if err := handle.cmd.Process.Kill(); err != nil {
			if tryReceiveProcessDone(handle) {
				return nil
			}
			return fmt.Errorf("kill plugin %q: %w", handle.Spec.No, err)
		}
		<-handle.done
		return nil
	}
}

func tryReceiveProcessDone(handle *ProcessHandle) bool {
	select {
	case <-handle.done:
		return true
	default:
		return false
	}
}
