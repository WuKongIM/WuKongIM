package reviewagent

import (
	"encoding/json"
	"errors"
	"io"
	"time"
)

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

// InteractionBudget bounds same-head model interactions.
type InteractionBudget struct {
	ReconsiderationsUsed uint32 `json:"reconsiderations_used"`
	ExplanationsUsed     uint32 `json:"explanations_used"`
	ResponseBytesUsed    uint64 `json:"response_bytes_used"`
}

// ProjectionIdentity records repairable GitHub views, never authority.
type ProjectionIdentity struct {
	StatusCommentID int64 `json:"status_comment_id"`
	ReviewID        int64 `json:"review_id"`
	CheckRunID      int64 `json:"check_run_id"`
}

// ReviewState is one canonical append-only per-PR state document.
type ReviewState struct {
	SchemaVersion       int                `json:"schema_version"`
	Generation          GenerationIdentity `json:"generation"`
	Sequence            uint64             `json:"sequence"`
	Phase               Phase              `json:"phase"`
	Reason              string             `json:"reason"`
	PreviousStateDigest string             `json:"previous_state_digest"`
	EvidenceDigest      string             `json:"evidence_digest"`
	ResultDigest        string             `json:"result_digest"`
	Budget              InteractionBudget  `json:"budget"`
	Projection          ProjectionIdentity `json:"projection"`
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
	if state.Sequence == 1 {
		if state.PreviousStateDigest != "" {
			return errors.New("initial Review state names a predecessor")
		}
	} else if !validDigest(state.PreviousStateDigest) {
		return errors.New("successor Review state lacks a predecessor digest")
	}
	if state.EvidenceDigest != "" && !validDigest(state.EvidenceDigest) ||
		state.ResultDigest != "" && !validDigest(state.ResultDigest) {
		return errors.New("invalid Review state artifact digest")
	}
	switch state.Phase {
	case PhaseApproved, PhaseChangesRequired, PhaseInconclusive:
		if !validDigest(state.EvidenceDigest) ||
			!validDigest(state.ResultDigest) {
			return errors.New("terminal Review state lacks evidence or result")
		}
	default:
		if state.ResultDigest != "" {
			return errors.New("non-decision Review state contains a result")
		}
	}
	if state.Projection.StatusCommentID < 0 ||
		state.Projection.ReviewID < 0 ||
		state.Projection.CheckRunID < 0 {
		return errors.New("invalid Review state projection identity")
	}
	if state.UpdatedAt.IsZero() || state.UpdatedAt.Location() != time.UTC {
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
