package issueagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	checkpointSchemaVersion = 1
	maxIdentityBytes        = 256
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	gitSHAPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// State is one durable Issue Agent lifecycle state.
type State string

const (
	StateAwaitingTriage State = "awaiting_triage"
	StateNeedsInfo      State = "needs_info"
	StateAuthorized     State = "authorized"
	StateVersionPinned  State = "version_pinned"
	StateReproducing    State = "reproducing"
	StateAlreadyFixed   State = "already_fixed"
	StateReproduced     State = "reproduced"
	StateDraftPROpen    State = "draft_pr_open"
	StateDiagnosing     State = "diagnosing"
	StateDiagnosed      State = "diagnosed"
	StateFixing         State = "fixing"
	StateValidating     State = "validating"
	StateReadyForReview State = "ready_for_review"
	StateReadyForHuman  State = "ready_for_human"
	StateMerged         State = "merged"
	StateCancelled      State = "cancelled"
	StateSuperseded     State = "superseded"
	StateWontFix        State = "wontfix"
)

// Action is one closed next-action value selected by the trusted planner.
type Action string

const (
	ActionNone           Action = "none"
	ActionPinVersions    Action = "pin_versions"
	ActionReproduce      Action = "reproduce"
	ActionOpenDraftPR    Action = "open_draft_pr"
	ActionDiagnose       Action = "diagnose"
	ActionImplementFix   Action = "implement_fix"
	ActionValidate       Action = "validate"
	ActionRequestReview  Action = "request_review"
	ActionWaitForHuman   Action = "wait_for_human"
	ActionReconcile      Action = "reconcile"
	ActionCreateBackport Action = "create_backport"
)

// FrozenInput binds the exact Issue facts accepted by a maintainer.
type FrozenInput struct {
	IssueBodySHA256    string  `json:"issue_body_sha256"`
	AffectedVersion    string  `json:"affected_version"`
	AcceptedCommentIDs []int64 `json:"accepted_comment_ids"`
	AuthorizationEvent string  `json:"authorization_event"`
	AuthorizedBy       string  `json:"authorized_by"`
}

// Versions separates the immutable diagnosis baseline from later integration.
type Versions struct {
	ReportedRef      string  `json:"reported_ref"`
	AffectedSHA      string  `json:"affected_sha"`
	DiagnosisBaseSHA string  `json:"diagnosis_base_sha"`
	IntegrationBase  *string `json:"integration_base_sha"`
}

// Budget is cumulative resource accounting stored in every checkpoint.
type Budget struct {
	ReproductionAttempts  uint32 `json:"reproduction_attempts"`
	RemediationAttempts   uint32 `json:"remediation_attempts"`
	CIRepairAttempts      uint32 `json:"ci_repair_attempts"`
	InfrastructureRetries uint32 `json:"infrastructure_attempts"`
	WorkerSeconds         uint64 `json:"worker_seconds"`
}

