package reviewagent

import (
	"encoding/json"
	"errors"
	"io"
	"time"
)

// MaxReviewStateBytes is the single read/write/canonical state document bound.
const MaxReviewStateBytes = 256 << 10

// Phase is the durable state of one pull-request generation.
type Phase string

const (
	PhaseAwaitingReady   Phase = "awaiting_ready"
	PhaseQueued          Phase = "queued"
	PhaseReviewing       Phase = "reviewing"
	PhaseApproved        Phase = "approved"
	PhaseChangesRequired Phase = "changes_required"
	PhaseInconclusive    Phase = "inconclusive"
	PhaseCanceled        Phase = "canceled"
	PhaseSuperseded      Phase = "superseded"
	PhaseClosed          Phase = "closed"
)

// DecisionSource records which trusted path produced a terminal adjudication.
type DecisionSource string

const (
	DecisionSourceModel          DecisionSource = "model"
	DecisionSourceMergeConflict  DecisionSource = "merge_conflict"
	DecisionSourcePolicy         DecisionSource = "policy"
	DecisionSourceInfrastructure DecisionSource = "infrastructure"
)

// InteractionBudget bounds same-head model interactions.
type InteractionBudget struct {
	AutomaticReviewsUsed      uint32 `json:"automatic_reviews_used"`
	ReconsiderationsUsed      uint32 `json:"reconsiderations_used"`
	ExplanationsUsed          uint32 `json:"explanations_used"`
	InfrastructureRetriesUsed uint32 `json:"infrastructure_retries_used"`
	ResponseBytesUsed         uint64 `json:"response_bytes_used"`
}

