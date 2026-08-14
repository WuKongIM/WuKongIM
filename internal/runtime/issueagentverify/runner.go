package issueagentverify

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ProcessRunner executes fixed argv plans with a minimal no-secret environment.
type ProcessRunner struct {
	root           string
	temporaryRoot  string
	maxOutputBytes int
}

// NewProcessRunner constructs a clean-checkout Verifier process boundary.
func NewProcessRunner(
	root string,
	temporaryRoot string,
	maxOutputBytes int,
) (*ProcessRunner, error) {
	if !safeAbsoluteDirectory(root) ||
		!safeAbsoluteDirectory(temporaryRoot) ||
		maxOutputBytes <= 0 ||
		maxOutputBytes > 16<<20 {
		return nil, errors.New("Verifier process runner configuration is invalid")
	}
	return &ProcessRunner{
		root: root, temporaryRoot: temporaryRoot,
		maxOutputBytes: maxOutputBytes,
	}, nil
}

// Run executes one command directly, without a shell or inherited credentials.
func (runner *ProcessRunner) Run(
	ctx context.Context,
	plan VerificationCommandPlan,
) (VerificationCommandResult, error) {
	if runner == nil || ctx == nil {
		return VerificationCommandResult{}, errors.New("Verifier process request is invalid")
	}
	if err := validateVerificationPlan(plan); err != nil {
		return VerificationCommandResult{}, err
	}
	workingDirectory, err := runner.workingDirectory(plan.WorkingDir)
	if err != nil {
		return VerificationCommandResult{}, err
	}
	timeout := 15 * time.Minute
	if plan.TimeoutSeconds > 0 {
		timeout = time.Duration(plan.TimeoutSeconds) * time.Second
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	home := filepath.Join(runner.temporaryRoot, "home")
	temporary := filepath.Join(runner.temporaryRoot, "tmp")
	goCache := filepath.Join(runner.temporaryRoot, "go-build")
	configHome := filepath.Join(runner.temporaryRoot, "config")
	for _, directory := range []string{home, temporary, goCache, configHome} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return VerificationCommandResult{}, errors.New("prepare Verifier process directory")
		}
	}
	if err := disableGoTelemetry(home, configHome); err != nil {
		return VerificationCommandResult{}, errors.New("prepare Verifier telemetry mode")
	}
	command := exec.CommandContext(
		commandContext,
		plan.Arguments[0],
		plan.Arguments[1:]...,
	)
	command.Dir = workingDirectory
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"HOME=" + home,
		"TMPDIR=" + temporary,
		"XDG_CONFIG_HOME=" + configHome,
		"GOCACHE=" + goCache,
		"GOWORK=off",
	}
	if moduleCache := os.Getenv("ISSUE_AGENT_VERIFY_GOMODCACHE"); safeAbsoluteDirectory(moduleCache) {
		command.Env = append(command.Env, "GOMODCACHE="+moduleCache)
	}
	stdout := &boundedProcessBuffer{limit: runner.maxOutputBytes}
	stderr := &boundedProcessBuffer{limit: runner.maxOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr

	started := time.Now()
	runErr := command.Run()
	duration := time.Since(started)
	if stdout.overflow || stderr.overflow {
		return VerificationCommandResult{}, errors.New("Verifier process output exceeds limit")
	}
	exitCode := 0
	if runErr != nil {
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) {
			if commandContext.Err() != nil {
				return VerificationCommandResult{}, errors.New("Verifier process timed out")
			}
			return VerificationCommandResult{}, errors.New("start Verifier process")
		}
		exitCode = exitError.ExitCode()
	}
	return VerificationCommandResult{
		ExitCode: exitCode,
		Stdout:   bytes.Clone(stdout.buffer.Bytes()),
		Stderr:   bytes.Clone(stderr.buffer.Bytes()),
		Duration: duration,
	}, nil
}

// disableGoTelemetry prevents verifier commands from spawning a telemetry
// sidecar that can outlive the direct command and mutate its disposable HOME.
func disableGoTelemetry(home string, configHome string) error {
	telemetryHome := configHome
	if runtime.GOOS == "darwin" {
		telemetryHome = filepath.Join(home, "Library", "Application Support")
	}
	modeFile := filepath.Join(telemetryHome, "go", "telemetry", "mode")
	if err := os.MkdirAll(filepath.Dir(modeFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(modeFile, []byte("off\n"), 0o600)
}

func (runner *ProcessRunner) workingDirectory(relative string) (string, error) {
	if relative == "." {
		return runner.root, nil
	}
	target := filepath.Join(runner.root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", errors.New("resolve Verifier working directory")
	}
	within, err := filepath.Rel(runner.root, resolved)
	if err != nil || within == ".." ||
		strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", errors.New("Verifier working directory escapes checkout")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("Verifier working directory is invalid")
	}
	return resolved, nil
}

func safeAbsoluteDirectory(value string) bool {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	info, err := os.Lstat(value)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

type boundedProcessBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (buffer *boundedProcessBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.overflow = buffer.overflow || original > 0
		return original, nil
	}
	if len(value) > remaining {
		buffer.overflow = true
		value = value[:remaining]
	}
	_, _ = buffer.buffer.Write(value)
	return original, nil
}
