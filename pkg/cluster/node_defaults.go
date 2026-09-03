package cluster

import (
	"context"
	"fmt"
	"path/filepath"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelreplication "github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func (n *Node) ensureDefaultRuntime() (bool, error) {
	if n.control == nil {
		if err := n.ensureDefaultTransport(); err != nil {
			return false, err
		}
		n.registerPendingRPCHandlers()
		controlPeers := n.defaultControlRuntimePeers()
		raftTransport := control.NewRaftTransportWithOptions(n.transportClient, control.RaftTransportOptions{Observer: n.cfg.Transport.Observer})
		runtime, err := control.NewRuntime(control.RuntimeConfig{
			NodeID:                 n.cfg.NodeID,
			Addr:                   n.cfg.ListenAddr,
			StateDir:               n.cfg.Control.StateDir,
			ClusterID:              n.cfg.Control.ClusterID,
			Role:                   control.RuntimeRole(n.cfg.Control.Role),
			Voters:                 controlPeers,
			AllowBootstrap:         n.cfg.Control.AllowBootstrap,
			InitialSlotCount:       n.cfg.Slots.InitialSlotCount,
			HashSlotCount:          n.cfg.Slots.HashSlotCount,
			ReplicaCount:           n.cfg.Slots.ReplicaCount,
			RaftTransport:          raftTransport,
			RaftObserver:           n.cfg.Control.RaftObserver,
			TaskTransitionObserver: n.cfg.Control.TaskTransitionObserver,
			SyncPeers:              control.NewStaticPeerPicker(n.transportClient, controlPeers),
			TaskClient:             control.NewTaskClient(n.transportClient),
			ControlWriteClient:     control.NewControlWriteClient(n.transportClient),
			HealthReportTTL:        n.cfg.HealthReport.TTL,
			Logger:                 namedLogger(n.cfg.Logger, "controller"),
		})
		if err != nil {
			raftTransport.Stop()
			return false, err
		}
		if n.cfg.Control.Role == ControlRoleVoter {
			n.registerControlRuntimeRPCHandlers(runtime)
		}
		n.control = runtime
		n.defaultControl = true
		n.defaultControlRaftTransport = raftTransport
	}
	if n.proposer == nil {
		if err := n.ensureDefaultSlots(); err != nil {
			return false, err
		}
		var forward propose.ForwardClient
		if n.transportClient != nil {
			forward = propose.NewNetworkForwardClient(n.transportClient)
		}
		n.proposer = propose.NewService(propose.Config{
			LocalNode: n.cfg.NodeID,
			Router:    n.router,
			Slots:     n.defaultSlotProposer,
			Forward:   forward,
		})
	}
	createdDefaultChannels := false
	if n.channels == nil {
		storeFactory := n.defaultChannelStore
		createdStoreFactory := false
		if storeFactory == nil {
			storeFactory = n.newDefaultChannelStore()
			createdStoreFactory = true
		}
		var transport *channels.TransportClient
		if n.transportClient != nil {
			transport = channels.NewTransportClient(n.transportClient)
		}
		storeAdapter, err := channelreplication.NewStoreAdapter(channelreplication.StoreAdapterConfig{
			Factory: storeFactory, MaxBatchItems: channelreplication.MaxExchangeBatchItems,
			MaxBatchBytes: channelreplication.MaxExchangeBatchBytes,
		})
		if err != nil {
			if createdStoreFactory {
				_ = storeFactory.Close()
			}
			return false, err
		}
		peerLink, err := channels.NewQuorumPeerLink(channelruntime.NodeID(n.cfg.NodeID), n.transportClient)
		if err != nil {
			if createdStoreFactory {
				_ = storeFactory.Close()
			}
			return false, err
		}
		var replicationObserver channelreplication.StageObserver
		if n.cfg.Channel.Observer != nil {
			replicationObserver, _ = n.cfg.Channel.Observer.(channelreplication.StageObserver)
		}
		quorumRuntime, err := channelreplication.NewRuntime(channelreplication.RuntimeConfig{
			LocalNode: channelruntime.NodeID(n.cfg.NodeID), Store: storeAdapter, Link: peerLink,
			Goroutines: n.cfg.Goroutines, MaxChannels: n.cfg.Channel.MaxChannels,
			MaxVoters: int(n.cfg.Channel.ReplicaCount), Observer: replicationObserver,
		})
		if err != nil {
			if createdStoreFactory {
				_ = storeFactory.Close()
			}
			return false, err
		}
		service, err := channels.NewService(channels.Config{
			LocalNode:                     channelruntime.NodeID(n.cfg.NodeID),
			ReactorCount:                  n.cfg.Channel.ReactorCount,
			StoreAppendWorkers:            n.cfg.Channel.StoreAppendWorkers,
			StoreAppendBatchMaxWait:       n.cfg.Channel.StoreAppendBatchMaxWait,
			StoreApplyWorkers:             n.cfg.Channel.StoreApplyWorkers,
			RPCWorkers:                    n.cfg.Channel.RPCWorkers,
			RPCBatchMaxItems:              n.cfg.Channel.RPCBatchMaxItems,
			MailboxSize:                   n.cfg.Channel.MailboxSize,
			MaxChannels:                   n.cfg.Channel.MaxChannels,
			AppendBatchMaxRecords:         n.cfg.Channel.AppendBatchMaxRecords,
			AppendBatchMaxWait:            n.cfg.Channel.AppendBatchMaxWait,
			AppendBatchAdaptiveFlush:      n.cfg.Channel.AppendBatchAdaptiveFlush,
			AppendBatchColdMaxWait:        n.cfg.Channel.AppendBatchColdMaxWait,
			FollowerRecoveryProbeInterval: n.cfg.Channel.FollowerRecoveryProbeInterval,
			FollowerRecoveryProbeJitter:   n.cfg.Channel.FollowerRecoveryProbeJitter,
			Observer:                      n.cfg.Channel.Observer,
			Goroutines:                    n.cfg.Goroutines,
			AppendAdmissionGuard:          n.channelDataPlaneLease,
			Store:                         storeFactory,
			Transport:                     transport,
			QuorumLog:                     quorumRuntime.Log(),
			MetaSource:                    n.defaultChannelMetaSource(),
			MigrationStore:                n.defaultChannelMigrationStore(),
		})
		if err != nil {
			_ = quorumRuntime.Close(context.Background())
			if createdStoreFactory {
				_ = storeFactory.Close()
			}
			return false, err
		}
		if n.transportServer != nil {
			if n.channelRPCGateway == nil {
				n.channelRPCGateway = channels.NewServiceGateway(service)
				channels.RegisterServiceHandlersOn(
					n.transportServer, n.channelRPCGateway,
				)
			} else {
				n.channelRPCGateway.Replace(service)
			}
			if n.channelQuorumGateway == nil {
				n.channelQuorumGateway = channels.NewQuorumExchangeGateway(quorumRuntime.ExchangeServer())
				channels.RegisterQuorumExchangeHandlerOn(n.transportServer, n.channelQuorumGateway)
			} else {
				n.channelQuorumGateway.Replace(quorumRuntime.ExchangeServer())
			}
		}
		n.channels = service
		n.defaultChannels = true
		n.defaultChannelStore = storeFactory
		n.defaultChannelReplication = quorumRuntime
		createdDefaultChannels = true
	}
	return createdDefaultChannels, nil
}

