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

const (
	latestMessageScanBatch      = 64
	latestMessageScanMinBudget  = 512
	latestMessageScanMultiplier = 16
)

type localLatestChannelKey struct {
	id  string
	typ uint8
}

type localLatestVisibility struct {
	hw               uint64
	retentionThrough uint64
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
	// slotStatusRuntime proves current local Slot runtime availability and observed leadership for write readiness.
	slotStatusRuntime slotStatusRuntime
	slots             slotReconciler
	tasks             taskExecutor
	// preferredLeaderReconciler is an idle-only background seam and is never
	// invoked synchronously by Start, applySnapshot, or the control watch loop.
	preferredLeaderReconciler taskExecutor
	channels                  channelService
	// channelDataNodes tracks health-schedulable data nodes for default Channel placement.
	channelDataNodes dataNodeView
	// channelDataPlaneLease gates local Channel leader appends on fresh control visibility.
	channelDataPlaneLease *channelDataPlaneLeaseGuard
	// defaultControl reports whether Node constructed the Controller runtime.
	defaultControl bool
	// defaultControlRaftTransport owns the bounded Controller Raft send workers.
	defaultControlRaftTransport *control.RaftTransport
	// defaultTransport reports whether Node constructed the node RPC transport.
	defaultTransport bool
	// defaultChannels reports whether Node constructed channels during Start.
	defaultChannels bool
	// defaultChannelStore owns the Node-created message DB factory.
	defaultChannelStore *channelstore.MessageDBFactory
	// channelStoreFactory is the narrow acquisition surface used by local message read facades.
	// Production falls back to defaultChannelStore; tests may supply an independently tracked factory.
	channelStoreFactory channelstore.Factory
	// defaultSlots reports whether Node constructed the local Slot runtime.
	defaultSlots bool
	// defaultPreferredLeaderReconciler reports whether Node constructed the idle placement reconciler.
	defaultPreferredLeaderReconciler bool
	// defaultSlotRuntime owns the Node-created Slot Multi-Raft runtime.
	defaultSlotRuntime *multiraft.Runtime
	// defaultSlotRaftDB owns the Node-created Slot Raft log store.
	defaultSlotRaftDB *raftlog.DB
	// defaultSlotMetaDB owns the Node-created Slot metadata store.
	defaultSlotMetaDB *metadb.DB
	// defaultSlotProposer adapts the default Slot runtime to the propose service.
	defaultSlotProposer propose.SlotRuntime
	// messageEventStreamCache keeps in-flight stream event projections on the Slot leader.
	messageEventStreamCache *messageEventStreamCache
	// messageEventFinishCoalescer groups concurrent stream.finish proposals for the same channel.
	messageEventFinishCoalescer *messageEventFinishCoalescer
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
	controlApplyMu        sync.Mutex
	taskReconcileMu       sync.Mutex
	taskReconcileCancel   context.CancelFunc
	taskReconcileWG       sync.WaitGroup
	preferredLeaderCancel context.CancelFunc
	preferredLeaderWG     sync.WaitGroup
	// preferredLeaderInterval is a test override for the idle background interval.
	preferredLeaderInterval time.Duration
	// preferredLeaderIntentMu protects the currently published Controller-intent
	// generation used to cancel strict transfers before a newer snapshot apply begins.
	preferredLeaderIntentMu         sync.Mutex
	preferredLeaderIntentGeneration *preferredLeaderIntentGeneration
	watchCancel                     context.CancelFunc
	watchWG                         sync.WaitGroup
	channelTickCancel               context.CancelFunc
	channelTickWG                   sync.WaitGroup
	channelRetentionCancel          context.CancelFunc
	channelRetentionWG              sync.WaitGroup
	channelRetentionGCMu            sync.Mutex
	channelRetentionCursor          channelruntime.ChannelKey
	channelMigrationCancel          context.CancelFunc
	channelMigrationWG              sync.WaitGroup
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

// preferredLeaderIntentGeneration linearizes snapshot invalidation against
// the final nonblocking Raft TransferLeader call.
type preferredLeaderIntentGeneration struct {
	// mu serializes invalidation with the final guarded transfer action.
	mu sync.Mutex
	// ctx is canceled when snapshot apply or shutdown invalidates this generation.
	ctx context.Context
	// cancel cancels ctx exactly once while mu is held.
	cancel context.CancelFunc
	// current reports whether guarded actions may still execute.
	current bool
	// snapshot is the immutable applied Controller intent for exact matching.
	snapshot control.Snapshot
}

// New validates cfg and creates a cluster node.
func New(cfg Config, opts ...Option) (*Node, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	node := &Node{cfg: cfg, router: routing.NewRouter(), discovery: clusternet.NewDiscovery(), snapshot: Snapshot{NodeID: cfg.NodeID}, channelDataPlaneLease: newChannelDataPlaneLeaseGuard(time.Now, cfg.HealthReport.TTL), messageEventStreamCache: newMessageEventStreamCache(0), messageEventFinishCoalescer: newMessageEventFinishCoalescer(defaultMessageEventFinishCoalesceWindow)}
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

func withSlotStatusRuntime(runtime slotStatusRuntime) Option {
	return func(n *Node) { n.slotStatusRuntime = runtime }
}

func withTaskExecutor(executor taskExecutor) Option {
	return func(n *Node) { n.tasks = executor }
}

func withPreferredLeaderReconciler(reconciler taskExecutor) Option {
	return func(n *Node) { n.preferredLeaderReconciler = reconciler }
}

func withPreferredLeaderReconcileInterval(interval time.Duration) Option {
	return func(n *Node) { n.preferredLeaderInterval = interval }
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

// ClusterID returns the effective cluster identity after configuration defaults are applied.
func (n *Node) ClusterID() string {
	if n == nil {
		return ""
	}
	return n.cfg.Control.ClusterID
}

// ListenAddr returns the bound cluster transport address when the node is running.
func (n *Node) ListenAddr() string {
	if n == nil {
		return ""
	}
	if n.transportServer != nil {
		if addr := n.transportServer.Addr(); addr != "" {
			return addr
		}
	}
	return n.cfg.ListenAddr
}

// NodeCount returns the number of nodes in the latest locally visible control snapshot.
func (n *Node) NodeCount() int {
	if n == nil {
		return 0
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.controlSnapshot.Nodes)
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

// RouteAuthorities routes keys using one installed route snapshot and returns
// only distributed authority fence fields plus the local observation epoch.
func (n *Node) RouteAuthorities(keys []string) ([]RouteAuthority, error) {
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	authorities, err := n.router.RouteAuthorities(keys)
	if err != nil {
		return nil, mapRouteError(err)
	}
	n.mu.RLock()
	for i := range authorities {
		authorities[i].AuthorityEpoch = n.routeAuthorityEpochs[authorities[i].HashSlot]
	}
	n.mu.RUnlock()
	return authorities, nil
}

// RouteKeysPartial routes keys using one installed route snapshot and preserves aligned key-specific failures.
// The outer error reports Node lifecycle or missing-table failures; key-specific failures stay in the result.
func (n *Node) RouteKeysPartial(keys []string) ([]RouteKeyResult, error) {
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	results, err := n.router.RouteKeysPartial(keys)
	if err != nil {
		return nil, mapRouteError(err)
	}
	out := make([]RouteKeyResult, len(results))
	for i, result := range results {
		converted, err := convertRoute(result.Route, result.Err)
		if err != nil {
			out[i].Err = err
			continue
		}
		out[i].Route = converted
	}
	n.mu.RLock()
	for i := range out {
		if out[i].Err == nil {
			out[i].Route.AuthorityEpoch = n.routeAuthorityEpochs[out[i].Route.HashSlot]
		}
	}
	n.mu.RUnlock()
	return out, nil
}

// RouteAuthoritiesPartial routes keys using one installed route snapshot and
// preserves aligned key-specific failures without cloning placement fields.
func (n *Node) RouteAuthoritiesPartial(keys []string) ([]RouteAuthorityResult, error) {
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	results, err := n.router.RouteAuthoritiesPartial(keys)
	if err != nil {
		return nil, mapRouteError(err)
	}
	n.mu.RLock()
	for i := range results {
		if results[i].Err != nil {
			results[i].Err = mapRouteError(results[i].Err)
			continue
		}
		results[i].Authority.AuthorityEpoch = n.routeAuthorityEpochs[results[i].Authority.HashSlot]
	}
	n.mu.RUnlock()
	return results, nil
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
	return nil, ErrProposalResultUnsupported
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
	n.clearMessageEventStreamCacheForLostLocalAuthority(before, after)
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

// InvalidateChannelAppendAuthority conditionally removes one failed cached
// authority version so the next bounded router attempt resolves fresh metadata.
func (n *Node) InvalidateChannelAppendAuthority(id channelruntime.ChannelID, leader uint64, epoch uint64, leaderEpoch uint64, routeGeneration uint64) {
	if n == nil || n.channels == nil {
		return
	}
	invalidator, ok := n.channels.(interface {
		InvalidateAppendAuthority(channelruntime.ChannelID, channelruntime.NodeID, uint64, uint64, uint64)
	})
	if !ok {
		return
	}
	invalidator.InvalidateAppendAuthority(id, channelruntime.NodeID(leader), epoch, leaderEpoch, routeGeneration)
}

// ReadChannelCommitted reads locally committed channel messages from the Node-created Channel store.
func (n *Node) ReadChannelCommitted(ctx context.Context, id channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	storeFactory := n.localChannelStoreFactory()
	if storeFactory == nil {
		return channelstore.ReadCommittedResult{}, ErrNotStarted
	}
	store, err := storeFactory.ChannelStore(channelruntime.ChannelKeyForID(id), id)
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	defer func() { _ = store.Close() }()
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

// ReadLocalLatestMessages reads one newest-first page from this node's persisted message replicas.
func (n *Node) ReadLocalLatestMessages(ctx context.Context, beforeMessageID uint64, limit int) ([]channelruntime.Message, bool, uint64, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, false, 0, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, false, 0, err
	}
	storeFactory := n.localChannelStoreFactory()
	if storeFactory == nil {
		return nil, false, 0, ErrNotStarted
	}
	latestStore, ok := storeFactory.(interface {
		ListLatestMessages(context.Context, uint64, int) ([]channelruntime.Message, bool, uint64, error)
		DeleteLatestMessageIndexes(context.Context, []uint64) error
	})
	if !ok {
		return nil, false, 0, channelruntime.ErrInvalidConfig
	}
	if limit <= 0 {
		return nil, false, 0, channelruntime.ErrInvalidConfig
	}
	if n.defaultSlotMetaDB == nil {
		return nil, false, 0, ErrNotStarted
	}

	visibility := make(map[localLatestChannelKey]localLatestVisibility)
	visible := make([]channelruntime.Message, 0, limit+1)
	scanBefore := beforeMessageID
	scanLimit := max(limit+1, latestMessageScanBatch)
	scanBudget := max(latestMessageScanMinBudget, limit*latestMessageScanMultiplier)
	scanned := 0
	for {
		remaining := scanBudget - scanned
		if remaining <= 0 {
			return nil, false, 0, channelruntime.ErrBackpressured
		}
		items, hasMore, next, err := latestStore.ListLatestMessages(ctx, scanBefore, min(scanLimit, remaining))
		if err != nil {
			return nil, false, 0, err
		}
		scanned += len(items)
		if err := n.loadLocalLatestVisibility(ctx, storeFactory, items, visibility); err != nil {
			return nil, false, 0, err
		}
		retainedMessageIDs := make([]uint64, 0)
		pageFull := false
		for _, item := range items {
			state := visibility[localLatestChannelKey{id: item.ChannelID, typ: item.ChannelType}]
			if item.MessageSeq <= state.retentionThrough {
				retainedMessageIDs = append(retainedMessageIDs, item.MessageID)
				continue
			}
			if item.MessageSeq > state.hw {
				continue
			}
			visible = append(visible, item)
			if len(visible) == limit+1 {
				pageFull = true
				break
			}
		}
		if len(retainedMessageIDs) > 0 {
			if err := latestStore.DeleteLatestMessageIndexes(ctx, retainedMessageIDs); err != nil {
				return nil, false, 0, err
			}
		}
		if pageFull {
			return visible[:limit], true, visible[limit-1].MessageID, nil
		}
		if !hasMore || next == 0 || next == scanBefore {
			break
		}
		if scanned >= scanBudget {
			return nil, false, 0, channelruntime.ErrBackpressured
		}
		scanBefore = next
	}
	return visible, false, 0, nil
}

func (n *Node) loadLocalLatestVisibility(ctx context.Context, storeFactory channelstore.Factory, items []channelruntime.Message, cache map[localLatestChannelKey]localLatestVisibility) error {
	unknown := make([]channelruntime.ChannelID, 0, len(items))
	seen := make(map[localLatestChannelKey]struct{}, len(items))
	for _, item := range items {
		key := localLatestChannelKey{id: item.ChannelID, typ: item.ChannelType}
		if _, ok := cache[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unknown = append(unknown, channelruntime.ChannelID{ID: item.ChannelID, Type: item.ChannelType})
	}
	if len(unknown) == 0 {
		return nil
	}
	if n.channels == nil {
		return ErrNotStarted
	}
	probe, err := n.channels.RuntimeProbe(ctx, channelruntime.RuntimeSelector{ChannelIDs: unknown})
	if err != nil {
		return err
	}
	runtimeHW := make(map[localLatestChannelKey]uint64, len(probe.Channels))
	for _, channel := range probe.Channels {
		runtimeHW[localLatestChannelKey{id: channel.ChannelID.ID, typ: channel.ChannelID.Type}] = channel.HW
	}
	for _, id := range unknown {
		key := localLatestChannelKey{id: id.ID, typ: id.Type}
		route, err := n.RouteKey(id.ID)
		if err != nil {
			return err
		}
		meta, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			return err
		}
		hw, loaded := runtimeHW[key]
		if !loaded {
			store, err := storeFactory.ChannelStore(channelruntime.ChannelKeyForID(id), id)
			if err != nil {
				return err
			}
			state, loadErr := store.Load(ctx)
			closeErr := store.Close()
			if loadErr != nil {
				return loadErr
			}
			if closeErr != nil {
				return closeErr
			}
			hw = state.HW
			if localLeaderCommitsOwnLEO(meta, n.NodeID()) {
				hw = state.LEO
			}
		}
		cache[key] = localLatestVisibility{hw: hw, retentionThrough: meta.RetentionThroughSeq}
	}
	return nil
}

func localLeaderCommitsOwnLEO(meta metadb.ChannelRuntimeMeta, localNodeID uint64) bool {
	return meta.Leader == localNodeID && meta.MinISR <= 1
}

// LookupChannelIdempotency reads one local Channel idempotency index entry.
func (n *Node) LookupChannelIdempotency(ctx context.Context, id channelruntime.ChannelID, fromUID string, clientMsgNo string) (channelstore.IdempotencyHit, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return channelstore.IdempotencyHit{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelstore.IdempotencyHit{}, false, err
	}
	storeFactory := n.localChannelStoreFactory()
	if storeFactory == nil {
		return channelstore.IdempotencyHit{}, false, ErrNotStarted
	}
	store, err := storeFactory.ChannelStore(channelruntime.ChannelKeyForID(id), id)
	if err != nil {
		return channelstore.IdempotencyHit{}, false, err
	}
	defer func() { _ = store.Close() }()
	lookup, ok := store.(channelstore.IdempotencyLookup)
	if !ok {
		return channelstore.IdempotencyHit{}, false, channelruntime.ErrInvalidConfig
	}
	return lookup.LookupIdempotency(ctx, fromUID, clientMsgNo)
}

func (n *Node) localChannelStoreFactory() channelstore.Factory {
	if n == nil {
		return nil
	}
	if n.channelStoreFactory != nil {
		return n.channelStoreFactory
	}
	return n.defaultChannelStore
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
