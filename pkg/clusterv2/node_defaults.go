package clusterv2

import (
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
)

func (n *Node) ensureDefaultRuntime() (bool, error) {
	if n.control == nil {
		if err := n.ensureDefaultTransport(); err != nil {
			return false, err
		}
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
		storeFactory := channelstore.NewMessageDBFactory(n.defaultChannelStorePath())
		var transport *channels.TransportClient
		if n.transportClient != nil {
			transport = channels.NewTransportClient(n.transportClient)
		}
		service, err := channels.NewService(channels.Config{
			LocalNode:    channelv2.NodeID(n.cfg.NodeID),
			ReactorCount: n.cfg.Channel.ReactorCount,
			MailboxSize:  n.cfg.Channel.MailboxSize,
			Store:        storeFactory,
			Transport:    transport,
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
	if n.transportServer != nil && n.transportClient != nil {
		return nil
	}
	if n.discovery != nil {
		n.discovery.Update(controlVoterNodes(n.cfg.Control.Voters))
	}
	n.transportServer = clusternet.NewTransportServer(clusternet.TransportServerConfig{})
	n.transportClient = clusternet.NewTransportClient(clusternet.TransportClientConfig{Discovery: n.discovery, PoolSize: 1})
	n.defaultTransport = true
	return nil
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
	if n == nil || !n.defaultTransport {
		return
	}
	if n.transportClient != nil {
		n.transportClient.Stop()
	}
	if n.transportServer != nil {
		n.transportServer.Stop()
	}
	n.transportClient = nil
	n.transportServer = nil
	n.defaultTransport = false
}
