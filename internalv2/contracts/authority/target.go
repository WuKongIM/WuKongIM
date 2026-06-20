package authority

import "errors"

var (
	// ErrInvalidTarget reports a missing or unusable UID authority target.
	ErrInvalidTarget = errors.New("internalv2/contracts/authority: invalid target")
)

// Target fences work to one observed UID hash-slot authority.
type Target struct {
	// HashSlot is the logical UID hash slot selected for the request.
	HashSlot uint16
	// SlotID is the physical Slot that owns HashSlot.
	SlotID uint32
	// LeaderNodeID is the node that was authority leader when this target was resolved.
	LeaderNodeID uint64
	// LeaderTerm is the Slot Raft term observed for LeaderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch.
	ConfigEpoch uint64
	// RouteRevision is the clusterv2 route-table revision used to resolve this target.
	RouteRevision uint64
	// AuthorityEpoch is a local observation sequence for diagnostics and compatibility. It is not a distributed fence.
	AuthorityEpoch uint64
}

// Validate reports whether the target can fence foreground authority work.
func (t Target) Validate() error {
	if t.LeaderNodeID == 0 {
		return ErrInvalidTarget
	}
	return nil
}

// IsLocal reports whether nodeID is the target leader.
func (t Target) IsLocal(nodeID uint64) bool {
	return nodeID != 0 && t.LeaderNodeID == nodeID
}
