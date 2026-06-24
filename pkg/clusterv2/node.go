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

type taskExecutor interface {
	Reconcile(context.Context, control.Snapshot) error
}

type channelService interface {
	Append(context.Context, channelv2.AppendRequest) (channelv2.AppendResult, error)
	AppendBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error)
	ResolveAppendAuthority(context.Context, channelv2.ChannelID) (channelv2.Meta, error)
	ReadChannelLastVisible(context.Context, channelv2.ChannelID, uint64) (channelv2.Message, bool, error)
	RetentionView(context.Context, channelv2.ChannelID) (channelv2.RetentionView, error)
	ApplyRetentionBoundary(context.Context, channelv2.RetentionApplyRequest) (channelv2.RetentionApplyResult, error)
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
	tasks           taskExecutor
	channels        channelService
	// channelDataNodes tracks active data-role nodes for default ChannelV2 placement.
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
	// routeAuthorityPublished tracks the last authority identity published per logical hash slot.
	routeAuthorityPublished map[uint16]routeAuthorityKey
	proposer                interface {
		Propose(context.Context, propose.Request) error
	}
	group lifecycle.Group

	mu                     sync.RWMutex
	snapshot               Snapshot
	controlSnapshot        control.Snapshot
	watchCancel            context.CancelFunc
	watchWG                sync.WaitGroup
	channelTickCancel      context.CancelFunc
	channelTickWG          sync.WaitGroup
	channelRetentionCancel context.CancelFunc
	channelRetentionWG     sync.WaitGroup
	channelRetentionGCMu   sync.Mutex
	channelRetentionCursor channelv2.ChannelKey
	slotLeaderCancel       context.CancelFunc
	slotLeaderWG           sync.WaitGroup
	started                atomic.Bool
	stopping               atomic.Bool
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

