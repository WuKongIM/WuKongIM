package control

import (
	"context"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
)

const controllerVoterCountEvenWarning = "controller_voter_count_even"

// PromoteControllerVoterRequest requests online promotion of an active node into Controller Raft voting membership.
type PromoteControllerVoterRequest struct {
	// NodeID is the target node.
	NodeID uint64 `json:"node_id"`
	// ExpectedRevision fences the manager intent to the observed control revision.
	ExpectedRevision uint64 `json:"expected_revision,omitempty"`
	// ExpectedVoters fences the write to the previously observed Controller voter set.
	ExpectedVoters []uint64 `json:"expected_voters"`
	// ObservedConfigIndex is the live Controller Raft config index proving the target voter is active.
	ObservedConfigIndex uint64 `json:"observed_config_index,omitempty"`
	// ObservedVoters is the live Controller Raft voter set observed after the target joined voting membership.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
}

// PromoteControllerVoterResult describes the control-state result of promotion.
type PromoteControllerVoterResult struct {
	// Changed reports whether durable ControllerV2 state changed.
	Changed bool `json:"changed"`
	// Node is the promoted node record.
	Node Node `json:"node"`
	// Revision is the resulting control-state revision.
	Revision uint64 `json:"revision"`
	// PreviousVoters is the voter set before promotion.
	PreviousVoters []uint64 `json:"previous_voters,omitempty"`
	// NextVoters is the voter set after promotion.
	NextVoters []uint64 `json:"next_voters,omitempty"`
	// Warnings contains bounded operator warnings.
	Warnings []string `json:"warnings,omitempty"`
}

// PrepareControllerVoter prepares the local ControllerV2 backend for Controller voter promotion.
func (r *Runtime) PrepareControllerVoter(ctx context.Context, req cv2.PrepareControllerVoterRequest) (cv2.PrepareControllerVoterResult, error) {
	if err := ctxErr(ctx); err != nil {
		return cv2.PrepareControllerVoterResult{}, err
	}
	if r == nil || r.backend == nil {
		return cv2.PrepareControllerVoterResult{}, cv2.ErrNotStarted
	}
	return r.backend.PrepareControllerVoter(ctx, req)
}

func promoteControllerVoterResultFromCV2(result cv2.PromoteControllerVoterResult) PromoteControllerVoterResult {
	return PromoteControllerVoterResult{
		Changed:        result.Changed,
		Node:           controlNodeFromControllerNode(result.Node),
		Revision:       result.Revision,
		PreviousVoters: append([]uint64(nil), result.PreviousVoters...),
		NextVoters:     append([]uint64(nil), result.NextVoters...),
		Warnings:       controllerVoterPromotionWarnings(result.NextVoters),
	}
}

func copyOptionalUint64Slice(in []uint64) []uint64 {
	if in == nil {
		return nil
	}
	out := make([]uint64, len(in))
	copy(out, in)
	return out
}

func controllerVoterPromotionWarnings(voters []uint64) []string {
	if len(voters) > 0 && len(voters)%2 == 0 {
		return []string{controllerVoterCountEvenWarning}
	}
	return nil
}
