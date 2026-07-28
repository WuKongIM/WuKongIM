package issueagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	// MaxFrozenIssueBytes bounds untrusted Issue text copied into a task.
	MaxFrozenIssueBytes = 64 << 10

	maxTaskSliceItems  = 128
	maxTaskStringBytes = 512
)

var executablePattern = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

// Phase is one bounded Worker activity.
type Phase string

const (
	PhaseReproduce     Phase = "reproduce"
	PhaseDiagnose      Phase = "diagnose"
	PhaseFix           Phase = "fix"
	PhaseAddressReview Phase = "address_review"
)

// Provider selects one model Adapter without changing task semantics.
type Provider string

const (
	ProviderCodex    Provider = "codex"
	ProviderDeepSeek Provider = "deepseek"
)

// FileDigest freezes one repository instruction or prompt input.
type FileDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// CommandRule permits one executable with a fixed argument prefix.
type CommandRule struct {
	Executable string   `json:"executable"`
	ArgvPrefix []string `json:"argv_prefix"`
	MaxArgs    int      `json:"max_args"`
}

// ResourceLimits are enforced by the trusted Worker and tool broker.
type ResourceLimits struct {
	WallTime       time.Duration `json:"wall_time"`
	MaxOutputBytes int64         `json:"max_output_bytes"`
	MaxFiles       int           `json:"max_files"`
	MaxFileBytes   int64         `json:"max_file_bytes"`
	MaxTotalBytes  int64         `json:"max_total_bytes"`
}

// TaskEnvelope is the immutable, provider-neutral input for one Worker phase.
type TaskEnvelope struct {
	SchemaVersion      int            `json:"schema_version"`
	Repository         string         `json:"repository"`
	IssueNumber        int64          `json:"issue_number"`
	Generation         uint64         `json:"generation"`
	Sequence           uint64         `json:"sequence"`
	OperationID        string         `json:"operation_id"`
	Phase              Phase          `json:"phase"`
	CheckpointDigest   string         `json:"checkpoint_digest"`
	PolicyDigest       string         `json:"policy_digest"`
	PromptDigest       string         `json:"prompt_digest"`
	AffectedSHA        string         `json:"affected_sha"`
	DiagnosisBaseSHA   string         `json:"diagnosis_base_sha"`
	CandidateSHA       string         `json:"candidate_sha,omitempty"`
	FrozenIssue        string         `json:"frozen_issue"`
	AcceptedCommentIDs []int64        `json:"accepted_comment_ids"`
	InstructionDigests []FileDigest   `json:"instruction_digests"`
	AllowedPaths       []string       `json:"allowed_paths"`
	AllowedCommands    []CommandRule  `json:"allowed_commands"`
	Limits             ResourceLimits `json:"limits"`
	RequiredTopology   string         `json:"required_topology,omitempty"`
	RequiredRuns       int            `json:"required_runs,omitempty"`
	// ProductionChangesAllowed is false for reproduction and is explicit in
	// the signed task so a provider cannot widen a no-fix phase.
	ProductionChangesAllowed bool     `json:"production_changes_allowed"`
	Provider                 Provider `json:"provider"`
	Model                    string   `json:"model"`
}

