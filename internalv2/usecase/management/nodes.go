package management

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

const controllerRaftHealthUnknown = "unknown"

// ControlSnapshotReader exposes the clusterv2 control snapshot used by manager read models.
type ControlSnapshotReader interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// LocalControlSnapshot returns the latest locally visible clusterv2 control snapshot.
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

// RuntimeSummaryReader reads local or remote node runtime counters for manager inventory.
type RuntimeSummaryReader interface {
	// NodeRuntimeSummary returns online and gateway counters for one cluster node.
	NodeRuntimeSummary(ctx context.Context, nodeID uint64) (NodeRuntimeSummary, error)
}

// NodeLifecycleWriter submits cluster-authoritative node lifecycle mutations.
type NodeLifecycleWriter interface {
	// JoinNode submits a data-node join request to the control writer.
	JoinNode(context.Context, control.JoinNodeRequest) (control.JoinNodeResult, error)
	// ActivateNode submits a node activation request to the control writer.
	ActivateNode(context.Context, control.ActivateNodeRequest) (control.ActivateNodeResult, error)
	// MarkNodeLeaving submits a node leaving request to the control writer.
	MarkNodeLeaving(context.Context, control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error)
	// MarkNodeRemoved submits a node removed request to the control writer.
	MarkNodeRemoved(context.Context, control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error)
}

// ControllerVoterPromoter submits cluster-authoritative Controller voter promotion writes.
type ControllerVoterPromoter interface {
	PromoteControllerVoter(context.Context, control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error)
}

// ControllerVoterReadinessReader reads target readiness before Controller voter promotion.
type ControllerVoterReadinessReader interface {
	ControllerVoterReadiness(context.Context, uint64) (ControllerVoterReadiness, error)
}

// ControllerVoterPreparer prepares a target node and may return best-effort local Controller Raft status.
type ControllerVoterPreparer interface {
	PrepareControllerVoter(context.Context, PrepareControllerVoterRequest) (PrepareControllerVoterResponse, error)
}

