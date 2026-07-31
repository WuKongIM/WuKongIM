package issueagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// IssueState is one durable v2 Issue Agent lifecycle state.
type IssueState string

const (
	IssueStateTriaging                IssueState = "triaging"
	IssueStateWaitingForInformation   IssueState = "waiting_for_information"
	IssueStateWaitingForAuthorization IssueState = "waiting_for_authorization"
	IssueStateEngineering             IssueState = "engineering"
	IssueStateDraft                   IssueState = "draft"
	IssueStateReviewing               IssueState = "reviewing"
	IssueStateReadyForReview          IssueState = "ready_for_review"
	IssueStateNeedsHuman              IssueState = "needs_human"
	IssueStateCompleted               IssueState = "completed"
	IssueStateCancelled               IssueState = "cancelled"
	IssueStateTakenOver               IssueState = "taken_over"
)

// IssueBudget records cumulative v2 resource consumption.
type IssueBudget struct {
	EngineerAttempts uint32 `json:"engineer_attempts"`
	ReviewIterations uint32 `json:"review_iterations"`
}

// TaskIdentity binds one exact ephemeral engineering task.
type TaskIdentity struct {
	ID           string   `json:"id"`
	Kind         TaskKind `json:"kind"`
	BaseSHA      string   `json:"base_sha"`
	AffectedSHA  string   `json:"affected_sha"`
	PolicyDigest string   `json:"policy_digest"`
	PromptDigest string   `json:"prompt_digest"`
}

// IssueWork binds the App-owned repair branch and optional pull request.
type IssueWork struct {
	Branch      string `json:"branch"`
	HeadSHA     string `json:"head_sha"`
	PullRequest int64  `json:"pull_request"`
	Draft       bool   `json:"draft"`
}

