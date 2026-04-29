package app

import (
	"context"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestNetworkObservabilityRecordsTransportAndRPCWindow(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID:   1,
		LocalNodeName: "node-1",
		Window:        time.Minute,
		MaxEvents:     50,
		Now:           func() time.Time { return now },
	})
	hooks := collector.TransportHooks()

	hooks.OnSend(1, 1024)
	hooks.OnReceive(2, 512)
	hooks.OnDial(transport.DialEvent{TargetNode: 2, Result: "dial_error", Duration: 5 * time.Millisecond})
	hooks.OnEnqueue(transport.EnqueueEvent{TargetNode: 2, Kind: "rpc", Result: "queue_full"})
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 35, Inflight: 1})
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 35, Result: "timeout", Duration: 200 * time.Millisecond, Inflight: 0})

	snap := collector.NetworkSnapshot(now)
	require.True(t, snap.LocalCollectorAvailable)
	require.Equal(t, int64(1024), snap.Traffic.TXBytes1m)
	require.Equal(t, int64(512), snap.Traffic.RXBytes1m)
	require.False(t, snap.Traffic.PeerBreakdownAvailable)
	require.Equal(t, 1, snap.PeerErrors[uint64(2)].DialError1m)
	require.Equal(t, 1, snap.PeerErrors[uint64(2)].QueueFull1m)
	require.Equal(t, 0, snap.PeerErrors[uint64(2)].Timeout1m)
	require.Equal(t, "channel_long_poll_fetch", snap.Services[0].Service)
	require.Equal(t, 1, snap.Services[0].ExpectedTimeout1m)
	require.Equal(t, 0, snap.Services[0].Timeout1m)
	require.Equal(t, 0, snap.ChannelReplication.LongPollTimeouts1m)
	require.False(t, hasNetworkEvent(snap.Events, "rpc_timeout", "channel_long_poll_fetch"))
	require.NotEmpty(t, snap.Events)
}

func TestNetworkObservabilityPrunesOldEvents(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Window:      time.Minute,
		Now:         func() time.Time { return now },
	})
	hooks := collector.TransportHooks()

	hooks.OnDial(transport.DialEvent{TargetNode: 2, Result: "dial_error", Duration: 5 * time.Millisecond})
	now = now.Add(61 * time.Second)
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 33, Result: "ok", Duration: 10 * time.Millisecond})

	snap := collector.NetworkSnapshot(now)
	require.Equal(t, 0, snap.PeerErrors[uint64(2)].DialError1m)
	require.False(t, hasNetworkEvent(snap.Events, "dial_error", ""))
	require.Len(t, snap.Services, 1)
	require.Equal(t, "channel_append", snap.Services[0].Service)
	require.Equal(t, 1, snap.Services[0].Calls1m)
}

func TestNetworkObservabilityPrunesStoredSamplesAndEventsOnWrite(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Window:      time.Minute,
		Now:         func() time.Time { return now },
	})
	hooks := collector.TransportHooks()

	hooks.OnSend(1, 100)
	hooks.OnReceive(2, 200)
	hooks.OnDial(transport.DialEvent{TargetNode: 2, Result: "dial_error", Duration: 5 * time.Millisecond})
	hooks.OnEnqueue(transport.EnqueueEvent{TargetNode: 2, Kind: "rpc", Result: "queue_full"})
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 33, Result: "remote_error", Duration: 5 * time.Millisecond})

	now = now.Add(61 * time.Second)
	hooks.OnSend(3, 300)

	collector.mu.Lock()
	defer collector.mu.Unlock()
	require.Len(t, collector.traffic, 1)
	require.Empty(t, collector.dials)
	require.Empty(t, collector.enqueues)
	require.Empty(t, collector.rpcs)
	require.Empty(t, collector.events)
}

func TestNetworkObservabilityCapsStoredSamplesOnWrite(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Window:      time.Minute,
		Now:         func() time.Time { return now },
	})
	hooks := collector.TransportHooks()

	for i := 0; i < defaultNetworkObservabilityMaxSamples+1; i++ {
		hooks.OnSend(1, 1)
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	require.Len(t, collector.traffic, defaultNetworkObservabilityMaxSamples)
}

func TestNetworkObservabilityCapsStoredEventsOnWrite(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Window:      time.Minute,
		MaxEvents:   2,
		Now:         func() time.Time { return now },
	})
	hooks := collector.TransportHooks()

	hooks.OnDial(transport.DialEvent{TargetNode: 2, Result: "dial_error", Duration: 5 * time.Millisecond})
	hooks.OnDial(transport.DialEvent{TargetNode: 3, Result: "dial_error", Duration: 5 * time.Millisecond})
	hooks.OnDial(transport.DialEvent{TargetNode: 4, Result: "dial_error", Duration: 5 * time.Millisecond})

	collector.mu.Lock()
	defer collector.mu.Unlock()
	require.Len(t, collector.events, 2)
	require.Equal(t, uint64(3), collector.events[0].TargetNode)
	require.Equal(t, uint64(4), collector.events[1].TargetNode)
}

func TestNetworkObservabilityDropsZeroInflightServicesAfterSamplesExpire(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Window:      time.Minute,
		Now:         func() time.Time { return now },
	})
	hooks := collector.TransportHooks()

	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 33, Inflight: 1})
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 33, Inflight: 0})
	now = now.Add(61 * time.Second)

	snap := collector.NetworkSnapshot(now)
	require.Empty(t, snap.Services)
}

