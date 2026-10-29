package state

import (
	"reflect"
	"sort"
)

const (
	// MaxPresenceOwners bounds capability probes and durable owner obligations.
	MaxPresenceOwners = 256
	// PresenceProtocolVersion identifies the committed-generation protocol.
	PresenceProtocolVersion uint32 = 1
	// PresenceControllerFormatVersion proves semantic roster fencing support.
	PresenceControllerFormatVersion uint32 = 2
)

// PresenceFence is actual Slot authority; diagnostic route revisions are excluded.
type PresenceFence struct {
	// HashSlot is the logical partition; SlotID is its current physical Raft group.
	HashSlot uint16 `json:"hash_slot"`
	SlotID   uint32 `json:"slot_id"`
	// LeaderNodeID and LeaderTerm must be verified against actual Slot runtime.
	LeaderNodeID uint64 `json:"leader_node_id"`
	LeaderTerm   uint64 `json:"leader_term"`
	// ConfigEpoch binds the committed physical placement.
	ConfigEpoch uint64 `json:"config_epoch"`
}

// PresenceCapabilities is a response from the exact running peer, not a build label.
type PresenceCapabilities struct {
	NodeID uint64 `json:"node_id"`
	// BootID is an equality-only process identity, never distributed ordering.
	BootID                   uint64 `json:"boot_id"`
	ControllerStateVersion   uint32 `json:"controller_state_version"`
	ControllerCommandVersion uint32 `json:"controller_command_version"`
	PresenceProtocolVersion  uint32 `json:"presence_protocol_version"`
}

// PresenceOwner is one required ingress owner, including nodes without data replicas.
type PresenceOwner struct {
	NodeID    uint64        `json:"node_id"`
	Addr      string        `json:"addr"`
	JoinState NodeJoinState `json:"join_state"`
	// CapabilityGeneration binds the exact peer boot which answered the probe.
	CapabilityGeneration uint64 `json:"capability_generation"`
}

// PresenceOwnerSet retains obligations until a separately proven replacement.
type PresenceOwnerSet struct {
	// Epoch is the Raft index which committed this complete obligation vector.
	Epoch uint64 `json:"epoch"`
	// Owners is complete and sorted, independent of current health status.
	Owners []PresenceOwner `json:"owners"`
}

// Clone returns a caller-owned owner list.
func (s PresenceOwnerSet) Clone() PresenceOwnerSet {
	s.Owners = append([]PresenceOwner(nil), s.Owners...)
	return s
}

// PresenceGeneration is the exact immutable result of one committed claim.
type PresenceGeneration struct {
	// Fence excludes diagnostic route revisions and local authority counters.
	Fence PresenceFence `json:"fence"`
	// OwnerSetEpoch binds the exact required owner vector at activation.
	OwnerSetEpoch uint64 `json:"owner_set_epoch"`
	// ServingBoot is the equality-only authority process identity.
	ServingBoot uint64 `json:"serving_boot"`
	// Generation is this claim's committed Raft index, never a random nonce.
	Generation uint64 `json:"generation"`
	// PreviousGeneration is the accepted CAS predecessor.
	PreviousGeneration uint64 `json:"previous_generation"`
	// AppliedRaftIndex retains the original apply receipt even for an exact retry.
	AppliedRaftIndex uint64 `json:"applied_raft_index"`
}

// PresenceMember freezes semantic membership, retaining removed tombstones.
// Health, names and placement weights do not authorize a different endpoint.
type PresenceMember struct {
	NodeID    uint64        `json:"node_id"`
	Addr      string        `json:"addr"`
	JoinState NodeJoinState `json:"join_state"`
	Roles     []NodeRole    `json:"roles"`
}

// PresenceBasis binds capability responses to the committed cut that selected
// their endpoints. Before first activation its two high-water fields are zero.
type PresenceBasis struct {
	RosterRevision uint64            `json:"roster_revision"`
	OwnerSetEpoch  uint64            `json:"owner_set_epoch"`
	Members        []PresenceMember  `json:"members"`
	Controllers    []ControllerVoter `json:"controllers"`
}

