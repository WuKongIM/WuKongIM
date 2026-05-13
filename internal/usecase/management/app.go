package management

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// ClusterReader exposes the cluster reads needed by manager queries.
type ClusterReader interface {
	// SlotIDs returns the configured physical slot ids.
	SlotIDs() []multiraft.SlotID
	// SlotForKey maps a channel key to its owning physical slot.
	SlotForKey(key string) multiraft.SlotID
	// HashSlotForKey maps a channel key to its logical hash slot.
	HashSlotForKey(key string) uint16
	// ListNodesStrict returns the controller leader's node snapshot without local fallback.
	ListNodesStrict(ctx context.Context) ([]controllermeta.ClusterNode, error)
	// ListSlotAssignmentsStrict returns the controller leader's slot assignments without local fallback.
	ListSlotAssignmentsStrict(ctx context.Context) ([]controllermeta.SlotAssignment, error)
	// ListObservedRuntimeViewsStrict returns the controller leader's runtime views without local fallback.
	ListObservedRuntimeViewsStrict(ctx context.Context) ([]controllermeta.SlotRuntimeView, error)
	// SlotLogStatusOnNode returns one node's local Raft log watermark for a managed Slot.
	SlotLogStatusOnNode(ctx context.Context, nodeID uint64, slotID uint32) (raftcluster.SlotLogStatus, error)
	// SlotLogEntriesOnNode returns one node's local Raft log entries for a managed Slot.
	SlotLogEntriesOnNode(ctx context.Context, nodeID uint64, slotID uint32, opts raftcluster.SlotLogEntriesOptions) (raftcluster.SlotLogEntries, error)
	// ControllerLogEntriesOnNode returns one node's local Controller Raft log entries.
	ControllerLogEntriesOnNode(ctx context.Context, nodeID uint64, opts raftcluster.ControllerLogEntriesOptions) (raftcluster.ControllerLogEntries, error)
	// ControllerRaftStatusOnNode returns one node's local Controller Raft status.
	ControllerRaftStatusOnNode(ctx context.Context, nodeID uint64) (raftcluster.ControllerRaftStatus, error)
	// CompactControllerRaftLogOnNode triggers one node's local Controller Raft log compaction.
	CompactControllerRaftLogOnNode(ctx context.Context, nodeID uint64) (raftcluster.ControllerRaftCompactionResult, error)
	// CompactSlotRaftLogOnNode triggers one node's local Slot Raft log compaction.
	CompactSlotRaftLogOnNode(ctx context.Context, nodeID uint64, slotID uint32) (raftcluster.SlotRaftCompactionResult, error)
	// ListTasksStrict returns the controller leader's task snapshot without local fallback.
	ListTasksStrict(ctx context.Context) ([]controllermeta.ReconcileTask, error)
	// ListActiveMigrationsStrict returns controller-leader active hash-slot migrations.
	ListActiveMigrationsStrict(ctx context.Context) ([]raftcluster.HashSlotMigration, error)
	// GetReconcileTaskStrict returns the controller leader's task detail without local fallback.
	GetReconcileTaskStrict(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error)
	// MarkNodeDraining marks a node as draining through the controller leader.
	MarkNodeDraining(ctx context.Context, nodeID uint64) error
	// ResumeNode marks a node back to alive through the controller leader.
	ResumeNode(ctx context.Context, nodeID uint64) error
	// TransferSlotLeader transfers one slot leader to the target node.
	TransferSlotLeader(ctx context.Context, slotID uint32, nodeID multiraft.NodeID) error
	// RecoverSlotStrict runs slot recovery against controller-leader assignments only.
	RecoverSlotStrict(ctx context.Context, slotID uint32, strategy raftcluster.RecoverStrategy) error
	// Rebalance starts the current hash-slot rebalance plan.
	Rebalance(ctx context.Context) ([]raftcluster.MigrationPlan, error)
	// GetMigrationStatus returns the currently active hash-slot migrations.
	GetMigrationStatus() []raftcluster.HashSlotMigration
	// AddSlot creates a new physical slot through the controller leader.
	AddSlot(ctx context.Context) (multiraft.SlotID, error)
	// RemoveSlot starts removal for one physical slot through the controller leader.
	RemoveSlot(ctx context.Context, slotID multiraft.SlotID) error
	ControllerLeaderID() uint64
	// ListNodeOnboardingCandidates returns data nodes and current Slot load for onboarding review.
	ListNodeOnboardingCandidates(ctx context.Context) ([]raftcluster.NodeOnboardingCandidate, error)
	// CreateNodeOnboardingPlan creates a durable reviewed onboarding plan on the controller leader.
	CreateNodeOnboardingPlan(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error)
	// StartNodeOnboardingJob starts a planned onboarding job.
	StartNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
	// ListNodeOnboardingJobs returns durable onboarding jobs in manager order.
	ListNodeOnboardingJobs(ctx context.Context, limit int, cursor string) ([]controllermeta.NodeOnboardingJob, string, bool, error)
	// GetNodeOnboardingJob returns one durable onboarding job.
	GetNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
	// RetryNodeOnboardingJob creates a replacement plan for a failed onboarding job.
	RetryNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
	// TransportPoolStats returns local outbound cluster transport pool counters by peer.
	TransportPoolStats() []transport.PoolPeerStats
}

