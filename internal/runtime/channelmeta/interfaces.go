package channelmeta

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// MetaReader reads one authoritative channel runtime metadata record.
type MetaReader interface {
	// GetChannelRuntimeMeta reads one authoritative runtime metadata record.
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

// MetaLister lists authoritative channel runtime metadata records.
type MetaLister interface {
	// ListChannelRuntimeMeta lists authoritative runtime metadata visible to this node.
	ListChannelRuntimeMeta(ctx context.Context) ([]metadb.ChannelRuntimeMeta, error)
}

// MetaSource reads authoritative channel runtime metadata.
type MetaSource interface {
	MetaReader
	MetaLister
}

// BootstrapStore persists metadata created by bootstrap flows.
type BootstrapStore interface {
	MetaReader
	// UpsertChannelRuntimeMeta writes an authoritative runtime metadata record.
	UpsertChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) error
}

// RepairStore persists metadata when this node is the authoritative slot leader.
type RepairStore interface {
	MetaReader
	// UpsertChannelRuntimeMetaIfLocalLeader writes metadata only when this node is the local authoritative slot leader.
	UpsertChannelRuntimeMetaIfLocalLeader(ctx context.Context, meta metadb.ChannelRuntimeMeta) error
}

// HashSlotTableVersionSource reports the currently observed hash-slot table version.
type HashSlotTableVersionSource interface {
	// HashSlotTableVersion returns the cluster hash-slot table version seen by the source.
	HashSlotTableVersion() uint64
}

// BootstrapCluster provides cluster topology needed to bootstrap missing runtime metadata.
type BootstrapCluster interface {
	// SlotForKey maps a channel key to its authoritative slot.
	SlotForKey(key string) multiraft.SlotID
	// PeersForSlot returns the replica set configured for a slot.
	PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID
	// LeaderOf returns the current authoritative slot leader.
	LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
}

// RepairCluster provides cluster routing needed for authoritative leader repair.
type RepairCluster interface {
	// SlotForKey maps a channel key to its authoritative slot.
	SlotForKey(key string) multiraft.SlotID
	// LeaderOf returns the current authoritative slot leader.
	LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
	// IsLocal reports whether the provided node ID is this process.
	IsLocal(nodeID multiraft.NodeID) bool
}

// LivenessSource reads controller-observed node liveness without local fallback.
type LivenessSource interface {
	// ListNodesStrict returns the controller leader's node snapshot.
	ListNodesStrict(ctx context.Context) ([]controllermeta.ClusterNode, error)
}

// RoutingRuntime applies cluster-visible channel routing metadata.
type RoutingRuntime interface {
	// ApplyRoutingMeta applies channel metadata to the routing layer.
	ApplyRoutingMeta(meta channel.Meta) error
}

// LocalRuntime manages node-local channel runtime state.
type LocalRuntime interface {
	// EnsureLocalRuntime ensures a local runtime exists for the channel metadata.
	EnsureLocalRuntime(meta channel.Meta) error
	// RemoveLocalRuntime removes node-local runtime for a channel key.
	RemoveLocalRuntime(key channel.ChannelKey) error
	// Channel returns the active local channel observer.
	Channel(key channel.ChannelKey) (ChannelObserver, bool)
}

// ChannelObserver exposes read-only channel state needed by channelmeta flows.
type ChannelObserver interface {
	// Meta returns the currently applied channel metadata.
	Meta() channel.Meta
	// Status returns the current local replica status.
	Status() channel.ReplicaState
}

// Runtime applies both routing and node-local channel runtime metadata.
type Runtime interface {
	RoutingRuntime
	LocalRuntime
}

// Refresher refreshes authoritative channel metadata into routing and local runtime views.
type Refresher interface {
	// RefreshChannelMeta refreshes one channel's authoritative runtime metadata.
	RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
}

// Activator loads channel metadata on demand for channel runtime paths.
type Activator interface {
	// ActivateByID activates a channel by protocol channel identity.
	ActivateByID(ctx context.Context, id channel.ChannelID, source channelruntime.ActivationSource) (channel.Meta, error)
	// ActivateByKey activates a channel by node-local channel key.
	ActivateByKey(ctx context.Context, key channel.ChannelKey, source channelruntime.ActivationSource) (channel.Meta, error)
}

// Bootstrapper creates or renews authoritative channel runtime metadata.
type Bootstrapper interface {
	// EnsureChannelRuntimeMeta creates missing authoritative runtime metadata when needed.
	EnsureChannelRuntimeMeta(ctx context.Context, id channel.ChannelID) (metadb.ChannelRuntimeMeta, bool, error)
	// RenewChannelLeaderLease renews a live local leader lease before it expires.
	RenewChannelLeaderLease(ctx context.Context, meta metadb.ChannelRuntimeMeta, localNode uint64, renewBefore time.Duration) (metadb.ChannelRuntimeMeta, bool, error)
}

// Repairer repairs authoritative channel leader metadata when observations prove it is stale.
type Repairer interface {
	// RepairIfNeeded repairs metadata when the caller's reason still applies.
	RepairIfNeeded(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error)
}

// AuthoritativeRepairer handles repair requests on the authoritative slot leader.
type AuthoritativeRepairer interface {
	// RepairChannelLeaderAuthoritative repairs the channel leader from the authoritative slot leader.
	RepairChannelLeaderAuthoritative(ctx context.Context, req LeaderRepairRequest) (LeaderRepairResult, error)
}

// RepairRemote calls peer nodes for channel leader repair and candidate evaluation.
type RepairRemote interface {
	// RepairChannelLeader asks the current slot leader to repair channel leader metadata.
	RepairChannelLeader(ctx context.Context, req LeaderRepairRequest) (LeaderRepairResult, error)
	// EvaluateChannelLeaderCandidate asks a peer replica to dry-run channel leader promotion.
	EvaluateChannelLeaderCandidate(ctx context.Context, nodeID uint64, req LeaderEvaluateRequest) (LeaderPromotionReport, error)
}

// LocalEvaluator evaluates whether local durable channel state can safely lead.
type LocalEvaluator interface {
	// EvaluateChannelLeaderCandidate dry-runs leader promotion for the local replica.
	EvaluateChannelLeaderCandidate(ctx context.Context, req LeaderEvaluateRequest) (LeaderPromotionReport, error)
}
