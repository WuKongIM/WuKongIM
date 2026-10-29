package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

type (
	// PresenceFence binds actual Slot authority, excluding diagnostic revisions.
	PresenceFence = state.PresenceFence
	// PresenceCapabilities proves a running peer implements the activated formats.
	PresenceCapabilities = state.PresenceCapabilities
	// PresenceOwner includes every required ingress node, including non-data nodes.
	PresenceOwner = state.PresenceOwner
	// PresenceOwnerSet is the committed, bounded owner obligation vector.
	PresenceOwnerSet = state.PresenceOwnerSet
	// PresenceGeneration is an immutable exact command apply receipt.
	PresenceGeneration = state.PresenceGeneration
	// PresenceState is the bounded committed control section.
	PresenceState = state.PresenceState
	// PresenceCapabilityProbe queries an exact peer; missing proof never downgrades.
	PresenceCapabilityProbe func(context.Context, uint64, string) (PresenceCapabilities, error)
)

const PresenceProtocolVersion = state.PresenceProtocolVersion

// PresenceControllerFormatVersion includes semantic membership high-water fencing.
const PresenceControllerFormatVersion = state.PresenceControllerFormatVersion

var (
	// ErrPresenceGenerationNotReady preserves unknown capability, membership or authority.
	ErrPresenceGenerationNotReady = errors.New("controller: presence generation not ready")
	// ErrPresenceGenerationConflict rejects a stale or incompatible CAS predecessor.
	ErrPresenceGenerationConflict = errors.New("controller: presence generation conflict")
)

// ClaimPresenceGenerationRequest asks for one exact, monotonically committed successor.
type ClaimPresenceGenerationRequest struct {
	Fence              PresenceFence `json:"fence"`
	OwnerSetEpoch      uint64        `json:"owner_set_epoch"`
	ServingBoot        uint64        `json:"serving_boot"`
	ExpectedGeneration uint64        `json:"expected_generation"`
}

// PresenceGenerationSnapshot returns the generation and owner set from one committed cut.
type PresenceGenerationSnapshot struct {
	// RosterRevision is the durable semantic-membership high water; it must not exceed M.
	RosterRevision uint64 `json:"roster_revision"`
	// Revision orders locally published committed cuts; it is not generation G.
	Revision   uint64             `json:"revision"`
	Generation PresenceGeneration `json:"generation"`
	OwnerSet   PresenceOwnerSet   `json:"owner_set"`
}

// EnsurePresenceOwnerSet activates new durable formats only after every voter
// and possible mirror proves support. It cannot silently discharge old owners.
func (r *Runtime) EnsurePresenceOwnerSet(ctx context.Context) (PresenceOwnerSet, error) {
	ctx, cancel := presenceOperationContext(ctx)
	defer cancel()
	st, err := r.presenceCommittedState(ctx)
	if err != nil {
		return PresenceOwnerSet{}, err
	}
	proofs, err := r.presenceCapabilities(ctx, st)
	if err != nil {
		return PresenceOwnerSet{}, err
	}
	// An unchanged activated M is a linearizable read of the quorum cut above,
	// not a mutation. Fresh capabilities must still match every required owner;
	// a roster high-water advance or boot change always goes through the CAS.
	if st.Presence != nil && st.PresenceRosterCurrent(st.Presence.OwnerSet.Owners) {
		owners, proofErr := st.PresenceOwnersFromCapabilities(proofs)
		if proofErr == nil && reflect.DeepEqual(owners, st.Presence.OwnerSet.Owners) {
			if err := ctx.Err(); err != nil {
				return PresenceOwnerSet{}, err
			}
			return st.Presence.OwnerSet.Clone(), nil
		}
	}
	result, err := r.raftService().ProposeResult(ctx, command.Command{
		Kind: command.KindEnsurePresenceOwnerSet, IssuedAt: r.cfg.Now().UTC(),
		ExpectedRevision: &st.Revision, PresenceBasis: st.PresenceAdmissionBasis(), PresenceCapabilities: proofs,
	})
	if err != nil {
		return PresenceOwnerSet{}, presenceProposalError(err)
	}
	if result.PresenceOwnerSet == nil {
		return PresenceOwnerSet{}, ErrPresenceGenerationNotReady
	}
	if err := r.publishFromState(ctx); err != nil {
		return PresenceOwnerSet{}, err
	}
	return result.PresenceOwnerSet.Clone(), nil
}

