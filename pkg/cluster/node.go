package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/internal/lifecycle"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/observe"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Option customizes Node construction.
type Option func(*Node)

type resultProposer interface {
	ProposeResult(context.Context, propose.Request) ([]byte, error)
}

type slotReconciler interface {
	Reconcile(context.Context, control.Snapshot) error
}

type taskExecutor interface {
	Reconcile(context.Context, control.Snapshot) error
}

type channelService interface {
	Append(context.Context, channelruntime.AppendRequest) (channelruntime.AppendResult, error)
	AppendBatch(context.Context, channelruntime.AppendBatchRequest) (channelruntime.AppendBatchResult, error)
	ResolveAppendAuthority(context.Context, channelruntime.ChannelID) (channelruntime.Meta, error)
	ReadChannelLastVisible(context.Context, channelruntime.ChannelID, uint64) (channelruntime.Message, bool, error)
	RetentionView(context.Context, channelruntime.ChannelID) (channelruntime.RetentionView, error)
	ApplyRetentionBoundary(context.Context, channelruntime.RetentionApplyRequest) (channelruntime.RetentionApplyResult, error)
	RuntimeSnapshot(context.Context) (channelruntime.RuntimeSnapshot, error)
	RuntimeProbe(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error)
	RuntimeEvict(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error)
	DrainChannel(context.Context, channelruntime.DrainChannelRequest) (channelruntime.DrainChannelResult, error)
	Tick(context.Context) error
	Close() error
}

// Node is the cluster lifecycle root and public runtime facade.
type Node struct {
	cfg              Config
	resources        []lifecycle.NamedResource
	control          control.Controller
	router           *routing.Router
	discovery        *clusternet.Discovery
	transportServer  *clusternet.TransportServer
	transportClient  *clusternet.TransportClient
	slotStatusCaller clusternet.Caller
	slots            slotReconciler
	tasks            taskExecutor
	channels         channelService
	// channelDataNodes tracks health-schedulable data nodes for default Channel placement.
	channelDataNodes dataNodeView
	// channelDataPlaneLease gates local Channel leader appends on fresh control visibility.
	channelDataPlaneLease *channelDataPlaneLeaseGuard
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

	mu              sync.RWMutex
	snapshot        Snapshot
	controlSnapshot control.Snapshot
	// controlApplyMu serializes control snapshot application from startup, watches, and probes.
	controlApplyMu         sync.Mutex
	taskReconcileMu        sync.Mutex
	taskReconcileCancel    context.CancelFunc
	taskReconcileWG        sync.WaitGroup
	watchCancel            context.CancelFunc
	watchWG                sync.WaitGroup
	channelTickCancel      context.CancelFunc
	channelTickWG          sync.WaitGroup
	channelRetentionCancel context.CancelFunc
	channelRetentionWG     sync.WaitGroup
	channelRetentionGCMu   sync.Mutex
	channelRetentionCursor channelruntime.ChannelKey
	channelMigrationCancel context.CancelFunc
	channelMigrationWG     sync.WaitGroup
	// healthReportCancel stops the low-frequency Controller health reporter.
	healthReportCancel context.CancelFunc
	// healthReporter sends low-frequency Controller node health reports.
	healthReporter *observe.Reporter
	// healthReportWG waits for the low-frequency health reporter to exit.
	healthReportWG   sync.WaitGroup
	slotLeaderCancel context.CancelFunc
	slotLeaderWG     sync.WaitGroup
	started          atomic.Bool
	stopping         atomic.Bool
}

