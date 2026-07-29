package issueagentworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DockerSandboxConfig fixes the no-network Linux tool-container envelope.
type DockerSandboxConfig struct {
	DockerBinary  string
	Image         string
	Workspace     string
	CPUs          float64
	MemoryBytes   int64
	PIDs          int
	TempBytes     int64
	ReadOnlyFiles []ReadOnlyFileMount
	ModuleCache   string
}

// ReadOnlyFileMount exposes one trusted host-built binary at a fixed container path.
type ReadOnlyFileMount struct {
	HostPath      string
	ContainerPath string
}

// DockerSandboxRunner launches target commands inside disposable containers.
type DockerSandboxRunner struct {
	config  DockerSandboxConfig
	volume  string
	runMu   sync.Mutex
	counter atomic.Uint64
	closed  atomic.Bool
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
	moduleCache, err := filepath.Abs(config.ModuleCache)
	if err != nil {
		return nil, errors.New("resolve Docker sandbox module cache")
	}
	moduleCache, err = filepath.EvalSymlinks(moduleCache)
	if err != nil {
		return nil, errors.New("Docker sandbox module cache is unavailable")
	}
	moduleInfo, err := os.Stat(moduleCache)
	if err != nil || !moduleInfo.IsDir() || moduleCache == workspace ||
		strings.HasPrefix(moduleCache, workspace+string(filepath.Separator)) {
		return nil, errors.New("Docker sandbox module cache is invalid")
	}
	config.ModuleCache = moduleCache
	seenMounts := make(map[string]struct{}, len(config.ReadOnlyFiles))
	for index := range config.ReadOnlyFiles {
		mount := &config.ReadOnlyFiles[index]
		switch mount.ContainerPath {
		case "/issue-agent/bin/affected", "/issue-agent/bin/diagnosis-base":
		default:
			return nil, errors.New("Docker sandbox read-only mount target is invalid")
		}
		if _, duplicate := seenMounts[mount.ContainerPath]; duplicate {
			return nil, errors.New("Docker sandbox read-only mount is duplicated")
		}
		seenMounts[mount.ContainerPath] = struct{}{}
		if strings.ContainsAny(mount.HostPath, ",\r\n") {
			return nil, errors.New("Docker sandbox read-only mount source is invalid")
		}
		resolved, err := filepath.Abs(mount.HostPath)
		if err != nil {
			return nil, errors.New("resolve Docker sandbox read-only file")
		}
		resolved, err = filepath.EvalSymlinks(resolved)
		if err != nil {
			return nil, errors.New("Docker sandbox read-only file is unavailable")
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return nil, errors.New("Docker sandbox read-only file is not executable")
		}
		mount.HostPath = resolved
	}
	volume := fmt.Sprintf("wk-issue-agent-workspace-%d", time.Now().UnixNano())
	create := exec.Command(
		config.DockerBinary, "volume", "create",
		"--driver", "local",
		"--opt", "type=tmpfs",
		"--opt", "device=tmpfs",
		"--opt", "o=size="+strconv.FormatInt(config.TempBytes, 10)+",mode=0755",
		volume,
	)
	create.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	if output, err := create.CombinedOutput(); err != nil ||
		strings.TrimSpace(string(output)) != volume {
		return nil, errors.New("create bounded Docker sandbox workspace")
	}
	return &DockerSandboxRunner{config: config, volume: volume}, nil
}

