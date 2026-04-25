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
	ControllerLeaderID() uint64
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

// Options configures the management usecase app.
type Options struct {
	// LocalNodeID is the node ID of the current process.
	LocalNodeID uint64
	// ControllerPeerIDs lists the configured controller peer node IDs.
	ControllerPeerIDs []uint64
	// Cluster provides distributed cluster read access.
	Cluster ClusterReader
	// Online provides local online connection reads.
	Online online.Registry
	// ChannelRuntimeMeta provides authoritative slot-level runtime meta pages.
	ChannelRuntimeMeta ChannelRuntimeMetaReader
	// Messages provides authoritative channel message pages.
	Messages MessageReader
	// Now returns the current time for manager aggregations.
	Now func() time.Time
}

// App serves manager-oriented read usecases.
type App struct {
	localNodeID        uint64
	controllerPeerIDs  map[uint64]struct{}
	cluster            ClusterReader
	online             online.Registry
	channelRuntimeMeta ChannelRuntimeMetaReader
	messages           MessageReader
	now                func() time.Time
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
	return &App{
		localNodeID:        opts.LocalNodeID,
		controllerPeerIDs:  peers,
		cluster:            opts.Cluster,
		online:             opts.Online,
		channelRuntimeMeta: opts.ChannelRuntimeMeta,
		messages:           opts.Messages,
		now:                now,
	}
}
