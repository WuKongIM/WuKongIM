package fsm

import (
	"fmt"
	"reflect"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

const (
	// ReasonPresenceNotReady retains unknown capability, membership or authority evidence.
	ReasonPresenceNotReady = "presence_not_ready"
	// ReasonPresenceGenerationConflict rejects an obsolete or incompatible predecessor.
	ReasonPresenceGenerationConflict = "presence_generation_conflict"
	// ReasonPresenceOwnerObligation rejects removal without a separately proven drain.
	ReasonPresenceOwnerObligation = "presence_owner_obligation"
)

func (sm *StateMachine) applyEnsurePresenceOwnerSet(next *state.ClusterState, index uint64, cmd command.Command) ApplyResult {
	owners, err := next.PresenceOwnersFromCapabilities(cmd.PresenceCapabilities)
	if err != nil || next.Revision == 0 || cmd.ExpectedRevision == nil {
		return reject(ReasonPresenceNotReady)
	}
	if next.Presence != nil {
		for _, previous := range next.Presence.OwnerSet.Owners {
			found := false
			for _, current := range owners {
				if current.NodeID == previous.NodeID {
					found = true
					break
				}
			}
			if !found {
				return reject(ReasonPresenceOwnerObligation)
			}
		}
		if reflect.DeepEqual(next.Presence.OwnerSet.Owners, owners) {
			set := next.Presence.OwnerSet.Clone()
			result := noop(ReasonNoChange)
			result.PresenceOwnerSet = &set
			return result
		}
	}
	before := next.Clone()
	if next.Presence == nil {
		next.Presence = &state.PresenceState{}
	}
	next.Presence.OwnerSet = state.PresenceOwnerSet{Epoch: index, Owners: owners}
	result := validateChanged(next, before, cmd)
	if !result.Rejected {
		set := next.Presence.OwnerSet.Clone()
		result.PresenceOwnerSet = &set
	}
	return result
}

func (sm *StateMachine) applyClaimPresenceGeneration(next *state.ClusterState, index uint64, cmd command.Command) ApplyResult {
	claim := cmd.PresenceClaim
	if claim == nil || next.Presence == nil || cmd.ExpectedRevision == nil || claim.OwnerSetEpoch == 0 || claim.OwnerSetEpoch != next.Presence.OwnerSet.Epoch || claim.ServingBoot == 0 {
		return reject(ReasonPresenceNotReady)
	}
	owners, err := next.PresenceOwnersFromCapabilities(cmd.PresenceCapabilities)
	if err != nil || !reflect.DeepEqual(owners, next.Presence.OwnerSet.Owners) || !presenceFenceMapped(*next, claim.Fence) {
		return reject(ReasonPresenceNotReady)
	}
	serving := false
	for _, owner := range owners {
		if owner.NodeID == claim.Fence.LeaderNodeID && owner.CapabilityGeneration == claim.ServingBoot {
			serving = true
			break
		}
	}
	if !serving {
		return reject(ReasonPresenceNotReady)
	}
	position := -1
	var previous state.PresenceGeneration
	for i, generation := range next.Presence.Generations {
		if generation.Fence.HashSlot == claim.Fence.HashSlot {
			position, previous = i, generation
			break
		}
	}
	if previous.Generation != claim.ExpectedGeneration {
		if previous.Generation != 0 && previous.PreviousGeneration == claim.ExpectedGeneration && previous.Fence == claim.Fence && previous.OwnerSetEpoch == claim.OwnerSetEpoch && previous.ServingBoot == claim.ServingBoot {
			result := noop(ReasonNoChange)
			result.PresenceGeneration = &previous
			return result
		}
		return reject(ReasonPresenceGenerationConflict)
	}
	if index <= previous.Generation {
		return reject(ReasonPresenceGenerationConflict)
	}
	before := next.Clone()
	generation := state.PresenceGeneration{Fence: claim.Fence, OwnerSetEpoch: claim.OwnerSetEpoch, ServingBoot: claim.ServingBoot, Generation: index, PreviousGeneration: claim.ExpectedGeneration, AppliedRaftIndex: index}
	if position < 0 {
		next.Presence.Generations = append(next.Presence.Generations, generation)
	} else {
		next.Presence.Generations[position] = generation
	}
	next.Normalize()
	result := validateChanged(next, before, cmd)
	if !result.Rejected {
		result.PresenceGeneration = &generation
	}
	return result
}

// The Controller can prove mapping and desired membership, not actual Slot
// leadership. The Node seam must separately validate the live leader/term.
func presenceFenceMapped(st state.ClusterState, fence state.PresenceFence) bool {
	if fence.HashSlot >= st.Config.HashSlotCount || fence.SlotID == 0 || fence.LeaderNodeID == 0 || fence.LeaderTerm == 0 || fence.ConfigEpoch == 0 {
		return false
	}
	mapped := false
	for _, interval := range st.HashSlots.Ranges {
		if fence.HashSlot >= interval.From && fence.HashSlot <= interval.To {
			mapped = interval.SlotID == fence.SlotID
			break
		}
	}
	if !mapped {
		return false
	}
	for _, assignment := range st.Slots {
		if assignment.SlotID != fence.SlotID || assignment.ConfigEpoch != fence.ConfigEpoch {
			continue
		}
		for _, peer := range assignment.DesiredPeers {
			if peer == fence.LeaderNodeID {
				return true
			}
		}
	}
	return false
}

// Snapshot replacement may advance but never erase an already observed G/M.
// An empty new process relies on the existing authoritative Raft WAL recovery.
func preservePresenceHighWater(current, next state.ClusterState) error {
	if current.Presence == nil {
		return nil
	}
	if next.Presence == nil || next.ClusterID != current.ClusterID || next.Presence.OwnerSet.Epoch < current.Presence.OwnerSet.Epoch {
		return fmt.Errorf("controller: presence snapshot high-water rollback")
	}
	if next.Presence.OwnerSet.Epoch == current.Presence.OwnerSet.Epoch && !reflect.DeepEqual(next.Presence.OwnerSet, current.Presence.OwnerSet) {
		return fmt.Errorf("controller: presence owner set changed at equal epoch")
	}
	for _, previous := range current.Presence.Generations {
		found := false
		for _, candidate := range next.Presence.Generations {
			if candidate.Fence.HashSlot != previous.Fence.HashSlot {
				continue
			}
			found = candidate.Generation > previous.Generation || candidate == previous
			break
		}
		if !found {
			return fmt.Errorf("controller: presence generation snapshot rollback")
		}
	}
	return nil
}

// Existing-state member/health transitions need no fresh schema activation.
// A new endpoint must prove support before receiving the activated state format.
func presenceNodeAdmission(st state.ClusterState, node state.Node, proof *state.PresenceCapabilities) bool {
	if st.Presence == nil || node.JoinState == state.NodeJoinStateRemoved {
		return true
	}
	for _, old := range st.Nodes {
		if old.NodeID == node.NodeID && old.Addr == node.Addr && old.JoinState != state.NodeJoinStateRemoved {
			return true
		}
	}
	return proof != nil && proof.NodeID == node.NodeID && proof.BootID != 0 && proof.ControllerStateVersion == 1 && proof.ControllerCommandVersion == 1 && proof.PresenceProtocolVersion == state.PresenceProtocolVersion
}
