package issueagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// RolloutMode is one administrator-owned capability ceiling.
type RolloutMode string

const (
	RolloutDisabled     RolloutMode = "disabled"
	RolloutShadow       RolloutMode = "shadow"
	RolloutIntake       RolloutMode = "intake"
	RolloutReproduction RolloutMode = "reproduction"
	RolloutRemediation  RolloutMode = "remediation"
	RolloutGeneral      RolloutMode = "general"
)

// IssueBudget bounds cumulative work for one Issue generation chain.
type IssueBudget struct {
	MaxReproductionAttempts  int
	MaxRemediationAttempts   int
	MaxCIRepairAttempts      int
	MaxInfrastructureRetries int
	MaxWorkerTime            time.Duration
}

// RepositoryBudget bounds concurrently active and rolling Worker use.
type RepositoryBudget struct {
	MaxActiveWorkers     int
	MaxHeavyWorkers      int
	RollingWindow        time.Duration
	MaxStartedWorkerTime time.Duration
}

// WorkerReservation is the fixed lease shape for one Worker phase.
type WorkerReservation struct {
	Duration time.Duration
	Heavy    bool
}

// ProviderPolicy contains non-secret Adapter selection configuration.
type ProviderPolicy struct {
	Provider           issueagentcontract.Provider `json:"provider"`
	Endpoint           string                      `json:"endpoint"`
	ModelVariable      string                      `json:"model_variable"`
	CredentialVariable string                      `json:"credential_variable"`
}

// Policy is protected default-branch configuration for trusted planning.
type Policy struct {
	SchemaVersion             int
	Enabled                   bool
	RolloutMode               RolloutMode
	DefaultProvider           issueagentcontract.Provider
	SandboxImage              string
	IssueBudget               IssueBudget
	RepositoryBudget          RepositoryBudget
	ProtectedPaths            []string
	GeneratedFiles            []string
	HighRiskClasses           []string
	Providers                 []ProviderPolicy
	RemediationIssueAllowlist []int64
	AllowedBackportBranches   []string
}

type policyJSON struct {
	SchemaVersion   int                         `json:"schema_version"`
	Enabled         bool                        `json:"enabled"`
	RolloutMode     RolloutMode                 `json:"rollout_mode"`
	DefaultProvider issueagentcontract.Provider `json:"default_provider"`
	SandboxImage    string                      `json:"sandbox_image"`
	IssueBudget     struct {
		MaxReproductionAttempts  int    `json:"max_reproduction_attempts"`
		MaxRemediationAttempts   int    `json:"max_remediation_attempts"`
		MaxCIRepairAttempts      int    `json:"max_ci_repair_attempts"`
		MaxInfrastructureRetries int    `json:"max_infrastructure_retries"`
		MaxWorkerTime            string `json:"max_worker_time"`
	} `json:"issue_budget"`
	RepositoryBudget struct {
		MaxActiveWorkers     int    `json:"max_active_workers"`
		MaxHeavyWorkers      int    `json:"max_heavy_workers"`
		RollingWindow        string `json:"rolling_window"`
		MaxStartedWorkerTime string `json:"max_started_worker_time"`
	} `json:"repository_budget"`
	ProtectedPaths            []string         `json:"protected_paths"`
	GeneratedFiles            []string         `json:"generated_files"`
	HighRiskClasses           []string         `json:"high_risk_classes"`
	Providers                 []ProviderPolicy `json:"providers"`
	RemediationIssueAllowlist []int64          `json:"remediation_issue_allowlist"`
	AllowedBackportBranches   []string         `json:"allowed_backport_branches"`
}

var requiredProtectedPaths = []string{
	".agents",
	".github/ISSUE_TEMPLATE",
	".github/issue-agent",
	".github/workflows",
	"AGENTS.md",
	"cmd/wkissueagent",
	"internal/access/issueagentcli",
	"internal/app/issue_agent",
	"internal/contracts/issueagent",
	"internal/infra/issueagentgithub",
	"internal/infra/issueagentmodel",
	"internal/runtime/issueagentworker",
	"internal/usecase/issueagent",
	"scripts/issue_agent",
}

