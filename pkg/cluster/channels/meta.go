package channels

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// ChannelMetaSource resolves authoritative Channel metadata.
type ChannelMetaSource interface {
	// ResolveChannelMeta returns metadata for id.
	ResolveChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error)
}

// ChannelMetaEnsurer resolves metadata and may create it for append admission.
type ChannelMetaEnsurer interface {
	// EnsureChannelMeta returns metadata for id, creating the initial record when needed.
	EnsureChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error)
}

// RuntimeMetaReader reads authoritative ChannelRuntimeMeta from unified metadata storage.
type RuntimeMetaReader interface {
	// GetChannelRuntimeMeta reads one authoritative runtime metadata record.
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
}

// RuntimeMetaWriter persists authoritative ChannelRuntimeMeta through Slot ownership.
type RuntimeMetaWriter interface {
	// UpsertChannelRuntimeMeta persists one runtime metadata record.
	UpsertChannelRuntimeMeta(context.Context, metadb.ChannelRuntimeMeta) error
}

// RuntimeMetaCreateResult reports whether the authoritative create inserted the row.
type RuntimeMetaCreateResult struct {
	// HashSlot and identity bind Created to one requested row.
	HashSlot    uint16
	ChannelID   string
	ChannelType int64
	// Created is true only when the authoritative Slot apply inserted the row;
	// false is a successful concurrent-create loser result.
	Created bool
}

// RuntimeMetaCreateItem is one logical create owned by a physical Slot batch.
type RuntimeMetaCreateItem struct {
	// HashSlot is the logical shard owning Meta.
	HashSlot uint16
	// Meta is the normalized create-only candidate.
	Meta metadb.ChannelRuntimeMeta
}

// RuntimeMetaReadResult is one aligned authoritative batch reread outcome.
type RuntimeMetaReadResult struct {
	Meta metadb.ChannelRuntimeMeta
	Err  error
}

// RuntimeMetaBatchRouter supplies one-snapshot routes for bounded create batches.
type RuntimeMetaBatchRouter interface {
	RouteKey(string) (routing.Route, error)
	RouteKeys([]string) ([]routing.Route, error)
}

// RuntimeMetaBatchStore commits and rereads one physical Slot metadata batch.
type RuntimeMetaBatchStore interface {
	CreateChannelRuntimeMetaBatch(context.Context, routing.Route, []RuntimeMetaCreateItem) ([]RuntimeMetaCreateResult, error)
	BatchGetChannelRuntimeMetas(context.Context, routing.Route, []RuntimeMetaCreateItem) ([]RuntimeMetaReadResult, error)
}

// MetaCreateBatchObserver receives bounded coalescer state and batch outcomes.
type MetaCreateBatchObserver interface {
	ObserveChannelMetaCreateCoalesced(slotID uint32)
	SetChannelMetaCreateQueueDepth(slotID uint32, depth int)
	ObserveChannelMetaCreateBatch(slotID uint32, result string, items int)
}

// MetaCreateResult is the closed authoritative outcome vocabulary for initial metadata creation.
type MetaCreateResult string

const (
	// MetaCreateCreated means the authoritative Slot apply inserted the row.
	MetaCreateCreated MetaCreateResult = "created"
	// MetaCreateAlreadyExisting means another authoritative create already inserted the row.
	MetaCreateAlreadyExisting MetaCreateResult = "already_existing"
	// MetaCreateError means proposal, apply, or result decoding failed.
	MetaCreateError MetaCreateResult = "error"
)

// MetaCreateObserver receives one outcome after the authoritative Slot proposal resolves.
type MetaCreateObserver interface {
	// ObserveChannelMetaCreate records the route's logical Slot Raft Group ID and closed create outcome.
	ObserveChannelMetaCreate(slotID uint32, result MetaCreateResult)
}

// ChannelPlacement describes the initial Channel data-plane placement.
type ChannelPlacement struct {
	// Leader is the initial Channel leader.
	Leader ch.NodeID
	// Replicas are the initial Channel replicas.
	Replicas []ch.NodeID
	// MinISR is the initial write quorum size.
	MinISR int
}

// ChannelPlacementResolver resolves first-append data placement after Slot route readiness.
type ChannelPlacementResolver interface {
	// ResolveChannelPlacement returns the initial placement for id.
	ResolveChannelPlacement(context.Context, ch.ChannelID) (ChannelPlacement, error)
}

// ChannelPlacementBatchResolver derives aligned placements from the exact
// one-snapshot routes selected for a submitted create batch.
type ChannelPlacementBatchResolver interface {
	ResolveChannelPlacementBatch(context.Context, []ch.ChannelID, []routing.Route) ([]ChannelPlacement, error)
}

// PlacementRouter routes channel IDs to their authoritative Slot placement.
type PlacementRouter interface {
	// RouteKey returns the current route for key.
	RouteKey(string) (routing.Route, error)
}

// DataNodeProvider returns active data-node candidates and the exact control
// revision from which they were derived for initial Channel placement.
type DataNodeProvider interface {
	PlacementDataNodes(context.Context, uint64) ([]uint64, error)
}

// SlotMetaSourceOptions configures first-append metadata creation.
type SlotMetaSourceOptions struct {
	// DefaultReplicas are the initial Channel replicas when metadata is missing.
	DefaultReplicas []ch.NodeID
	// DefaultMinISR is the initial write quorum; defaults to 1 when replicas exist.
	DefaultMinISR int
	// Placement resolves an entire submitted create batch from one current
	// data-node/control snapshot after Slot route revalidation.
	Placement ChannelPlacementBatchResolver
	// Router and BatchStore enable the node-owned bounded create coalescer.
	// They must be supplied together for any create-capable source.
	Router     RuntimeMetaBatchRouter
	BatchStore RuntimeMetaBatchStore
	// BatchObserver receives low-cardinality coalescer metrics.
	BatchObserver MetaCreateBatchObserver
	// Goroutines supervises the lazily created per-Slot batch owners.
	Goroutines *goruntimeregistry.Registry
	// Observer receives low-cardinality metadata resolve stage metrics.
	Observer AppendStageObserver
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

var _ ChannelMetaSource = (*SlotMetaSource)(nil)
var _ ChannelMetaEnsurer = (*SlotMetaSource)(nil)
var _ ChannelMetaEnsurer = (*StaticMetaSource)(nil)
