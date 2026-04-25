//go:build e2e

package suite

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	defaultStopTimeout   = 5 * time.Second
	diagnosticsTailBytes = 4096
	diagnosticsTailLines = 16
)

// NodeProcess wraps one real child process used by the e2e suite.
type NodeProcess struct {
	Spec         NodeSpec
	BinaryPath   string
	StartTimeout time.Duration
	StopTimeout  time.Duration

	Cmd       *exec.Cmd
	StdoutLog *os.File
	StderrLog *os.File

	command *exec.Cmd
}

// Start launches the child process and redirects stdout and stderr to files.
func (p *NodeProcess) Start() error {
	rootDir := p.Spec.RootDir
	if rootDir == "" {
		rootDir = filepath.Dir(p.Spec.StdoutPath)
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return err
	}

	stdoutLog, err := os.Create(p.Spec.StdoutPath)
	if err != nil {
		return err
	}
	stderrLog, err := os.Create(p.Spec.StderrPath)
	if err != nil {
		_ = stdoutLog.Close()
		return err
	}

	cmd := p.command
	if cmd == nil {
		cmd = exec.Command(p.BinaryPath, "-config", p.Spec.ConfigPath)
	}
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog

	p.StdoutLog = stdoutLog
	p.StderrLog = stderrLog
	p.Cmd = cmd

	if err := cmd.Start(); err != nil {
		_ = stdoutLog.Close()
		_ = stderrLog.Close()
		return err
	}
	return nil
}

// Stop terminates the child process and waits for it to exit.
func (p *NodeProcess) Stop() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- p.Cmd.Wait()
	}()

	if err := p.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	timeout := p.StopTimeout
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}

	select {
	case err := <-waitCh:
		p.closeLogs()
		return normalizeStopError(err)
	case <-time.After(timeout):
		if err := p.Cmd.Process.Kill(); err != nil {
			p.closeLogs()
			return err
		}
		err := <-waitCh
		p.closeLogs()
		return normalizeStopError(err)
	}
}

// DumpDiagnostics returns a small human-readable snapshot of process artifacts.
func (p *NodeProcess) DumpDiagnostics() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s\n", p.Spec.ConfigPath)
	fmt.Fprintf(&b, "stdout: %s\n", p.Spec.StdoutPath)
	fmt.Fprintf(&b, "stderr: %s\n", p.Spec.StderrPath)
	appendLogTail(&b, "stdout", p.Spec.StdoutPath)
	appendLogTail(&b, "stderr", p.Spec.StderrPath)

	logDir := p.Spec.LogDir
	if logDir == "" && p.Spec.RootDir != "" {
		logDir = filepath.Join(p.Spec.RootDir, "logs")
	}
	if logDir != "" {
		fmt.Fprintf(&b, "app-log: %s\n", filepath.Join(logDir, "app.log"))
		appendLogTail(&b, "app-log", filepath.Join(logDir, "app.log"))
		fmt.Fprintf(&b, "error-log: %s\n", filepath.Join(logDir, "error.log"))
		appendLogTail(&b, "error-log", filepath.Join(logDir, "error.log"))
	}
	return b.String()
}

func (p *NodeProcess) closeLogs() {
	if p.StdoutLog != nil {
		_ = p.StdoutLog.Close()
	}
	if p.StderrLog != nil {
		_ = p.StderrLog.Close()
	}
}

func appendLogTail(b *strings.Builder, name, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(b, "%s-read-error: %v\n", name, err)
		return
	}
	truncated := false
	if len(data) > diagnosticsTailBytes {
		data = data[len(data)-diagnosticsTailBytes:]
		truncated = true
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > diagnosticsTailLines {
		lines = lines[len(lines)-diagnosticsTailLines:]
		truncated = true
		data = []byte(strings.Join(lines, "\n"))
	}
	if truncated {
		fmt.Fprintf(b, "%s-tail: [truncated]\n", name)
	}
	fmt.Fprintf(b, "%s-content:\n%s", name, data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
}

func normalizeStopError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
