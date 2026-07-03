package controller

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controller/sync"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	// CurrentSchemaVersion is the supported cluster-state.json schema version.
	CurrentSchemaVersion = state.CurrentSchemaVersion
	// CurrentHashSlotTableVersion is the supported hash-slot table schema version.
	CurrentHashSlotTableVersion = state.CurrentHashSlotTableVersion
)

type (
	// ClusterState is the strongly typed cluster-state.json document.
	ClusterState = state.ClusterState
	// ClusterConfig stores durable cluster sizing and placement defaults.
	ClusterConfig = state.ClusterConfig
	// ControllerVoter identifies a Controller Raft voting member.
	ControllerVoter = state.ControllerVoter
	// ControllerRole describes a Controller Raft membership role.
	ControllerRole = state.ControllerRole
	// Node is a durable cluster membership record.
	Node = state.Node
	// NodeRole describes a durable node capability.
	NodeRole = state.NodeRole
	// NodeJoinState describes whether a node is assignment-ready.
	NodeJoinState = state.NodeJoinState
	// NodeStatus describes durable node health.
	NodeStatus = state.NodeStatus
	// NodeHealthReport stores one bounded low-frequency runtime health report.
	NodeHealthReport = state.NodeHealthReport
	// SlotAssignment describes desired placement for one physical Slot.
	SlotAssignment = state.SlotAssignment
	// HashSlotTable maps hash slots to physical Slot IDs.
	HashSlotTable = state.HashSlotTable
	// HashSlotRange maps an inclusive hash-slot range to one physical Slot.
	HashSlotRange = state.HashSlotRange
	// ReconcileTask is an active durable task needed to converge data-plane state.
	ReconcileTask = state.ReconcileTask
	// TaskKind describes one reconcile workflow kind.
	TaskKind = state.TaskKind
	// TaskStep describes the current step inside a reconcile workflow.
	TaskStep = state.TaskStep
	// TaskStatus describes whether a durable reconcile task is actionable.
	TaskStatus = state.TaskStatus
	// TaskCompletionPolicy describes how participant progress becomes task completion.
	TaskCompletionPolicy = state.TaskCompletionPolicy
	// TaskParticipantStatus describes one node's local task progress.
	TaskParticipantStatus = state.TaskParticipantStatus
	// TaskParticipantProgress stores one participant's progress for a task attempt.
	TaskParticipantProgress = state.TaskParticipantProgress
	// TaskResult describes the result of a data-plane reconcile task attempt.
	TaskResult = command.TaskResult
	// TaskProgress describes one participant's local progress for a barrier task.
	TaskProgress = command.TaskProgress
	// CommandKind identifies a durable Controller command replicated through Controller Raft.
	CommandKind = command.Kind
	// TaskTransition describes one durable ControllerV2 task state edge.
	TaskTransition = fsm.TaskTransition
	// SlotReplicaMovePhaseAdvance records an observed Slot Raft config phase.
	SlotReplicaMovePhaseAdvance = command.SlotReplicaMovePhaseAdvance
	// SlotReplicaMoveCommit fences the final durable assignment replacement.
	SlotReplicaMoveCommit = command.SlotReplicaMoveCommit
)

