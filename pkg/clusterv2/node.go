package clusterv2

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/internal/lifecycle"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

// Option customizes Node construction.
type Option func(*Node)

type slotReconciler interface {
	Reconcile(context.Context, control.Snapshot) error
}

type channelService interface {
	Append(context.Context, channelv2.AppendRequest) (channelv2.AppendResult, error)
	AppendBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error)
	Fetch(context.Context, channelv2.FetchRequest) (channelv2.FetchResult, error)
	Tick(context.Context) error
	Close() error
}

// Node is the clusterv2 lifecycle root and public runtime facade.
type Node struct {
	cfg       Config
	resources []lifecycle.NamedResource
	control   control.Controller
	router    *routing.Router
	discovery *clusternet.Discovery
	slots     slotReconciler
	channels  channelService
	// defaultChannels reports whether Node constructed channels during Start.
	defaultChannels bool
	// defaultChannelStore owns the Node-created message DB factory.
	defaultChannelStore *channelstore.MessageDBFactory
	proposer            interface {
		Propose(context.Context, propose.Request) error
	}
	group lifecycle.Group

	mu                sync.RWMutex
	snapshot          Snapshot
	watchCancel       context.CancelFunc
	channelTickCancel context.CancelFunc
	channelTickWG     sync.WaitGroup
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

// Start starts the node runtime and hosted background loops.
func (n *Node) Start(ctx context.Context) error {
	if n == nil {
		return ErrNotStarted
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	createdDefaultChannels, err := n.ensureDefaultRuntime()
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started && createdDefaultChannels {
			n.discardDefaultChannels()
			n.markChannelsReady(false)
		}
	}()
	n.stopping.Store(false)
	if err := n.group.Start(ctx, n.resources...); err != nil {
		n.started.Store(false)
		return err
	}
	if n.control != nil {
		if err := n.control.Start(ctx); err != nil {
			_ = n.group.Stop(ctx)
			return err
		}
		snapshot, err := n.control.LocalSnapshot(ctx)
		if err != nil {
			_ = n.control.Stop(ctx)
			_ = n.group.Stop(ctx)
			return err
		}
		if err := n.applySnapshot(ctx, snapshot); err != nil {
			_ = n.control.Stop(ctx)
			_ = n.group.Stop(ctx)
			return err
		}
		n.startWatchLoop()
	}
	n.markChannelsReady(n.channels != nil)
	n.startChannelTickLoop()
	n.started.Store(true)
	started = true
	return nil
}

func (n *Node) ensureDefaultRuntime() (bool, error) {
	if n.proposer == nil {
		n.proposer = propose.NewService(propose.Config{LocalNode: n.cfg.NodeID, Router: n.router})
	}
	createdDefaultChannels := false
	if n.channels == nil {
		storeFactory := channelstore.NewMessageDBFactory(n.defaultChannelStorePath())
		service, err := channels.NewService(channels.Config{
			LocalNode:    channelv2.NodeID(n.cfg.NodeID),
			ReactorCount: n.cfg.Channel.ReactorCount,
			MailboxSize:  n.cfg.Channel.MailboxSize,
			Store:        storeFactory,
		})
		if err != nil {
			_ = storeFactory.Close()
			return false, err
		}
		n.channels = service
		n.defaultChannels = true
		n.defaultChannelStore = storeFactory
		createdDefaultChannels = true
	}
	return createdDefaultChannels, nil
}

func (n *Node) defaultChannelStorePath() string {
	return filepath.Join(n.cfg.DataDir, "messages")
}

func (n *Node) discardDefaultChannels() {
	if n == nil || !n.defaultChannels {
		return
	}
	if n.channels != nil {
		_ = n.channels.Close()
	}
	n.channels = nil
	n.defaultChannels = false
	_ = n.closeDefaultChannelStore()
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
	if n.watchCancel != nil {
		n.watchCancel()
	}
	n.stopChannelTickLoop()
	var errs []error
	if n.channels != nil {
		if err := n.channels.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if n.defaultChannels {
		n.channels = nil
		n.defaultChannels = false
		if err := n.closeDefaultChannelStore(); err != nil {
			errs = append(errs, err)
		}
	}
	n.markChannelsReady(false)
	if n.control != nil {
		if err := n.control.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := n.group.Stop(ctx); err != nil {
		errs = append(errs, err)
	}
	n.started.Store(false)
	return errors.Join(errs...)
}

func (n *Node) closeDefaultChannelStore() error {
	if n == nil || n.defaultChannelStore == nil {
		return nil
	}
	err := n.defaultChannelStore.Close()
	n.defaultChannelStore = nil
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

// FetchChannel fetches committed messages through the hosted ChannelV2 service.
func (n *Node) FetchChannel(ctx context.Context, req channelv2.FetchRequest) (channelv2.FetchResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.FetchResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.FetchResult{}, err
	}
	if n.channels == nil {
		return channelv2.FetchResult{}, ErrNotStarted
	}
	return n.channels.Fetch(ctx, req)
}

func (n *Node) applySnapshot(ctx context.Context, snapshot control.Snapshot) error {
	if n.router != nil {
		if err := n.router.UpdateControlSnapshot(snapshot); err != nil {
			return err
		}
	}
	if n.discovery != nil {
		n.discovery.Update(discoveryNodes(snapshot.Nodes))
	}
	if n.slots != nil {
		if err := n.slots.Reconcile(ctx, snapshot); err != nil {
			return err
		}
	}
	n.mu.Lock()
	n.snapshot = Snapshot{NodeID: n.cfg.NodeID, ControllerLead: snapshot.ControllerID, StateRevision: snapshot.Revision, RoutesReady: n.router != nil && n.router.Table() != nil, SlotsReady: true, ChannelsReady: n.channels != nil, SlotCount: uint32(len(snapshot.Slots)), HashSlotCount: snapshot.HashSlots.Count}
	n.mu.Unlock()
	return nil
}

func discoveryNodes(nodes []control.Node) []clusternet.NodeAddress {
	out := make([]clusternet.NodeAddress, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, clusternet.NodeAddress{NodeID: node.NodeID, Addr: node.Addr})
	}
	return out
}

func (n *Node) markChannelsReady(ready bool) {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.snapshot.NodeID = n.cfg.NodeID
	n.snapshot.ChannelsReady = ready
	n.mu.Unlock()
}

func (n *Node) startWatchLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	n.watchCancel = cancel
	watch := n.control.Watch()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watch:
				if !ok {
					return
				}
				_ = n.applySnapshot(ctx, ev.Snapshot)
			}
		}
	}()
}