// ReviewState is one canonical append-only per-PR state document.
type ReviewState struct {
	SchemaVersion       int                `json:"schema_version"`
	Generation          GenerationIdentity `json:"generation"`
	Sequence            uint64             `json:"sequence"`
	Phase               Phase              `json:"phase"`
	DecisionSource      DecisionSource     `json:"decision_source"`
	Reason              string             `json:"reason"`
	InteractionRequest  string             `json:"interaction_request"`
	PreviousStateDigest string             `json:"previous_state_digest"`
	EvidenceDigest      string             `json:"evidence_digest"`
	ResultDigest        string             `json:"result_digest"`
	ExplanationDigest   string             `json:"explanation_digest"`
	ExplanationReply    string             `json:"explanation_reply"`
	PriorFindings       []Finding          `json:"prior_findings"`
	Budget              InteractionBudget  `json:"budget"`
	StartedAt           time.Time          `json:"started_at"`
	SessionDeadlineAt   time.Time          `json:"session_deadline_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

// ValidateReviewState rejects malformed canonical state content. Transition
// legality belongs to the usecase layer.
func ValidateReviewState(state ReviewState) error {
	if state.SchemaVersion != 1 {
		return errors.New("unsupported Review state schema version")
	}
	if err := ValidateGenerationIdentity(state.Generation); err != nil {
		return err
	}
	if state.Sequence == 0 {
		return errors.New("invalid Review state sequence")
	}
	switch state.Phase {
	case PhaseAwaitingReady,
		PhaseQueued,
		PhaseReviewing,
		PhaseApproved,
		PhaseChangesRequired,
		PhaseInconclusive,
		PhaseCanceled,
		PhaseSuperseded,
		PhaseClosed:
	default:
		return errors.New("invalid Review state phase")
	}
	if !validText(state.Reason, 2048, true) {
		return errors.New("invalid Review state reason")
	}
	if !validText(state.InteractionRequest, 4096, false) {
		return errors.New("invalid Review state interaction request")
	}
	if state.InteractionRequest != "" &&
		state.Phase != PhaseApproved &&
		state.Phase != PhaseChangesRequired &&
		state.Phase != PhaseInconclusive {
		return errors.New(
			"non-decision Review state contains an interaction request",
		)
	}
	if state.Sequence == 1 {
		if state.PreviousStateDigest != "" {
			return errors.New("initial Review state names a predecessor")
		}
	} else if !validDigest(state.PreviousStateDigest) {
		return errors.New("successor Review state lacks a predecessor digest")
	}
	if state.EvidenceDigest != "" && !validDigest(state.EvidenceDigest) ||
		state.ResultDigest != "" && !validDigest(state.ResultDigest) ||
		state.ExplanationDigest != "" &&
			!validDigest(state.ExplanationDigest) {
		return errors.New("invalid Review state artifact digest")
	}
	if state.ExplanationDigest != "" &&
		state.Phase != PhaseApproved &&
		state.Phase != PhaseChangesRequired &&
		state.Phase != PhaseInconclusive {
		return errors.New(
			"non-decision Review state contains an explanation",
		)
	}
	if !validText(
		state.ExplanationReply,
		MaxExplanationReplyBytes,
		false,
	) || (state.ExplanationDigest == "") != (state.ExplanationReply == "") {
		return errors.New("invalid Review state explanation payload")
	}
	if len(state.PriorFindings) > MaxFindings {
		return errors.New("too many prior Review findings")
	}
	for _, finding := range state.PriorFindings {
		if err := validateFinding(finding); err != nil {
			return err
		}
	}
	findingsBody, err := json.Marshal(state.PriorFindings)
	if err != nil || len(findingsBody) > MaxPersistedFindingsBytes {
		return errors.New("prior Review findings exceed persisted byte budget")
	}
	switch state.Phase {
	case PhaseApproved:
		if state.DecisionSource != DecisionSourceModel ||
			!validDigest(state.EvidenceDigest) ||
			!validDigest(state.ResultDigest) {
			return errors.New("terminal Review state lacks evidence or result")
		}
	case PhaseChangesRequired:
		switch state.DecisionSource {
		case DecisionSourceModel:
			if !validDigest(state.EvidenceDigest) ||
				!validDigest(state.ResultDigest) {
				return errors.New(
					"model Review decision lacks evidence or result",
				)
			}
		case DecisionSourceMergeConflict:
			if state.EvidenceDigest != "" ||
				state.ResultDigest != "" {
				return errors.New(
					"merge-conflict decision contains model artifacts",
				)
			}
		default:
			return errors.New("invalid changes-required decision source")
		}
	case PhaseInconclusive:
		switch state.DecisionSource {
		case DecisionSourceModel:
			if !validDigest(state.EvidenceDigest) ||
				!validDigest(state.ResultDigest) {
				return errors.New(
					"model Review decision lacks evidence or result",
				)
			}
		case DecisionSourcePolicy, DecisionSourceInfrastructure:
			if state.EvidenceDigest != "" ||
				state.ResultDigest != "" {
				return errors.New(
					"deterministic decision contains model artifacts",
				)
			}
		default:
			return errors.New("invalid inconclusive decision source")
		}
	default:
		if state.DecisionSource != "" {
			return errors.New("non-decision Review state has a decision source")
		}
		if state.ResultDigest != "" {
			return errors.New("non-decision Review state contains a result")
		}
	}
	if state.StartedAt.IsZero() ||
		state.StartedAt.Location() != time.UTC ||
		!state.SessionDeadlineAt.IsZero() &&
			state.SessionDeadlineAt.Location() != time.UTC ||
		state.UpdatedAt.IsZero() ||
		state.UpdatedAt.Location() != time.UTC ||
		state.StartedAt.After(state.UpdatedAt) ||
		!state.SessionDeadlineAt.IsZero() &&
			!state.SessionDeadlineAt.After(state.StartedAt) {
		return errors.New("Review state timestamp must use UTC")
	}
	return nil
}

// CanonicalReviewState returns the exact bytes stored on a per-PR state ref.
func CanonicalReviewState(state ReviewState) ([]byte, error) {
	if err := ValidateReviewState(state); err != nil {
		return nil, err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return nil, errors.New("encode Review state")
	}
	if len(body) > MaxReviewStateBytes {
		return nil, errors.New("Review state exceeds canonical byte budget")
	}
	return body, nil
}

// DecodeReviewState strictly decodes one bounded state document.
func DecodeReviewState(reader io.Reader, maxBytes int64) (ReviewState, error) {
	var state ReviewState
	if err := decodeStrictJSON(reader, maxBytes, &state); err != nil {
		return ReviewState{}, err
	}
	if err := ValidateReviewState(state); err != nil {
		return ReviewState{}, err
	}
	return state, nil
}

// ReviewStateDigest identifies the canonical state content.
func ReviewStateDigest(state ReviewState) (string, error) {
	body, err := CanonicalReviewState(state)
	if err != nil {
		return "", err
	}
	return canonicalDigest(json.RawMessage(body), "digest Review state")
}