// ClaimPresenceGeneration returns its own committed apply receipt, never a
// racy read of a later claimant. This is a rare activation, not a SEND operation.
func (r *Runtime) ClaimPresenceGeneration(ctx context.Context, req ClaimPresenceGenerationRequest) (PresenceGeneration, error) {
	ctx, cancel := presenceOperationContext(ctx)
	defer cancel()
	st, err := r.presenceCommittedState(ctx)
	if err != nil {
		return PresenceGeneration{}, err
	}
	if st.Presence == nil || st.Presence.OwnerSet.Epoch != req.OwnerSetEpoch {
		return PresenceGeneration{}, ErrPresenceGenerationNotReady
	}
	proofs, err := r.presenceCapabilities(ctx, st)
	if err != nil {
		return PresenceGeneration{}, err
	}
	result, err := r.raftService().ProposeResult(ctx, command.Command{
		Kind: command.KindClaimPresenceGeneration, IssuedAt: r.cfg.Now().UTC(),
		ExpectedRevision: &st.Revision, PresenceBasis: st.PresenceAdmissionBasis(), PresenceCapabilities: proofs,
		PresenceClaim: &command.ClaimPresenceGeneration{Fence: req.Fence, OwnerSetEpoch: req.OwnerSetEpoch, ServingBoot: req.ServingBoot, ExpectedGeneration: req.ExpectedGeneration},
	})
	if err != nil {
		return PresenceGeneration{}, presenceProposalError(err)
	}
	if result.PresenceGeneration == nil {
		return PresenceGeneration{}, ErrPresenceGenerationNotReady
	}
	if err := r.publishFromState(ctx); err != nil {
		return PresenceGeneration{}, err
	}
	return *result.PresenceGeneration, nil
}

// GetPresenceGeneration reads one committed cut. A zero generation is not an
// offline proof; it means the Hash Slot has no committed activation yet.
func (r *Runtime) GetPresenceGeneration(ctx context.Context, hashSlot uint16) (PresenceGenerationSnapshot, error) {
	ctx, cancel := presenceOperationContext(ctx)
	defer cancel()
	st, err := r.presenceCommittedState(ctx)
	if err != nil {
		return PresenceGenerationSnapshot{}, err
	}
	if st.Presence == nil || hashSlot >= st.Config.HashSlotCount || !st.PresenceRosterCurrent(st.Presence.OwnerSet.Owners) {
		return PresenceGenerationSnapshot{}, ErrPresenceGenerationNotReady
	}
	out := PresenceGenerationSnapshot{RosterRevision: st.Presence.RosterRevision, Revision: st.Revision, OwnerSet: st.Presence.OwnerSet.Clone()}
	for _, generation := range st.Presence.Generations {
		if generation.Fence.HashSlot == hashSlot {
			out.Generation = generation
			break
		}
	}
	return out, nil
}

// A quorum ReadIndex plus successful durable apply supplies a fresh committed
// cut without an empty log write. It is confined to rare control operations.
func (r *Runtime) presenceCommittedState(ctx context.Context) (ClusterState, error) {
	service := r.raftService()
	if service == nil {
		return ClusterState{}, ErrNotStarted
	}
	if _, err := service.ReadBarrier(ctx); err != nil {
		return ClusterState{}, err
	}
	r.mu.RLock()
	sm := r.sm
	r.mu.RUnlock()
	if sm == nil {
		return ClusterState{}, ErrNotStarted
	}
	st := sm.Snapshot(ctx)
	if st.Revision == 0 {
		return ClusterState{}, ErrPresenceGenerationNotReady
	}
	return st, nil
}

func (r *Runtime) presenceCapabilities(ctx context.Context, st ClusterState) ([]PresenceCapabilities, error) {
	if r.cfg.PresenceCapabilityProbe == nil {
		return nil, ErrPresenceGenerationNotReady
	}
	participants, err := st.PresenceParticipants()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPresenceGenerationNotReady, err)
	}
	proofs := make([]PresenceCapabilities, 0, len(participants))
	for _, node := range participants {
		proof, err := r.cfg.PresenceCapabilityProbe(ctx, node.NodeID, node.Addr)
		if err != nil {
			return nil, errors.Join(ErrPresenceGenerationNotReady, fmt.Errorf("capability node %d: %w", node.NodeID, err))
		}
		if proof.NodeID != node.NodeID {
			return nil, fmt.Errorf("%w: capability node identity %d", ErrPresenceGenerationNotReady, node.NodeID)
		}
		proofs = append(proofs, proof)
	}
	if _, err := st.PresenceOwnersFromCapabilities(proofs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPresenceGenerationNotReady, err)
	}
	return proofs, nil
}

func presenceOperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 5*time.Second)
}

func presenceProposalError(err error) error {
	var rejection controllerraft.ProposalRejectedError
	if errors.As(err, &rejection) {
		if rejection.Reason == fsm.ReasonPresenceGenerationConflict {
			return fmt.Errorf("%w: %w", ErrPresenceGenerationConflict, err)
		}
		return fmt.Errorf("%w: %w", ErrPresenceGenerationNotReady, err)
	}
	return err
}