func TestNetworkObservabilityCallsDataPlanePoolStatsOutsideLock(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Window:      time.Minute,
		Now:         func() time.Time { return now },
	})
	hooks := collector.TransportHooks()
	collector.cfg.DataPlanePoolStats = func() []transport.PoolPeerStats {
		hooks.OnSend(1, 10)
		return []transport.PoolPeerStats{{NodeID: 2, Active: 1}}
	}

	done := make(chan managementusecase.NetworkObservationSnapshot, 1)
	go func() {
		done <- collector.NetworkSnapshot(now)
	}()

	select {
	case snap := <-done:
		require.Equal(t, []managementusecase.NetworkPoolPeerStats{{NodeID: 2, Active: 1}}, snap.DataPlanePools)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("NetworkSnapshot deadlocked while DataPlanePoolStats re-entered collector hooks")
	}
}

func TestNetworkObservabilityLongPollTimeoutCountedOnceInManagementSummary(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Window:      time.Minute,
		Now:         func() time.Time { return now },
	})

	collector.TransportHooks().OnRPCClient(transport.RPCClientEvent{
		TargetNode: 2,
		ServiceID:  35,
		Result:     "timeout",
		Duration:   200 * time.Millisecond,
	})

	manager := managementusecase.New(managementusecase.Options{
		LocalNodeID: 1,
		Cluster: fakeObservabilityCluster{
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Status: controllermeta.NodeStatusAlive},
				{NodeID: 2, Status: controllermeta.NodeStatusAlive},
			},
		},
		Network: collector,
		Now:     func() time.Time { return now },
	})

	summary, err := manager.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, summary.Services[0].ExpectedTimeout1m)
	require.Equal(t, 1, summary.ChannelReplication.LongPollTimeouts1m)
	require.Len(t, summary.ChannelReplication.Services, 1)
	require.Equal(t, "channel_long_poll_fetch", summary.ChannelReplication.Services[0].Service)
}

func TestNetworkObservabilitySnapshotIncludesConfigAndDataPlanePools(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID:                   1,
		LocalNodeName:                 "node-1",
		ListenAddr:                    "0.0.0.0:12000",
		AdvertiseAddr:                 "10.0.0.1:12000",
		Seeds:                         []string{"10.0.0.2:12000"},
		StaticNodes:                   []NodeConfigRef{{ID: 1, Addr: "10.0.0.1:12000"}, {ID: 2, Addr: "10.0.0.2:12000"}},
		PoolSize:                      4,
		DataPlanePoolSize:             6,
		DialTimeout:                   3 * time.Second,
		ControllerObservationInterval: 4 * time.Second,
		DataPlaneRPCTimeout:           5 * time.Second,
		LongPollLaneCount:             8,
		LongPollMaxWait:               200 * time.Millisecond,
		LongPollMaxBytes:              64 * 1024,
		LongPollMaxChannels:           32,
		Now:                           func() time.Time { return now },
		DataPlanePoolStats: func() []transport.PoolPeerStats {
			return []transport.PoolPeerStats{{NodeID: 2, Active: 3, Idle: 1}}
		},
	})

	snap := collector.NetworkSnapshot(now)
	require.Equal(t, "0.0.0.0:12000", snap.Discovery.ListenAddr)
	require.Equal(t, "10.0.0.1:12000", snap.Discovery.AdvertiseAddr)
	require.Equal(t, []string{"10.0.0.2:12000"}, snap.Discovery.Seeds)
	require.Equal(t, []managementusecase.NetworkDiscoveryNode{{NodeID: 1, Addr: "10.0.0.1:12000"}, {NodeID: 2, Addr: "10.0.0.2:12000"}}, snap.Discovery.StaticNodes)
	require.Equal(t, 4, snap.Discovery.PoolSize)
	require.Equal(t, 6, snap.Discovery.DataPlanePoolSize)
	require.Equal(t, 3*time.Second, snap.Discovery.DialTimeout)
	require.Equal(t, 4*time.Second, snap.Discovery.ControllerObservationInterval)
	require.Equal(t, 5*time.Second, snap.ChannelReplication.DataPlaneRPCTimeout)
	require.Equal(t, managementusecase.NetworkLongPollConfig{LaneCount: 8, MaxWait: 200 * time.Millisecond, MaxBytes: 64 * 1024, MaxChannels: 32}, snap.ChannelReplication.LongPollConfig)
	require.Equal(t, []managementusecase.NetworkPoolPeerStats{{NodeID: 2, Active: 3, Idle: 1}}, snap.DataPlanePools)
}

func TestNetworkObservabilityClusterHooksRecordsNodeStatusEvents(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	collector := newNetworkObservability(networkObservabilityConfig{
		LocalNodeID: 1,
		Now:         func() time.Time { return now },
	})

	collector.ClusterHooks().OnNodeStatusChange(2, controllermeta.NodeStatusAlive, controllermeta.NodeStatusSuspect)

	snap := collector.NetworkSnapshot(now)
	require.True(t, hasNetworkEvent(snap.Events, "node_status_change", ""))
}

func hasNetworkEvent(events []managementusecase.NetworkEvent, kind, service string) bool {
	for _, event := range events {
		if event.Kind == kind && event.Service == service {
			return true
		}
	}
	return false
}
