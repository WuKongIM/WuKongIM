package issueagent

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ResultStatus distinguishes a completed proposal from a classified failure.
type ResultStatus string

const (
	ResultStatusSuccess ResultStatus = "success"
	ResultStatusFailed  ResultStatus = "failed"
)

// FailureClass prevents infrastructure or provider failures becoming diagnoses.
type FailureClass string

const (
	FailureNeedsInfo            FailureClass = "needs_info"
	FailureAlreadyFixed         FailureClass = "already_fixed"
	FailureProductAssertion     FailureClass = "product_assertion"
	FailureTestHarness          FailureClass = "test_harness"
	FailureWorkerInfrastructure FailureClass = "worker_infrastructure"
	FailureProvider             FailureClass = "provider"
	FailureUnsafeScope          FailureClass = "unsafe_scope"
	FailureStateConflict        FailureClass = "state_conflict"
	FailureBudgetExhausted      FailureClass = "budget_exhausted"
	FailureCancelled            FailureClass = "cancelled"
)

// Failure is one bounded terminal result for the current Worker attempt.
type Failure struct {
	Class   FailureClass `json:"class"`
	Summary string       `json:"summary"`
}

// CommandEvidence binds one broker-executed argv call to bounded output.
type CommandEvidence struct {
	Executable   string   `json:"executable"`
	Arguments    []string `json:"arguments"`
	WorkingDir   string   `json:"working_dir"`
	ExitCode     int      `json:"exit_code"`
	StdoutSHA256 string   `json:"stdout_sha256"`
	StderrSHA256 string   `json:"stderr_sha256"`
	DurationMS   int64    `json:"duration_ms"`
}

// EvidenceManifest is the redacted evidence index for one result Artifact.
type EvidenceManifest struct {
	ArtifactSHA256 string            `json:"artifact_sha256"`
	Commands       []CommandEvidence `json:"commands"`
}

// ModelUsage records auditable provider selection and bounded token use.
type ModelUsage struct {
	Provider     Provider `json:"provider"`
	Model        string   `json:"model"`
	InputTokens  uint64   `json:"input_tokens"`
	OutputTokens uint64   `json:"output_tokens"`
}

// AgentResult is an untrusted Worker proposal validated before publication.
type AgentResult struct {
	SchemaVersion   int              `json:"schema_version"`
	Repository      string           `json:"repository"`
	IssueNumber     int64            `json:"issue_number"`
	Generation      uint64           `json:"generation"`
	Sequence        uint64           `json:"sequence"`
	OperationID     string           `json:"operation_id"`
	Phase           Phase            `json:"phase"`
	Status          ResultStatus     `json:"status"`
	RequestedState  State            `json:"requested_state"`
	RequestedAction Action           `json:"requested_action"`
	ChangeSet       ChangeSet        `json:"change_set"`
	Evidence        EvidenceManifest `json:"evidence"`
	Usage           ModelUsage       `json:"usage"`
	Failure         *Failure         `json:"failure"`
}

// ValidateAgentResult binds a Worker proposal to the exact immutable task.
func ValidateAgentResult(result AgentResult, task TaskEnvelope) error {
	if err := ValidateTaskEnvelope(task); err != nil {
		return fmt.Errorf("invalid source task: %w", err)
	}
	if result.SchemaVersion != 1 ||
		result.Repository != task.Repository ||
		result.IssueNumber != task.IssueNumber ||
		result.Generation != task.Generation ||
		result.Sequence != task.Sequence ||
		result.OperationID != task.OperationID ||
		result.Phase != task.Phase {
		return errors.New("result identity does not match task")
	}
	if result.Status != ResultStatusSuccess && result.Status != ResultStatusFailed {
		return errors.New("invalid result status")
	}
	if !validState(result.RequestedState) || !validAction(result.RequestedAction) {
		return errors.New("invalid requested transition")
	}
	if result.Status == ResultStatusSuccess && result.Failure != nil {
		return errors.New("successful result must not contain a failure")
	}
	if result.Status == ResultStatusFailed {
		if result.Failure == nil || !validFailureClass(result.Failure.Class) ||
			strings.TrimSpace(result.Failure.Summary) == "" ||
			len(result.Failure.Summary) > 2048 {
			return errors.New("failed result requires a bounded classified failure")
		}
	}
	if err := ValidateChangeSet(result.ChangeSet, ChangeSetLimits{
		MaxFiles:      task.Limits.MaxFiles,
		MaxFileBytes:  int(task.Limits.MaxTotalBytes),
		MaxTotalBytes: int(task.Limits.MaxTotalBytes),
		MaxDeletions:  task.Limits.MaxFiles,
	}); err != nil {
		return err
	}
	for _, file := range result.ChangeSet.Files {
		if !pathWithinAny(file.Path, task.AllowedPaths) {
			return fmt.Errorf("file %q is outside task path policy", file.Path)
		}
	}
	if !digestPattern.MatchString(result.Evidence.ArtifactSHA256) ||
		len(result.Evidence.Commands) == 0 ||
		len(result.Evidence.Commands) > maxTaskSliceItems {
		return errors.New("invalid evidence manifest")
	}
	for _, command := range result.Evidence.Commands {
		if err := validateCommandEvidence(command, task.AllowedCommands); err != nil {
			return err
		}
	}
	if result.Usage.Provider != task.Provider ||
		result.Usage.Model != task.Model {
		return errors.New("result model identity does not match task")
	}
	return nil
}

func validateCommandEvidence(command CommandEvidence, rules []CommandRule) error {
	if command.DurationMS <= 0 ||
		!digestPattern.MatchString(command.StdoutSHA256) ||
		!digestPattern.MatchString(command.StderrSHA256) {
		return errors.New("invalid command evidence")
	}
	if command.WorkingDir != "." {
		if err := validateRepositoryPath(command.WorkingDir); err != nil {
			return fmt.Errorf("command working directory: %w", err)
		}
	}
	for _, argument := range command.Arguments {
		if len(argument) == 0 || len(argument) > maxTaskStringBytes ||
			strings.ContainsRune(argument, '\x00') {
			return errors.New("command evidence contains an unsafe argument")
		}
	}
	for _, rule := range rules {
		if command.Executable == rule.Executable &&
			len(command.Arguments) <= rule.MaxArgs &&
			len(command.Arguments) >= len(rule.ArgvPrefix) &&
			slices.Equal(command.Arguments[:len(rule.ArgvPrefix)], rule.ArgvPrefix) {
			return nil
		}
	}
	return fmt.Errorf("command %q is outside task policy", command.Executable)
}

func pathWithinAny(filePath string, allowedPaths []string) bool {
	for _, allowedPath := range allowedPaths {
		if filePath == allowedPath || strings.HasPrefix(filePath, allowedPath+"/") {
			return true
		}
	}
	return false
}

func validFailureClass(class FailureClass) bool {
	switch class {
	case FailureNeedsInfo, FailureAlreadyFixed, FailureProductAssertion,
		FailureTestHarness, FailureWorkerInfrastructure, FailureProvider,
		FailureUnsafeScope, FailureStateConflict, FailureBudgetExhausted,
		FailureCancelled:
		return true
	default:
		return false
	}
}
