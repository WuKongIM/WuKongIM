package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

// NodeRPCHandler handles one cluster node RPC payload.
type NodeRPCHandler interface {
	// HandleRPC handles payload and returns a response payload.
	HandleRPC(context.Context, []byte) ([]byte, error)
}

// RouteAuthority identifies the current authority for one physical hash slot.
// It aliases the scalar-only routing representation so hot batch lookups can
// fill the Node-local AuthorityEpoch without copying another result slice.
type RouteAuthority = routing.AuthorityRoute

// RouteAuthorityResult is the aligned lightweight authority routing outcome
// for one batch key.
type RouteAuthorityResult = routing.AuthorityRouteResult

// RouteAuthorityEvent reports route authority changes.
type RouteAuthorityEvent struct {
	// Authorities lists changed physical hash-slot authorities.
	Authorities []RouteAuthority
}

// ProposeRequest submits one Slot metadata command through cluster routing.
type ProposeRequest struct {
	// Key is the routing key used when Target does not carry an explicit physical hash slot.
	Key string
	// Command is the encoded Slot state machine command payload.
	Command []byte
	// Target optionally carries an explicit physical hash slot and logical Slot Raft Group target.
	Target ProposeTarget
}

// ProposeTarget identifies an explicit physical hash slot and optional logical Slot Raft Group target.
type ProposeTarget struct {
	// HashSlot is the physical hash slot. It is valid only when HasHashSlot is true.
	HashSlot uint16
	// HasHashSlot distinguishes an explicit hash slot 0 from an omitted hash slot.
	HasHashSlot bool
	// SlotID is the logical Slot Raft Group ID. It is valid only when HasSlotID is true.
	SlotID uint32
	// HasSlotID indicates whether SlotID was explicitly provided by the caller.
	HasSlotID bool
}

// Route describes the current routing decision for one hash slot.
type Route struct {
	// HashSlot is the physical hash slot selected for the request.
	HashSlot uint16
	// SlotID is the logical Slot Raft Group that owns HashSlot.
	SlotID uint32
	// Leader is the best-known Slot Raft leader node ID. Zero means unknown.
	Leader uint64
	// LeaderTerm is the Slot Raft term observed for Leader.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch for SlotID.
	ConfigEpoch uint64
	// PreferredLeader is the desired data-plane leader from the latest control snapshot.
	PreferredLeader uint64
	// Peers are the desired Slot replica node IDs from the latest control snapshot.
	Peers []uint64
	// Revision is the control snapshot revision that produced this route.
	Revision uint64
	// AuthorityEpoch is a local observation sequence for diagnostics and compatibility. It is not a distributed fence.
	AuthorityEpoch uint64
}

// RouteKeyResult is the aligned routing outcome for one batch key.
type RouteKeyResult struct {
	// Route is populated when Err is nil.
	Route Route
	// Err records a key-specific routing failure.
	Err error
}

// Snapshot summarizes local cluster readiness and routing state.
type Snapshot struct {
	// NodeID is this node's stable cluster identity.
	NodeID uint64
	// ControllerLead is the best-known Controller leader node ID.
	ControllerLead uint64
	// StateRevision is the latest applied control snapshot revision.
	StateRevision uint64
	// RoutesReady reports whether a valid route table is installed.
	RoutesReady bool
	// SlotsReady reports whether local assigned Slots have been opened or bootstrapped.
	SlotsReady bool
	// ChannelsReady reports whether the Channel service is available.
	ChannelsReady bool
	// SlotCount is the number of logical Slot Raft Groups in the current control view.
	SlotCount uint32
	// HashSlotCount is the number of physical hash slots in the current route table.
	HashSlotCount uint16
	// LastTaskReconcileError records the latest background task reconcile error for diagnostics.
	LastTaskReconcileError string
}