const (
	// ControllerRoleVoter is a Controller Raft voting member.
	ControllerRoleVoter = state.ControllerRoleVoter
	// NodeRoleControllerVoter marks a node that participates in Controller Raft.
	NodeRoleControllerVoter = state.NodeRoleControllerVoter
	// NodeRoleData marks a node that can host physical Slot replicas.
	NodeRoleData = state.NodeRoleData
	// NodeJoinStateActive means the node is an active cluster member.
	NodeJoinStateActive = state.NodeJoinStateActive
	// NodeJoinStateJoining means the node is being introduced.
	NodeJoinStateJoining = state.NodeJoinStateJoining
	// NodeJoinStateLeaving means the node is being drained.
	NodeJoinStateLeaving = state.NodeJoinStateLeaving
	// NodeJoinStateRemoved means the node identity is retained as a tombstone.
	NodeJoinStateRemoved = state.NodeJoinStateRemoved
	// NodeStatusAlive means the node is considered available.
	NodeStatusAlive = state.NodeStatusAlive
	// NodeStatusSuspect means the node may be unavailable.
	NodeStatusSuspect = state.NodeStatusSuspect
	// NodeStatusDown means the node is considered unavailable.
	NodeStatusDown = state.NodeStatusDown
	// TaskKindBootstrap converges an initial physical Slot assignment.
	TaskKindBootstrap = state.TaskKindBootstrap
	// TaskKindLeaderTransfer records an operator-requested Slot Raft leadership transfer.
	TaskKindLeaderTransfer = state.TaskKindLeaderTransfer
	// TaskKindSlotReplicaMove moves one physical Slot voter from SourceNode to TargetNode.
	TaskKindSlotReplicaMove = state.TaskKindSlotReplicaMove
	// TaskStepCreateSlot creates or verifies the Slot replica group for an assignment.
	TaskStepCreateSlot = state.TaskStepCreateSlot
	// TaskStepTransferLeader asks Slot Raft to move leadership away from the observed source.
	TaskStepTransferLeader = state.TaskStepTransferLeader
	// TaskStepOpenLearner opens the target replica as a non-voting Slot learner.
	TaskStepOpenLearner = state.TaskStepOpenLearner
	// TaskStepAddLearner adds the target node to the Slot Raft learner set.
	TaskStepAddLearner = state.TaskStepAddLearner
	// TaskStepPromoteLearner promotes the target learner into the Slot Raft voter set.
	TaskStepPromoteLearner = state.TaskStepPromoteLearner
	// TaskStepRemoveVoter removes the source node from the Slot Raft voter set.
	TaskStepRemoveVoter = state.TaskStepRemoveVoter
	// TaskStepCommitAssignment commits the durable Slot assignment after Slot Raft converges.
	TaskStepCommitAssignment = state.TaskStepCommitAssignment
	// TaskStatusPending means the task is waiting for a worker.
	TaskStatusPending = state.TaskStatusPending
	// TaskStatusRunning means the task is actively being attempted.
	TaskStatusRunning = state.TaskStatusRunning
	// TaskStatusFailed means the task remains active after a failed attempt.
	TaskStatusFailed = state.TaskStatusFailed
	// TaskCompletionPolicySingleObserver means one eligible executor can complete the task.
	TaskCompletionPolicySingleObserver = state.TaskCompletionPolicySingleObserver
	// TaskCompletionPolicyAllTargetPeers requires every target peer to report done.
	TaskCompletionPolicyAllTargetPeers = state.TaskCompletionPolicyAllTargetPeers
	// TaskParticipantStatusPending means the participant has not completed its local work.
	TaskParticipantStatusPending = state.TaskParticipantStatusPending
	// TaskParticipantStatusDone means the participant completed its local work.
	TaskParticipantStatusDone = state.TaskParticipantStatusDone
	// TaskParticipantStatusFailed means the participant's latest local attempt failed.
	TaskParticipantStatusFailed = state.TaskParticipantStatusFailed
	// CommandKindReportTaskProgress records one participant's local task progress.
	CommandKindReportTaskProgress = command.KindReportTaskProgress
)

type (
	// RaftObserver receives low-cardinality local ControllerV2 Raft runtime metrics.
	RaftObserver = raft.Observer
	// ApplyStateObserver receives volatile Controller Raft commit/applied progress for monitoring.
	ApplyStateObserver = raft.ApplyStateObserver
	// TaskTransitionObserver receives ControllerV2 task edges after applied metadata is durable.
	TaskTransitionObserver = raft.TaskTransitionObserver
	// TaskTransitionObserverFunc adapts a function to TaskTransitionObserver.
	TaskTransitionObserverFunc = raft.TaskTransitionObserverFunc
	// RaftStatus is a goroutine-safe local ControllerV2 Raft status snapshot.
	RaftStatus = raft.Status
	// LogCompactionResult describes one local ControllerV2 Raft compaction attempt.
	LogCompactionResult = raft.LogCompactionResult
	// LogCompactionStatus describes the latest local ControllerV2 Raft compaction attempt.
	LogCompactionStatus = raft.LogCompactionStatus
	// LogEntriesOptions controls a node-local ControllerV2 Raft log entry page.
	LogEntriesOptions = raft.LogEntriesOptions
	// LogEntry is a read-only summary of one ControllerV2 Raft log entry.
	LogEntry = raft.LogEntry
	// LogEntries is one node-local page of ControllerV2 Raft log entries.
	LogEntries = raft.LogEntries
	// GetStateRequest asks a Controller leader for a full cluster-state snapshot.
	GetStateRequest = cv2sync.GetStateRequest
	// GetStateResponse carries a full state file payload or sync status.
	GetStateResponse = cv2sync.GetStateResponse
	// Endpoint serves ControllerV2 state sync requests.
	Endpoint = cv2sync.Endpoint
	// PeerPicker resolves Controller peer IDs to sync endpoints.
	PeerPicker = cv2sync.PeerPicker
	// SyncClient installs validated leader cluster-state files into a local store.
	SyncClient = cv2sync.Client
	// StateSyncServerConfig wires a full-file sync server to local Controller state.
	StateSyncServerConfig = cv2sync.ServerConfig
	// StateSyncServer serves full cluster-state files from a ready Controller leader.
	StateSyncServer = cv2sync.Server
)

// PromoteControllerVoterRequest finalizes one proven Controller voter promotion in ControllerV2 state.
type PromoteControllerVoterRequest struct {
	// NodeID is the target node promoted into Controller Raft voting membership.
	NodeID uint64
	// Addr is the target Controller address; empty uses the durable node address.
	Addr string
	// ExpectedRevision fences the state write to the caller's observed revision.
	ExpectedRevision uint64
	// ExpectedVoters fences the state write to the previous durable Controller voter set.
	ExpectedVoters []uint64
	// ObservedConfigIndex is the live Controller Raft config index proving the promotion.
	ObservedConfigIndex uint64
	// ObservedVoters is the live Controller Raft voter set observed after promotion.
	ObservedVoters []uint64
}

