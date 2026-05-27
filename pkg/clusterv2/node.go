package clusterv2

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/internal/lifecycle"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Option customizes Node construction.
type Option func(*Node)

type slotReconciler interface {
	Reconcile(context.Context, control.Snapshot) error
}

type channelService interface {
	Append(context.Context, channelv2.AppendRequest) (channelv2.AppendResult, error)
	AppendBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error)
	Tick(context.Context) error
	Close() error
}

// Node is the clusterv2 lifecycle root and public runtime facade.
type Node struct {
	cfg             Config
	resources       []lifecycle.NamedResource
	control         control.Controller
	router          *routing.Router
	discovery       *clusternet.Discovery
	transportServer *clusternet.TransportServer
	transportClient *clusternet.TransportClient
	slots           slotReconciler
	channels        channelService
	// defaultControl reports whether Node constructed the Controller runtime.
	defaultControl bool
	// defaultTransport reports whether Node constructed the node RPC transport.
	defaultTransport bool
	// defaultChannels reports whether Node constructed channels during Start.
	defaultChannels bool
	// defaultChannelStore owns the Node-created message DB factory.
	defaultChannelStore *channelstore.MessageDBFactory
	// defaultSlots reports whether Node constructed the local Slot runtime.
	defaultSlots bool
	// defaultSlotRuntime owns the Node-created Slot Multi-Raft runtime.
	defaultSlotRuntime *multiraft.Runtime
	// defaultSlotRaftDB owns the Node-created Slot Raft log store.
	defaultSlotRaftDB *raftlog.DB
	// defaultSlotMetaDB owns the Node-created Slot metadata store.
	defaultSlotMetaDB *metadb.DB
	// defaultSlotProposer adapts the default Slot runtime to the propose service.
	defaultSlotProposer propose.SlotRuntime
	proposer            interface {
		Propose(context.Context, propose.Request) error
	}
	group lifecycle.Group

	mu                sync.RWMutex
	snapshot          Snapshot
	controlSnapshot   control.Snapshot
	watchCancel       context.CancelFunc
	channelTickCancel context.CancelFunc
	channelTickWG     sync.WaitGroup
	slotLeaderCancel  context.CancelFunc
	slotLeaderWG      sync.WaitGroup
	started           atomic.Bool
	stopping          atomic.Bool
}

// New validates cfg and creates a clusterv2 node.
func New(cfg Config, opts ...Option) (*Node, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	node := &Node{cfg: cfg, router: routing.NewRouter(), discovery: clusternet.NewDiscovery(), snapshot: Snapshot{NodeID: cfg.NodeID}}
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

func withController(controller control.Controller) Option {
	return func(n *Node) { n.control = controller }
}

func withSlotReconciler(reconciler slotReconciler) Option {
	return func(n *Node) { n.slots = reconciler }
}

// WithChannels overrides the default ChannelV2 service hosted by Node.
func WithChannels(service *channels.Service) Option {
	return func(n *Node) {
		n.channels = service
		n.defaultChannels = false
	}
}

// WithProposer overrides the default Slot propose service used by Node.Propose.
func WithProposer(proposer interface {
	Propose(context.Context, propose.Request) error
}) Option {
	return func(n *Node) { n.proposer = proposer }
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
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.snapshot
}

// RouteKey routes key using the currently installed route snapshot.
func (n *Node) RouteKey(key string) (Route, error) {
	if err := n.ensureForeground(); err != nil {
		return Route{}, err
	}
	return convertRoute(n.router.RouteKey(key))
}

// RouteHashSlot routes hashSlot using the currently installed route snapshot.
func (n *Node) RouteHashSlot(hashSlot uint16) (Route, error) {
	if err := n.ensureForeground(); err != nil {
		return Route{}, err
	}
	return convertRoute(n.router.RouteHashSlot(hashSlot))
}

// Propose submits a Slot metadata command through clusterv2 routing.
func (n *Node) Propose(ctx context.Context, req ProposeRequest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := n.ensureForeground(); err != nil {
		return err
	}
	if n.proposer == nil {
		return ErrNotStarted
	}
	return n.proposer.Propose(ctx, propose.Request{
		Key:     req.Key,
		Command: req.Command,
		Target:  propose.Target{HashSlot: req.Target.HashSlot, HasHashSlot: req.Target.HasHashSlot, SlotID: req.Target.SlotID, HasSlotID: req.Target.HasSlotID},
	})
}

// AppendChannel appends one message through the hosted ChannelV2 service.
func (n *Node) AppendChannel(ctx context.Context, req channelv2.AppendRequest) (channelv2.AppendResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.AppendResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.AppendResult{}, err
	}
	if n.channels == nil {
		return channelv2.AppendResult{}, ErrNotStarted
	}
	return n.channels.Append(ctx, req)
}

// AppendChannelBatch appends a batch of messages through the hosted ChannelV2 service.
func (n *Node) AppendChannelBatch(ctx context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.AppendBatchResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.AppendBatchResult{}, err
	}
	if n.channels == nil {
		return channelv2.AppendBatchResult{}, ErrNotStarted
	}
	return n.channels.AppendBatch(ctx, req)
}
