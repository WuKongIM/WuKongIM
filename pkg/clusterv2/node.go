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
	RuntimeSnapshot(context.Context) (channelv2.RuntimeSnapshot, error)
	RuntimeProbe(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error)
	RuntimeEvict(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error)
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
	// channelDataNodes tracks alive data-role nodes for default ChannelV2 placement.
	channelDataNodes dataNodeView
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
	// pendingRPCHandlers stores public RPC handlers until the default transport exists.
	pendingRPCHandlers map[uint8]clusternet.Handler
	// registeredRPCHandlers records handlers installed on the current default transport.
	registeredRPCHandlers map[uint8]struct{}
	// routeAuthorityWatchers receive best-effort route authority change events.
	routeAuthorityWatchers []chan RouteAuthorityEvent
	// routeAuthorityEpochs tracks observed authority epochs by logical hash slot.
	routeAuthorityEpochs map[uint16]uint64
	proposer             interface {
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
	return n.routeWithAuthorityEpoch(convertRoute(n.router.RouteKey(key)))
}

// RouteHashSlot routes hashSlot using the currently installed route snapshot.
func (n *Node) RouteHashSlot(hashSlot uint16) (Route, error) {
	if err := n.ensureForeground(); err != nil {
		return Route{}, err
	}
	return n.routeWithAuthorityEpoch(convertRoute(n.router.RouteHashSlot(hashSlot)))
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

// RegisterRPC registers a node RPC handler on the default clusterv2 transport.
func (n *Node) RegisterRPC(serviceID uint8, handler NodeRPCHandler) {
	if n == nil || serviceID == 0 || handler == nil {
		return
	}
	wrapped := clusternet.HandlerFunc(handler.HandleRPC)
	n.mu.Lock()
	if n.pendingRPCHandlers == nil {
		n.pendingRPCHandlers = make(map[uint8]clusternet.Handler)
	}
	n.pendingRPCHandlers[serviceID] = wrapped
	server := n.transportServer
	register := false
	if server != nil {
		if n.registeredRPCHandlers == nil {
			n.registeredRPCHandlers = make(map[uint8]struct{})
		}
		if _, ok := n.registeredRPCHandlers[serviceID]; !ok {
			n.registeredRPCHandlers[serviceID] = struct{}{}
			register = true
		}
	}
	n.mu.Unlock()
	if register {
		server.Register(serviceID, wrapped)
	}
}

// CallRPC invokes a node RPC service on a peer node.
func (n *Node) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	n.mu.RLock()
	client := n.transportClient
	n.mu.RUnlock()
	if client == nil {
		return nil, ErrNotStarted
	}
	return client.Call(ctx, nodeID, serviceID, payload)
}

// WatchRouteAuthorities returns a buffered stream of route authority changes.
func (n *Node) WatchRouteAuthorities() <-chan RouteAuthorityEvent {
	ch := make(chan RouteAuthorityEvent, 16)
	if n == nil {
		close(ch)
		return ch
	}
	n.mu.Lock()
	if n.stopping.Load() {
		close(ch)
		n.mu.Unlock()
		return ch
	}
	n.routeAuthorityWatchers = append(n.routeAuthorityWatchers, ch)
	n.mu.Unlock()
	return ch
}

func (n *Node) publishRouteAuthority(authorities ...RouteAuthority) {
	if n == nil || len(authorities) == 0 {
		return
	}
	event := RouteAuthorityEvent{Authorities: append([]RouteAuthority(nil), authorities...)}
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, watcher := range n.routeAuthorityWatchers {
		select {
		case watcher <- event:
		default:
		}
	}
}

func (n *Node) closeRouteAuthorityWatchers() {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, watcher := range n.routeAuthorityWatchers {
		close(watcher)
	}
	n.routeAuthorityWatchers = nil
}

func (n *Node) nextAuthorityEpoch(hashSlot uint16, leaderNodeID uint64) uint64 {
	if n == nil {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.routeAuthorityEpochs == nil {
		n.routeAuthorityEpochs = make(map[uint16]uint64)
	}
	n.routeAuthorityEpochs[hashSlot]++
	return n.routeAuthorityEpochs[hashSlot]
}

func (n *Node) authorityEpochForChange(hashSlot uint16, previous routeAuthorityKey, previousOK bool, current routeAuthorityKey) uint64 {
	if n == nil {
		return 0
	}
	if !previousOK || previous.slotID != current.slotID || previous.leaderNodeID != current.leaderNodeID {
		return n.nextAuthorityEpoch(hashSlot, current.leaderNodeID)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.routeAuthorityEpochs == nil {
		n.routeAuthorityEpochs = make(map[uint16]uint64)
	}
	if n.routeAuthorityEpochs[hashSlot] == 0 {
		n.routeAuthorityEpochs[hashSlot] = 1
	}
	return n.routeAuthorityEpochs[hashSlot]
}

func (n *Node) routeWithAuthorityEpoch(route Route, err error) (Route, error) {
	if err != nil || n == nil {
		return route, err
	}
	n.mu.RLock()
	route.AuthorityEpoch = n.routeAuthorityEpochs[route.HashSlot]
	n.mu.RUnlock()
	return route, nil
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

// ReadChannelCommitted reads locally committed channel messages from the Node-created ChannelV2 store.
func (n *Node) ReadChannelCommitted(ctx context.Context, id channelv2.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	if n.defaultChannelStore == nil {
		return channelstore.ReadCommittedResult{}, ErrNotStarted
	}
	store, err := n.defaultChannelStore.ChannelStore(channelv2.ChannelKeyForID(id), id)
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	return store.ReadCommitted(ctx, req)
}
