package management

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
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

// ChannelBusinessReader exposes authoritative channel metadata and subscriber reads.
type ChannelBusinessReader interface {
	// ScanChannelsSlotPage returns one authoritative channel page for a physical Slot.
	ScanChannelsSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error)
	// GetChannelForPermission returns one authoritative channel metadata record.
	GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
	// HasChannelSubscribers reports whether one authoritative subscriber list is non-empty.
	HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error)
}

// ChannelBusinessOperator exposes channel business mutations and member pages.
type ChannelBusinessOperator interface {
	// UpdateInfo updates the persisted channel metadata flags.
	UpdateInfo(ctx context.Context, info channelusecase.Info) error
	// AddSubscribers appends ordinary subscribers.
	AddSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error
	// RemoveSubscribers removes ordinary subscribers.
	RemoveSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error
	// AddAllowlist appends allowlist members.
	AddAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error
	// RemoveAllowlist removes allowlist members.
	RemoveAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error
	// AddDenylist appends denylist members.
	AddDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error
	// RemoveDenylist removes denylist members.
	RemoveDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error
	// ListSubscribersPage returns one ordinary subscriber page.
	ListSubscribersPage(ctx context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error)
	// ListAllowlistPage returns one allowlist page.
	ListAllowlistPage(ctx context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error)
	// ListDenylistPage returns one denylist page.
	ListDenylistPage(ctx context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error)
}

// UserOperator exposes user mutations reused by manager actions.
type UserOperator interface {
	// UpdateToken creates or replaces a user's device token.
	UpdateToken(ctx context.Context, cmd userusecase.UpdateTokenCommand) error
	// DeviceQuit clears stored device tokens and kicks matching local sessions.
	DeviceQuit(ctx context.Context, cmd userusecase.DeviceQuitCommand) error
}

// SystemUserOperator exposes persisted system UID mutations reused by manager actions.
type SystemUserOperator interface {
	// ListSystemUIDs returns the persisted system account UID list.
	ListSystemUIDs(ctx context.Context) ([]string, error)
	// AddSystemUIDs persists system account UIDs and refreshes caches.
	AddSystemUIDs(ctx context.Context, uids []string) error
	// RemoveSystemUIDs removes persisted system account UIDs and refreshes caches.
	RemoveSystemUIDs(ctx context.Context, uids []string) error
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

// MessageMetaMaxSeqReader reads max sequence from an already-loaded runtime meta.
type MessageMetaMaxSeqReader interface {
	// MaxMessageSeqForMeta returns the maximum committed message sequence without reloading runtime metadata.
	MaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error)
}