// PromoteControllerVoterResult describes the durable state after final promotion.
type PromoteControllerVoterResult struct {
	// Changed reports whether the promotion advanced ControllerV2 state.
	Changed bool
	// Node is the durable node record after final promotion.
	Node Node
	// Revision is the observed ControllerV2 state revision after the write.
	Revision uint64
	// PreviousVoters is the durable Controller voter set before the promotion request.
	PreviousVoters []uint64
	// NextVoters is the durable Controller voter set after final promotion.
	NextVoters []uint64
}

// PrepareControllerVoterRequest asks a mirror node to become ready for Controller Raft learner traffic.
type PrepareControllerVoterRequest struct {
	// NodeID is the local node identity that should prepare for Controller Raft.
	NodeID uint64
	// ClusterID is the stable cluster identity expected in the mirrored state.
	ClusterID string
	// ExpectedRevision is the minimum mirrored ControllerV2 revision required before preparing.
	ExpectedRevision uint64
	// NextVoters is the Controller voter set that includes the preparing node.
	NextVoters []Voter
}

// PrepareControllerVoterResult reports target-side preparation state.
type PrepareControllerVoterResult struct {
	// Prepared reports whether the node is ready to receive Controller Raft traffic.
	Prepared bool
	// StateRevision is the mirrored state revision preserved before Raft startup.
	StateRevision uint64
}

var (
	// ErrNotStarted indicates that the ControllerV2 runtime has not started.
	ErrNotStarted = raft.ErrNotStarted
	// ErrStopped indicates that the ControllerV2 runtime has stopped.
	ErrStopped = raft.ErrStopped
	// ErrNotLeader indicates that proposals must be sent to the current leader.
	ErrNotLeader = raft.ErrNotLeader
	// ErrProposalRejected indicates that a committed proposal was semantically rejected.
	ErrProposalRejected = raft.ErrProposalRejected
)

// RuntimeRole declares how the local runtime participates in ControllerV2.
type RuntimeRole string

const (
	// RuntimeRoleVoter runs ControllerV2 Raft and serves authoritative state.
	RuntimeRoleVoter RuntimeRole = "voter"
	// RuntimeRoleMirror mirrors ControllerV2 state from Controller voters.
	RuntimeRoleMirror RuntimeRole = "mirror"
)

// Voter identifies one ControllerV2 voter endpoint.
type Voter struct {
	// NodeID is the stable non-zero node identity of the Controller voter.
	NodeID uint64
	// Addr is the cluster RPC address used to reach this Controller voter.
	Addr string
}

// Transport sends ControllerV2 Raft messages.
type Transport interface {
	Send([]raftpb.Message)
}

// RuntimeConfig wires the ControllerV2 runtime facade.
type RuntimeConfig struct {
	// NodeID is the local node identity.
	NodeID uint64
	// Addr is the local cluster RPC address.
	Addr string
	// StateDir stores ControllerV2 state and Raft files.
	StateDir string
	// ClusterID is the stable cluster identity.
	ClusterID string
	// Role declares voter or mirror behavior.
	Role RuntimeRole
	// Voters lists ControllerV2 Raft voters.
	Voters []Voter
	// AllowBootstrap permits this node to initialize a new ControllerV2 Raft log.
	AllowBootstrap bool
	// InitialSlotCount is the number of physical Slots created during bootstrap.
	InitialSlotCount uint32
	// HashSlotCount is the number of logical hash slots in the initial table.
	HashSlotCount uint16
	// ReplicaCount is the desired replica count for each physical Slot.
	ReplicaCount uint16
	// TickInterval controls ControllerV2 Raft ticking.
	TickInterval time.Duration
	// RaftTransport sends ControllerV2 Raft messages.
	RaftTransport Transport
	// RaftObserver receives local ControllerV2 Raft queue metrics.
	RaftObserver RaftObserver
	// TaskTransitionObserver receives task edges after applied metadata is persisted.
	TaskTransitionObserver TaskTransitionObserver
	// SyncClient mirrors ControllerV2 state for non-voter nodes.
	SyncClient *SyncClient
	// SyncPeers resolves ControllerV2 state sync endpoints for mirror nodes.
	SyncPeers PeerPicker
	// Now returns timestamps used for ControllerV2 commands.
	Now func() time.Time
	// Goroutines is the optional goroutine registry for lifecycle tracking.
	Goroutines *goroutine.Registry
}

// StateEvent notifies consumers that cluster-state.json changed locally.
type StateEvent struct {
	// State is the validated, locally visible cluster state snapshot.
	State ClusterState
}

// NewStateSyncServer creates a full-file sync endpoint.
func NewStateSyncServer(cfg StateSyncServerConfig) *StateSyncServer {
	return cv2sync.NewServer(cfg)
}
