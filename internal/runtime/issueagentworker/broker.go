package issueagentworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// BrokerConfig is trusted task policy for one credential-free workspace.
type BrokerConfig struct {
	Workspace         string
	AllowedWritePaths []string
	AllowedCommands   []issueagent.CommandRule
	MaxFileBytes      int64
	MaxOutputBytes    int64
}

// ExecRequest is an argv-only process request passed to the sandbox runner.
type ExecRequest struct {
	Executable  string
	Arguments   []string
	WorkingDir  string
	Timeout     time.Duration
	OutputLimit int64
	Environment []string
}

// ExecResult is bounded while the ToolRunner captures process output.
type ExecResult struct {
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	Duration        time.Duration
}

// ToolRunner executes one command inside the untrusted no-network sandbox.
type ToolRunner interface {
	Run(context.Context, ExecRequest) (ExecResult, error)
}

// CommandRequest is the provider-neutral command_run input.
type CommandRequest struct {
	Argv        []string      `json:"argv"`
	WorkingDir  string        `json:"working_dir"`
	Timeout     time.Duration `json:"timeout"`
	OutputLimit int64         `json:"output_limit"`
}

// ReadResult is one bounded workspace_read response.
type ReadResult struct {
	ID      uint64 `json:"id"`
	Content []byte `json:"content"`
	SHA256  string `json:"sha256"`
}

