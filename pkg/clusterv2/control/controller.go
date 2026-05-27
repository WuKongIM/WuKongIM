package control

import "context"

// Controller provides local access to clusterv2 control state.
type Controller interface {
	// Start starts the control adapter.
	Start(context.Context) error
	// Stop stops the control adapter.
	Stop(context.Context) error
	// LocalSnapshot returns the latest locally visible control snapshot.
	LocalSnapshot(context.Context) (Snapshot, error)
	// LeaderID returns the best-known Controller leader node ID.
	LeaderID() uint64
	// ReportNode reports low-frequency local node state.
	ReportNode(context.Context, NodeReport) error
	// ReportSlots reports low-frequency local Slot runtime state.
	ReportSlots(context.Context, SlotRuntimeReport) error
	// Watch returns snapshot update events.
	Watch() <-chan SnapshotEvent
}

// ProposeProbe verifies whether the local control runtime can commit a Controller proposal.
type ProposeProbe interface {
	// ProbePropose commits a non-mutating Controller proposal probe.
	ProbePropose(context.Context) error
}

// SnapshotEvent notifies consumers that a new immutable snapshot is available.
type SnapshotEvent struct {
	// Snapshot is the new control snapshot.
	Snapshot Snapshot
}

// NodeReport contains low-frequency node status reported to the control adapter.
type NodeReport struct {
	// NodeID is the reporting node ID.
	NodeID uint64
	// Addr is the reporting node cluster RPC address.
	Addr string
	// Status is the reporting node health state.
	Status NodeStatus
}

// SlotRuntimeReport contains local Slot runtime observations for one node.
type SlotRuntimeReport struct {
	// NodeID is the reporting node ID.
	NodeID uint64
	// Slots contains runtime observations for local Slots.
	Slots []SlotRuntimeView
}

// SlotRuntimeView describes one observed local Slot runtime state.
type SlotRuntimeView struct {
	// SlotID is the observed physical Slot ID.
	SlotID uint32
	// Leader is the best-known Slot leader node ID.
	Leader uint64
	// Peers are the observed Slot replica node IDs.
	Peers []uint64
}
