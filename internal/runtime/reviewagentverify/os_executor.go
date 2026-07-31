package reviewagentverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	HomeDir       string
	Path          string
	TempDir       string
	WorkspaceRoot string
	SandboxBinary string
	HelperBinary  string
}

// OSExecutor runs already-resolved catalog commands with a minimal
// credential-free environment.
type OSExecutor struct {
	environment   []string
	homeDir       string
	tempDir       string
	workspaceRoot string
	sandboxBinary string
	gitBinary     string
	helperBinary  string
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
	if err != nil || temp == string(filepath.Separator) {
		return nil, errors.New("resolve named-check temporary directory")
	}
	if err := os.MkdirAll(temp, 0o700); err != nil {
		return nil, errors.New("create named-check temporary directory")
	}
	tempInfo, err := os.Lstat(temp)
	if err != nil || !tempInfo.IsDir() ||
		tempInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe named-check temporary directory")
	}
	workspaceRoot := ""
	sandboxBinary := ""
	gitBinary := ""
	helperBinary := ""
	if config.SandboxBinary != "" || config.WorkspaceRoot != "" {
		workspaceRoot, sandboxBinary, err = validateProcessSandbox(config)
		if err != nil {
			return nil, err
		}
		helperBinary, err = validateExecutable(
			config.HelperBinary,
			"named-check helper",
		)
		if err != nil {
			return nil, err
		}
		gitBinary, err = executableInPath(config.Path, "git")
		if err != nil {
			return nil, err
		}
	}
	environment := []string{
		"PATH=" + config.Path,
		"HOME=" + home,
		"TMPDIR=" + temp,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"GOWORK=off",
	}
	return &OSExecutor{
		environment:   environment,
		homeDir:       home,
		tempDir:       temp,
		workspaceRoot: workspaceRoot,
		sandboxBinary: sandboxBinary,
		gitBinary:     gitBinary,
		helperBinary:  helperBinary,
	}, nil
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
	if executor.workspaceRoot != "" {
		if err := secureDirectoryWithin(
			executor.workspaceRoot,
			workingDir,
		); err != nil {
			return ProcessResult{}, errors.New("unsafe OS process working directory")
		}
	}

	sandbox, err := executor.prepareSandbox(workingDir)
	if err != nil {
		return ProcessResult{}, err
	}
	if sandbox != nil {
		defer sandbox.cleanup()
	}

	processContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	executable, arguments, commandDir, commandEnvironment :=
		executor.processCommand(request.Arguments, workingDir, sandbox)
	command := exec.CommandContext(processContext, executable, arguments...)
	command.Dir = commandDir
	command.Env = commandEnvironment
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout := &boundedBuffer{limit: request.MaxOutputBytes}
	stderr := &boundedBuffer{limit: request.MaxOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 5 * time.Second

	started := time.Now()
	waitErr := command.Run()
	duration := time.Since(started)
	if processContext.Err() != nil {
		return ProcessResult{
			ExitCode: -1,
			Stdout:   stdout.Bytes(),
			Stderr:   stderr.Bytes(),
			Duration: duration,
		}, errors.New("named-check process deadline exceeded")
	}
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

func validateProcessSandbox(
	config OSExecutorConfig,
) (string, string, error) {
	if config.SandboxBinary == "" || config.WorkspaceRoot == "" ||
		!filepath.IsAbs(config.SandboxBinary) {
		return "", "", errors.New("incomplete named-check process sandbox")
	}
	sandboxInfo, err := os.Lstat(config.SandboxBinary)
	if err != nil || !sandboxInfo.Mode().IsRegular() ||
		sandboxInfo.Mode()&0o111 == 0 ||
		sandboxInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("unsafe named-check process sandbox")
	}
	workspaceRoot, err := filepath.Abs(config.WorkspaceRoot)
	if err != nil || workspaceRoot == string(filepath.Separator) {
		return "", "", errors.New("unsafe named-check sandbox workspace")
	}
	workspaceInfo, err := os.Lstat(workspaceRoot)
	if err != nil || !workspaceInfo.IsDir() ||
		workspaceInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("unsafe named-check sandbox workspace")
	}
	return workspaceRoot, config.SandboxBinary, nil
}

func validateExecutable(pathValue string, label string) (string, error) {
	if !filepath.IsAbs(pathValue) {
		return "", fmt.Errorf("%s path is not absolute", label)
	}
	info, err := os.Lstat(pathValue)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&0o111 == 0 ||
		info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is unsafe", label)
	}
	return pathValue, nil
}

func secureDirectoryWithin(root string, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("directory escapes root")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() ||
			info.Mode()&os.ModeSymlink != 0 {
			return errors.New("directory path is unsafe")
		}
	}
	return nil
}