func (n *Node) startChannelTickLoop() {
	if n == nil || n.channels == nil || n.channelTickCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.channelTickCancel = cancel
	n.channelTickWG.Add(1)
	go func() {
		defer n.channelTickWG.Done()
		ticker := time.NewTicker(n.cfg.Channel.TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = n.channels.Tick(ctx)
			}
		}
	}()
}

func (n *Node) stopChannelTickLoop() {
	if n == nil || n.channelTickCancel == nil {
		return
	}
	n.channelTickCancel()
	n.channelTickWG.Wait()
	n.channelTickCancel = nil
}

func convertRoute(route routing.Route, err error) (Route, error) {
	if err != nil {
		return Route{}, mapRouteError(err)
	}
	return Route{HashSlot: route.HashSlot, SlotID: route.SlotID, Leader: route.Leader, Peers: append([]uint64(nil), route.Peers...), Revision: route.Revision}, nil
}

func mapRouteError(err error) error {
	switch {
	case errors.Is(err, routing.ErrRouteNotReady):
		return ErrRouteNotReady
	case errors.Is(err, routing.ErrNoSlotLeader):
		return ErrNoSlotLeader
	case errors.Is(err, routing.ErrRouteMismatch):
		return ErrRouteNotReady
	default:
		return err
	}
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