// Lease is populated only while one Worker operation owns the Issue.
type Lease struct {
	OperationID       string    `json:"operation_id"`
	Workflow          string    `json:"workflow"`
	DispatchRequestID string    `json:"dispatch_request_id"`
	Phase             Phase     `json:"phase"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	TaskSHA256        string    `json:"task_sha256"`
	ReservedSeconds   uint64    `json:"reserved_seconds"`
	Heavy             bool      `json:"heavy"`
}

// TestFile freezes one regression-test path and Git blob.
type TestFile struct {
	Path    string `json:"path"`
	BlobSHA string `json:"blob_sha"`
}

// ReproductionRun is one process-level black-box invocation.
type ReproductionRun struct {
	RunID           int64  `json:"run_id"`
	SourceSHA       string `json:"source_sha"`
	BinarySHA256    string `json:"binary_sha256"`
	CommandSHA256   string `json:"command_sha256"`
	AssertionSHA256 string `json:"assertion_sha256"`
	Outcome         string `json:"outcome"`
}

// Reproduction freezes the accepted fail-before contract and evidence.
type Reproduction struct {
	TestFiles         []TestFile        `json:"test_files"`
	Assertion         string            `json:"assertion"`
	AssertionSHA256   string            `json:"assertion_sha256"`
	Topology          string            `json:"topology"`
	AffectedRuns      []ReproductionRun `json:"affected_runs"`
	DiagnosisBaseRuns []ReproductionRun `json:"diagnosis_base_runs"`
	ArtifactRunID     int64             `json:"artifact_run_id"`
	ArtifactName      string            `json:"artifact_name"`
	ArtifactSHA256    string            `json:"artifact_sha256"`
}

// Work binds the deterministic Agent branch and optional Draft PR.
type Work struct {
	Branch   string `json:"branch"`
	HeadSHA  string `json:"head_sha"`
	PRNumber int64  `json:"pr_number"`
}

// Diagnosis records the required causal checkpoint before any production fix.
type Diagnosis struct {
	Summary            string   `json:"summary"`
	ViolatedInvariant  string   `json:"violated_invariant"`
	EvidenceSHA256     string   `json:"evidence_sha256"`
	IntendedPaths      []string `json:"intended_paths"`
	ClusterSemantics   string   `json:"cluster_semantics"`
	ValidationSuites   []string `json:"validation_suites"`
	RiskClasses        []string `json:"risk_classes"`
	AuthorizationEvent string   `json:"authorization_event,omitempty"`
}

// Validation binds local and remote validation to an exact candidate head.
type Validation struct {
	HeadSHA        string   `json:"head_sha"`
	TestMergeSHA   string   `json:"test_merge_sha"`
	GateGeneration uint64   `json:"gate_generation"`
	RequestRunID   int64    `json:"request_run_id"`
	EvidenceRunID  int64    `json:"evidence_run_id"`
	RequiredSuites []string `json:"required_suites"`
	LocalPasses    uint32   `json:"local_passes"`
	Conclusion     string   `json:"conclusion"`
}

// ModelAttempt records provider selection and auditable bounded usage.
type ModelAttempt struct {
	Provider            Provider `json:"provider"`
	Model               string   `json:"model"`
	AdapterVersion      string   `json:"adapter_version"`
	PromptPolicyVersion string   `json:"prompt_policy_version"`
	InputTokens         uint64   `json:"input_tokens"`
	OutputTokens        uint64   `json:"output_tokens"`
	ElapsedMilliseconds uint64   `json:"elapsed_milliseconds"`
	CostMicrounits      uint64   `json:"cost_microunits"`
	TerminalResult      string   `json:"terminal_result"`
	ModelChanged        bool     `json:"model_changed"`
}

// Checkpoint is the complete durable workflow snapshot stored on the Issue.
type Checkpoint struct {
	SchemaVersion                int           `json:"schema_version"`
	Repository                   string        `json:"repository"`
	IssueNumber                  int64         `json:"issue_number"`
	Generation                   uint64        `json:"generation"`
	Sequence                     uint64        `json:"sequence"`
	ExpectedPreviousCheckpointID *int64        `json:"expected_previous_checkpoint_id"`
	PreviousCheckpointSHA256     *string       `json:"previous_checkpoint_sha256"`
	State                        State         `json:"state"`
	FrozenInput                  FrozenInput   `json:"frozen_input"`
	Versions                     Versions      `json:"versions"`
	Lease                        *Lease        `json:"lease"`
	Reproduction                 *Reproduction `json:"reproduction"`
	Work                         *Work         `json:"work"`
	Diagnosis                    *Diagnosis    `json:"diagnosis"`
	Validation                   *Validation   `json:"validation"`
	Budget                       Budget        `json:"budget"`
	Model                        *ModelAttempt `json:"model"`
	NextAction                   Action        `json:"next_action"`
}

// ValidateCheckpoint rejects a checkpoint before it can be signed or trusted.
func ValidateCheckpoint(checkpoint Checkpoint) error {
	if checkpoint.SchemaVersion != checkpointSchemaVersion {
		return fmt.Errorf("unsupported checkpoint schema version %d", checkpoint.SchemaVersion)
	}
	if !validRepository(checkpoint.Repository) {
		return errors.New("invalid repository identity")
	}
	if checkpoint.IssueNumber <= 0 || checkpoint.Generation == 0 || checkpoint.Sequence == 0 {
		return errors.New("checkpoint identity numbers must be positive")
	}
	if checkpoint.Sequence == 1 {
		if checkpoint.ExpectedPreviousCheckpointID != nil || checkpoint.PreviousCheckpointSHA256 != nil {
			return errors.New("first checkpoint must not name a predecessor")
		}
	} else if checkpoint.ExpectedPreviousCheckpointID == nil ||
		checkpoint.PreviousCheckpointSHA256 == nil ||
		*checkpoint.ExpectedPreviousCheckpointID <= 0 ||
		!digestPattern.MatchString(*checkpoint.PreviousCheckpointSHA256) {
		return errors.New("non-first checkpoint requires a valid predecessor")
	}
	if !validState(checkpoint.State) {
		return fmt.Errorf("invalid checkpoint state %q", checkpoint.State)
	}
	if err := validateFrozenInput(checkpoint.FrozenInput); err != nil {
		return err
	}
	if err := validateVersions(checkpoint.Versions); err != nil {
		return err
	}
	if checkpoint.Lease != nil {
		if err := validateLease(*checkpoint.Lease); err != nil {
			return err
		}
	}
	if checkpoint.Reproduction != nil {
		if err := validateReproduction(*checkpoint.Reproduction); err != nil {
			return err
		}
	}
	if checkpoint.Work != nil {
		if err := validateWork(*checkpoint.Work, checkpoint.IssueNumber); err != nil {
			return err
		}
	}
	if checkpoint.Diagnosis != nil {
		if err := validateDiagnosis(*checkpoint.Diagnosis); err != nil {
			return err
		}
	}
	if checkpoint.Validation != nil {
		if err := validateValidation(*checkpoint.Validation); err != nil {
			return err
		}
	}
	if checkpoint.Model != nil {
		if err := validateModelAttempt(*checkpoint.Model); err != nil {
			return err
		}
	}
	if !validAction(checkpoint.NextAction) {
		return fmt.Errorf("invalid next action %q", checkpoint.NextAction)
	}
	return nil
}

// CanonicalCheckpoint returns the exact bytes covered by the Ed25519 signature.
func CanonicalCheckpoint(checkpoint Checkpoint) ([]byte, error) {
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(checkpoint); err != nil {
		return nil, fmt.Errorf("encode checkpoint: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func validRepository(repository string) bool {
	return len(repository) > 0 &&
		len(repository) <= maxIdentityBytes &&
		repositoryPattern.MatchString(repository) &&
		!strings.Contains(repository, "..")
}

func validateLease(lease Lease) error {
	if !digestPattern.MatchString(lease.OperationID) ||
		!digestPattern.MatchString(lease.TaskSHA256) ||
		strings.TrimSpace(lease.Workflow) == "" || len(lease.Workflow) > 256 ||
		strings.TrimSpace(lease.DispatchRequestID) == "" ||
		len(lease.DispatchRequestID) > 256 ||
		!validPhase(lease.Phase) ||
		lease.IssuedAt.IsZero() || !lease.ExpiresAt.After(lease.IssuedAt) ||
		lease.ExpiresAt.Sub(lease.IssuedAt) > 3*time.Hour ||
		lease.ReservedSeconds == 0 || lease.ReservedSeconds > 2*60*60 {
		return errors.New("invalid Worker lease")
	}
	return nil
}

func validateReproduction(reproduction Reproduction) error {
	if len(reproduction.TestFiles) == 0 || len(reproduction.TestFiles) > 32 ||
		len(reproduction.Assertion) == 0 || len(reproduction.Assertion) > 2048 ||
		!digestPattern.MatchString(reproduction.AssertionSHA256) ||
		len(reproduction.AffectedRuns) != 3 ||
		len(reproduction.DiagnosisBaseRuns) != 3 ||
		reproduction.ArtifactRunID <= 0 ||
		strings.TrimSpace(reproduction.ArtifactName) == "" ||
		len(reproduction.ArtifactName) > 256 ||
		!digestPattern.MatchString(reproduction.ArtifactSHA256) {
		return errors.New("invalid reproduction evidence")
	}
	switch reproduction.Topology {
	case "single-node-cluster", "three-node-cluster", "multi-node-cluster":
	default:
		return errors.New("invalid reproduction topology")
	}
	var previousPath string
	for _, file := range reproduction.TestFiles {
		if err := validateRepositoryPath(file.Path); err != nil ||
			!gitSHAPattern.MatchString(file.BlobSHA) ||
			previousPath != "" && file.Path <= previousPath {
			return errors.New("invalid or unsorted reproduction test file")
		}
		previousPath = file.Path
	}
	for _, runs := range [][]ReproductionRun{
		reproduction.AffectedRuns, reproduction.DiagnosisBaseRuns,
	} {
		for _, run := range runs {
			if run.RunID <= 0 || !gitSHAPattern.MatchString(run.SourceSHA) ||
				!digestPattern.MatchString(run.BinarySHA256) ||
				!digestPattern.MatchString(run.CommandSHA256) ||
				run.AssertionSHA256 != reproduction.AssertionSHA256 ||
				run.Outcome != "assertion_failed" && run.Outcome != "passed" {
				return errors.New("invalid reproduction run evidence")
			}
		}
	}
	return nil
}

func validateWork(work Work, issueNumber int64) error {
	if work.Branch != fmt.Sprintf("agent/issue-%d", issueNumber) ||
		!gitSHAPattern.MatchString(work.HeadSHA) || work.PRNumber < 0 {
		return errors.New("invalid Agent branch or pull request reference")
	}
	return nil
}

func validateDiagnosis(diagnosis Diagnosis) error {
	for _, statement := range []string{
		diagnosis.Summary,
		diagnosis.ViolatedInvariant,
		diagnosis.ClusterSemantics,
	} {
		if strings.TrimSpace(statement) == "" || len(statement) > 4096 {
			return errors.New("diagnosis statement is empty or oversized")
		}
	}
	if !digestPattern.MatchString(diagnosis.EvidenceSHA256) ||
		len(diagnosis.IntendedPaths) == 0 || len(diagnosis.IntendedPaths) > 64 ||
		len(diagnosis.ValidationSuites) == 0 ||
		len(diagnosis.ValidationSuites) > 32 ||
		len(diagnosis.RiskClasses) > 32 {
		return errors.New("diagnosis evidence or scope is invalid")
	}
	for index, intendedPath := range diagnosis.IntendedPaths {
		if err := validateRepositoryPath(intendedPath); err != nil ||
			index > 0 && intendedPath <= diagnosis.IntendedPaths[index-1] {
			return errors.New("diagnosis paths must be safe and strictly sorted")
		}
	}
	if !strictStrings(diagnosis.ValidationSuites) ||
		!strictStrings(diagnosis.RiskClasses) {
		return errors.New("diagnosis lists must be strictly sorted and unique")
	}
	if len(diagnosis.AuthorizationEvent) > 256 {
		return errors.New("diagnosis authorization event is oversized")
	}
	return nil
}

func validateValidation(validation Validation) error {
	if !gitSHAPattern.MatchString(validation.HeadSHA) ||
		!gitSHAPattern.MatchString(validation.TestMergeSHA) ||
		validation.GateGeneration == 0 ||
		validation.RequestRunID <= 0 || validation.EvidenceRunID <= 0 ||
		len(validation.RequiredSuites) < 2 ||
		len(validation.RequiredSuites) > 16 ||
		!strictStrings(validation.RequiredSuites) ||
		!slices.Contains(validation.RequiredSuites, "go-e2e") ||
		!slices.Contains(validation.RequiredSuites, "go-fast") ||
		validation.LocalPasses != 3 ||
		validation.Conclusion != "success" {
		return errors.New("invalid validation evidence")
	}
	return nil
}

func validateModelAttempt(model ModelAttempt) error {
	if model.Provider != ProviderCodex && model.Provider != ProviderDeepSeek ||
		strings.TrimSpace(model.Model) == "" || len(model.Model) > 256 ||
		strings.TrimSpace(model.AdapterVersion) == "" ||
		len(model.AdapterVersion) > 64 ||
		strings.TrimSpace(model.PromptPolicyVersion) == "" ||
		len(model.PromptPolicyVersion) > 64 ||
		model.ElapsedMilliseconds == 0 ||
		strings.TrimSpace(model.TerminalResult) == "" ||
		len(model.TerminalResult) > 256 {
		return errors.New("invalid model attempt")
	}
	return nil
}

func strictStrings(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > 256 ||
			index > 0 && value == values[index-1] {
			return false
		}
	}
	return true
}

func validateFrozenInput(input FrozenInput) error {
	if !digestPattern.MatchString(input.IssueBodySHA256) {
		return errors.New("invalid frozen Issue body digest")
	}
	if input.AffectedVersion == "" ||
		len(input.AffectedVersion) > maxIdentityBytes ||
		strings.EqualFold(strings.TrimSpace(input.AffectedVersion), "latest") {
		return errors.New("invalid affected version")
	}
	if input.AuthorizationEvent == "" || len(input.AuthorizationEvent) > maxIdentityBytes {
		return errors.New("invalid authorization event")
	}
	if input.AuthorizedBy == "" || len(input.AuthorizedBy) > maxIdentityBytes {
		return errors.New("invalid authorizing actor")
	}
	if !slices.IsSorted(input.AcceptedCommentIDs) {
		return errors.New("accepted comment IDs must be sorted")
	}
	for index, id := range input.AcceptedCommentIDs {
		if id <= 0 || index > 0 && id == input.AcceptedCommentIDs[index-1] {
			return errors.New("accepted comment IDs must be positive and unique")
		}
	}
	return nil
}

func validateVersions(versions Versions) error {
	if versions.ReportedRef == "" ||
		len(versions.ReportedRef) > maxIdentityBytes ||
		strings.EqualFold(strings.TrimSpace(versions.ReportedRef), "latest") {
		return errors.New("invalid reported ref")
	}
	if versions.AffectedSHA != "" && !gitSHAPattern.MatchString(versions.AffectedSHA) {
		return errors.New("invalid affected SHA")
	}
	if !gitSHAPattern.MatchString(versions.DiagnosisBaseSHA) {
		return errors.New("invalid diagnosis-base SHA")
	}
	if versions.IntegrationBase != nil && !gitSHAPattern.MatchString(*versions.IntegrationBase) {
		return errors.New("invalid integration-base SHA")
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StateAwaitingTriage, StateNeedsInfo, StateAuthorized, StateVersionPinned,
		StateReproducing, StateAlreadyFixed, StateReproduced, StateDraftPROpen,
		StateDiagnosing, StateDiagnosed, StateFixing, StateValidating,
		StateReadyForReview, StateReadyForHuman, StateMerged, StateCancelled,
		StateSuperseded, StateWontFix:
		return true
	default:
		return false
	}
}

func validAction(action Action) bool {
	switch action {
	case ActionNone, ActionPinVersions, ActionReproduce, ActionOpenDraftPR,
		ActionDiagnose, ActionImplementFix, ActionValidate, ActionRequestReview,
		ActionWaitForHuman, ActionReconcile, ActionCreateBackport:
		return true
	default:
		return false
	}
}