// TaskDigest returns the canonical digest used by a signed Worker lease.
func TaskDigest(task TaskEnvelope) (string, error) {
	if err := ValidateTaskEnvelope(task); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		return "", errors.New("encode task envelope")
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ValidateTaskEnvelope rejects an unsafe or ambiguous Worker task.
func ValidateTaskEnvelope(task TaskEnvelope) error {
	if task.SchemaVersion != 1 {
		return errors.New("unsupported task schema version")
	}
	if !validRepository(task.Repository) ||
		task.IssueNumber <= 0 ||
		task.Generation == 0 ||
		task.Sequence == 0 {
		return errors.New("invalid task identity")
	}
	for name, value := range map[string]string{
		"operation":  task.OperationID,
		"checkpoint": task.CheckpointDigest,
		"policy":     task.PolicyDigest,
		"prompt":     task.PromptDigest,
	} {
		if !digestPattern.MatchString(value) {
			return fmt.Errorf("invalid %s digest", name)
		}
	}
	if !validPhase(task.Phase) {
		return fmt.Errorf("invalid task phase %q", task.Phase)
	}
	if !gitSHAPattern.MatchString(task.AffectedSHA) ||
		!gitSHAPattern.MatchString(task.DiagnosisBaseSHA) {
		return errors.New("task source SHAs must be immutable")
	}
	if task.Phase == PhaseReproduce && task.CandidateSHA != "" ||
		task.Phase != PhaseReproduce && !gitSHAPattern.MatchString(task.CandidateSHA) {
		return errors.New("task candidate SHA does not match its phase")
	}
	if len(task.FrozenIssue) == 0 || len(task.FrozenIssue) > MaxFrozenIssueBytes {
		return errors.New("frozen Issue input is empty or oversized")
	}
	if err := validatePositiveSortedIDs(task.AcceptedCommentIDs); err != nil {
		return fmt.Errorf("accepted comment IDs: %w", err)
	}
	if err := validateInstructionDigests(task.InstructionDigests); err != nil {
		return err
	}
	if err := validateAllowedPaths(task.AllowedPaths); err != nil {
		return err
	}
	if err := validateCommandRules(task.AllowedCommands); err != nil {
		return err
	}
	if task.Limits.WallTime <= 0 || task.Limits.WallTime > 2*time.Hour ||
		task.Limits.MaxOutputBytes <= 0 || task.Limits.MaxOutputBytes > 16<<20 ||
		task.Limits.MaxFiles <= 0 || task.Limits.MaxFiles > 128 ||
		task.Limits.MaxFileBytes <= 0 || task.Limits.MaxFileBytes > 8<<20 ||
		task.Limits.MaxTotalBytes <= 0 || task.Limits.MaxTotalBytes > 32<<20 {
		return errors.New("task resource limits are outside policy bounds")
	}
	if task.Phase == PhaseReproduce {
		if task.RequiredRuns != 3 || task.ProductionChangesAllowed ||
			task.RequiredTopology != "single-node-cluster" &&
				task.RequiredTopology != "three-node-cluster" &&
				task.RequiredTopology != "multi-node-cluster" {
			return errors.New("reproduction task contract is invalid")
		}
		for _, allowed := range task.AllowedPaths {
			if !strings.HasPrefix(allowed, "test/e2e/") {
				return errors.New("reproduction task permits a production path")
			}
		}
	} else {
		if task.Phase == PhaseDiagnose && task.ProductionChangesAllowed {
			return errors.New("diagnosis task cannot change repository files")
		}
		if task.Phase == PhaseDiagnose &&
			(task.RequiredTopology != "" || task.RequiredRuns != 0) {
			return errors.New("diagnosis task contains an execution proof contract")
		}
		if (task.Phase == PhaseFix || task.Phase == PhaseAddressReview) &&
			(!task.ProductionChangesAllowed || task.RequiredRuns != 3 ||
				task.RequiredTopology != "single-node-cluster" &&
					task.RequiredTopology != "three-node-cluster" &&
					task.RequiredTopology != "multi-node-cluster") {
			return errors.New("remediation task lacks an exact fixed-E2E contract")
		}
	}
	if task.Provider != ProviderCodex && task.Provider != ProviderDeepSeek {
		return fmt.Errorf("unsupported model provider %q", task.Provider)
	}
	if strings.TrimSpace(task.Model) == "" || len(task.Model) > maxTaskStringBytes {
		return errors.New("model identity is empty or oversized")
	}
	return nil
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhaseReproduce, PhaseDiagnose, PhaseFix, PhaseAddressReview:
		return true
	default:
		return false
	}
}

func validatePositiveSortedIDs(ids []int64) error {
	if len(ids) > maxTaskSliceItems || !slices.IsSorted(ids) {
		return errors.New("IDs must be bounded and sorted")
	}
	for index, id := range ids {
		if id <= 0 || index > 0 && ids[index-1] == id {
			return errors.New("IDs must be positive and unique")
		}
	}
	return nil
}

func validateInstructionDigests(digests []FileDigest) error {
	if len(digests) == 0 || len(digests) > maxTaskSliceItems {
		return errors.New("instruction digests are empty or oversized")
	}
	for index, digest := range digests {
		if err := validateRepositoryPath(digest.Path); err != nil {
			return fmt.Errorf("instruction digest: %w", err)
		}
		if !digestPattern.MatchString(digest.SHA256) {
			return fmt.Errorf("invalid instruction digest for %q", digest.Path)
		}
		if index > 0 && digest.Path <= digests[index-1].Path {
			return errors.New("instruction digests must be strictly sorted")
		}
	}
	return nil
}

func validateAllowedPaths(paths []string) error {
	if len(paths) == 0 || len(paths) > maxTaskSliceItems {
		return errors.New("allowed paths are empty or oversized")
	}
	for index, allowedPath := range paths {
		if err := validateRepositoryPath(allowedPath); err != nil {
			return fmt.Errorf("allowed path: %w", err)
		}
		if index > 0 && allowedPath <= paths[index-1] {
			return errors.New("allowed paths must be strictly sorted")
		}
	}
	return nil
}

func validateCommandRules(rules []CommandRule) error {
	if len(rules) == 0 || len(rules) > 32 {
		return errors.New("command rules are empty or oversized")
	}
	for _, rule := range rules {
		if !executablePattern.MatchString(rule.Executable) || forbiddenExecutable(rule.Executable) {
			return fmt.Errorf("unsafe executable %q", rule.Executable)
		}
		if rule.MaxArgs <= 0 || rule.MaxArgs > 32 || len(rule.ArgvPrefix) > rule.MaxArgs {
			return fmt.Errorf("invalid argument bound for %q", rule.Executable)
		}
		for _, argument := range rule.ArgvPrefix {
			if len(argument) == 0 || len(argument) > maxTaskStringBytes ||
				strings.ContainsRune(argument, '\x00') {
				return fmt.Errorf("unsafe argument prefix for %q", rule.Executable)
			}
		}
	}
	return nil
}

func forbiddenExecutable(executable string) bool {
	switch strings.ToLower(executable) {
	case "sh", "bash", "zsh", "fish", "curl", "wget", "nc", "ncat", "ssh", "scp":
		return true
	default:
		return false
	}
}