func executableInPath(pathValue string, name string) (string, error) {
	for _, directory := range filepath.SplitList(pathValue) {
		if !filepath.IsAbs(directory) {
			continue
		}
		candidate := filepath.Join(directory, name)
		info, err := os.Lstat(candidate)
		if err == nil && info.Mode().IsRegular() &&
			info.Mode()&0o111 != 0 && info.Mode()&os.ModeSymlink == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("trusted git executable is unavailable")
}

type processSandbox struct {
	root       string
	workspace  string
	home       string
	temp       string
	workingDir string
	executor   *OSExecutor
}

func (executor *OSExecutor) prepareSandbox(
	workingDir string,
) (*processSandbox, error) {
	if executor.sandboxBinary == "" {
		return nil, nil
	}
	relativeDir, err := filepath.Rel(executor.workspaceRoot, workingDir)
	if err != nil || relativeDir == ".." ||
		strings.HasPrefix(relativeDir, ".."+string(filepath.Separator)) {
		return nil, errors.New("named-check working directory escapes workspace")
	}
	root, err := os.MkdirTemp(
		filepath.Dir(executor.homeDir),
		".review-agent-check-",
	)
	if err != nil {
		return nil, errors.New("create named-check sandbox root")
	}
	sandbox := &processSandbox{
		root:      root,
		workspace: filepath.Join(root, "workspace"),
		home:      filepath.Join(root, "home"),
		temp:      filepath.Join(root, "tmp"),
		executor:  executor,
	}
	cleanupOnError := func() {
		sandbox.cleanup()
	}
	if err := os.Mkdir(sandbox.home, 0o700); err != nil {
		cleanupOnError()
		return nil, errors.New("create isolated named-check home")
	}
	if err := os.Mkdir(sandbox.temp, 0o700); err != nil {
		cleanupOnError()
		return nil, errors.New("create isolated named-check temporary directory")
	}
	command := exec.Command(
		executor.gitBinary,
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"-C", executor.workspaceRoot,
		"worktree", "add", "--detach", sandbox.workspace, "HEAD",
	)
	command.Env = trustedGitEnvironment(executor.gitBinary)
	if output, err := command.CombinedOutput(); err != nil {
		cleanupOnError()
		return nil, fmt.Errorf(
			"create isolated named-check worktree: %s",
			strings.TrimSpace(string(output)),
		)
	}
	sandbox.workingDir = filepath.Join(sandbox.workspace, relativeDir)
	if err := secureDirectoryWithin(
		sandbox.workspace,
		sandbox.workingDir,
	); err != nil {
		cleanupOnError()
		return nil, errors.New("isolated named-check working directory is unsafe")
	}
	return sandbox, nil
}

func trustedGitEnvironment(gitBinary string) []string {
	return []string{
		"PATH=" + filepath.Dir(gitBinary),
		"HOME=/nonexistent",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
}

func (sandbox *processSandbox) cleanup() {
	if sandbox == nil || sandbox.executor == nil {
		return
	}
	command := exec.Command(
		sandbox.executor.gitBinary,
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"-C", sandbox.executor.workspaceRoot,
		"worktree", "remove", "--force", sandbox.workspace,
	)
	command.Env = trustedGitEnvironment(sandbox.executor.gitBinary)
	_ = command.Run()
	_ = os.RemoveAll(sandbox.root)
}

func (executor *OSExecutor) processCommand(
	arguments []string,
	workingDir string,
	sandbox *processSandbox,
) (string, []string, string, []string) {
	if executor.sandboxBinary == "" {
		return arguments[0], arguments[1:], workingDir, executor.Environment()
	}
	arguments = slices.Clone(arguments)
	if arguments[0] == "review-agent-check" {
		arguments[0] = executor.helperBinary
	}
	sandboxArguments := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--bind", sandbox.workspace, sandbox.workspace,
		"--ro-bind",
		filepath.Join(sandbox.workspace, ".git"),
		filepath.Join(sandbox.workspace, ".git"),
		"--bind", sandbox.home, executor.homeDir,
		"--bind", sandbox.temp, executor.tempDir,
		"--bind", sandbox.temp, "/tmp",
		"--bind", sandbox.temp, "/var/tmp",
		"--chdir", sandbox.workingDir,
		"--clearenv",
	}
	for _, value := range executor.environment {
		name, content, _ := strings.Cut(value, "=")
		if name == "HOME" {
			content = executor.homeDir
		}
		if name == "TMPDIR" {
			content = executor.tempDir
		}
		sandboxArguments = append(
			sandboxArguments,
			"--setenv",
			name,
			content,
		)
	}
	sandboxArguments = append(sandboxArguments, "--cap-drop", "ALL")
	sandboxArguments = append(sandboxArguments, "--")
	sandboxArguments = append(sandboxArguments, arguments...)
	return executor.sandboxBinary,
		sandboxArguments,
		string(filepath.Separator),
		[]string{"PATH=" + os.Getenv("PATH")}
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
