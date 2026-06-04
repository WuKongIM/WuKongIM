package clusterv2

import (
	"context"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func (n *Node) ensureDefaultRuntime() (bool, error) {
	if n.control == nil {
		if err := n.ensureDefaultTransport(); err != nil {
			return false, err
		}
		n.registerPendingRPCHandlers()
		runtime, err := control.NewRuntime(control.RuntimeConfig{
			NodeID:           n.cfg.NodeID,
			Addr:             n.cfg.ListenAddr,
			StateDir:         n.cfg.Control.StateDir,
			ClusterID:        n.cfg.Control.ClusterID,
			Role:             control.RuntimeRole(n.cfg.Control.Role),
			Voters:           runtimeVoters(n.cfg.Control.Voters),
			AllowBootstrap:   n.cfg.Control.AllowBootstrap,
			InitialSlotCount: n.cfg.Slots.InitialSlotCount,
			HashSlotCount:    n.cfg.Slots.HashSlotCount,
			ReplicaCount:     n.cfg.Slots.ReplicaCount,
			RaftTransport:    control.NewRaftTransport(n.transportClient),
			RaftObserver:     n.cfg.Control.RaftObserver,
			SyncPeers:        control.NewStaticPeerPicker(n.transportClient, runtimeVoters(n.cfg.Control.Voters)),
		})
		if err != nil {
			return false, err
		}
		if n.cfg.Control.Role == ControlRoleVoter && n.transportServer != nil {
			n.transportServer.Register(clusternet.RPCControlRaft, control.NewRaftHandler(runtime))
			n.transportServer.Register(clusternet.RPCControlStateSync, control.NewStateSyncHandler(runtime))
		}
		n.control = runtime
		n.defaultControl = true
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
		storeFactory := channelstore.NewMessageDBFactoryWithOptions(n.defaultChannelStorePath(), channelstore.MessageDBFactoryOptions{
			CommitFlushWindow: n.cfg.Storage.CommitFlushWindow,
			CommitMaxRequests: n.cfg.Storage.CommitMaxRequests,
			CommitMaxRecords:  n.cfg.Storage.CommitMaxRecords,
			CommitMaxBytes:    n.cfg.Storage.CommitMaxBytes,
			CommitObserver:    n.cfg.Storage.CommitObserver,
		})
		var transport *channels.TransportClient
		if n.transportClient != nil {
			transport = channels.NewTransportClient(n.transportClient)
		}
		service, err := channels.NewService(channels.Config{
			LocalNode:                     channelv2.NodeID(n.cfg.NodeID),
			ReactorCount:                  n.cfg.Channel.ReactorCount,
			MailboxSize:                   n.cfg.Channel.MailboxSize,
			MaxChannels:                   n.cfg.Channel.MaxChannels,
			AppendBatchMaxRecords:         n.cfg.Channel.AppendBatchMaxRecords,
			AppendBatchMaxWait:            n.cfg.Channel.AppendBatchMaxWait,
			FollowerRecoveryProbeInterval: n.cfg.Channel.FollowerRecoveryProbeInterval,
			FollowerRecoveryProbeJitter:   n.cfg.Channel.FollowerRecoveryProbeJitter,
			Observer:                      n.cfg.Channel.Observer,
			Store:                         storeFactory,
			Transport:                     transport,
			MetaSource:                    n.defaultChannelMetaSource(),
		})
		if err != nil {
			_ = storeFactory.Close()
			return false, err
		}
		if n.transportServer != nil {
			channels.RegisterServiceHandlersOn(n.transportServer, service)
		}
		n.channels = service
		n.defaultChannels = true
		n.defaultChannelStore = storeFactory
		createdDefaultChannels = true
	}
	return createdDefaultChannels, nil
}

func (n *Node) ensureDefaultTransport() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.transportServer != nil && n.transportClient != nil {
		return nil
	}
	if n.discovery != nil {
		n.discovery.Update(controlVoterNodes(n.cfg.Control.Voters))
	}
	n.transportServer = clusternet.NewTransportServer(clusternet.TransportServerConfig{})
	n.transportClient = clusternet.NewTransportClient(clusternet.TransportClientConfig{Discovery: n.discovery})
	n.defaultTransport = true
	n.registeredRPCHandlers = make(map[uint8]struct{})
	return nil
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

func (n *Node) defaultChannelStorePath() string {
	return filepath.Join(n.cfg.DataDir, "messages")
}

func (n *Node) defaultChannelMetaSource() channels.ChannelMetaSource {
	if n == nil || n.defaultSlotMetaDB == nil {
		return nil
	}
	var observer channels.AppendStageObserver
	if n.cfg.Channel.Observer != nil {
		observer, _ = n.cfg.Channel.Observer.(channels.AppendStageObserver)
	}
	store := defaultChannelRuntimeMetaStore{node: n, observer: observer}
	return channels.NewSlotMetaSource(store, channels.SlotMetaSourceOptions{
		Placement: channels.NewSlotPlacementResolver(n.router, &n.channelDataNodes, int(n.cfg.Channel.ReplicaCount)),
		Observer:  observer,
	})
}

// defaultChannelRuntimeMetaStore reads Slot-owned channel metadata and writes through Node.Propose.
type defaultChannelRuntimeMetaStore struct {
	node     *Node
	observer channels.AppendStageObserver
}

func (s defaultChannelRuntimeMetaStore) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if s.node == nil || s.node.defaultSlotMetaDB == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	route, err := s.node.RouteKey(channelID)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return s.node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, channelID, channelType)
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

func (n *Node) discardDefaultSlots() {
	if n == nil || !n.defaultSlots {
		return
	}
	n.stopSlotLeaderLoop()
	if n.defaultSlotRuntime != nil {
		_ = n.defaultSlotRuntime.Close()
		n.defaultSlotRuntime = nil
	}
	if n.defaultSlotRaftDB != nil {
		_ = n.defaultSlotRaftDB.Close()
		n.defaultSlotRaftDB = nil
	}
	if n.defaultSlotMetaDB != nil {
		_ = n.defaultSlotMetaDB.Close()
		n.defaultSlotMetaDB = nil
	}
	n.defaultSlotProposer = nil
	n.slots = nil
	n.defaultSlots = false
}

func (n *Node) discardDefaultControl() {
	if n == nil || !n.defaultControl {
		return
	}
	n.control = nil
	n.defaultControl = false
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
	n.defaultTransport = false
	n.registeredRPCHandlers = nil
	n.mu.Unlock()
	if client != nil {
		client.Stop()
	}
	if server != nil {
		server.Stop()
	}
}
