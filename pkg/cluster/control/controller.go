package control

import (
	"context"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// Controller provides local access to cluster control state.
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
	// CompleteTask submits a fenced global task completion result.
	CompleteTask(context.Context, TaskResult) error
	// FailTask submits a fenced global task failure result.
	FailTask(context.Context, TaskResult) error
	// ReportTaskProgress submits one participant's fenced progress report.
	ReportTaskProgress(context.Context, TaskProgress) error
	// AdvanceSlotReplicaMovePhase submits a fenced Slot replica move phase update.
	AdvanceSlotReplicaMovePhase(context.Context, SlotReplicaMovePhaseAdvance) error
	// CommitSlotReplicaMove submits the final fenced Slot replica move assignment commit.
	CommitSlotReplicaMove(context.Context, SlotReplicaMoveCommit) error
	// RequestSlotLeaderTransfer submits a Controller-backed Slot leader transfer intent.
	RequestSlotLeaderTransfer(context.Context, SlotLeaderTransferRequest) (SlotLeaderTransferResult, error)
	// RequestSlotReplicaMove submits a Controller-backed staged Slot replica move intent.
	RequestSlotReplicaMove(context.Context, SlotReplicaMoveRequest) (SlotReplicaMoveResult, error)
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

type (
	// TaskResult describes the result of a data-plane reconcile task attempt.
	TaskResult = cv2.TaskResult
	// TaskProgress describes one participant's local progress for a barrier task.
	TaskProgress = cv2.TaskProgress
	// SlotReplicaMovePhaseAdvance records an observed Slot Raft config phase.
	SlotReplicaMovePhaseAdvance = cv2.SlotReplicaMovePhaseAdvance
	// SlotReplicaMoveCommit fences the final durable assignment replacement.
	SlotReplicaMoveCommit = cv2.SlotReplicaMoveCommit
)

// NodeReport contains low-frequency node status reported to the control adapter.
type NodeReport struct {
	// NodeID is the reporting node ID.
	NodeID uint64 `json:"node_id"`
	// Addr is the reporting node cluster RPC address.
	Addr string `json:"addr,omitempty"`
	// Status is the reporting node health state.
	Status NodeStatus `json:"status"`
	// RuntimeReady reports whether the node can serve foreground cluster traffic.
	RuntimeReady bool `json:"runtime_ready"`
	// ObservedControlRevision is the latest ControllerV2 revision observed by the node.
	ObservedControlRevision uint64 `json:"observed_control_revision"`
	// ObservedSlotRevision is the latest local Slot runtime revision observed by the node.
	ObservedSlotRevision uint64 `json:"observed_slot_revision,omitempty"`
	// ReportSeq is a node-local sequence used for diagnostics.
	ReportSeq uint64 `json:"report_seq"`
	// ErrorCode is a bounded machine-readable runtime reason.
	ErrorCode string `json:"error_code,omitempty"`
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