// ChannelRuntimeMetaReader exposes authoritative slot-level channel runtime pages.
type ChannelRuntimeMetaReader interface {
	// ScanChannelRuntimeMetaSlotPage returns one authoritative page for a physical slot.
	ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
	// GetChannelRuntimeMeta returns one authoritative channel runtime metadata record.
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

// UserReader exposes authoritative user and device reads for manager pages.
type UserReader interface {
	// ScanUsersSlotPage returns one authoritative user page for a physical Slot.
	ScanUsersSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error)
	// GetUser returns one authoritative user record.
	GetUser(ctx context.Context, uid string) (metadb.User, error)
	// GetDevice returns one authoritative device record.
	GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
}

// UserOperator exposes user mutations reused by manager actions.
type UserOperator interface {
	// UpdateToken creates or replaces a user's device token.
	UpdateToken(ctx context.Context, cmd userusecase.UpdateTokenCommand) error
	// DeviceQuit clears stored device tokens and kicks matching local sessions.
	DeviceQuit(ctx context.Context, cmd userusecase.DeviceQuitCommand) error
}

// UserPresenceDirectory exposes authoritative online routes keyed by UID.
type UserPresenceDirectory interface {
	// EndpointsByUIDs returns authoritative online routes keyed by UID.
	EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error)
}

// UserRouteActionDispatcher applies manager force-offline actions on route owners.
type UserRouteActionDispatcher interface {
	// ApplyRouteAction applies a close/kick action on the route owner node.
	ApplyRouteAction(ctx context.Context, action presence.RouteAction) error
}