func withTaskExecutor(executor taskExecutor) Option {
	return func(n *Node) { n.tasks = executor }
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

// RouteKeys routes keys using the currently installed route snapshot and preserves input order.
func (n *Node) RouteKeys(keys []string) ([]Route, error) {
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	routes, err := n.router.RouteKeys(keys)
	if err != nil {
		return nil, mapRouteError(err)
	}
	out := make([]Route, len(routes))
	for i, route := range routes {
		converted, err := convertRoute(route, nil)
		if err != nil {
			return nil, err
		}
		out[i] = converted
	}
	n.mu.RLock()
	for i := range out {
		out[i].AuthorityEpoch = n.routeAuthorityEpochs[out[i].HashSlot]
	}
	n.mu.RUnlock()
	return out, nil
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
	n.recordPublishedRouteAuthority(authorities)
	n.dispatchRouteAuthority(authorities...)
}

func (n *Node) publishRouteAuthorityChanges(before, after *routing.Table) {
	if n == nil {
		return
	}
	n.dispatchRouteAuthority(n.routeAuthorityChangesForPublish(before, after)...)
}

func (n *Node) dispatchRouteAuthority(authorities ...RouteAuthority) {
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

func (n *Node) routeAuthorityChangesForPublish(before, after *routing.Table) []RouteAuthority {
	if n == nil || after == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.routeAuthorityPublished == nil {
		n.routeAuthorityPublished = make(map[uint16]routeAuthorityKey)
	}
	out := make([]RouteAuthority, 0)
	for hashSlot, slotID := range after.HashToSlot {
		if slotID == 0 {
			continue
		}
		hashSlotID := uint16(hashSlot)
		current := routeAuthorityKey{slotID: slotID, leaderNodeID: after.SlotLeaders[slotID], leaderTerm: after.SlotLeaderTerms[slotID], configEpoch: after.SlotConfigEpochs[slotID], revision: after.Revision}
		previous, ok := routeAuthorityFromTable(before, hashSlotID)
		if ok && previous == current {
			continue
		}
		if published, ok := n.routeAuthorityPublished[hashSlotID]; ok && sameRouteAuthorityIdentity(published, current) {
			continue
		}
		n.routeAuthorityPublished[hashSlotID] = current
		out = append(out, RouteAuthority{
			HashSlot:       hashSlotID,
			SlotID:         current.slotID,
			LeaderNodeID:   current.leaderNodeID,
			LeaderTerm:     current.leaderTerm,
			ConfigEpoch:    current.configEpoch,
			RouteRevision:  current.revision,
			AuthorityEpoch: n.authorityEpochForChangeLocked(hashSlotID, previous, ok, current),
		})
	}
	return out
}

func (n *Node) recordPublishedRouteAuthority(authorities []RouteAuthority) {
	if n == nil || len(authorities) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.routeAuthorityPublished == nil {
		n.routeAuthorityPublished = make(map[uint16]routeAuthorityKey)
	}
	for _, authority := range authorities {
		n.routeAuthorityPublished[authority.HashSlot] = routeAuthorityKey{
			slotID:       authority.SlotID,
			leaderNodeID: authority.LeaderNodeID,
			leaderTerm:   authority.LeaderTerm,
			configEpoch:  authority.ConfigEpoch,
			revision:     authority.RouteRevision,
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
	return n.nextAuthorityEpochLocked(hashSlot)
}

func (n *Node) authorityEpochForChange(hashSlot uint16, previous routeAuthorityKey, previousOK bool, current routeAuthorityKey) uint64 {
	if n == nil {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.authorityEpochForChangeLocked(hashSlot, previous, previousOK, current)
}

func (n *Node) authorityEpochForChangeLocked(hashSlot uint16, previous routeAuthorityKey, previousOK bool, current routeAuthorityKey) uint64 {
	if !previousOK || !sameRouteAuthorityIdentity(previous, current) {
		return n.nextAuthorityEpochLocked(hashSlot)
	}
	if n.routeAuthorityEpochs == nil {
		n.routeAuthorityEpochs = make(map[uint16]uint64)
	}
	if n.routeAuthorityEpochs[hashSlot] == 0 {
		n.routeAuthorityEpochs[hashSlot] = 1
	}
	return n.routeAuthorityEpochs[hashSlot]
}

func (n *Node) nextAuthorityEpochLocked(hashSlot uint16) uint64 {
	if n.routeAuthorityEpochs == nil {
		n.routeAuthorityEpochs = make(map[uint16]uint64)
	}
	n.routeAuthorityEpochs[hashSlot]++
	return n.routeAuthorityEpochs[hashSlot]
}

func sameRouteAuthorityIdentity(a, b routeAuthorityKey) bool {
	return a.slotID == b.slotID &&
		a.leaderNodeID == b.leaderNodeID &&
		a.leaderTerm == b.leaderTerm &&
		a.configEpoch == b.configEpoch
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

// ResolveChannelAppendAuthority resolves the ChannelV2 append authority through the hosted service.
func (n *Node) ResolveChannelAppendAuthority(ctx context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.Meta{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.Meta{}, err
	}
	if n.channels == nil {
		return channelv2.Meta{}, ErrNotStarted
	}
	return n.channels.ResolveAppendAuthority(ctx, id)
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
	if n.defaultSlotMetaDB == nil {
		return channelstore.ReadCommittedResult{}, ErrNotStarted
	}
	route, err := n.RouteKey(id.ID)
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	meta, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	req.MinSeq = maxReadCommittedMinSeq(req.MinSeq, minAvailableSeq(meta.RetentionThroughSeq))
	return store.ReadCommitted(ctx, req)
}

// ReadChannelLastVisible reads the newest visible message from the authoritative channel leader.
func (n *Node) ReadChannelLastVisible(ctx context.Context, id channelv2.ChannelID, visibleAfterSeq uint64) (channelv2.Message, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.Message{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.Message{}, false, err
	}
	if n.channels == nil {
		return channelv2.Message{}, false, ErrNotStarted
	}
	return n.channels.ReadChannelLastVisible(ctx, id, visibleAfterSeq)
}

func minAvailableSeq(retentionThroughSeq uint64) uint64 {
	if retentionThroughSeq == ^uint64(0) {
		return retentionThroughSeq
	}
	return retentionThroughSeq + 1
}

func maxReadCommittedMinSeq(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