// New validates cfg and creates a cluster node.
func New(cfg Config, opts ...Option) (*Node, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	node := &Node{cfg: cfg, router: routing.NewRouter(), discovery: clusternet.NewDiscovery(), snapshot: Snapshot{NodeID: cfg.NodeID}, channelDataPlaneLease: newChannelDataPlaneLeaseGuard(time.Now, cfg.HealthReport.TTL)}
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

// WithChannels overrides the default Channel service hosted by Node.
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

// Snapshot returns the latest locally visible cluster readiness summary.
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

// Propose submits a Slot metadata command through cluster routing.
func (n *Node) Propose(ctx context.Context, req ProposeRequest) error {
	_, err := n.ProposeResult(ctx, req)
	return err
}

// ProposeResult submits a Slot metadata command and returns FSM apply bytes when supported.
func (n *Node) ProposeResult(ctx context.Context, req ProposeRequest) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.proposer == nil {
		return nil, ErrNotStarted
	}
	proposeReq := propose.Request{
		Key:     req.Key,
		Command: req.Command,
		Target:  propose.Target{HashSlot: req.Target.HashSlot, HasHashSlot: req.Target.HasHashSlot, SlotID: req.Target.SlotID, HasSlotID: req.Target.HasSlotID},
	}
	if proposer, ok := n.proposer.(resultProposer); ok {
		return proposer.ProposeResult(ctx, proposeReq)
	}
	return nil, n.proposer.Propose(ctx, proposeReq)
}

// RegisterRPC registers a node RPC handler on the default cluster transport.
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

// AppendChannel appends one message through the hosted Channel service.
func (n *Node) AppendChannel(ctx context.Context, req channelruntime.AppendRequest) (channelruntime.AppendResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.AppendResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.AppendResult{}, err
	}
	if n.channels == nil {
		return channelruntime.AppendResult{}, ErrNotStarted
	}
	return n.channels.Append(ctx, req)
}

// AppendChannelBatch appends a batch of messages through the hosted Channel service.
func (n *Node) AppendChannelBatch(ctx context.Context, req channelruntime.AppendBatchRequest) (channelruntime.AppendBatchResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.AppendBatchResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.AppendBatchResult{}, err
	}
	if n.channels == nil {
		return channelruntime.AppendBatchResult{}, ErrNotStarted
	}
	return n.channels.AppendBatch(ctx, req)
}

// ResolveChannelAppendAuthority resolves the Channel append authority through the hosted service.
func (n *Node) ResolveChannelAppendAuthority(ctx context.Context, id channelruntime.ChannelID) (channelruntime.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.Meta{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.Meta{}, err
	}
	if n.channels == nil {
		return channelruntime.Meta{}, ErrNotStarted
	}
	return n.channels.ResolveAppendAuthority(ctx, id)
}

// ReadChannelCommitted reads locally committed channel messages from the Node-created Channel store.
func (n *Node) ReadChannelCommitted(ctx context.Context, id channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	if n.defaultChannelStore == nil {
		return channelstore.ReadCommittedResult{}, ErrNotStarted
	}
	store, err := n.defaultChannelStore.ChannelStore(channelruntime.ChannelKeyForID(id), id)
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

// LookupChannelIdempotency reads one local Channel idempotency index entry.
func (n *Node) LookupChannelIdempotency(ctx context.Context, id channelruntime.ChannelID, fromUID string, clientMsgNo string) (channelstore.IdempotencyHit, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return channelstore.IdempotencyHit{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelstore.IdempotencyHit{}, false, err
	}
	if n.defaultChannelStore == nil {
		return channelstore.IdempotencyHit{}, false, ErrNotStarted
	}
	store, err := n.defaultChannelStore.ChannelStore(channelruntime.ChannelKeyForID(id), id)
	if err != nil {
		return channelstore.IdempotencyHit{}, false, err
	}
	lookup, ok := store.(channelstore.IdempotencyLookup)
	if !ok {
		return channelstore.IdempotencyHit{}, false, channelruntime.ErrInvalidConfig
	}
	return lookup.LookupIdempotency(ctx, fromUID, clientMsgNo)
}

// ReadChannelLastVisible reads the newest visible message from the authoritative channel leader.
func (n *Node) ReadChannelLastVisible(ctx context.Context, id channelruntime.ChannelID, visibleAfterSeq uint64) (channelruntime.Message, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.Message{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.Message{}, false, err
	}
	if n.channels == nil {
		return channelruntime.Message{}, false, ErrNotStarted
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