// ChannelReplicaStatusReader exposes proven live channel runtime status.
type ChannelReplicaStatusReader interface {
	// ChannelRuntimeStatus returns the best proven runtime status for one channel.
	ChannelRuntimeStatus(ctx context.Context, id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
}

// ChannelLeaderRepairOperator exposes policy-driven channel leader repair.
type ChannelLeaderRepairOperator interface {
	// RepairChannelLeader repairs or validates authoritative channel leader metadata.
	RepairChannelLeader(ctx context.Context, req RepairChannelClusterLeaderRequest) (RepairChannelClusterLeaderResult, error)
}

// ChannelLeaderTransferOperator exposes explicit safe channel leader transfer.
type ChannelLeaderTransferOperator interface {
	// TransferChannelLeader safely transfers authoritative channel leadership.
	TransferChannelLeader(ctx context.Context, req TransferChannelClusterLeaderRequest) (TransferChannelClusterLeaderResult, error)
}

// ChannelMigrationStore exposes authoritative channel migration task mutations.
type ChannelMigrationStore interface {
	// CreateChannelMigrationTaskWithRuntimeGuard creates a new active migration task fenced to observed runtime metadata.
	CreateChannelMigrationTaskWithRuntimeGuard(ctx context.Context, req metadb.ChannelMigrationTaskCreate) error
	// GetActiveChannelMigrationTask returns the active task for one channel when present.
	GetActiveChannelMigrationTask(ctx context.Context, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error)
	// ListActiveChannelMigrationTasksForNode returns active channel migration tasks whose source or target is the node.
	ListActiveChannelMigrationTasksForNode(ctx context.Context, nodeID uint64, limit int) ([]metadb.ChannelMigrationTask, bool, error)
	// AbortChannelMigration marks an active migration task aborted.
	AbortChannelMigration(ctx context.Context, req metadb.ChannelMigrationAbortRequest) error
}

// MessageReader exposes authoritative channel message page reads.
type MessageReader interface {
	// QueryMessages returns one authoritative message page for a channel.
	QueryMessages(ctx context.Context, req MessageQueryRequest) (MessageQueryPage, error)
	// MaxMessageSeq returns the maximum committed message sequence for a channel.
	MaxMessageSeq(ctx context.Context, id channel.ChannelID) (uint64, error)
}

// MessageRetentionOperator exposes destructive channel message retention operations.
type MessageRetentionOperator interface {
	// AdvanceMessageRetention advances one channel's history retention boundary.
	AdvanceMessageRetention(ctx context.Context, req AdvanceMessageRetentionRequest) (AdvanceMessageRetentionResponse, error)
}

// RuntimeSummaryReader reads local or remote node runtime state needed by scale-in safety checks.
type RuntimeSummaryReader interface {
	// NodeRuntimeSummary returns online and gateway counters for one cluster node.
	NodeRuntimeSummary(ctx context.Context, nodeID uint64) (NodeRuntimeSummary, error)
}

// ConnectionReader reads node-local connection inventory for non-local manager filters.
type ConnectionReader interface {
	// NodeConnections returns active connections currently registered on one cluster node.
	NodeConnections(ctx context.Context, nodeID uint64) ([]Connection, error)
	// NodeConnection returns one connection currently registered on one cluster node.
	NodeConnection(ctx context.Context, nodeID uint64, sessionID uint64) (ConnectionDetail, error)
}

// DiagnosticsReader reads node-local diagnostics events through an entry-agnostic adapter.
type DiagnosticsReader interface {
	// QueryNodeDiagnostics returns retained diagnostics events from one cluster node.
	QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error)
}

// NodeRuntimeSummary contains target-node connection and gateway admission counters.
type NodeRuntimeSummary struct {
	// NodeID identifies the cluster node described by this summary.
	NodeID uint64
	// ActiveOnline counts active authenticated online connections.
	ActiveOnline int
	// ClosingOnline counts authenticated online connections that are closing but not fully removed.
	ClosingOnline int
	// TotalOnline counts all authenticated online connections tracked by the node.
	TotalOnline int
	// GatewaySessions counts all gateway sessions, including unauthenticated sessions.
	GatewaySessions int
	// SessionsByListener groups gateway sessions by listener name or address.
	SessionsByListener map[string]int
	// AcceptingNewSessions reports whether gateway admission currently accepts new sessions.
	AcceptingNewSessions bool
	// Draining reports whether the target node believes it is in drain mode.
	Draining bool
	// Unknown means runtime counters could not be read and must fail closed for scale-in safety.
	Unknown bool
}

const defaultScaleInRuntimeViewMaxAge = 30 * time.Second