// CommandResult is one bounded command_run response and evidence record.
type CommandResult struct {
	ID              uint64 `json:"id"`
	ExitCode        int    `json:"exit_code"`
	Stdout          []byte `json:"stdout"`
	Stderr          []byte `json:"stderr"`
	StdoutSHA256    string `json:"stdout_sha256"`
	StderrSHA256    string `json:"stderr_sha256"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	DurationMS      int64  `json:"duration_ms"`
}

// Broker owns the closed tool catalog and monotonic evidence sequence.
type Broker struct {
	root            string
	allowedWrites   []string
	allowedCommands []issueagent.CommandRule
	maxFileBytes    int64
	maxOutputBytes  int64
	runner          ToolRunner

	mu       sync.Mutex
	nextID   uint64
	evidence []ToolEvidence
}

// NewBroker validates one workspace and returns a no-secret broker.
func NewBroker(config BrokerConfig, runner ToolRunner) (*Broker, error) {
	if runner == nil || config.MaxFileBytes <= 0 ||
		config.MaxFileBytes > 8<<20 || config.MaxOutputBytes <= 0 ||
		config.MaxOutputBytes > 16<<20 ||
		len(config.AllowedCommands) == 0 {
		return nil, errors.New("Worker broker configuration is invalid")
	}
	root, err := filepath.Abs(config.Workspace)
	if err != nil {
		return nil, errors.New("resolve Worker workspace")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("Worker workspace does not exist")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("Worker workspace is not a directory")
	}
	for _, allowed := range config.AllowedWritePaths {
		if !safeRelativePath(allowed) {
			return nil, errors.New("Worker write policy contains an unsafe path")
		}
	}
	if !slices.IsSorted(config.AllowedWritePaths) {
		return nil, errors.New("Worker write paths must be sorted")
	}
	for _, rule := range config.AllowedCommands {
		if rule.Executable == "" || rule.MaxArgs <= 0 ||
			len(rule.ArgvPrefix) > rule.MaxArgs {
			return nil, errors.New("Worker command policy is invalid")
		}
	}
	return &Broker{
		root:            root,
		allowedWrites:   append([]string(nil), config.AllowedWritePaths...),
		allowedCommands: append([]issueagent.CommandRule(nil), config.AllowedCommands...),
		maxFileBytes:    config.MaxFileBytes,
		maxOutputBytes:  config.MaxOutputBytes,
		runner:          runner,
	}, nil
}

// Read performs one path-confined bounded workspace read.
func (broker *Broker) Read(ctx context.Context, relativePath string) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	resolved, err := broker.resolveExisting(relativePath)
	if err != nil {
		return ReadResult{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > broker.maxFileBytes {
		return ReadResult{}, errors.New("workspace file is not a bounded regular file")
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return ReadResult{}, errors.New("read workspace file")
	}
	id := broker.record(ToolEvidence{
		Tool: "workspace_read", Path: relativePath,
		OutputSHA256: digest(content),
	})
	return ReadResult{ID: id, Content: content, SHA256: digest(content)}, nil
}

// RunCommand validates an argv request and invokes the injected sandbox runner.
func (broker *Broker) RunCommand(
	ctx context.Context,
	request CommandRequest,
) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	if len(request.Argv) == 0 || len(request.Argv) > 33 ||
		request.Timeout <= 0 || request.Timeout > 2*time.Hour ||
		request.OutputLimit <= 0 ||
		request.OutputLimit > broker.maxOutputBytes {
		return CommandResult{}, errors.New("command_run request is outside bounds")
	}
	for _, value := range request.Argv {
		if value == "" || len(value) > 4096 || strings.ContainsRune(value, 0) {
			return CommandResult{}, errors.New("command_run argv is invalid")
		}
	}
	if !broker.commandAllowed(request.Argv) {
		return CommandResult{}, errors.New("command_run argv is outside task policy")
	}
	workingDir, err := broker.resolveExisting(request.WorkingDir)
	if err != nil {
		return CommandResult{}, err
	}
	info, err := os.Stat(workingDir)
	if err != nil || !info.IsDir() {
		return CommandResult{}, errors.New("command_run working directory is invalid")
	}
	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	started := time.Now()
	result, err := broker.runner.Run(runCtx, ExecRequest{
		Executable:  request.Argv[0],
		Arguments:   append([]string(nil), request.Argv[1:]...),
		WorkingDir:  workingDir,
		Timeout:     request.Timeout,
		OutputLimit: request.OutputLimit,
		Environment: []string{
			"HOME=/nonexistent",
			"LANG=C.UTF-8",
			"LC_ALL=C.UTF-8",
			"PATH=/usr/local/go/bin:/usr/bin:/bin",
			"GOWORK=off",
			"GOMODCACHE=/go/pkg/mod",
			"GOCACHE=/tmp/go-build",
		},
	})
	if ctxErr := runCtx.Err(); ctxErr != nil {
		return CommandResult{}, ctxErr
	}
	if err != nil {
		return CommandResult{}, errors.New("sandbox command failed to start")
	}
	if result.Duration <= 0 {
		result.Duration = time.Since(started)
	}
	stdout, stdoutTruncated := truncate(result.Stdout, request.OutputLimit)
	stderr, stderrTruncated := truncate(result.Stderr, request.OutputLimit)
	stdoutTruncated = stdoutTruncated || result.StdoutTruncated
	stderrTruncated = stderrTruncated || result.StderrTruncated
	evidence := ToolEvidence{
		Tool: "command_run", Executable: request.Argv[0],
		Arguments: append([]string(nil), request.Argv[1:]...),
		Path:      request.WorkingDir, ExitCode: result.ExitCode,
		OutputSHA256: digest(stdout), ErrorSHA256: digest(stderr),
		DurationMS: result.Duration.Milliseconds(),
	}
	evidence.AssertionSHA256 = assertionDigestFromOutput(stdout, stderr)
	id := broker.record(evidence)
	return CommandResult{
		ID: id, ExitCode: result.ExitCode, Stdout: stdout, Stderr: stderr,
		StdoutSHA256:    evidence.OutputSHA256,
		StderrSHA256:    evidence.ErrorSHA256,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
		DurationMS:      evidence.DurationMS,
	}, nil
}

var assertionMarkerPattern = regexp.MustCompile(
	`(?m)^WK_ISSUE_AGENT_ASSERTION_FAILED (sha256:[0-9a-f]{64})\r?$`,
)

func assertionDigestFromOutput(stdout, stderr []byte) string {
	matches := assertionMarkerPattern.FindAllSubmatch(
		append(append([]byte(nil), stdout...), stderr...),
		-1,
	)
	if len(matches) != 1 {
		return ""
	}
	return string(matches[0][1])
}

func (broker *Broker) commandAllowed(argv []string) bool {
	for _, rule := range broker.allowedCommands {
		if argv[0] != rule.Executable || len(argv)-1 > rule.MaxArgs ||
			len(argv)-1 < len(rule.ArgvPrefix) {
			continue
		}
		if slices.Equal(argv[1:1+len(rule.ArgvPrefix)], rule.ArgvPrefix) {
			return true
		}
	}
	return false
}

func (broker *Broker) resolveExisting(relativePath string) (string, error) {
	if broker == nil || !safeRelativePath(relativePath) {
		return "", errors.New("workspace path is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(broker.root, relativePath))
	if err != nil {
		return "", errors.New("workspace path does not exist")
	}
	if !withinRoot(broker.root, resolved) {
		return "", errors.New("workspace path escapes task root")
	}
	return resolved, nil
}

func safeRelativePath(value string) bool {
	if value == "." {
		return true
	}
	return value != "" && !filepath.IsAbs(value) && !strings.Contains(value, "\\") &&
		!strings.ContainsRune(value, 0) &&
		filepath.Clean(value) == value &&
		value != ".." && !strings.HasPrefix(value, "../")
}

func withinRoot(root string, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, "../") &&
		!filepath.IsAbs(relative)
}

func truncate(value []byte, limit int64) ([]byte, bool) {
	if int64(len(value)) <= limit {
		return append([]byte(nil), value...), false
	}
	return append([]byte(nil), value[:limit]...), true
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (broker *Broker) record(evidence ToolEvidence) uint64 {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.nextID++
	evidence.ID = broker.nextID
	broker.evidence = append(broker.evidence, evidence)
	return broker.nextID
}
