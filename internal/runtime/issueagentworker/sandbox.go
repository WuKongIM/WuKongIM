package issueagentworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// DockerSandboxConfig fixes the no-network Linux tool-container envelope.
type DockerSandboxConfig struct {
	DockerBinary string
	Image        string
	Workspace    string
	CPUs         float64
	MemoryBytes  int64
	PIDs         int
	TempBytes    int64
}

// DockerSandboxRunner launches target commands inside disposable containers.
type DockerSandboxRunner struct {
	config  DockerSandboxConfig
	counter atomic.Uint64
}

// NewDockerSandboxRunner validates a digest-pinned sandbox image and limits.
func NewDockerSandboxRunner(config DockerSandboxConfig) (*DockerSandboxRunner, error) {
	if config.DockerBinary == "" {
		config.DockerBinary = "docker"
	}
	if !strings.Contains(config.Image, "@sha256:") ||
		config.CPUs <= 0 || config.CPUs > 4 ||
		config.MemoryBytes < 128<<20 || config.MemoryBytes > 8<<30 ||
		config.PIDs <= 0 || config.PIDs > 1024 ||
		config.TempBytes <= 0 || config.TempBytes > 8<<30 {
		return nil, errors.New("Docker sandbox configuration is invalid")
	}
	workspace, err := filepath.Abs(config.Workspace)
	if err != nil {
		return nil, errors.New("resolve Docker sandbox workspace")
	}
	config.Workspace = workspace
	return &DockerSandboxRunner{config: config}, nil
}

// Run executes one argv without exposing Docker or host control to the model.
func (runner *DockerSandboxRunner) Run(
	ctx context.Context,
	request ExecRequest,
) (ExecResult, error) {
	if runner == nil || request.Executable == "" ||
		request.Timeout <= 0 || len(request.Arguments) > 32 {
		return ExecResult{}, errors.New("Docker sandbox request is invalid")
	}
	relative, err := filepath.Rel(runner.config.Workspace, request.WorkingDir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return ExecResult{}, errors.New("Docker sandbox working directory escapes workspace")
	}
	containerDir := "/workspace"
	if relative != "." {
		containerDir += "/" + filepath.ToSlash(relative)
	}
	name := fmt.Sprintf(
		"wk-issue-agent-%d-%d", time.Now().UnixNano(), runner.counter.Add(1),
	)
	args := []string{
		"run", "--rm", "--name", name,
		"--network", "none",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(runner.config.PIDs),
		"--cpus", strconv.FormatFloat(runner.config.CPUs, 'f', 2, 64),
		"--memory", strconv.FormatInt(runner.config.MemoryBytes, 10),
		"--memory-swap", strconv.FormatInt(runner.config.MemoryBytes, 10),
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=" +
			strconv.FormatInt(runner.config.TempBytes, 10),
		"--mount", "type=bind,src=" + runner.config.Workspace +
			",dst=/workspace,rw",
		"--workdir", containerDir,
	}
	for _, environment := range request.Environment {
		if strings.ContainsAny(environment, "\r\n") ||
			strings.HasPrefix(environment, "GITHUB_") ||
			strings.Contains(environment, "_TOKEN=") ||
			strings.Contains(environment, "_API_KEY=") {
			return ExecResult{}, errors.New("Docker sandbox environment is unsafe")
		}
		args = append(args, "--env", environment)
	}
	args = append(args, runner.config.Image, request.Executable)
	args = append(args, request.Arguments...)

	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, runner.config.DockerBinary, args...)
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	started := time.Now()
	err = command.Run()
	duration := time.Since(started)
	if runCtx.Err() != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanup := exec.CommandContext(
			cleanupCtx, runner.config.DockerBinary, "rm", "-f", name,
		)
		cleanup.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
		_ = cleanup.Run()
		return ExecResult{}, runCtx.Err()
	}
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			return ExecResult{}, errors.New("start Docker sandbox")
		}
		exitCode = exitError.ExitCode()
	}
	return ExecResult{
		ExitCode: exitCode, Stdout: stdout.Bytes(), Stderr: stderr.Bytes(),
		Duration: duration,
	}, nil
}