// RecentConversationSyncer exposes UID-scoped conversation sync for manager read views.
type RecentConversationSyncer interface {
	// Sync returns the bounded recent conversation working set for one UID.
	Sync(ctx context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error)
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

// MonitorMetricsReader reads node-local dashboard metrics for monitor fanout.
type MonitorMetricsReader interface {
	// NodeMonitorMetrics returns timestamped local monitor metrics from one cluster node.
	NodeMonitorMetrics(ctx context.Context, nodeID uint64, window, step time.Duration) (metrics.QueryResult, error)
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

// DiagnosticsTrackingOperator mutates node-local diagnostics tracking rules through manager fanout.
type DiagnosticsTrackingOperator interface {
	// AddNodeDiagnosticsTrackingRule installs a runtime diagnostics tracking rule on one node.
	AddNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
	// ListNodeDiagnosticsTrackingRules returns active runtime diagnostics tracking rules from one node.
	ListNodeDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error)
	// DeleteNodeDiagnosticsTrackingRule removes a runtime diagnostics tracking rule from one node.
	DeleteNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error
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
	// MonitorMetrics reads node-local dashboard metrics for cluster monitor aggregation.
	MonitorMetrics MonitorMetricsReader
	// Connections reads remote node connection inventory for manager connection filters.
	Connections ConnectionReader
	// Diagnostics reads local or remote node diagnostics events for manager aggregations.
	Diagnostics DiagnosticsReader
	// DiagnosticsTracking mutates local or remote runtime diagnostics tracking rules.
	DiagnosticsTracking DiagnosticsTrackingOperator
	// ChannelRuntimeMeta provides authoritative slot-level runtime meta pages.
	ChannelRuntimeMeta ChannelRuntimeMetaReader
	// Users provides authoritative user and device reads.
	Users UserReader
	// ChannelBusinessReader provides authoritative channel business reads.
	ChannelBusinessReader ChannelBusinessReader
	// ChannelBusinessOperator applies channel business mutations.
	ChannelBusinessOperator ChannelBusinessOperator
	// UserOperator applies user token and device mutations.
	UserOperator UserOperator
	// SystemUsers applies persisted system UID mutations.
	SystemUsers SystemUserOperator
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
	// Conversations provides UID-scoped recent conversation working sets.
	Conversations RecentConversationSyncer
	// MessageRetention provides destructive channel message retention operations.
	MessageRetention MessageRetentionOperator
	// Plugins reads and mutates node-local plugin runtime state through an adapter.
	Plugins PluginNodeClient
	// PluginBindings mutates cluster-authoritative UID to plugin bindings.
	PluginBindings PluginBindingUsecase
	// Network provides local node network observations for manager network pages.
	Network NetworkSnapshotReader
	// Now returns the current time for manager aggregations.
	Now func() time.Time
	// MetricsRegistry provides the Prometheus metrics registry for the dashboard collector.
	MetricsRegistry *metrics.Registry
	// DashboardCollector provides a shared dashboard collector owned by the composition root.
	DashboardCollector *metrics.DashboardCollector
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
	monitorMetrics           MonitorMetricsReader
	connections              ConnectionReader
	diagnostics              DiagnosticsReader
	diagnosticsTracking      DiagnosticsTrackingOperator
	channelRuntimeMeta       ChannelRuntimeMetaReader
	users                    UserReader
	channelBusinessReader    ChannelBusinessReader
	channelBusinessOperator  ChannelBusinessOperator
	userOperator             UserOperator
	systemUsers              SystemUserOperator
	userPresence             UserPresenceDirectory
	userActions              UserRouteActionDispatcher
	channelReplicaStatus     ChannelReplicaStatusReader
	channelLeaderRepair      ChannelLeaderRepairOperator
	channelLeaderTransfer    ChannelLeaderTransferOperator
	channelMigration         ChannelMigrationStore
	messages                 MessageReader
	conversations            RecentConversationSyncer
	messageRetention         MessageRetentionOperator
	plugins                  PluginNodeClient
	pluginBindings           PluginBindingUsecase
	network                  NetworkSnapshotReader
	now                      func() time.Time
	dashCollector            *metrics.DashboardCollector
	ownsDashCollector        bool
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
	var dashCollector *metrics.DashboardCollector
	ownsDashCollector := false
	if opts.DashboardCollector != nil {
		dashCollector = opts.DashboardCollector
	} else if opts.MetricsRegistry != nil {
		dashCollector = metrics.NewDashboardCollector(opts.MetricsRegistry)
		dashCollector.Start()
		ownsDashCollector = true
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
		monitorMetrics:           opts.MonitorMetrics,
		connections:              opts.Connections,
		diagnostics:              opts.Diagnostics,
		diagnosticsTracking:      opts.DiagnosticsTracking,
		channelRuntimeMeta:       opts.ChannelRuntimeMeta,
		users:                    opts.Users,
		channelBusinessReader:    opts.ChannelBusinessReader,
		channelBusinessOperator:  opts.ChannelBusinessOperator,
		userOperator:             opts.UserOperator,
		systemUsers:              opts.SystemUsers,
		userPresence:             opts.UserPresence,
		userActions:              opts.UserActions,
		channelReplicaStatus:     opts.ChannelReplicaStatus,
		channelLeaderRepair:      opts.ChannelLeaderRepair,
		channelLeaderTransfer:    opts.ChannelLeaderTransfer,
		channelMigration:         opts.ChannelMigration,
		messages:                 opts.Messages,
		conversations:            opts.Conversations,
		messageRetention:         opts.MessageRetention,
		plugins:                  opts.Plugins,
		pluginBindings:           opts.PluginBindings,
		network:                  opts.Network,
		now:                      now,
		dashCollector:            dashCollector,
		ownsDashCollector:        ownsDashCollector,
	}
}

// Stop shuts down background collectors owned by the management app.
func (a *App) Stop() {
	if a.dashCollector != nil && a.ownsDashCollector {
		a.dashCollector.Stop()
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
