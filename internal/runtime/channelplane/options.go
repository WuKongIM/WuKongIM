package channelplane

import (
	"context"
	"runtime"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	defaultReactorInboxSize     = 1024
	defaultMaxPendingPerChannel = 1024
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

// RemoteAppender is the temporary compatibility bridge for remote channel leaders.
type RemoteAppender interface {
	AppendRemoteBatch(ctx context.Context, nodeID channel.NodeID, req channel.AppendBatchRequest, route ChannelRoute) (channel.AppendBatchResult, error)
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
	// Resolver loads authoritative channel write routes.
	Resolver RouteResolver
	// LocalOwner appends when this node is the channel leader.
	LocalOwner LocalOwnerAppender
	// RemoteAppender temporarily forwards appends to remote channel leaders until peer reactors own RPC batching.
	RemoteAppender RemoteAppender
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
	if o.Now == nil {
		o.Now = time.Now
	}
}

func (o Options) validate() error {
	if o.LocalNode == 0 || o.ReactorCount <= 0 || o.ReactorInboxSize <= 0 || o.MaxPendingPerChannel <= 0 || o.Resolver == nil || o.LocalOwner == nil {
		return channel.ErrInvalidConfig
	}
	return nil
}