func (n *Node) newDefaultChannelStore() *channelstore.MessageDBFactory {
	return channelstore.NewMessageDBFactoryWithOptions(
		n.defaultChannelStorePath(),
		channelstore.MessageDBFactoryOptions{
			CommitFlushWindow: n.cfg.Storage.CommitFlushWindow,
			CommitMaxRequests: n.cfg.Storage.CommitMaxRequests,
			CommitMaxRecords:  n.cfg.Storage.CommitMaxRecords,
			CommitMaxBytes:    n.cfg.Storage.CommitMaxBytes,
			CommitShards:      n.cfg.Storage.CommitShards,
			CommitObserver:    n.cfg.Storage.CommitObserver,
			Logger:            namedLogger(n.cfg.Logger, "message_db"),
		},
	)
}

func (n *Node) ensureDefaultTransport() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.transportServer != nil && n.transportClient != nil {
		return nil
	}
	if n.discovery != nil {
		n.discovery.Update(n.defaultBootstrapDiscoveryNodes())
	}
	n.transportServer = clusternet.NewTransportServer(clusternet.TransportServerConfig{
		NodeID:   n.cfg.NodeID,
		Observer: n.cfg.Transport.Observer,
	})
	n.transportClient = clusternet.NewTransportClient(clusternet.TransportClientConfig{
		NodeID:    n.cfg.NodeID,
		Discovery: n.discovery,
		Observer:  n.cfg.Transport.Observer,
	})
	n.slotStatusCaller = n.transportClient
	n.defaultTransport = true
	n.registeredRPCHandlers = make(map[uint8]struct{})
	return nil
}

