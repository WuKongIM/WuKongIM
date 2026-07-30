package reviewagentverify

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// OSExecutorConfig contains only non-secret process environment roots.
type OSExecutorConfig struct {
	HomeDir string
	Path    string
	TempDir string
}

// OSExecutor runs already-resolved catalog commands with a minimal
// credential-free environment.
type OSExecutor struct {
	environment []string
}

// NewOSExecutor constructs an executor without inheriting the job
// environment.
func NewOSExecutor(config OSExecutorConfig) (*OSExecutor, error) {
	home, err := filepath.Abs(config.HomeDir)
	if err != nil || home == string(filepath.Separator) {
		return nil, errors.New("unsafe named-check home directory")
	}
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe named-check home directory")
	}
	if config.Path == "" || strings.ContainsRune(config.Path, '\x00') {
		return nil, errors.New("invalid named-check executable path")
	}
	temp := config.TempDir
	if temp == "" {
		temp = os.TempDir()
	}
	temp, err = filepath.Abs(temp)
	if err != nil {
		return nil, errors.New("resolve named-check temporary directory")
	}
	environment := []string{
		"PATH=" + config.Path,
		"HOME=" + home,
		"TMPDIR=" + temp,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"GOWORK=off",
	}
	return &OSExecutor{environment: environment}, nil
}

// Environment returns the exact sanitized child environment.
func (executor *OSExecutor) Environment() []string {
	if executor == nil {
		return nil
	}
	return slices.Clone(executor.environment)
}

// Execute runs one fixed argv command with wall-time and output bounds.
func (executor *OSExecutor) Execute(
	ctx context.Context,
	request ProcessRequest,
) (ProcessResult, error) {
	if executor == nil || ctx == nil {
		return ProcessResult{}, errors.New("OS executor is unavailable")
	}
	if len(request.Environment) != 0 {
		return ProcessResult{}, errors.New(
			"caller environment override is forbidden",
		)
	}
	if len(request.Arguments) == 0 ||
		request.Timeout <= 0 ||
		request.Timeout > 90*time.Minute ||
		request.MaxOutputBytes <= 0 ||
		request.MaxOutputBytes > 16<<20 {
		return ProcessResult{}, errors.New("invalid OS process request")
	}
	for _, argument := range request.Arguments {
		if argument == "" || len(argument) > 4096 ||
			strings.ContainsRune(argument, '\x00') {
			return ProcessResult{}, errors.New("invalid OS process argument")
		}
	}
	workingDir, err := filepath.Abs(request.WorkingDir)
	if err != nil {
		return ProcessResult{}, errors.New("resolve OS process working directory")
	}
	info, err := os.Lstat(workingDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ProcessResult{}, errors.New("unsafe OS process working directory")
	}

	processContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	command := exec.Command(
		request.Arguments[0],
		request.Arguments[1:]...,
	)
	command.Dir = workingDir
	command.Env = executor.Environment()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout := &boundedBuffer{limit: request.MaxOutputBytes}
	stderr := &boundedBuffer{limit: request.MaxOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr

	started := time.Now()
	if err := command.Start(); err != nil {
		return ProcessResult{}, errors.New("start named-check process")
	}
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-processContext.Done():
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-waited
		return ProcessResult{
			ExitCode: -1,
			Stdout:   stdout.Bytes(),
			Stderr:   stderr.Bytes(),
			Duration: time.Since(started),
		}, errors.New("named-check process deadline exceeded")
	}
	duration := time.Since(started)
	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			return ProcessResult{}, errors.New("wait for named-check process")
		}
		exitCode = exitError.ExitCode()
	}
	result := ProcessResult{
		ExitCode: exitCode,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Duration: duration,
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return result, errors.New("named-check process output limit exceeded")
	}
	return result, nil
}

type boundedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	original := len(value)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.exceeded = true
		return original, nil
	}
	if len(value) > remaining {
		buffer.exceeded = true
		value = value[:remaining]
	}
	_, _ = buffer.buffer.Write(value)
	return original, nil
}

func (buffer *boundedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *boundedBuffer) Exceeded() bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.exceeded
}
