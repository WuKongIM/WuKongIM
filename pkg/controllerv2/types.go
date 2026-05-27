package controllerv2

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
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
	// NodeStatusAlive means the node is considered available.
	NodeStatusAlive = state.NodeStatusAlive
	// NodeStatusSuspect means the node may be unavailable.
	NodeStatusSuspect = state.NodeStatusSuspect
	// NodeStatusDown means the node is considered unavailable.
	NodeStatusDown = state.NodeStatusDown
	// TaskKindBootstrap converges an initial physical Slot assignment.
	TaskKindBootstrap = state.TaskKindBootstrap
	// TaskStepCreateSlot creates or verifies the Slot replica group for an assignment.
	TaskStepCreateSlot = state.TaskStepCreateSlot
	// TaskStatusPending means the task is waiting for a worker.
	TaskStatusPending = state.TaskStatusPending
	// TaskStatusRunning means the task is actively being attempted.
	TaskStatusRunning = state.TaskStatusRunning
	// TaskStatusFailed means the task remains active after a failed attempt.
	TaskStatusFailed = state.TaskStatusFailed
)

type (
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
	// SyncClient mirrors ControllerV2 state for non-voter nodes.
	SyncClient *SyncClient
	// SyncPeers resolves ControllerV2 state sync endpoints for mirror nodes.
	SyncPeers PeerPicker
	// Now returns timestamps used for ControllerV2 commands.
	Now func() time.Time
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