func (n *Node) defaultControlRuntimePeers() []control.RuntimeVoter {
	if n == nil {
		return nil
	}
	if n != nil && n.cfg.seedJoinMode() {
		return seedJoinRuntimePeers(n.cfg.Join.Seeds)
	}
	return runtimeVoters(n.cfg.Control.Voters)
}

func (n *Node) defaultBootstrapDiscoveryNodes() []clusternet.NodeAddress {
	if n == nil {
		return nil
	}
	nodes := controlVoterNodes(n.cfg.Control.Voters)
	if n != nil && n.cfg.seedJoinMode() {
		nodes = append(nodes, seedJoinDiscoveryNodes(n.cfg.Join.Seeds)...)
	}
	return nodes
}

func (n *Node) registerPendingRPCHandlers() {
	if n == nil {
		return
	}
	n.mu.Lock()
	server := n.transportServer
	if server == nil {
		n.mu.Unlock()
		return
	}
	if n.registeredRPCHandlers == nil {
		n.registeredRPCHandlers = make(map[uint8]struct{})
	}
	handlers := make(map[uint8]clusternet.Handler, len(n.pendingRPCHandlers))
	for serviceID, handler := range n.pendingRPCHandlers {
		if _, ok := n.registeredRPCHandlers[serviceID]; ok {
			continue
		}
		n.registeredRPCHandlers[serviceID] = struct{}{}
		handlers[serviceID] = handler
	}
	n.mu.Unlock()
	for serviceID, handler := range handlers {
		server.Register(serviceID, handler)
	}
}

type controlRuntimeRPCHandler struct {
	serviceID uint8
	handler   clusternet.Handler
}

func (n *Node) registerControlRuntimeRPCHandlers(runtime *control.Runtime) {
	if n == nil || runtime == nil {
		return
	}
	n.mu.Lock()
	server := n.transportServer
	if server == nil {
		n.mu.Unlock()
		return
	}
	if n.registeredRPCHandlers == nil {
		n.registeredRPCHandlers = make(map[uint8]struct{})
	}
	candidates := []controlRuntimeRPCHandler{
		{serviceID: clusternet.RPCControlRaft, handler: control.NewRaftHandler(runtime)},
		{serviceID: clusternet.RPCControlStateSync, handler: control.NewStateSyncHandler(runtime)},
		{serviceID: clusternet.RPCControlTaskResult, handler: control.NewTaskHandler(runtime)},
		{serviceID: clusternet.RPCControlWrite, handler: control.NewControlWriteHandler(runtime)},
	}
	handlers := make([]controlRuntimeRPCHandler, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := n.registeredRPCHandlers[candidate.serviceID]; ok {
			continue
		}
		n.registeredRPCHandlers[candidate.serviceID] = struct{}{}
		handlers = append(handlers, candidate)
	}
	n.mu.Unlock()
	for _, candidate := range handlers {
		server.Register(candidate.serviceID, candidate.handler)
	}
}

func (n *Node) defaultChannelStorePath() string {
	return filepath.Join(n.cfg.DataDir, "messages")
}

func (n *Node) defaultChannelMetaSource() channels.ChannelMetaSource {
	if n == nil || n.defaultSlotMetaDB == nil {
		return nil
	}
	var observer channels.AppendStageObserver
	var batchObserver channels.MetaCreateBatchObserver
	if n.cfg.Channel.Observer != nil {
		observer, _ = n.cfg.Channel.Observer.(channels.AppendStageObserver)
		batchObserver, _ = n.cfg.Channel.Observer.(channels.MetaCreateBatchObserver)
	}
	store := defaultChannelRuntimeMetaStore{node: n, observer: observer}
	return channels.NewSlotMetaSource(store, channels.SlotMetaSourceOptions{
		Placement:     channels.NewSlotPlacementResolver(n.router, &n.channelDataNodes, int(n.cfg.Channel.ReplicaCount)),
		Router:        n.router,
		BatchStore:    store,
		BatchObserver: batchObserver,
		Goroutines:    n.cfg.Goroutines,
		Observer:      observer,
	})
}