// DecodePolicy strictly decodes bounded protected policy JSON.
func DecodePolicy(reader io.Reader, maxBytes int64) (Policy, error) {
	if reader == nil || maxBytes <= 0 {
		return Policy{}, errors.New("policy input limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return Policy{}, fmt.Errorf("read policy: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return Policy{}, errors.New("policy exceeds byte limit")
	}
	var encoded policyJSON
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&encoded); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Policy{}, errors.New("policy contains trailing JSON")
	}

	maxWorkerTime, err := time.ParseDuration(encoded.IssueBudget.MaxWorkerTime)
	if err != nil {
		return Policy{}, errors.New("invalid per-Issue worker time")
	}
	rollingWindow, err := time.ParseDuration(encoded.RepositoryBudget.RollingWindow)
	if err != nil {
		return Policy{}, errors.New("invalid repository rolling window")
	}
	maxStartedWorkerTime, err := time.ParseDuration(
		encoded.RepositoryBudget.MaxStartedWorkerTime,
	)
	if err != nil {
		return Policy{}, errors.New("invalid repository started-worker budget")
	}
	policy := Policy{
		SchemaVersion:   encoded.SchemaVersion,
		Enabled:         encoded.Enabled,
		RolloutMode:     encoded.RolloutMode,
		DefaultProvider: encoded.DefaultProvider,
		SandboxImage:    encoded.SandboxImage,
		IssueBudget: IssueBudget{
			MaxReproductionAttempts:  encoded.IssueBudget.MaxReproductionAttempts,
			MaxRemediationAttempts:   encoded.IssueBudget.MaxRemediationAttempts,
			MaxCIRepairAttempts:      encoded.IssueBudget.MaxCIRepairAttempts,
			MaxInfrastructureRetries: encoded.IssueBudget.MaxInfrastructureRetries,
			MaxWorkerTime:            maxWorkerTime,
		},
		RepositoryBudget: RepositoryBudget{
			MaxActiveWorkers:     encoded.RepositoryBudget.MaxActiveWorkers,
			MaxHeavyWorkers:      encoded.RepositoryBudget.MaxHeavyWorkers,
			RollingWindow:        rollingWindow,
			MaxStartedWorkerTime: maxStartedWorkerTime,
		},
		ProtectedPaths:  append([]string(nil), encoded.ProtectedPaths...),
		GeneratedFiles:  append([]string(nil), encoded.GeneratedFiles...),
		HighRiskClasses: append([]string(nil), encoded.HighRiskClasses...),
		Providers:       append([]ProviderPolicy(nil), encoded.Providers...),
		RemediationIssueAllowlist: append(
			[]int64(nil), encoded.RemediationIssueAllowlist...,
		),
		AllowedBackportBranches: append([]string(nil), encoded.AllowedBackportBranches...),
	}
	if err := ValidatePolicy(policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// ValidatePolicy prevents protected configuration from widening approved bounds.
func ValidatePolicy(policy Policy) error {
	if policy.SchemaVersion != 1 || !validRolloutMode(policy.RolloutMode) {
		return errors.New("invalid Issue Agent policy identity")
	}
	if !sandboxImagePattern.MatchString(policy.SandboxImage) {
		return errors.New("Issue Agent sandbox image is not digest-pinned")
	}
	if policy.IssueBudget.MaxReproductionAttempts <= 0 ||
		policy.IssueBudget.MaxReproductionAttempts > 3 ||
		policy.IssueBudget.MaxRemediationAttempts <= 0 ||
		policy.IssueBudget.MaxRemediationAttempts > 3 ||
		policy.IssueBudget.MaxCIRepairAttempts <= 0 ||
		policy.IssueBudget.MaxCIRepairAttempts > 2 ||
		policy.IssueBudget.MaxInfrastructureRetries <= 0 ||
		policy.IssueBudget.MaxInfrastructureRetries > 3 ||
		policy.IssueBudget.MaxWorkerTime <= 0 ||
		policy.IssueBudget.MaxWorkerTime > 6*time.Hour {
		return errors.New("per-Issue policy exceeds approved budget")
	}
	if policy.RepositoryBudget.MaxActiveWorkers <= 0 ||
		policy.RepositoryBudget.MaxActiveWorkers > 3 ||
		policy.RepositoryBudget.MaxHeavyWorkers <= 0 ||
		policy.RepositoryBudget.MaxHeavyWorkers > 1 ||
		policy.RepositoryBudget.RollingWindow != 24*time.Hour ||
		policy.RepositoryBudget.MaxStartedWorkerTime <= 0 ||
		policy.RepositoryBudget.MaxStartedWorkerTime > 24*time.Hour {
		return errors.New("repository policy exceeds approved budget")
	}
	for _, required := range requiredProtectedPaths {
		if !slices.Contains(policy.ProtectedPaths, required) {
			return fmt.Errorf("protected path %q is missing", required)
		}
	}
	if !strictlySortedUnique(policy.ProtectedPaths) ||
		!strictlySortedUnique(policy.GeneratedFiles) ||
		!strictlySortedUnique(policy.HighRiskClasses) ||
		!strictlySortedUnique(policy.AllowedBackportBranches) {
		return errors.New("policy string lists must be strictly sorted and unique")
	}
	if len(policy.HighRiskClasses) == 0 || len(policy.Providers) != 2 {
		return errors.New("policy risk classes or providers are incomplete")
	}
	if !slices.IsSorted(policy.RemediationIssueAllowlist) ||
		len(policy.RemediationIssueAllowlist) > 100 {
		return errors.New("remediation Issue allowlist is invalid")
	}
	for index, issueNumber := range policy.RemediationIssueAllowlist {
		if issueNumber <= 0 ||
			index > 0 && policy.RemediationIssueAllowlist[index-1] == issueNumber {
			return errors.New("remediation Issue allowlist is invalid")
		}
	}
	seenProviders := make(map[issueagentcontract.Provider]struct{}, len(policy.Providers))
	for _, provider := range policy.Providers {
		if provider.Provider != issueagentcontract.ProviderCodex &&
			provider.Provider != issueagentcontract.ProviderDeepSeek {
			return errors.New("unsupported policy provider")
		}
		if _, duplicate := seenProviders[provider.Provider]; duplicate {
			return errors.New("duplicate policy provider")
		}
		seenProviders[provider.Provider] = struct{}{}
		if provider.ModelVariable == "" || provider.CredentialVariable == "" {
			return errors.New("provider variable names are required")
		}
		if provider.Provider == issueagentcontract.ProviderDeepSeek &&
			provider.Endpoint != "https://api.deepseek.com" {
			return errors.New("DeepSeek endpoint is outside the allowlist")
		}
		if provider.Provider == issueagentcontract.ProviderCodex && provider.Endpoint != "" {
			return errors.New("Codex CLI policy must not define a remote endpoint")
		}
	}
	if _, ok := seenProviders[policy.DefaultProvider]; !ok {
		return errors.New("default provider is not configured")
	}
	return nil
}

// AllowsReproduction reports whether protected rollout permits no-fix E2E
// reproduction.
func AllowsReproduction(policy Policy) bool {
	if !policy.Enabled {
		return false
	}
	switch policy.RolloutMode {
	case RolloutReproduction, RolloutRemediation, RolloutGeneral:
		return true
	default:
		return false
	}
}

// AllowsAutomatedRemediation applies the protected rollout allowlist.
func AllowsAutomatedRemediation(policy Policy, issueNumber int64) bool {
	return policy.Enabled && issueNumber > 0 &&
		(policy.RolloutMode == RolloutGeneral ||
			policy.RolloutMode == RolloutRemediation &&
				slices.Contains(policy.RemediationIssueAllowlist, issueNumber))
}

// WorkerReservationForPhase returns the closed lease duration and weight.
func WorkerReservationForPhase(
	phase issueagentcontract.Phase,
) (WorkerReservation, error) {
	switch phase {
	case issueagentcontract.PhaseReproduce:
		return WorkerReservation{Duration: 95 * time.Minute, Heavy: true}, nil
	case issueagentcontract.PhaseDiagnose:
		return WorkerReservation{Duration: 65 * time.Minute}, nil
	case issueagentcontract.PhaseFix, issueagentcontract.PhaseAddressReview:
		return WorkerReservation{Duration: 95 * time.Minute, Heavy: true}, nil
	default:
		return WorkerReservation{}, errors.New("unsupported Worker reservation phase")
	}
}

// CheckIssueWorkerBudget applies cumulative time and phase-attempt budgets
// before a task is leased.
func CheckIssueWorkerBudget(
	checkpoint issueagentcontract.Checkpoint,
	policy Policy,
	phase issueagentcontract.Phase,
) error {
	reservation, err := WorkerReservationForPhase(phase)
	if err != nil {
		return err
	}
	var attempts uint32
	var maxAttempts int
	switch phase {
	case issueagentcontract.PhaseReproduce:
		attempts = checkpoint.Budget.ReproductionAttempts
		maxAttempts = policy.IssueBudget.MaxReproductionAttempts
	case issueagentcontract.PhaseFix, issueagentcontract.PhaseAddressReview:
		attempts = checkpoint.Budget.RemediationAttempts
		maxAttempts = policy.IssueBudget.MaxRemediationAttempts
	}
	if maxAttempts > 0 && int(attempts) >= maxAttempts {
		return errors.New("per-Issue Worker attempt budget is exhausted")
	}
	maxSeconds := uint64(policy.IssueBudget.MaxWorkerTime / time.Second)
	reservedSeconds := uint64(reservation.Duration / time.Second)
	if maxSeconds == 0 || checkpoint.Budget.WorkerSeconds > maxSeconds ||
		reservedSeconds > maxSeconds-checkpoint.Budget.WorkerSeconds {
		return errors.New("per-Issue Worker-time budget is exhausted")
	}
	return nil
}

var sandboxImagePattern = regexp.MustCompile(
	`^[a-z0-9][a-z0-9./_-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`,
)

func validRolloutMode(mode RolloutMode) bool {
	switch mode {
	case RolloutDisabled, RolloutShadow, RolloutIntake, RolloutReproduction,
		RolloutRemediation, RolloutGeneral:
		return true
	default:
		return false
	}
}

func strictlySortedUnique(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if strings.TrimSpace(value) == "" || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}