// NodeReadinessReader reads app-local readiness for activation gates.
type NodeReadinessReader interface {
	// NodeReadiness returns the selected node's activation readiness view.
	NodeReadiness(context.Context, uint64) (NodeReadiness, error)
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

// ScaleInStatusObserver observes completed scale-in status projections.
type ScaleInStatusObserver interface {
	// ObserveScaleInStatus observes one scale-in status response after construction.
	ObserveScaleInStatus(NodeScaleInStatusResponse)
}

// NodeLifecycleAttemptObserver observes node lifecycle write attempts.
type NodeLifecycleAttemptObserver interface {
	// ObserveNodeLifecycleAttempt observes one lifecycle operation attempt with a bounded result class.
	ObserveNodeLifecycleAttempt(operation, result string)
}

// ControllerVoterPromotionObserver observes Controller voter promotion attempts and phases.
type ControllerVoterPromotionObserver interface {
	// ObserveControllerVoterPromotionAttempt observes one promotion attempt result.
	ObserveControllerVoterPromotionAttempt(result string)
	// ObserveControllerVoterPromotionBlocker observes one bounded promotion blocker reason.
	ObserveControllerVoterPromotionBlocker(reason string)
	// ObserveControllerVoterPromotionPhase observes one promotion phase duration.
	ObserveControllerVoterPromotionPhase(phase string, duration time.Duration)
}

// ControllerRaftStatusObserver observes Controller Raft status collection.
type ControllerRaftStatusObserver interface {
	// ObserveControllerRaftStatus observes one successfully collected Controller Raft status.
	ObserveControllerRaftStatus(ControllerRaftStatus)
}

// Options configures the manager management usecase.
type Options struct {
	// Cluster reads clusterv2 control state.
	Cluster ControlSnapshotReader
	// RuntimeSummary reads node runtime counters for the manager node list.
	RuntimeSummary RuntimeSummaryReader
	// GatewayDrain mutates gateway admission drain mode locally or remotely.
	GatewayDrain GatewayDrainWriter
	// NodeLifecycle submits cluster-authoritative node join, activation, and leaving requests.
	NodeLifecycle NodeLifecycleWriter
	// ControllerVoterPromoter submits cluster-authoritative Controller voter promotion writes.
	ControllerVoterPromoter ControllerVoterPromoter
	// ControllerVoterReadiness reads selected-node readiness before Controller voter promotion.
	ControllerVoterReadiness ControllerVoterReadinessReader
	// ControllerVoterPreparer prepares a target node and may return best-effort local Controller Raft status.
	ControllerVoterPreparer ControllerVoterPreparer
	// NodeReadiness reads selected-node readiness before activation writes.
	NodeReadiness NodeReadinessReader
	// Diagnostics reads local or remote node diagnostics events for manager aggregations.
	Diagnostics DiagnosticsReader
	// DiagnosticsTracking mutates local or remote runtime diagnostics tracking rules.
	DiagnosticsTracking DiagnosticsTrackingOperator
	// ScaleInStatusObserver observes generated scale-in safety statuses.
	ScaleInStatusObserver ScaleInStatusObserver
	// NodeLifecycleAttemptObserver observes lifecycle write attempts for metrics.
	NodeLifecycleAttemptObserver NodeLifecycleAttemptObserver
	// ControllerVoterPromotionObserver observes Controller voter promotion attempts for metrics.
	ControllerVoterPromotionObserver ControllerVoterPromotionObserver
	// ControllerRaftStatusObserver observes Controller Raft status collection for metrics.
	ControllerRaftStatusObserver ControllerRaftStatusObserver
	// ChannelRuntimeMeta scans channel runtime metadata for cluster channel pages.
	ChannelRuntimeMeta ChannelRuntimeMetaReader
	// ChannelBusinessReader scans durable channel metadata for manager channel pages.
	ChannelBusinessReader ChannelBusinessReader
	// RemoteBusinessChannels reads manager channel pages from peer nodes.
	RemoteBusinessChannels RemoteBusinessChannelReader
	// Users scans durable UID and device metadata for manager user pages.
	Users UserReader
	// UserOperator applies user mutations reused by manager actions.
	UserOperator UserOperator
	// UserPresence reads authoritative online routes for user summaries.
	UserPresence UserPresenceDirectory
	// UserActions applies owner-route actions for manager force-offline requests.
	UserActions UserRouteActionDispatcher
	// SystemUsers lists and mutates persisted system UID rows.
	SystemUsers SystemUserOperator
	// Conversations syncs UID-owned recent conversations for manager pages.
	Conversations ConversationSyncer
	// Messages reads committed channel messages for manager pages.
	Messages MessageReader
	// MessageRetention advances channel message retention boundaries.
	MessageRetention MessageRetentionOperator
	// Connections reads owner-local gateway session snapshots for manager connection pages.
	Connections ConnectionReader
	// RemoteConnections reads owner-local gateway session snapshots from peer nodes.
	RemoteConnections RemoteConnectionReader
	// Plugins reads node-local plugin runtime snapshots for manager plugin pages.
	Plugins PluginReader
	// RemotePlugins reads node-local plugin runtime snapshots from peer nodes.
	RemotePlugins RemotePluginReader
	// PluginBindings reads and mutates cluster-authoritative UID plugin bindings.
	PluginBindings PluginBindingStore
	// Logs reads node-local distributed Raft log pages.
	Logs LogReader
	// SlotRaft runs node-local Slot Raft compaction operations.
	SlotRaft SlotRaftOperator
	// LeaderTransfer submits Controller-backed Slot leader transfer intents.
	LeaderTransfer SlotLeaderTransferWriter
	// SlotReplicaMove submits Controller-backed staged Slot replica move intents.
	SlotReplicaMove SlotReplicaMoveWriter
	// SlotRuntimeStatus reads the live Slot Raft status used to validate transfers and summarize node leadership.
	SlotRuntimeStatus SlotRuntimeStatusReader
	// ControllerRaft runs node-local Controller Raft status and compaction operations.
	ControllerRaft ControllerRaftOperator
	// ControllerTaskAudit reads retained ControllerV2 task audit history.
	ControllerTaskAudit ControllerTaskAuditReader
	// ApplicationLogs reads selected-node ordinary application log pages.
	ApplicationLogs ApplicationLogReader
	// DBInspect runs read-only node-local DB inspect queries.
	DBInspect DBInspectReader
	// RemoteDBInspect runs read-only DB inspect queries on peer nodes.
	RemoteDBInspect RemoteDBInspectReader
	// Now returns timestamps for generated read models.
	Now func() time.Time
}

// App serves read-only manager management usecases for internalv2.
type App struct {
	cluster                      ControlSnapshotReader
	runtimeSummary               RuntimeSummaryReader
	gatewayDrain                 GatewayDrainWriter
	nodeLifecycle                NodeLifecycleWriter
	controllerVoterPromoter      ControllerVoterPromoter
	controllerVoterReadiness     ControllerVoterReadinessReader
	controllerVoterPreparer      ControllerVoterPreparer
	nodeReadiness                NodeReadinessReader
	diagnostics                  DiagnosticsReader
	diagnosticsTracking          DiagnosticsTrackingOperator
	scaleInStatusObserver        ScaleInStatusObserver
	lifecycleAttempts            NodeLifecycleAttemptObserver
	controllerVoterPromotion     ControllerVoterPromotionObserver
	controllerRaftStatusObserver ControllerRaftStatusObserver
	channelRuntimeMeta           ChannelRuntimeMetaReader
	channelBusinessReader        ChannelBusinessReader
	remoteBusinessChannels       RemoteBusinessChannelReader
	users                        UserReader
	userOperator                 UserOperator
	userPresence                 UserPresenceDirectory
	userActions                  UserRouteActionDispatcher
	systemUsers                  SystemUserOperator
	conversations                ConversationSyncer
	messages                     MessageReader
	messageRetention             MessageRetentionOperator
	connections                  ConnectionReader
	remoteConnections            RemoteConnectionReader
	plugins                      PluginReader
	remotePlugins                RemotePluginReader
	pluginBindings               PluginBindingStore
	logs                         LogReader
	slotRaft                     SlotRaftOperator
	leaderTransfer               SlotLeaderTransferWriter
	slotReplicaMove              SlotReplicaMoveWriter
	slotRuntimeStatus            SlotRuntimeStatusReader
	controllerRaft               ControllerRaftOperator
	controllerTaskAudit          ControllerTaskAuditReader
	applicationLogs              ApplicationLogReader
	dbInspect                    DBInspectReader
	remoteDBInspect              RemoteDBInspectReader
	now                          func() time.Time
}

// New constructs the manager management usecase.
func New(opts Options) *App {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &App{
		cluster:                      opts.Cluster,
		runtimeSummary:               opts.RuntimeSummary,
		gatewayDrain:                 opts.GatewayDrain,
		nodeLifecycle:                opts.NodeLifecycle,
		controllerVoterPromoter:      opts.ControllerVoterPromoter,
		controllerVoterReadiness:     opts.ControllerVoterReadiness,
		controllerVoterPreparer:      opts.ControllerVoterPreparer,
		nodeReadiness:                opts.NodeReadiness,
		diagnostics:                  opts.Diagnostics,
		diagnosticsTracking:          opts.DiagnosticsTracking,
		scaleInStatusObserver:        opts.ScaleInStatusObserver,
		lifecycleAttempts:            opts.NodeLifecycleAttemptObserver,
		controllerVoterPromotion:     opts.ControllerVoterPromotionObserver,
		controllerRaftStatusObserver: opts.ControllerRaftStatusObserver,
		channelRuntimeMeta:           opts.ChannelRuntimeMeta,
		channelBusinessReader:        opts.ChannelBusinessReader,
		remoteBusinessChannels:       opts.RemoteBusinessChannels,
		users:                        opts.Users,
		userOperator:                 opts.UserOperator,
		userPresence:                 opts.UserPresence,
		userActions:                  opts.UserActions,
		systemUsers:                  opts.SystemUsers,
		conversations:                opts.Conversations,
		messages:                     opts.Messages,
		messageRetention:             opts.MessageRetention,
		connections:                  opts.Connections,
		remoteConnections:            opts.RemoteConnections,
		plugins:                      opts.Plugins,
		remotePlugins:                opts.RemotePlugins,
		pluginBindings:               opts.PluginBindings,
		logs:                         opts.Logs,
		slotRaft:                     opts.SlotRaft,
		leaderTransfer:               opts.LeaderTransfer,
		slotReplicaMove:              opts.SlotReplicaMove,
		slotRuntimeStatus:            opts.SlotRuntimeStatus,
		controllerRaft:               opts.ControllerRaft,
		controllerTaskAudit:          opts.ControllerTaskAudit,
		applicationLogs:              opts.ApplicationLogs,
		dbInspect:                    opts.DBInspect,
		remoteDBInspect:              opts.RemoteDBInspect,
		now:                          now,
	}
}

// NodeList is the manager-facing node inventory snapshot.
type NodeList struct {
	// GeneratedAt records when this inventory snapshot was built.
	GeneratedAt time.Time
	// ControllerLeaderID is the Controller leader known to this node.
	ControllerLeaderID uint64
	// Items contains ordered node inventory rows.
	Items []Node
}

// Node is the manager-facing node DTO.
type Node struct {
	// NodeID is the node identifier.
	NodeID uint64
	// Name is the operator-facing node name.
	Name string
	// Addr is the cluster listen address of the node.
	Addr string
	// Status is the manager-facing node status string.
	Status string
	// LastHeartbeatAt is the best available control snapshot timestamp for this node.
	LastHeartbeatAt time.Time
	// IsLocal reports whether the node is the current process node.
	IsLocal bool
	// CapacityWeight is the current relative planner capacity.
	CapacityWeight int
	// Membership contains durable membership role and lifecycle state.
	Membership NodeMembership
	// Health contains observed node health and operator state.
	Health NodeHealth
	// Controller contains Controller role and voter context.
	Controller NodeController
	// Slots contains lightweight Slot placement counts.
	Slots NodeSlotSummary
	// Runtime contains node-local online and gateway counters.
	Runtime NodeRuntimeSummary
	// Actions contains backend business capability hints for UI actions.
	Actions NodeActions
}

// NodeMembership describes durable cluster membership for one node.
type NodeMembership struct {
	// Role is the durable cluster membership role.
	Role string
	// JoinState is the durable membership lifecycle state.
	JoinState string
	// Schedulable reports whether the planner may place data replicas on this node.
	Schedulable bool
}

// NodeHealth describes observed node health and operator state.
type NodeHealth struct {
	// Status is the manager-facing health or operator state.
	Status string
	// LastHeartbeatAt is the best available control snapshot timestamp for this node.
	LastHeartbeatAt time.Time
	// Fresh reports whether the latest durable health report is within its TTL.
	Fresh bool
	// Freshness is fresh, stale, or missing.
	Freshness string
	// RuntimeReady reports whether the node can serve foreground traffic.
	RuntimeReady bool
	// ReportAgeMS is the current health report age in milliseconds.
	ReportAgeMS int64
	// ReportTTLMS is the configured freshness TTL in milliseconds.
	ReportTTLMS int64
	// ObservedControlRevision is the latest ControllerV2 revision observed by the node.
	ObservedControlRevision uint64
	// ObservedSlotRevision is the latest local Slot observation revision.
	ObservedSlotRevision uint64
	// ErrorCode is a bounded machine-readable runtime reason.
	ErrorCode string
}

// NodeController describes Controller role and voter context.
type NodeController struct {
	// Role is leader, follower, or none.
	Role string
	// Voter reports whether this node is a configured Controller voter.
	Voter bool
	// LeaderID is the current Controller leader known locally.
	LeaderID uint64
	// RaftHealth is the summarized local Controller Raft health state.
	RaftHealth string
	// FirstIndex is the first available local Controller Raft log index.
	FirstIndex uint64
	// AppliedIndex is the queried node's applied index watermark.
	AppliedIndex uint64
	// SnapshotIndex is the latest persisted Controller Raft snapshot index.
	SnapshotIndex uint64
}

// NodeSlotSummary contains lightweight Slot placement counts for one node.
type NodeSlotSummary struct {
	// ReplicaCount is the number of desired Slot replicas hosted by the node.
	ReplicaCount int
	// LeaderCount is the number of actual Slot Raft leaders hosted by the node.
	LeaderCount int
	// FollowerCount is the number of desired Slot replicas that are not observed leaders.
	FollowerCount int
	// QuorumLostCount is reserved for runtime observation once available.
	QuorumLostCount int
	// UnreportedCount is reserved for runtime observation once available.
	UnreportedCount int
}

// NodeRuntimeSummary contains node-local online and gateway counters.
type NodeRuntimeSummary struct {
	// NodeID identifies the cluster node described by this summary.
	NodeID uint64
	// ControlRevision is the local control snapshot revision observed by the node.
	ControlRevision uint64
	// ActiveOnline counts active authenticated online connections.
	ActiveOnline int
	// ClosingOnline counts authenticated online connections that are closing but not fully removed.
	ClosingOnline int
	// TotalOnline counts all authenticated online connections tracked by the node.
	TotalOnline int
	// GatewaySessions counts all gateway sessions, including unauthenticated sessions.
	GatewaySessions int
	// PendingActivations counts local sessions accepted but not yet authority-active.
	PendingActivations int
	// SessionsByListener groups gateway sessions by listener name or address.
	SessionsByListener map[string]int
	// AcceptingNewSessions reports whether gateway admission currently accepts new sessions.
	AcceptingNewSessions bool
	// Draining reports whether the target node believes it is in drain mode.
	Draining bool
	// Unknown means runtime counters could not be read.
	Unknown bool
}

// NodeActions contains backend business capability hints for UI actions.
type NodeActions struct {
	// CanDrain reports whether the legacy node-drain action can be used.
	CanDrain bool
	// CanResume reports whether the legacy node-resume action can be used.
	CanResume bool
	// CanScaleIn reports whether the data-node scale-in flow can be considered.
	CanScaleIn bool
	// CanOnboard reports whether the node can be considered for explicit resource allocation.
	CanOnboard bool
	// CanPromoteControllerVoter reports whether the node can be considered for Controller voter promotion.
	CanPromoteControllerVoter bool
}

// ListNodes returns the manager node list DTOs ordered by node ID.
func (a *App) ListNodes(ctx context.Context) (NodeList, error) {
	if a == nil || a.cluster == nil {
		return NodeList{}, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeList{}, err
	}
	generatedAt := a.now()
	slots := a.summarizeSlots(ctx, snapshot.Slots)
	nodes := make([]Node, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes = append(nodes, buildNode(nodeBuildOptions{
			node:               node,
			slots:              slots,
			runtime:            a.nodeRuntimeSummary(ctx, node.NodeID),
			localNodeID:        a.cluster.NodeID(),
			controllerLeaderID: snapshot.ControllerID,
			generatedAt:        generatedAt,
		}))
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return NodeList{GeneratedAt: generatedAt, ControllerLeaderID: snapshot.ControllerID, Items: nodes}, nil
}

type slotSummary struct {
	replicas map[uint64]int
	leaders  map[uint64]int
}

func (a *App) summarizeSlots(ctx context.Context, assignments []control.SlotAssignment) slotSummary {
	summary := slotSummary{replicas: map[uint64]int{}, leaders: map[uint64]int{}}
	for _, assignment := range assignments {
		for _, nodeID := range assignment.DesiredPeers {
			summary.replicas[nodeID]++
		}
		leaderID := a.actualSlotLeaderID(ctx, assignment)
		if leaderID != 0 {
			summary.leaders[leaderID]++
		}
	}
	return summary
}

func (a *App) actualSlotLeaderID(ctx context.Context, assignment control.SlotAssignment) uint64 {
	if a == nil || a.slotRuntimeStatus == nil {
		return 0
	}
	status, err := a.slotRuntimeStatus.SlotRuntimeStatus(ctx, assignment.SlotID, append([]uint64(nil), assignment.DesiredPeers...))
	if err != nil {
		return 0
	}
	return status.LeaderID
}

// NodeRuntimeSummary returns the best available runtime counters for one node.
func (a *App) NodeRuntimeSummary(ctx context.Context, nodeID uint64) (NodeRuntimeSummary, error) {
	return a.nodeRuntimeSummary(ctx, nodeID), nil
}

func (a *App) nodeRuntimeSummary(ctx context.Context, nodeID uint64) NodeRuntimeSummary {
	if a == nil || a.runtimeSummary == nil {
		return unknownNodeRuntimeSummary(nodeID)
	}
	summary, err := a.runtimeSummary.NodeRuntimeSummary(ctx, nodeID)
	if err != nil {
		return unknownNodeRuntimeSummary(nodeID)
	}
	if summary.NodeID == 0 {
		summary.NodeID = nodeID
	}
	if summary.SessionsByListener == nil {
		summary.SessionsByListener = map[string]int{}
	}
	return summary
}

func unknownNodeRuntimeSummary(nodeID uint64) NodeRuntimeSummary {
	return NodeRuntimeSummary{
		NodeID:             nodeID,
		SessionsByListener: map[string]int{},
		Unknown:            true,
	}
}

type nodeBuildOptions struct {
	node               control.Node
	slots              slotSummary
	runtime            NodeRuntimeSummary
	localNodeID        uint64
	controllerLeaderID uint64
	generatedAt        time.Time
}

func buildNode(opts nodeBuildOptions) Node {
	status := managerNodeStatus(opts.node.Status)
	role := managerNodeRole(opts.node.Roles)
	joinState := managerNodeJoinState(opts.node.JoinState)
	health := opts.node.Health
	healthStatus := health.Status
	if healthStatus == "" {
		healthStatus = opts.node.Status
	}
	lastHeartbeatAt := health.ReportedAt
	if lastHeartbeatAt.IsZero() {
		lastHeartbeatAt = opts.generatedAt
	}
	schedulable := control.NodeSchedulableForPlacement(opts.node)
	controllerVoter := hasRole(opts.node.Roles, control.RoleController)
	replicas := opts.slots.replicas[opts.node.NodeID]
	leaders := opts.slots.leaders[opts.node.NodeID]
	followers := replicas - leaders
	if followers < 0 {
		followers = 0
	}
	return Node{
		NodeID:          opts.node.NodeID,
		Name:            fmt.Sprintf("node-%d", opts.node.NodeID),
		Addr:            opts.node.Addr,
		Status:          status,
		LastHeartbeatAt: opts.generatedAt,
		IsLocal:         opts.node.NodeID == opts.localNodeID,
		CapacityWeight:  managerNodeCapacityWeight(opts.node.CapacityWeight),
		Membership: NodeMembership{
			Role:        role,
			JoinState:   joinState,
			Schedulable: schedulable,
		},
		Health: NodeHealth{
			Status:                  managerNodeStatus(healthStatus),
			LastHeartbeatAt:         lastHeartbeatAt,
			Fresh:                   health.Freshness == control.NodeHealthFresh,
			Freshness:               managerNodeHealthFreshness(health.Freshness),
			RuntimeReady:            health.RuntimeReady,
			ReportAgeMS:             health.ReportAge.Milliseconds(),
			ReportTTLMS:             health.ReportTTL.Milliseconds(),
			ObservedControlRevision: health.ObservedControlRevision,
			ObservedSlotRevision:    health.ObservedSlotRevision,
			ErrorCode:               health.ErrorCode,
		},
		Controller: NodeController{
			Role:       managerControllerRole(opts.node.NodeID, opts.controllerLeaderID, controllerVoter),
			Voter:      controllerVoter,
			LeaderID:   opts.controllerLeaderID,
			RaftHealth: controllerRaftHealthUnknown,
		},
		Slots: NodeSlotSummary{
			ReplicaCount:  replicas,
			LeaderCount:   leaders,
			FollowerCount: followers,
		},
		Runtime: opts.runtime,
		Actions: nodeActions(opts.node, controllerVoter),
	}
}

// nodeActions derives read-model lifecycle action hints from durable membership state.
func nodeActions(node control.Node, controllerVoter bool) NodeActions {
	role := managerNodeRole(node.Roles)
	joinState := managerNodeJoinState(node.JoinState)
	dataOnly := role == "data" && !controllerVoter
	return NodeActions{
		CanScaleIn:                dataOnly && (joinState == "active" || joinState == "leaving"),
		CanOnboard:                dataOnly && joinState == "active",
		CanPromoteControllerVoter: dataOnly && joinState == "active",
	}
}

func managerNodeJoinState(state control.NodeJoinState) string {
	switch managerControlJoinState(state) {
	case control.NodeJoinStateActive:
		return "active"
	case control.NodeJoinStateJoining:
		return "joining"
	case control.NodeJoinStateLeaving:
		return "leaving"
	case control.NodeJoinStateRemoved:
		return "removed"
	default:
		return "unknown"
	}
}

func managerNodeCapacityWeight(weight uint32) int {
	if weight == 0 {
		return 1
	}
	return int(weight)
}

func managerNodeStatus(status control.NodeStatus) string {
	switch status {
	case control.NodeAlive:
		return "alive"
	case control.NodeSuspect:
		return "suspect"
	case control.NodeDown:
		return "dead"
	default:
		return "unknown"
	}
}

func managerNodeHealthFreshness(freshness control.NodeHealthFreshness) string {
	switch freshness {
	case control.NodeHealthFresh:
		return "fresh"
	case control.NodeHealthStale:
		return "stale"
	case control.NodeHealthMissing, "":
		return "missing"
	default:
		return "unknown"
	}
}

func managerNodeRole(roles []control.Role) string {
	if hasRole(roles, control.RoleData) {
		return "data"
	}
	if hasRole(roles, control.RoleController) {
		return "controller_voter"
	}
	return "unknown"
}

func managerControllerRole(nodeID, leaderID uint64, voter bool) string {
	if nodeID != 0 && nodeID == leaderID {
		return "leader"
	}
	if voter {
		return "follower"
	}
	return "none"
}

func hasRole(roles []control.Role, want control.Role) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}
