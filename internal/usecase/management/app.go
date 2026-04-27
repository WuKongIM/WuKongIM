package management

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
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
}

// ChannelRuntimeMetaReader exposes authoritative slot-level channel runtime pages.
type ChannelRuntimeMetaReader interface {
	// ScanChannelRuntimeMetaSlotPage returns one authoritative page for a physical slot.
	ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
	// GetChannelRuntimeMeta returns one authoritative channel runtime metadata record.
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

// MessageReader exposes authoritative channel message page reads.
type MessageReader interface {
	// QueryMessages returns one authoritative message page for a channel.
	QueryMessages(ctx context.Context, req MessageQueryRequest) (MessageQueryPage, error)
}

// RuntimeSummaryReader reads local or remote node runtime state needed by scale-in safety checks.
type RuntimeSummaryReader interface {
	// NodeRuntimeSummary returns online and gateway counters for one cluster node.
	NodeRuntimeSummary(ctx context.Context, nodeID uint64) (NodeRuntimeSummary, error)
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
	// ChannelRuntimeMeta provides authoritative slot-level runtime meta pages.
	ChannelRuntimeMeta ChannelRuntimeMetaReader
	// Messages provides authoritative channel message pages.
	Messages MessageReader
	// Now returns the current time for manager aggregations.
	Now func() time.Time
}

// App serves manager-oriented read usecases.
type App struct {
	localNodeID              uint64
	controllerPeerIDs        map[uint64]struct{}
	slotReplicaN             int
	scaleInRuntimeViewMaxAge time.Duration
	cluster                  ClusterReader
	online                   online.Registry
	runtimeSummary           RuntimeSummaryReader
	channelRuntimeMeta       ChannelRuntimeMetaReader
	messages                 MessageReader
	now                      func() time.Time
}

// New constructs the management usecase app.
func New(opts Options) *App {
	peers := make(map[uint64]struct{}, len(opts.ControllerPeerIDs))
	for _, nodeID := range opts.ControllerPeerIDs {
		if nodeID == 0 {
			continue
		}
		peers[nodeID] = struct{}{}
	}
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
		slotReplicaN:             opts.SlotReplicaN,
		scaleInRuntimeViewMaxAge: scaleInRuntimeViewMaxAge,
		cluster:                  opts.Cluster,
		online:                   opts.Online,
		runtimeSummary:           opts.RuntimeSummary,
		channelRuntimeMeta:       opts.ChannelRuntimeMeta,
		messages:                 opts.Messages,
		now:                      now,
	}
}
