package management

import (
	"context"
	"fmt"
	"sort"
	"time"

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

// Options configures the manager management usecase.
type Options struct {
	// Cluster reads clusterv2 control state.
	Cluster ControlSnapshotReader
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
	// Logs reads node-local distributed Raft log pages.
	Logs LogReader
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
	cluster                ControlSnapshotReader
	channelRuntimeMeta     ChannelRuntimeMetaReader
	channelBusinessReader  ChannelBusinessReader
	remoteBusinessChannels RemoteBusinessChannelReader
	users                  UserReader
	userOperator           UserOperator
	userPresence           UserPresenceDirectory
	userActions            UserRouteActionDispatcher
	systemUsers            SystemUserOperator
	conversations          ConversationSyncer
	messages               MessageReader
	messageRetention       MessageRetentionOperator
	connections            ConnectionReader
	remoteConnections      RemoteConnectionReader
	logs                   LogReader
	applicationLogs        ApplicationLogReader
	dbInspect              DBInspectReader
	remoteDBInspect        RemoteDBInspectReader
	now                    func() time.Time
}

// New constructs the manager management usecase.
func New(opts Options) *App {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &App{
		cluster:                opts.Cluster,
		channelRuntimeMeta:     opts.ChannelRuntimeMeta,
		channelBusinessReader:  opts.ChannelBusinessReader,
		remoteBusinessChannels: opts.RemoteBusinessChannels,
		users:                  opts.Users,
		userOperator:           opts.UserOperator,
		userPresence:           opts.UserPresence,
		userActions:            opts.UserActions,
		systemUsers:            opts.SystemUsers,
		conversations:          opts.Conversations,
		messages:               opts.Messages,
		messageRetention:       opts.MessageRetention,
		connections:            opts.Connections,
		remoteConnections:      opts.RemoteConnections,
		logs:                   opts.Logs,
		applicationLogs:        opts.ApplicationLogs,
		dbInspect:              opts.DBInspect,
		remoteDBInspect:        opts.RemoteDBInspect,
		now:                    now,
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
	// LeaderCount is the number of preferred Slot leaders hosted by the node.
	LeaderCount int
	// FollowerCount is the number of non-leader desired Slot replicas hosted by the node.
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
	// Unknown means runtime counters could not be read.
	Unknown bool
}

// NodeActions contains backend business capability hints for UI actions.
type NodeActions struct {
	// CanDrain reports whether the node can be marked draining.
	CanDrain bool
	// CanResume reports whether the node can be resumed from draining.
	CanResume bool
	// CanScaleIn reports whether the data-node scale-in flow can be considered.
	CanScaleIn bool
	// CanOnboard reports whether the node can be considered for explicit resource allocation.
	CanOnboard bool
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
	slots := summarizeSlots(snapshot.Slots)
	nodes := make([]Node, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes = append(nodes, buildNode(nodeBuildOptions{
			node:               node,
			slots:              slots,
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

func summarizeSlots(assignments []control.SlotAssignment) slotSummary {
	summary := slotSummary{replicas: map[uint64]int{}, leaders: map[uint64]int{}}
	for _, assignment := range assignments {
		for _, nodeID := range assignment.DesiredPeers {
			summary.replicas[nodeID]++
		}
		if assignment.PreferredLeader != 0 {
			summary.leaders[assignment.PreferredLeader]++
		}
	}
	return summary
}

type nodeBuildOptions struct {
	node               control.Node
	slots              slotSummary
	localNodeID        uint64
	controllerLeaderID uint64
	generatedAt        time.Time
}

func buildNode(opts nodeBuildOptions) Node {
	status := managerNodeStatus(opts.node.Status)
	role := managerNodeRole(opts.node.Roles)
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
		CapacityWeight:  1,
		Membership: NodeMembership{
			Role:        role,
			JoinState:   "active",
			Schedulable: role == "data" && status == "alive",
		},
		Health: NodeHealth{
			Status:          status,
			LastHeartbeatAt: opts.generatedAt,
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
		Runtime: NodeRuntimeSummary{
			NodeID:             opts.node.NodeID,
			SessionsByListener: map[string]int{},
			Unknown:            true,
		},
		Actions: NodeActions{},
	}
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