// PresenceState holds bounded high-water marks, never UID or Session history.
type PresenceState struct {
	// RosterRevision is the last committed semantic membership change. It never
	// falls after activation; a value above M closes old generation admission.
	RosterRevision uint64           `json:"roster_revision,omitempty"`
	OwnerSet       PresenceOwnerSet `json:"owner_set"`
	// Generations retains at most one high-water mark per configured Hash Slot.
	Generations []PresenceGeneration `json:"generations"`
}

// Clone detaches the durable section from its caller.
func (s PresenceState) Clone() PresenceState {
	s.OwnerSet = s.OwnerSet.Clone()
	s.Generations = append([]PresenceGeneration(nil), s.Generations...)
	return s
}

// PresenceParticipants returns every current voter and potential state mirror.
// Joining nodes need schema support even before they own ingress obligations.
func (s ClusterState) PresenceParticipants() ([]Node, error) {
	byID := make(map[uint64]Node, len(s.Nodes))
	for _, node := range s.Nodes {
		if node.JoinState != NodeJoinStateRemoved {
			byID[node.NodeID] = node
		}
	}
	for _, voter := range s.Controllers {
		node, ok := byID[voter.NodeID]
		if !ok || node.Addr != voter.Addr {
			return nil, invalid("presence voter membership is unavailable")
		}
	}
	if len(byID) == 0 || len(byID) > MaxPresenceOwners {
		return nil, invalid("presence participant count is invalid")
	}
	out := make([]Node, 0, len(byID))
	for _, node := range byID {
		if node.NodeID == 0 || node.Addr == "" || len(node.Addr) > 1024 {
			return nil, invalid("presence participant identity is invalid")
		}
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

// PresenceOwnersFromCapabilities validates complete exact-member capability proof.
func (s ClusterState) PresenceOwnersFromCapabilities(proofs []PresenceCapabilities) ([]PresenceOwner, error) {
	participants, err := s.PresenceParticipants()
	if err != nil || len(proofs) != len(participants) {
		return nil, invalid("presence capability coverage is incomplete")
	}
	byID := make(map[uint64]PresenceCapabilities, len(proofs))
	for _, proof := range proofs {
		if proof.NodeID == 0 || proof.BootID == 0 || proof.ControllerStateVersion != PresenceControllerFormatVersion || proof.ControllerCommandVersion != PresenceControllerFormatVersion || proof.PresenceProtocolVersion != PresenceProtocolVersion {
			return nil, invalid("presence capability is unsupported")
		}
		if _, duplicate := byID[proof.NodeID]; duplicate {
			return nil, invalid("presence capability is duplicated")
		}
		byID[proof.NodeID] = proof
	}
	owners := make([]PresenceOwner, 0, len(participants))
	for _, node := range participants {
		proof, ok := byID[node.NodeID]
		if !ok {
			return nil, invalid("presence capability member is missing")
		}
		if node.JoinState != NodeJoinStateJoining {
			owners = append(owners, PresenceOwner{NodeID: node.NodeID, Addr: node.Addr, JoinState: node.JoinState, CapabilityGeneration: proof.BootID})
		}
	}
	if len(owners) == 0 {
		return nil, invalid("presence owner set is empty")
	}
	return owners, nil
}

// PresenceRosterCurrent checks durable membership without treating health as absence.
func (s ClusterState) PresenceRosterCurrent(owners []PresenceOwner) bool {
	if s.Presence == nil || s.Presence.RosterRevision == 0 || s.Presence.RosterRevision > s.Presence.OwnerSet.Epoch {
		return false
	}
	participants, err := s.PresenceParticipants()
	if err != nil {
		return false
	}
	i := 0
	for _, node := range participants {
		if node.JoinState == NodeJoinStateJoining {
			continue
		}
		if i >= len(owners) || owners[i].NodeID != node.NodeID || owners[i].Addr != node.Addr || owners[i].JoinState != node.JoinState {
			return false
		}
		i++
	}
	return i == len(owners)
}

func validatePresence(s ClusterState) error {
	if s.Presence == nil {
		return nil
	}
	p := s.Presence
	if p.OwnerSet.Epoch == 0 || len(p.OwnerSet.Owners) == 0 || len(p.OwnerSet.Owners) > MaxPresenceOwners || len(p.Generations) > int(s.Config.HashSlotCount) {
		return invalid("presence section exceeds its bounded shape")
	}
	var previous uint64
	for i, owner := range p.OwnerSet.Owners {
		if owner.NodeID == 0 || (i != 0 && owner.NodeID <= previous) || owner.Addr == "" || len(owner.Addr) > 1024 || owner.CapabilityGeneration == 0 || (owner.JoinState != NodeJoinStateActive && owner.JoinState != NodeJoinStateLeaving) {
			return invalid("presence owner obligation is invalid")
		}
		previous = owner.NodeID
	}
	var previousSlot uint16
	for i, generation := range p.Generations {
		f := generation.Fence
		if f.HashSlot >= s.Config.HashSlotCount || (i != 0 && f.HashSlot <= previousSlot) || f.SlotID == 0 || f.LeaderNodeID == 0 || f.LeaderTerm == 0 || f.ConfigEpoch == 0 || generation.OwnerSetEpoch == 0 || generation.ServingBoot == 0 || generation.Generation == 0 || generation.Generation != generation.AppliedRaftIndex || generation.PreviousGeneration >= generation.Generation {
			return invalid("presence generation is invalid")
		}
		previousSlot = f.HashSlot
	}
	return nil
}

// PresenceAdmissionBasis captures immutable endpoint-selection evidence. Removed
// members remain in this vector: a transient member cannot disappear at bootstrap.
func (s ClusterState) PresenceAdmissionBasis() *PresenceBasis {
	b := &PresenceBasis{Members: make([]PresenceMember, 0, len(s.Nodes)), Controllers: append([]ControllerVoter(nil), s.Controllers...)}
	for _, n := range s.Nodes {
		roles := append([]NodeRole(nil), n.Roles...)
		sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
		b.Members = append(b.Members, PresenceMember{NodeID: n.NodeID, Addr: n.Addr, JoinState: n.JoinState, Roles: roles})
	}
	sort.Slice(b.Members, func(i, j int) bool { return b.Members[i].NodeID < b.Members[j].NodeID })
	sort.Slice(b.Controllers, func(i, j int) bool { return b.Controllers[i].NodeID < b.Controllers[j].NodeID })
	if s.Presence != nil {
		b.RosterRevision = s.Presence.RosterRevision
		b.OwnerSetEpoch = s.Presence.OwnerSet.Epoch
	}
	return b
}

// PresenceRosterMatches compares the exact semantic member/voter vector, never
// health, placement load, names or unrelated cluster-state revisions.
func (s ClusterState) PresenceRosterMatches(b *PresenceBasis) bool {
	if b == nil {
		return false
	}
	current := s.PresenceAdmissionBasis()
	return reflect.DeepEqual(current.Members, b.Members) && reflect.DeepEqual(current.Controllers, b.Controllers)
}

// PresenceBasisCurrent rejects an old capability cut even after roster ABA or
// a competing boot update. A missing activated-format stamp never means fresh.
func (s ClusterState) PresenceBasisCurrent(b *PresenceBasis) bool {
	if !s.PresenceRosterMatches(b) {
		return false
	}
	if s.Presence == nil {
		return b.RosterRevision == 0 && b.OwnerSetEpoch == 0
	}
	return s.Presence.RosterRevision > 0 && b.RosterRevision == s.Presence.RosterRevision && b.OwnerSetEpoch == s.Presence.OwnerSet.Epoch
}