// Options configures the management usecase app.
type Options struct {
	// LocalNodeID is the node ID of the current process.
	LocalNodeID uint64
	// ControllerPeerIDs lists the configured controller peer node IDs.
	ControllerPeerIDs []uint64
	// SlotReplicaN is the configured target replica count for each managed slot.
	SlotReplicaN int
	// ScaleInRuntimeViewMaxAge bounds how old runtime observations may be for scale-in safety.
	ScaleInRuntimeViewMaxAge time.Duration
	// Cluster provides distributed cluster read access.
	Cluster ClusterReader
	// Online provides local online connection reads.
	Online online.Registry
	// RuntimeSummary reads local or remote node runtime counters for scale-in safety.
	RuntimeSummary RuntimeSummaryReader
	// Connections reads remote node connection inventory for manager connection filters.
	Connections ConnectionReader
	// Diagnostics reads local or remote node diagnostics events for manager aggregations.
	Diagnostics DiagnosticsReader
	// ChannelRuntimeMeta provides authoritative slot-level runtime meta pages.
	ChannelRuntimeMeta ChannelRuntimeMetaReader
	// Users provides authoritative user and device reads.
	Users UserReader
	// UserOperator applies user token and device mutations.
	UserOperator UserOperator
	// UserPresence reads authoritative user presence routes.
	UserPresence UserPresenceDirectory
	// UserActions applies route owner force-offline actions.
	UserActions UserRouteActionDispatcher
	// ChannelReplicaStatus provides proven live channel runtime status when available.
	ChannelReplicaStatus ChannelReplicaStatusReader
	// ChannelLeaderRepair provides policy-driven channel leader repair.
	ChannelLeaderRepair ChannelLeaderRepairOperator
	// ChannelLeaderTransfer provides explicit safe channel leader transfer.
	ChannelLeaderTransfer ChannelLeaderTransferOperator
	// ChannelMigration provides authoritative channel migration task mutations.
	ChannelMigration ChannelMigrationStore
	// Messages provides authoritative channel message pages.
	Messages MessageReader
	// MessageRetention provides destructive channel message retention operations.
	MessageRetention MessageRetentionOperator
	// Network provides local node network observations for manager network pages.
	Network NetworkSnapshotReader
	// Now returns the current time for manager aggregations.
	Now func() time.Time
}

// App serves manager-oriented read usecases.
type App struct {
	localNodeID              uint64
	controllerPeerIDs        map[uint64]struct{}
	controllerPeerIDList     []uint64
	slotReplicaN             int
	scaleInRuntimeViewMaxAge time.Duration
	cluster                  ClusterReader
	online                   online.Registry
	runtimeSummary           RuntimeSummaryReader
	connections              ConnectionReader
	diagnostics              DiagnosticsReader
	channelRuntimeMeta       ChannelRuntimeMetaReader
	users                    UserReader
	userOperator             UserOperator
	userPresence             UserPresenceDirectory
	userActions              UserRouteActionDispatcher
	channelReplicaStatus     ChannelReplicaStatusReader
	channelLeaderRepair      ChannelLeaderRepairOperator
	channelLeaderTransfer    ChannelLeaderTransferOperator
	channelMigration         ChannelMigrationStore
	messages                 MessageReader
	messageRetention         MessageRetentionOperator
	network                  NetworkSnapshotReader
	now                      func() time.Time
}

// New constructs the management usecase app.
func New(opts Options) *App {
	peerList, peers := normalizeControllerPeerIDs(opts.ControllerPeerIDs)
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	scaleInRuntimeViewMaxAge := opts.ScaleInRuntimeViewMaxAge
	if scaleInRuntimeViewMaxAge <= 0 {
		scaleInRuntimeViewMaxAge = defaultScaleInRuntimeViewMaxAge
	}
	return &App{
		localNodeID:              opts.LocalNodeID,
		controllerPeerIDs:        peers,
		controllerPeerIDList:     peerList,
		slotReplicaN:             opts.SlotReplicaN,
		scaleInRuntimeViewMaxAge: scaleInRuntimeViewMaxAge,
		cluster:                  opts.Cluster,
		online:                   opts.Online,
		runtimeSummary:           opts.RuntimeSummary,
		connections:              opts.Connections,
		diagnostics:              opts.Diagnostics,
		channelRuntimeMeta:       opts.ChannelRuntimeMeta,
		users:                    opts.Users,
		userOperator:             opts.UserOperator,
		userPresence:             opts.UserPresence,
		userActions:              opts.UserActions,
		channelReplicaStatus:     opts.ChannelReplicaStatus,
		channelLeaderRepair:      opts.ChannelLeaderRepair,
		channelLeaderTransfer:    opts.ChannelLeaderTransfer,
		channelMigration:         opts.ChannelMigration,
		messages:                 opts.Messages,
		messageRetention:         opts.MessageRetention,
		network:                  opts.Network,
		now:                      now,
	}
}

func normalizeControllerPeerIDs(ids []uint64) ([]uint64, map[uint64]struct{}) {
	peers := make(map[uint64]struct{}, len(ids))
	for _, nodeID := range ids {
		if nodeID == 0 {
			continue
		}
		peers[nodeID] = struct{}{}
	}
	list := make([]uint64, 0, len(peers))
	for nodeID := range peers {
		list = append(list, nodeID)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	return list, peers
}
