package channelplane

import (
	"context"
	"runtime"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

const (
	defaultReactorInboxSize     = 1024
	defaultMaxPendingPerChannel = 1024
	defaultPeerLaneCount        = 1
	defaultPeerBatchMaxWait     = 500 * time.Microsecond
	defaultPeerBatchMaxRecords  = 128
	defaultPeerBatchMaxBytes    = 256 * 1024
	defaultPeerMaxPending       = 1024
	defaultPeerMaxInflightRPC   = 1
	defaultEffectWorkerCount    = 0
	defaultEffectQueueSize      = 1024
	defaultCellIdleTTL          = 10 * time.Minute
	defaultCellSweepEvery       = time.Minute
)

// RouteResolver resolves and invalidates authoritative channel write routes.
type RouteResolver interface {
	ResolveRoute(ctx context.Context, id channel.ChannelID) (ChannelRoute, error)
	InvalidateRoute(id channel.ChannelID, routeGeneration uint64)
}

// LocalOwnerAppender appends to a channel leader owned by this process.
type LocalOwnerAppender interface {
	AppendLocalBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error)
}

// PeerClient sends batched append envelopes to one remote cluster peer.
type PeerClient interface {
	AppendBatches(ctx context.Context, nodeID channel.NodeID, req AppendBatchesRequest) (AppendBatchesResponse, error)
}

// Observer receives lightweight channel plane lifecycle and effect signals.
type Observer interface {
	OnAppendQueued(AppendEvent)
	OnAppendCompleted(AppendEvent)
}

// Options configures the channel append reactor plane.
type Options struct {
	// LocalNode is this process' cluster node ID.
	LocalNode channel.NodeID
	// ReactorCount is the number of channel-keyed reactor shards.
	ReactorCount int
	// ReactorInboxSize bounds each reactor command inbox.
	ReactorInboxSize int
	// MaxPendingPerChannel bounds queued append requests for one channel cell.
	MaxPendingPerChannel int
	// PeerLaneCount is the number of remote batching lanes per target peer.
	PeerLaneCount int
	// PeerBatchMaxWait bounds how long a peer lane waits before flushing a partial batch.
	PeerBatchMaxWait time.Duration
	// PeerRPCTimeout bounds one remote AppendBatches RPC. Zero leaves peer RPCs bound by caller or parent context only.
	PeerRPCTimeout time.Duration
	// PeerBatchMaxRecords bounds one remote append RPC by number of channel batches.
	PeerBatchMaxRecords int
	// PeerBatchMaxBytes bounds one remote append RPC by estimated serialized size.
	PeerBatchMaxBytes int
	// PeerMaxPending bounds queued and inflight appends per peer lane.
	PeerMaxPending int
	// EffectWorkerCount bounds concurrent route and local append effects. Zero uses a CPU-aware default.
	EffectWorkerCount int
	// EffectQueueSize bounds queued route and local append effects before backpressure.
	EffectQueueSize int
	// CellIdleTTL bounds how long an idle channel cell keeps cached route state before it can be evicted.
	CellIdleTTL time.Duration
	// CellSweepEvery controls how often each reactor scans idle channel cells for eviction.
	CellSweepEvery time.Duration
	// Resolver loads authoritative channel write routes.
	Resolver RouteResolver
	// LocalOwner appends when this node is the channel leader.
	LocalOwner LocalOwnerAppender
	// PeerClient sends remote owner append batches.
	PeerClient PeerClient
	// Observer receives optional metrics and diagnostics callbacks.
	Observer Observer
	// Now returns the current wall clock for metrics and tests.
	Now func() time.Time
}

func (o *Options) setDefaults() {
	if o.ReactorCount <= 0 {
		o.ReactorCount = max(4, runtime.GOMAXPROCS(0))
	}
	if o.ReactorInboxSize <= 0 {
		o.ReactorInboxSize = defaultReactorInboxSize
	}
	if o.MaxPendingPerChannel <= 0 {
		o.MaxPendingPerChannel = defaultMaxPendingPerChannel
	}
	if o.PeerLaneCount <= 0 {
		o.PeerLaneCount = defaultPeerLaneCount
	}
	if o.PeerBatchMaxWait <= 0 {
		o.PeerBatchMaxWait = defaultPeerBatchMaxWait
	}
	if o.PeerBatchMaxRecords <= 0 {
		o.PeerBatchMaxRecords = defaultPeerBatchMaxRecords
	}
	if o.PeerBatchMaxBytes <= 0 {
		o.PeerBatchMaxBytes = defaultPeerBatchMaxBytes
	}
	if o.PeerMaxPending <= 0 {
		o.PeerMaxPending = defaultPeerMaxPending
	}
	if o.EffectWorkerCount <= 0 {
		o.EffectWorkerCount = max(4, runtime.GOMAXPROCS(0))
	}
	if o.EffectQueueSize <= 0 {
		o.EffectQueueSize = defaultEffectQueueSize
	}
	if o.CellIdleTTL <= 0 {
		o.CellIdleTTL = defaultCellIdleTTL
	}
	if o.CellSweepEvery <= 0 {
		o.CellSweepEvery = defaultCellSweepEvery
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

func (o Options) validate() error {
	if o.LocalNode == 0 || o.ReactorCount <= 0 || o.ReactorInboxSize <= 0 || o.MaxPendingPerChannel <= 0 || o.PeerLaneCount <= 0 || o.PeerBatchMaxWait <= 0 || o.PeerBatchMaxRecords <= 0 || o.PeerBatchMaxBytes <= 0 || o.PeerMaxPending <= 0 || o.EffectWorkerCount <= 0 || o.EffectQueueSize <= 0 || o.CellIdleTTL <= 0 || o.CellSweepEvery <= 0 || o.Resolver == nil || o.LocalOwner == nil {
		return channel.ErrInvalidConfig
	}
	return nil
}
