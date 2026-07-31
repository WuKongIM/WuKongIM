package reviewagentverify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// ProcessRequest is one already-resolved protected command.
type ProcessRequest struct {
	Arguments      []string
	WorkingDir     string
	Environment    []string
	Timeout        time.Duration
	MaxOutputBytes int
}

// ProcessResult is bounded executor output.
type ProcessResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

// ProcessExecutor is the operating-system process boundary.
type ProcessExecutor interface {
	Execute(context.Context, ProcessRequest) (ProcessResult, error)
}

// RunnerConfig configures one credential-free named-check runner.
type RunnerConfig struct {
	WorkspaceRoot string
	Policy        Policy
	Executor      ProcessExecutor
	Ledger        EvidenceLedger
	Now           func() time.Time
}

// Runner resolves names through protected policy and records trusted results.
type Runner struct {
	workspaceRoot string
	policy        Policy
	executor      ProcessExecutor
	ledger        EvidenceLedger
	now           func() time.Time
}

// NewRunner validates the trusted runner boundary.
func NewRunner(config RunnerConfig) (*Runner, error) {
	if config.Executor == nil || config.Ledger == nil ||
		len(config.Policy.TrustedChecks) == 0 {
		return nil, errors.New("invalid named-check runner configuration")
	}
	root, err := filepath.Abs(config.WorkspaceRoot)
	if err != nil {
		return nil, errors.New("resolve named-check workspace")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("named-check workspace is unsafe")
	}
	for _, plan := range config.Policy.TrustedChecks {
		if err := validateCheckPlan(root, plan); err != nil {
			return nil, err
		}
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{
		workspaceRoot: root,
		policy:        config.Policy,
		executor:      config.Executor,
		ledger:        config.Ledger,
		now:           now,
	}, nil
}

// Names returns the sorted, immutable check registry.
func (runner *Runner) Names() []string {
	result := make([]string, 0, len(runner.policy.TrustedChecks))
	for name := range runner.policy.TrustedChecks {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

// Result returns the newest trusted ledger result for one catalog name.
func (runner *Runner) Result(
	generation contract.GenerationIdentity,
	name string,
) (contract.CheckEvidence, error) {
	if _, exists := runner.policy.TrustedChecks[name]; !exists {
		return contract.CheckEvidence{}, errors.New("unknown trusted check")
	}
	records, err := runner.ledger.List(generation)
	if err != nil {
		return contract.CheckEvidence{}, err
	}
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].Evidence.Name == name {
			return records[index].Evidence, nil
		}
	}
	return contract.CheckEvidence{}, errors.New("trusted check has no result")
}

// CollectEvidence validates the ledger chain and returns the newest result for
// every executed check, requiring the complete mandatory set.
func (runner *Runner) CollectEvidence(
	generation contract.GenerationIdentity,
	mandatory []string,
) (contract.ReviewEvidence, error) {
	if runner == nil {
		return contract.ReviewEvidence{}, errors.New(
			"evidence collector is unavailable",
		)
	}
	return CollectEvidence(
		runner.ledger,
		runner.policy,
		runner.now,
		generation,
		mandatory,
	)
}

// CollectEvidence reads and validates trusted ledger evidence without
// constructing a process executor.
func CollectEvidence(
	ledger EvidenceLedger,
	policy Policy,
	now func() time.Time,
	generation contract.GenerationIdentity,
	mandatory []string,
) (contract.ReviewEvidence, error) {
	if ledger == nil || len(policy.TrustedChecks) == 0 {
		return contract.ReviewEvidence{}, errors.New(
			"invalid evidence collector configuration",
		)
	}
	if now == nil {
		now = time.Now
	}
	if err := contract.ValidateGenerationIdentity(generation); err != nil {
		return contract.ReviewEvidence{}, err
	}
	records, err := ledger.List(generation)
	if err != nil {
		return contract.ReviewEvidence{}, err
	}
	latest := make(map[string]contract.CheckEvidence)
	for _, record := range records {
		if _, protected := policy.TrustedChecks[record.Evidence.Name]; !protected {
			return contract.ReviewEvidence{}, errors.New(
				"evidence ledger names an unknown trusted check",
			)
		}
		latest[record.Evidence.Name] = record.Evidence
	}
	for _, name := range mandatory {
		if _, exists := policy.TrustedChecks[name]; !exists {
			return contract.ReviewEvidence{}, errors.New(
				"mandatory evidence names an unknown trusted check",
			)
		}
		if _, exists := latest[name]; !exists {
			return contract.ReviewEvidence{}, errors.New(
				"mandatory trusted check has no evidence",
			)
		}
	}
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	slices.Sort(names)
	checks := make([]contract.CheckEvidence, 0, len(names))
	for _, name := range names {
		checks = append(checks, latest[name])
	}
	evidence := contract.ReviewEvidence{
		SchemaVersion: 1,
		Generation:    generation,
		Complete:      true,
		Checks:        checks,
		CreatedAt:     now().UTC(),
	}
	if err := contract.ValidateReviewEvidence(evidence); err != nil {
		return contract.ReviewEvidence{}, err
	}
	return evidence, nil
}

// Run executes exactly one protected named check.
func (runner *Runner) Run(
	ctx context.Context,
	generation contract.GenerationIdentity,
	name string,
) (contract.CheckEvidence, error) {
	if err := contract.ValidateGenerationIdentity(generation); err != nil {
		return contract.CheckEvidence{}, err
	}
	plan, exists := runner.policy.TrustedChecks[name]
	if !exists {
		return contract.CheckEvidence{}, errors.New("unknown trusted check")
	}
	workingDir := filepath.Join(runner.workspaceRoot, filepath.FromSlash(plan.WorkingDir))
	result, executeErr := runner.executor.Execute(ctx, ProcessRequest{
		Arguments:      slices.Clone(plan.Arguments),
		WorkingDir:     workingDir,
		Environment:    nil,
		Timeout:        time.Duration(plan.TimeoutSeconds) * time.Second,
		MaxOutputBytes: plan.MaxOutputBytes,
	})
	if result.Duration <= 0 {
		result.Duration = time.Millisecond
	}
	if len(result.Stdout) > plan.MaxOutputBytes ||
		len(result.Stderr) > plan.MaxOutputBytes {
		return contract.CheckEvidence{}, errors.New(
			"named-check output exceeds byte limit",
		)
	}
	outcome := contract.CheckOutcomePassed
	if executeErr != nil {
		outcome = contract.CheckOutcomeError
	} else if result.ExitCode != 0 {
		outcome = contract.CheckOutcomeFailed
	}
	planBody, err := json.Marshal(plan)
	if err != nil {
		return contract.CheckEvidence{}, errors.New("encode named-check plan")
	}
	evidence := contract.CheckEvidence{
		Name:          name,
		CommandDigest: bytesDigest(planBody),
		Outcome:       outcome,
		ExitCode:      result.ExitCode,
		DurationMS:    uint64(result.Duration.Milliseconds()),
		StdoutDigest:  bytesDigest(result.Stdout),
		StderrDigest:  bytesDigest(result.Stderr),
		Stdout:        outputExcerpt(result.Stdout),
		Stderr:        outputExcerpt(result.Stderr),
	}
	reviewEvidence := contract.ReviewEvidence{
		SchemaVersion: 1,
		Generation:    generation,
		Complete:      true,
		Checks:        []contract.CheckEvidence{evidence},
		CreatedAt:     runner.now().UTC(),
	}
	if err := contract.ValidateReviewEvidence(reviewEvidence); err != nil {
		return contract.CheckEvidence{}, err
	}
	if err := runner.ledger.Append(generation, evidence); err != nil {
		return contract.CheckEvidence{}, err
	}
	return evidence, nil
}

func outputExcerpt(output []byte) string {
	const marker = "\n... review-agent output excerpt truncated ...\n"
	if len(output) <= contract.MaxCheckOutputExcerptBytes {
		return boundedValidUTF8(output)
	}
	const available = contract.MaxCheckOutputExcerptBytes - len(marker)
	const first = available / 2
	const last = available - first
	return boundedValidUTF8(
		append(
			append(append([]byte(nil), output[:first]...), marker...),
			output[len(output)-last:]...,
		),
	)
}

func boundedValidUTF8(output []byte) string {
	normalized := strings.ToValidUTF8(string(output), "\uFFFD")
	if len(normalized) <= contract.MaxCheckOutputExcerptBytes {
		return normalized
	}
	end := contract.MaxCheckOutputExcerptBytes
	for end > 0 && !utf8.ValidString(normalized[:end]) {
		end--
	}
	return normalized[:end]
}

func validateCheckPlan(root string, plan CheckPlan) error {
	if len(plan.Arguments) == 0 || len(plan.Arguments) > 128 ||
		plan.TimeoutSeconds <= 0 || plan.TimeoutSeconds > 5400 ||
		plan.MaxOutputBytes <= 0 || plan.MaxOutputBytes > 16<<20 {
		return errors.New("invalid trusted check plan")
	}
	for _, argument := range plan.Arguments {
		if argument == "" || len(argument) > 4096 {
			return errors.New("invalid trusted check argument")
		}
	}
	workingDir := filepath.Clean(filepath.Join(
		root,
		filepath.FromSlash(plan.WorkingDir),
	))
	relative, err := filepath.Rel(root, workingDir)
	if err != nil || startsWithParent(relative) {
		return errors.New("trusted check working directory escapes workspace")
	}
	return nil
}