func (n *Node) defaultChannelMigrationStore() *channels.MigrationStore {
	if n == nil || n.defaultSlotMetaDB == nil {
		return nil
	}
	adapter := defaultChannelMigrationStore{node: n}
	return channels.NewMigrationStore(channels.MigrationStoreConfig{
		LocalNode: n.cfg.NodeID,
		Router:    n.router,
		Proposer:  adapter,
		Reader:    adapter,
	})
}

// defaultChannelRuntimeMetaStore reads Slot-owned channel metadata and writes through Node proposals.
type defaultChannelRuntimeMetaStore struct {
	node     *Node
	observer channels.AppendStageObserver
}

// CreateChannelRuntimeMetaBatch submits one bounded command-59 proposal and
// returns authoritative identity-bound outcomes aligned to items.
func (s defaultChannelRuntimeMetaStore) CreateChannelRuntimeMetaBatch(ctx context.Context, expected routing.Route, items []channels.RuntimeMetaCreateItem) ([]channels.RuntimeMetaCreateResult, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s.node == nil {
		return nil, ErrNotStarted
	}
	if err := s.validateRuntimeMetaBatchRoute(items, expected); err != nil {
		return nil, err
	}
	fsmItems := make([]metafsm.CreateChannelRuntimeMetaBatchItem, len(items))
	for i, item := range items {
		fsmItems[i] = metafsm.CreateChannelRuntimeMetaBatchItem{HashSlot: item.HashSlot, Meta: item.Meta}
	}
	command, err := metafsm.EncodeCreateChannelRuntimeMetaBatchCommandChecked(fsmItems)
	if err != nil {
		return nil, err
	}
	ctx = propose.WithStageObserver(ctx, s.observer)
	data, err := s.node.ProposeResult(ctx, ProposeRequest{
		Command: command,
		Target: ProposeTarget{
			HashSlot: items[0].HashSlot, HasHashSlot: true,
			SlotID: expected.SlotID, HasSlotID: true,
		},
	})
	if err != nil {
		return nil, err
	}
	decoded, err := metafsm.DecodeCreateChannelRuntimeMetaBatchResult(data)
	if err != nil || len(decoded) != len(items) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: runtime metadata batch result count", metadb.ErrCorruptValue)
	}
	type identity struct {
		hashSlot    uint16
		channelID   string
		channelType int64
	}
	byIdentity := make(map[identity]metafsm.CreateChannelRuntimeMetaBatchResult, len(decoded))
	for _, result := range decoded {
		key := identity{hashSlot: result.HashSlot, channelID: result.ChannelID, channelType: result.ChannelType}
		if _, exists := byIdentity[key]; exists {
			return nil, fmt.Errorf("%w: duplicate runtime metadata batch result", metadb.ErrCorruptValue)
		}
		byIdentity[key] = result
	}
	results := make([]channels.RuntimeMetaCreateResult, len(items))
	for i, item := range items {
		key := identity{hashSlot: item.HashSlot, channelID: item.Meta.ChannelID, channelType: item.Meta.ChannelType}
		result, ok := byIdentity[key]
		if !ok {
			return nil, fmt.Errorf("%w: missing runtime metadata batch result", metadb.ErrCorruptValue)
		}
		delete(byIdentity, key)
		results[i] = channels.RuntimeMetaCreateResult{
			HashSlot: result.HashSlot, ChannelID: result.ChannelID, ChannelType: result.ChannelType, Created: result.Created,
		}
	}
	if len(byIdentity) != 0 {
		return nil, fmt.Errorf("%w: extra runtime metadata batch result", metadb.ErrCorruptValue)
	}
	return results, nil
}

