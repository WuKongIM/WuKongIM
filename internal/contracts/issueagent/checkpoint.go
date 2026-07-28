package issueagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
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
type Lease struct{}

// Reproduction is populated after black-box reproduction is accepted.
type Reproduction struct{}

// Work is populated after an Agent branch or Draft PR exists.
type Work struct{}

// Diagnosis is populated after evidence supports one root cause.
type Diagnosis struct{}

// Validation is populated after local or remote validation evidence exists.
type Validation struct{}

// ModelAttempt records the selected provider attempt.
type ModelAttempt struct{}

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
		!*checkpoint.ExpectedPreviousCheckpointIDPositive() ||
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

func (checkpoint Checkpoint) ExpectedPreviousCheckpointIDPositive() *bool {
	positive := checkpoint.ExpectedPreviousCheckpointID != nil &&
		*checkpoint.ExpectedPreviousCheckpointID > 0
	return &positive
}

func validRepository(repository string) bool {
	return len(repository) > 0 &&
		len(repository) <= maxIdentityBytes &&
		repositoryPattern.MatchString(repository) &&
		!strings.Contains(repository, "..")
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