// IssueAgentState is the complete canonical v2 state stored on one state ref.
type IssueAgentState struct {
	SchemaVersion       int                  `json:"schema_version"`
	Repository          string               `json:"repository"`
	IssueNumber         int64                `json:"issue_number"`
	Sequence            uint64               `json:"sequence"`
	State               IssueState           `json:"state"`
	Reason              string               `json:"reason"`
	PreviousStateDigest string               `json:"previous_state_digest"`
	IssueSnapshotDigest string               `json:"issue_snapshot_digest"`
	SourceSHA           string               `json:"source_sha"`
	Task                *TaskIdentity        `json:"task"`
	Authorization       *AuthorizationRecord `json:"authorization"`
	Budget              IssueBudget          `json:"budget"`
	Work                *IssueWork           `json:"work"`
	StatusCommentID     int64                `json:"status_comment_id"`
	ContextDigest       string               `json:"context_digest"`
	CandidateDigest     string               `json:"candidate_digest"`
	EvidenceDigest      string               `json:"evidence_digest"`
	ReviewDigest        string               `json:"review_digest"`
	TakenOverBy         string               `json:"taken_over_by"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

// ValidateIssueAgentState rejects ambiguous state before it becomes durable.
func ValidateIssueAgentState(state IssueAgentState) error {
	if state.SchemaVersion != 2 {
		return errors.New("unsupported Issue Agent state schema version")
	}
	if !validRepository(state.Repository) || state.IssueNumber <= 0 ||
		state.Sequence == 0 {
		return errors.New("invalid Issue Agent state identity")
	}
	if !validIssueState(state.State) {
		return fmt.Errorf("invalid Issue Agent state %q", state.State)
	}
	if len(state.Reason) > 2048 || strings.ContainsRune(state.Reason, '\x00') {
		return errors.New("invalid Issue Agent state reason")
	}
	if state.Sequence == 1 {
		if state.PreviousStateDigest != "" {
			return errors.New("initial Issue Agent state names a predecessor")
		}
	} else if !digestPattern.MatchString(state.PreviousStateDigest) {
		return errors.New("successor Issue Agent state lacks a predecessor digest")
	}
	if !digestPattern.MatchString(state.IssueSnapshotDigest) ||
		!gitSHAPattern.MatchString(state.SourceSHA) {
		return errors.New("Issue Agent state source identity is invalid")
	}
	if state.UpdatedAt.IsZero() || state.UpdatedAt.Location() != time.UTC {
		return errors.New("Issue Agent state timestamp must use UTC")
	}
	if state.Authorization != nil {
		if err := validateAuthorizationRecordContract(
			*state.Authorization,
		); err != nil {
			return err
		}
	}
	if state.Task != nil {
		if err := validateV2TaskIdentity(*state.Task); err != nil {
			return err
		}
		if state.Authorization == nil {
			return errors.New("active Issue Agent task lacks authorization")
		}
	}
	if state.Work != nil {
		expectedBranch := fmt.Sprintf("agent/issue-%d", state.IssueNumber)
		if state.Work.Branch != expectedBranch ||
			!gitSHAPattern.MatchString(state.Work.HeadSHA) ||
			state.Work.PullRequest <= 0 {
			return errors.New("invalid Issue Agent work identity")
		}
	}
	for _, digest := range []string{
		state.ContextDigest,
		state.CandidateDigest,
		state.EvidenceDigest,
		state.ReviewDigest,
	} {
		if digest != "" && !digestPattern.MatchString(digest) {
			return errors.New("invalid optional Issue Agent digest")
		}
	}
	if state.StatusCommentID < 0 ||
		len(state.TakenOverBy) > 256 ||
		strings.ContainsRune(state.TakenOverBy, '\x00') {
		return errors.New("invalid Issue Agent projection identity")
	}
	if state.TakenOverBy != "" &&
		!validContextIdentity(state.TakenOverBy, 256) {
		return errors.New("invalid Issue Agent take-over identity")
	}
	if err := validateIssueStateLifecycle(state); err != nil {
		return err
	}
	return nil
}

func validateIssueStateLifecycle(state IssueAgentState) error {
	switch state.State {
	case IssueStateEngineering:
		if state.Task == nil || state.Task.Kind != TaskKindEngineer {
			return errors.New("engineering state lacks an Engineer task")
		}
	case IssueStateReviewing:
		if state.Task == nil || state.Task.Kind != TaskKindReview ||
			state.Work == nil ||
			!digestPattern.MatchString(state.ReviewDigest) {
			return errors.New("reviewing state lacks exact Agent work")
		}
	case IssueStateDraft:
		if state.Task != nil || state.Work == nil || !state.Work.Draft ||
			!digestPattern.MatchString(state.ContextDigest) ||
			!digestPattern.MatchString(state.CandidateDigest) ||
			!digestPattern.MatchString(state.EvidenceDigest) {
			return errors.New("Draft state lacks complete published work")
		}
	case IssueStateReadyForReview:
		if state.Task != nil || state.Work == nil || state.Work.Draft ||
			!digestPattern.MatchString(state.ContextDigest) ||
			!digestPattern.MatchString(state.CandidateDigest) ||
			!digestPattern.MatchString(state.EvidenceDigest) {
			return errors.New("ready state lacks complete published work")
		}
	case IssueStateCompleted:
		if state.Task != nil || state.Work == nil {
			return errors.New("completed state lacks merged Agent work")
		}
	case IssueStateTakenOver:
		if state.Task != nil || state.TakenOverBy == "" {
			return errors.New("taken-over state lacks its maintainer")
		}
	default:
		if state.Task != nil {
			return errors.New("inactive Issue Agent state contains a task")
		}
	}
	if state.State != IssueStateTakenOver && state.TakenOverBy != "" {
		return errors.New("non-taken-over state names a maintainer")
	}
	return nil
}

// CanonicalIssueAgentState returns the exact bytes stored in the state ref.
func CanonicalIssueAgentState(state IssueAgentState) ([]byte, error) {
	if err := ValidateIssueAgentState(state); err != nil {
		return nil, err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return nil, errors.New("encode Issue Agent state")
	}
	return body, nil
}

func validateAuthorizationRecordContract(
	authorization AuthorizationRecord,
) error {
	if !validContextIdentity(authorization.Actor, 256) ||
		!validContextIdentity(authorization.EventID, 512) {
		return errors.New("invalid state authorization identity")
	}
	switch authorization.Permission {
	case "write", "maintain", "admin", "review_agent":
	default:
		return errors.New("state authorization lacks write permission")
	}
	switch authorization.Command {
	case "", "/agent fix", "/agent retry", "/agent cancel", "/agent take-over":
		return nil
	default:
		return errors.New("state authorization command is invalid")
	}
}

// IssueAgentStateDigest identifies the canonical state content.
func IssueAgentStateDigest(state IssueAgentState) (string, error) {
	body, err := CanonicalIssueAgentState(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DecodeIssueAgentState decodes one bounded canonical state document.
func DecodeIssueAgentState(reader io.Reader, maxBytes int64) (IssueAgentState, error) {
	var state IssueAgentState
	if err := decodeStrictJSON(reader, maxBytes, &state); err != nil {
		return IssueAgentState{}, err
	}
	if err := ValidateIssueAgentState(state); err != nil {
		return IssueAgentState{}, err
	}
	return state, nil
}

func validIssueState(state IssueState) bool {
	switch state {
	case IssueStateTriaging,
		IssueStateWaitingForInformation,
		IssueStateWaitingForAuthorization,
		IssueStateEngineering,
		IssueStateDraft,
		IssueStateReviewing,
		IssueStateReadyForReview,
		IssueStateNeedsHuman,
		IssueStateCompleted,
		IssueStateCancelled,
		IssueStateTakenOver:
		return true
	default:
		return false
	}
}