// BatchGetChannelRuntimeMetas performs one aligned authoritative reread after
// the command-59 future has resolved.
func (s defaultChannelRuntimeMetaStore) BatchGetChannelRuntimeMetas(ctx context.Context, _ routing.Route, items []channels.RuntimeMetaCreateItem) ([]channels.RuntimeMetaReadResult, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s.node == nil || s.node.defaultSlotProxy == nil {
		return nil, ErrNotStarted
	}
	if _, err := s.validateRuntimeMetaBatchItems(items); err != nil {
		return nil, err
	}
	keys := make([]metadb.ChannelKey, len(items))
	for i, item := range items {
		keys[i] = metadb.ChannelKey{ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType}
	}
	metas, err := s.node.defaultSlotProxy.BatchGetChannelRuntimeMetas(ctx, keys)
	if err != nil {
		return nil, err
	}
	results := make([]channels.RuntimeMetaReadResult, len(keys))
	for i, key := range keys {
		meta, ok := metas[key]
		if !ok {
			results[i].Err = metadb.ErrNotFound
			continue
		}
		if meta.ChannelID != key.ChannelID || meta.ChannelType != key.ChannelType {
			results[i].Err = metadb.ErrCorruptValue
			continue
		}
		results[i].Meta = meta
	}
	return results, nil
}

// BatchReadChannelRuntimeMetas reads arbitrary channel identities through the
// Slot proxy and returns one outcome aligned with every key.
func (s defaultChannelRuntimeMetaStore) BatchReadChannelRuntimeMetas(ctx context.Context, keys []metadb.ChannelKey) ([]channels.RuntimeMetaReadResult, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s.node == nil || s.node.defaultSlotProxy == nil {
		return nil, ErrNotStarted
	}
	reads, err := s.node.defaultSlotProxy.ReadChannelRuntimeMetadataBatch(ctx, keys)
	if err != nil {
		return nil, err
	}
	results := make([]channels.RuntimeMetaReadResult, len(keys))
	for i, key := range keys {
		if reads[i].Err != nil {
			results[i].Err = reads[i].Err
			continue
		}
		meta := reads[i].Meta
		if meta.ChannelID != key.ChannelID || meta.ChannelType != key.ChannelType {
			results[i].Err = metadb.ErrCorruptValue
			continue
		}
		results[i].Meta = meta
	}
	return results, nil
}

func (s defaultChannelRuntimeMetaStore) validateRuntimeMetaBatchRoute(items []channels.RuntimeMetaCreateItem, expected routing.Route) error {
	routes, err := s.validateRuntimeMetaBatchItems(items)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.SlotID != expected.SlotID || route.Leader != expected.Leader ||
			route.LeaderTerm != expected.LeaderTerm || route.ConfigEpoch != expected.ConfigEpoch ||
			route.Revision != expected.Revision {
			return fmt.Errorf("%w: runtime metadata batch route changed", metadb.ErrStaleMeta)
		}
	}
	return nil
}

func (s defaultChannelRuntimeMetaStore) validateRuntimeMetaBatchItems(items []channels.RuntimeMetaCreateItem) ([]Route, error) {
	if len(items) == 0 || len(items) > metafsm.MaxCreateChannelRuntimeMetaBatchItems {
		return nil, metadb.ErrInvalidArgument
	}
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = item.Meta.ChannelID
	}
	routes, err := s.node.RouteKeys(keys)
	if err != nil {
		return nil, err
	}
	if len(routes) != len(items) {
		return nil, fmt.Errorf("%w: aligned runtime metadata batch routes", metadb.ErrCorruptValue)
	}
	for i, route := range routes {
		if route.HashSlot != items[i].HashSlot {
			return nil, fmt.Errorf("%w: runtime metadata batch hash-slot changed", metadb.ErrStaleMeta)
		}
	}
	return routes, nil
}

func (s defaultChannelRuntimeMetaStore) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if s.node == nil || s.node.defaultSlotProxy == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	return s.node.defaultSlotProxy.GetChannelRuntimeMeta(ctx, channelID, channelType)
}

func (s defaultChannelRuntimeMetaStore) UpsertChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s.node == nil {
		return ErrNotStarted
	}
	ctx = propose.WithStageObserver(ctx, s.observer)
	return s.node.Propose(ctx, ProposeRequest{
		Key:     meta.ChannelID,
		Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(meta),
	})
}

var _ channels.RuntimeMetaReader = defaultChannelRuntimeMetaStore{}
var _ channels.RuntimeMetaBatchReader = defaultChannelRuntimeMetaStore{}
var _ channels.RuntimeMetaBatchStore = defaultChannelRuntimeMetaStore{}
var _ channels.RuntimeMetaWriter = defaultChannelRuntimeMetaStore{}