// Run executes one argv without exposing Docker or host control to the model.
func (runner *DockerSandboxRunner) Run(
	ctx context.Context,
	request ExecRequest,
) (ExecResult, error) {
	if runner == nil || request.Executable == "" ||
		request.Timeout <= 0 || request.OutputLimit <= 0 ||
		request.OutputLimit > 16<<20 || len(request.Arguments) > 32 ||
		runner.closed.Load() {
		return ExecResult{}, errors.New("Docker sandbox request is invalid")
	}
	runner.runMu.Lock()
	defer runner.runMu.Unlock()
	relative, err := filepath.Rel(runner.config.Workspace, request.WorkingDir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return ExecResult{}, errors.New("Docker sandbox working directory escapes workspace")
	}
	containerDir := "/workspace"
	if relative != "." {
		containerDir += "/" + filepath.ToSlash(relative)
	}
	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	if err := runner.refreshWorkspace(runCtx); err != nil {
		return ExecResult{}, err
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
		"--mount", "type=volume,src=" + runner.volume +
			",dst=/workspace",
		"--mount", "type=tmpfs,dst=/workspace/.git,tmpfs-mode=0555",
		"--mount", "type=bind,src=" + runner.config.ModuleCache +
			",dst=/go/pkg/mod,readonly",
		"--workdir", containerDir,
	}
	for _, mount := range runner.config.ReadOnlyFiles {
		args = append(args,
			"--mount", "type=bind,src="+mount.HostPath+
				",dst="+mount.ContainerPath+",readonly",
		)
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

	command := exec.CommandContext(runCtx, runner.config.DockerBinary, args...)
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	stdout := newLimitedBuffer(request.OutputLimit)
	stderr := newLimitedBuffer(request.OutputLimit)
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
		StdoutTruncated: stdout.Truncated(), StderrTruncated: stderr.Truncated(),
		Duration: duration,
	}, nil
}

// Close removes the per-job bounded tmpfs volume.
func (runner *DockerSandboxRunner) Close() error {
	if runner == nil || !runner.closed.CompareAndSwap(false, true) {
		return nil
	}
	runner.runMu.Lock()
	defer runner.runMu.Unlock()
	remove := exec.Command(
		runner.config.DockerBinary, "volume", "rm", "-f", runner.volume,
	)
	remove.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	if err := remove.Run(); err != nil {
		return errors.New("remove Docker sandbox workspace")
	}
	return nil
}

func (runner *DockerSandboxRunner) refreshWorkspace(ctx context.Context) error {
	name := fmt.Sprintf(
		"wk-issue-agent-sync-%d-%d", time.Now().UnixNano(), runner.counter.Add(1),
	)
	const script = `
set -eu
if [ -L /workspace/.issue-agent-tmp ] || [ ! -d /workspace/.issue-agent-tmp ]; then
  rm -rf -- /workspace/.issue-agent-tmp
  mkdir -p /workspace/.issue-agent-tmp
fi
find /workspace -mindepth 1 -maxdepth 1 \
  ! -name .issue-agent-tmp -exec rm -rf -- {} +
tar --exclude=.git --exclude=./.git --exclude=.issue-agent-tmp \
  -C /host -cf - . | tar -C /workspace -xf -
`
	args := []string{
		"run", "--rm", "--name", name,
		"--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(runner.config.PIDs),
		"--memory", strconv.FormatInt(runner.config.MemoryBytes, 10),
		"--memory-swap", strconv.FormatInt(runner.config.MemoryBytes, 10),
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=67108864",
		"--mount", "type=bind,src=" + runner.config.Workspace +
			",dst=/host,readonly",
		"--mount", "type=volume,src=" + runner.volume +
			",dst=/workspace",
		runner.config.Image, "sh", "-c", script,
	}
	command := exec.CommandContext(ctx, runner.config.DockerBinary, args...)
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	output := newLimitedBuffer(64 << 10)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return errors.New("refresh bounded Docker sandbox workspace")
	}
	return nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func newLimitedBuffer(limit int64) limitedBuffer {
	return limitedBuffer{limit: limit}
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	buffer.written += int64(original)
	remaining := buffer.limit - int64(buffer.buffer.Len())
	if remaining > 0 {
		if int64(len(value)) > remaining {
			value = value[:remaining]
		}
		_, _ = buffer.buffer.Write(value)
	}
	if buffer.written > buffer.limit {
		buffer.truncated = true
	}
	return original, nil
}

func (buffer *limitedBuffer) Bytes() []byte {
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *limitedBuffer) Truncated() bool {
	return buffer.truncated
}
