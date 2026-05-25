package clusterv2

import (
	"context"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/internal/lifecycle"
)

// Option customizes Node construction.
type Option func(*Node)

// Node is the clusterv2 lifecycle root and public runtime facade.
type Node struct {
	cfg       Config
	resources []lifecycle.NamedResource
	group     lifecycle.Group
	started   atomic.Bool
	stopping  atomic.Bool
}

// New validates cfg and creates a clusterv2 node shell.
func New(cfg Config, opts ...Option) (*Node, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	node := &Node{cfg: cfg}
	for _, opt := range opts {
		if opt != nil {
			opt(node)
		}
	}
	return node, nil
}

func withResources(resources ...lifecycle.NamedResource) Option {
	return func(n *Node) {
		n.resources = append([]lifecycle.NamedResource(nil), resources...)
	}
}

// Start starts the node runtime. Later tasks wire concrete resources behind this shell.
func (n *Node) Start(ctx context.Context) error {
	if n == nil {
		return ErrNotStarted
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	n.stopping.Store(false)
	if err := n.group.Start(ctx, n.resources...); err != nil {
		n.started.Store(false)
		return err
	}
	n.started.Store(true)
	return nil
}

// Stop stops the node runtime and rejects new foreground work.
func (n *Node) Stop(ctx context.Context) error {
	if n == nil {
		return nil
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	n.stopping.Store(true)
	err := n.group.Stop(ctx)
	n.started.Store(false)
	return err
}

// NodeID returns this node's stable cluster identity.
func (n *Node) NodeID() uint64 {
	if n == nil {
		return 0
	}
	return n.cfg.NodeID
}

// Snapshot returns the latest locally visible clusterv2 readiness summary.
func (n *Node) Snapshot() Snapshot {
	if n == nil {
		return Snapshot{}
	}
	return Snapshot{NodeID: n.cfg.NodeID}
}

// RouteKey routes key using the currently installed route snapshot.
func (n *Node) RouteKey(key string) (Route, error) {
	if err := n.ensureForeground(); err != nil {
		return Route{}, err
	}
	return Route{}, ErrRouteNotReady
}

// RouteHashSlot routes hashSlot using the currently installed route snapshot.
func (n *Node) RouteHashSlot(hashSlot uint16) (Route, error) {
	if err := n.ensureForeground(); err != nil {
		return Route{}, err
	}
	return Route{}, ErrRouteNotReady
}

// Propose submits a Slot metadata command through clusterv2 routing.
func (n *Node) Propose(ctx context.Context, req ProposeRequest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := n.ensureForeground(); err != nil {
		return err
	}
	return ErrNotStarted
}

// AppendChannel appends one message through the hosted ChannelV2 service.
func (n *Node) AppendChannel(ctx context.Context, req channelv2.AppendRequest) (channelv2.AppendResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.AppendResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.AppendResult{}, err
	}
	return channelv2.AppendResult{}, ErrNotStarted
}

// AppendChannelBatch appends a batch of messages through the hosted ChannelV2 service.
func (n *Node) AppendChannelBatch(ctx context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.AppendBatchResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.AppendBatchResult{}, err
	}
	return channelv2.AppendBatchResult{}, ErrNotStarted
}

// FetchChannel fetches committed messages through the hosted ChannelV2 service.
func (n *Node) FetchChannel(ctx context.Context, req channelv2.FetchRequest) (channelv2.FetchResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.FetchResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.FetchResult{}, err
	}
	return channelv2.FetchResult{}, ErrNotStarted
}

func (n *Node) ensureForeground() error {
	if n == nil {
		return ErrNotStarted
	}
	if n.stopping.Load() {
		return ErrStopping
	}
	if !n.started.Load() {
		return ErrNotStarted
	}
	return nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