// defaultChannelMigrationStore adapts Slot-owned migration commands to Node.Propose.
type defaultChannelMigrationStore struct {
	node *Node
}

func (s defaultChannelMigrationStore) ProposeChannelMigrationCommand(ctx context.Context, slotID uint32, hashSlot uint16, command []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s.node == nil {
		return ErrNotStarted
	}
	return mapChannelMigrationRemoteError(s.node.Propose(ctx, ProposeRequest{
		Command: command,
		Target:  ProposeTarget{HashSlot: hashSlot, HasHashSlot: true, SlotID: slotID, HasSlotID: true},
	}))
}

func (s defaultChannelMigrationStore) GetChannelRuntimeMeta(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if s.node == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	return s.node.readChannelMigrationRuntimeMeta(ctx, hashSlot, channelID, channelType)
}

func (s defaultChannelMigrationStore) GetActiveChannelMigrationTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if s.node == nil {
		return metadb.ChannelMigrationTask{}, false, ErrNotStarted
	}
	return s.node.getActiveChannelMigrationTask(ctx, hashSlot, channelID, channelType)
}

func (s defaultChannelMigrationStore) GetChannelMigrationTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64, taskID string) (metadb.ChannelMigrationTask, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if s.node == nil {
		return metadb.ChannelMigrationTask{}, false, ErrNotStarted
	}
	return s.node.getChannelMigrationTask(ctx, hashSlot, channelID, channelType, taskID)
}

func (s defaultChannelMigrationStore) ListActiveChannelMigrationTasks(ctx context.Context, hashSlot uint16, limit int) ([]metadb.ChannelMigrationTask, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s.node == nil {
		return nil, ErrNotStarted
	}
	return s.node.listActiveChannelMigrationTasks(ctx, hashSlot, limit)
}

func (n *Node) discardDefaultChannels() {
	if n == nil || !n.defaultChannels {
		return
	}
	if n.channelRPCGateway != nil {
		n.channelRPCGateway.Clear()
	}
	if n.channelQuorumGateway != nil {
		n.channelQuorumGateway.Clear()
	}
	if n.channels != nil {
		_ = n.channels.Close()
	}
	if n.defaultChannelReplication != nil {
		_ = n.defaultChannelReplication.Close(context.Background())
		n.defaultChannelReplication = nil
	}
	n.channels = nil
	n.defaultChannels = false
	_ = n.closeDefaultChannelStore()
}

func (n *Node) discardDefaultSlots() {
	if n == nil || !n.defaultSlots {
		return
	}
	n.stopSlotLeaderLoop()
	if n.defaultSlotRuntime != nil {
		_ = n.defaultSlotRuntime.Close()
		n.defaultSlotRuntime = nil
	}
	n.slotRaftDiagnostics = nil
	n.slotStatusRuntime = nil
	if n.defaultSlotRaftDB != nil {
		_ = n.defaultSlotRaftDB.Close()
		n.defaultSlotRaftDB = nil
	}
	if n.defaultSlotMetaDB != nil {
		_ = n.defaultSlotMetaDB.Close()
		n.defaultSlotMetaDB = nil
	}
	n.defaultSlotProxy = nil
	n.defaultSlotProposer = nil
	n.slots = nil
	if n.defaultPreferredLeaderReconciler {
		n.preferredLeaderReconciler = nil
		n.defaultPreferredLeaderReconciler = false
	}
	n.defaultSlots = false
}

func (n *Node) discardDefaultControl() {
	if n == nil || !n.defaultControl {
		return
	}
	n.control = nil
	n.defaultControl = false
	if n.defaultControlRaftTransport != nil {
		n.defaultControlRaftTransport.Stop()
		n.defaultControlRaftTransport = nil
	}
}

func (n *Node) discardDefaultTransport() {
	if n == nil {
		return
	}
	n.mu.Lock()
	if !n.defaultTransport {
		n.mu.Unlock()
		return
	}
	client := n.transportClient
	server := n.transportServer
	n.transportClient = nil
	n.transportServer = nil
	n.slotStatusCaller = nil
	n.defaultTransport = false
	n.registeredRPCHandlers = nil
	n.channelRPCGateway = nil
	n.mu.Unlock()
	if client != nil {
		client.Stop()
	}
	if server != nil {
		server.Stop()
	}
}
