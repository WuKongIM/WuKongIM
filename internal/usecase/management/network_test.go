package management

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestListNetworkSummaryReturnsSingleNodeClusterEmptyState(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{{
				NodeID:          1,
				Name:            "node-1",
				Addr:            "127.0.0.1:7001",
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: now.Add(-time.Second),
			}},
		},
		Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
			LocalCollectorAvailable: true,
			Traffic:                 NetworkTraffic{Scope: "local_total_by_msg_type"},
		}},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, now, got.GeneratedAt)
	require.Equal(t, "local_node", got.Scope.View)
	require.Equal(t, uint64(1), got.Scope.LocalNodeID)
	require.Equal(t, uint64(1), got.Scope.ControllerLeaderID)
	require.Equal(t, 0, got.Headline.RemotePeers)
	require.Empty(t, got.Peers)
	require.Equal(t, "ok", got.SourceStatus.LocalCollector)
	require.Equal(t, "ok", got.SourceStatus.ControllerContext)
	require.Equal(t, "ok", got.SourceStatus.RuntimeViews)
	require.False(t, got.Traffic.PeerBreakdownAvailable)
}

func TestListNetworkSummaryAggregatesPeersPoolsRPCAndTraffic(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	events := make([]NetworkEvent, 0, 55)
	for i := 0; i < 55; i++ {
		events = append(events, NetworkEvent{
			At:         now.Add(time.Duration(i) * time.Second),
			Severity:   "warn",
			Kind:       "queue_full",
			TargetNode: 2,
			Service:    "delivery_push",
			Message:    fmt.Sprintf("event-%02d", i),
		})
	}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{
			controllerLeaderID: 2,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Name: "node-1", Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive, LastHeartbeatAt: now},
				{NodeID: 2, Name: "node-2", Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive, LastHeartbeatAt: now.Add(-2 * time.Second)},
				{NodeID: 3, Name: "node-3", Addr: "127.0.0.1:7003", Status: controllermeta.NodeStatusSuspect, LastHeartbeatAt: now.Add(-3 * time.Second)},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, LastReportAt: now.Add(-time.Minute)},
				{SlotID: 2, LastReportAt: now.Add(-time.Second)},
			},
			transportStats: []transport.PoolPeerStats{{NodeID: 2, Active: 2, Idle: 1}},
		},
		Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
			LocalCollectorAvailable: true,
			Traffic: NetworkTraffic{
				Scope:     "local_total_by_msg_type",
				TXBytes1m: 1024,
				RXBytes1m: 512,
				TXBps:     128,
				RXBps:     64,
			},
			DataPlanePools: []NetworkPoolPeerStats{{NodeID: 2, Active: 3, Idle: 4}},
			Services: []NetworkRPCService{
				{ServiceID: 35, Service: "channel_long_poll_fetch", Group: "channel_data_plane", TargetNode: 2, Calls1m: 1, ExpectedTimeout1m: 1, P95Ms: 100, LastSeenAt: now.Add(-time.Second)},
				{ServiceID: 7, Service: "delivery_push", Group: "usecase", TargetNode: 2, Inflight: 1, Calls1m: 2, Success1m: 1, Timeout1m: 1, P95Ms: 20, LastSeenAt: now},
			},
			Events: events,
		}},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, got.Headline.RemotePeers)
	require.Equal(t, 2, got.Headline.AliveNodes)
	require.Equal(t, 1, got.Headline.SuspectNodes)
	require.Equal(t, 5, got.Headline.PoolActive)
	require.Equal(t, 5, got.Headline.PoolIdle)
	require.Equal(t, 1, got.Headline.RPCInflight)
	require.Equal(t, 1, got.Headline.Timeouts1m)
	require.Equal(t, 1, got.Headline.StaleObservations)
	require.Equal(t, int64(1024), got.Traffic.TXBytes1m)
	require.Equal(t, int64(512), got.Traffic.RXBytes1m)
	require.False(t, got.Traffic.PeerBreakdownAvailable)
	require.Len(t, got.Peers, 2)
	require.Equal(t, uint64(2), got.Peers[0].NodeID)
	require.Equal(t, "node-2", got.Peers[0].Name)
	require.Equal(t, NetworkPoolStats{Active: 2, Idle: 1}, got.Peers[0].Pools.Cluster)
	require.Equal(t, NetworkPoolStats{Active: 3, Idle: 4}, got.Peers[0].Pools.DataPlane)
	require.Equal(t, 1, got.Peers[0].RPC.Inflight)
	require.Equal(t, 2, got.Peers[0].RPC.Calls1m)
	require.Equal(t, 1, got.Peers[0].Errors.Timeout1m)
	require.Equal(t, uint64(3), got.Peers[1].NodeID)
	require.Equal(t, "suspect", got.Peers[1].Health)
	require.Equal(t, []string{"channel_data_plane/channel_long_poll_fetch/2", "usecase/delivery_push/2"}, serviceSortKeys(got.Services))
	require.Equal(t, 1, got.Services[0].ExpectedTimeout1m)
	require.Equal(t, 0, got.Services[0].Timeout1m)
	require.Equal(t, 1, got.Services[1].Timeout1m)
	require.Equal(t, 1, got.ChannelReplication.LongPollTimeouts1m)
	require.Equal(t, NetworkPoolStats{Active: 3, Idle: 4}, got.ChannelReplication.Pool)
	require.Len(t, got.Events, 50)
	require.Equal(t, "event-54", got.Events[0].Message)
	require.Equal(t, "event-05", got.Events[49].Message)
}

func TestListNetworkSummaryPreservesLocalCollectorWhenControllerReadsFail(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{
			controllerLeaderID:          1,
			listNodesErr:                errors.New("nodes unavailable"),
			listObservedRuntimeViewsErr: errors.New("runtime views unavailable"),
		},
		Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
			LocalCollectorAvailable: true,
			Traffic:                 NetworkTraffic{Scope: "local_total_by_msg_type", TXBytes1m: 7},
			DataPlanePools:          []NetworkPoolPeerStats{{NodeID: 2, Active: 1, Idle: 1}},
			Services: []NetworkRPCService{{
				ServiceID:  7,
				Service:    "delivery_push",
				Group:      "usecase",
				TargetNode: 2,
				Inflight:   2,
				Calls1m:    3,
				Success1m:  2,
			}},
			Events: []NetworkEvent{{At: now, Severity: "info", Kind: "rpc_ok", TargetNode: 2, Service: "delivery_push", Message: "rpc ok"}},
		}},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", got.SourceStatus.LocalCollector)
	require.Equal(t, "unavailable", got.SourceStatus.ControllerContext)
	require.Equal(t, "unavailable", got.SourceStatus.RuntimeViews)
	require.Contains(t, got.SourceStatus.Errors["nodes"], "nodes unavailable")
	require.Contains(t, got.SourceStatus.Errors["runtime_views"], "runtime views unavailable")
	require.Equal(t, int64(7), got.Traffic.TXBytes1m)
	require.Len(t, got.Services, 1)
	require.Len(t, got.Events, 1)
	require.Len(t, got.Peers, 1)
	require.Equal(t, uint64(2), got.Peers[0].NodeID)
}

func TestListNetworkSummaryUsesRuntimeViewsWhenNodeReadFails(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	app := New(Options{
		LocalNodeID:              1,
		ScaleInRuntimeViewMaxAge: 10 * time.Second,
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			listNodesErr:       errors.New("nodes unavailable"),
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, LastReportAt: now.Add(-time.Minute)},
				{SlotID: 2, LastReportAt: now.Add(-time.Second)},
			},
			transportStats: []transport.PoolPeerStats{{NodeID: 2, Active: 1, Idle: 2}},
		},
		Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
			LocalCollectorAvailable: true,
			Traffic:                 NetworkTraffic{Scope: "local_total_by_msg_type"},
		}},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, "unavailable", got.SourceStatus.ControllerContext)
	require.Equal(t, "ok", got.SourceStatus.RuntimeViews)
	require.Contains(t, got.SourceStatus.Errors["nodes"], "nodes unavailable")
	require.NotContains(t, got.SourceStatus.Errors, "runtime_views")
	require.Equal(t, 1, got.Headline.StaleObservations)
	require.Equal(t, 0, got.Headline.AliveNodes)
	require.Len(t, got.Peers, 1)
	require.Equal(t, uint64(2), got.Peers[0].NodeID)
	require.Equal(t, NetworkPoolStats{Active: 1, Idle: 2}, got.Peers[0].Pools.Cluster)
}

func TestListNetworkSummaryUsesNodesWhenRuntimeViewReadFails(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{
			controllerLeaderID:          1,
			listObservedRuntimeViewsErr: errors.New("runtime views unavailable"),
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Name: "node-1", Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive, LastHeartbeatAt: now},
				{NodeID: 2, Name: "node-2", Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining, LastHeartbeatAt: now.Add(-time.Second)},
			},
		},
		Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
			LocalCollectorAvailable: true,
			Traffic:                 NetworkTraffic{Scope: "local_total_by_msg_type"},
		}},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", got.SourceStatus.ControllerContext)
	require.Equal(t, "unavailable", got.SourceStatus.RuntimeViews)
	require.NotContains(t, got.SourceStatus.Errors, "nodes")
	require.Contains(t, got.SourceStatus.Errors["runtime_views"], "runtime views unavailable")
	require.Equal(t, 1, got.Headline.AliveNodes)
	require.Equal(t, 1, got.Headline.DrainingNodes)
	require.Equal(t, 0, got.Headline.StaleObservations)
	require.Len(t, got.Peers, 1)
	require.Equal(t, uint64(2), got.Peers[0].NodeID)
	require.Equal(t, "node-2", got.Peers[0].Name)
	require.Equal(t, "draining", got.Peers[0].Health)
}

func TestListNetworkSummaryKeepsExpectedLongPollTimeoutsOutOfPeerRPCRate(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Status: controllermeta.NodeStatusAlive},
				{NodeID: 2, Status: controllermeta.NodeStatusAlive},
			},
		},
		Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
			LocalCollectorAvailable: true,
			Traffic:                 NetworkTraffic{Scope: "local_total_by_msg_type"},
			Services: []NetworkRPCService{{
				ServiceID:         35,
				Service:           "channel_long_poll_fetch",
				Group:             "channel_data_plane",
				TargetNode:        2,
				Calls1m:           1,
				ExpectedTimeout1m: 1,
			}},
		}},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Len(t, got.Peers, 1)
	require.Equal(t, 0, got.Peers[0].RPC.Calls1m)
	require.Nil(t, got.Peers[0].RPC.SuccessRate)
	require.Equal(t, 0, got.Peers[0].Errors.Timeout1m)
	require.Equal(t, 0, got.Headline.Timeouts1m)
	require.Equal(t, 1, got.Services[0].ExpectedTimeout1m)
	require.Equal(t, 1, got.ChannelReplication.LongPollTimeouts1m)
}

func TestListNetworkSummaryCopiesHistoryFromLocalCollector(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	sourceHistory := NetworkHistory{
		Window: time.Minute,
		Step:   5 * time.Second,
		Traffic: []NetworkTrafficHistoryPoint{{
			At:      now.Add(-5 * time.Second),
			TXBytes: 10,
			RXBytes: 20,
		}},
		RPC: []NetworkRPCHistoryPoint{{
			At:               now.Add(-5 * time.Second),
			Calls:            3,
			Success:          1,
			Errors:           1,
			ExpectedTimeouts: 1,
		}},
		Errors: []NetworkErrorHistoryPoint{{
			At:           now.Add(-5 * time.Second),
			DialErrors:   1,
			QueueFull:    2,
			Timeouts:     3,
			RemoteErrors: 4,
		}},
	}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{{
				NodeID: 1,
				Status: controllermeta.NodeStatusAlive,
			}},
		},
		Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
			LocalCollectorAvailable: true,
			Traffic:                 NetworkTraffic{Scope: "local_total_by_msg_type"},
			History:                 sourceHistory,
		}},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, "local_node", got.Scope.View)
	require.Equal(t, sourceHistory, got.History)

	sourceHistory.Traffic[0].TXBytes = 99
	sourceHistory.RPC[0].Calls = 99
	sourceHistory.Errors[0].DialErrors = 99
	require.Equal(t, int64(10), got.History.Traffic[0].TXBytes)
	require.Equal(t, 3, got.History.RPC[0].Calls)
	require.Equal(t, 1, got.History.Errors[0].DialErrors)
}

func TestListNetworkSummaryMarksNilNetworkCollectorUnavailable(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{{
				NodeID: 1,
				Status: controllermeta.NodeStatusAlive,
			}},
		},
		Now: func() time.Time { return now },
	})

	got, err := app.ListNetworkSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, "unavailable", got.SourceStatus.LocalCollector)
	require.Equal(t, "ok", got.SourceStatus.ControllerContext)
	require.Equal(t, "ok", got.SourceStatus.RuntimeViews)
	require.Equal(t, "local_total_by_msg_type", got.Traffic.Scope)
	require.False(t, got.Traffic.PeerBreakdownAvailable)
}

type fakeNetworkSnapshotReader struct {
	snapshot NetworkObservationSnapshot
}

func (f fakeNetworkSnapshotReader) NetworkSnapshot(time.Time) NetworkObservationSnapshot {
	return f.snapshot
}

func serviceSortKeys(services []NetworkRPCService) []string {
	keys := make([]string, 0, len(services))
	for _, service := range services {
		keys = append(keys, fmt.Sprintf("%s/%s/%d", service.Group, service.Service, service.TargetNode))
	}
	return keys
}

func (f *fakeNodeOperatorCluster) TransportPoolStats() []transport.PoolPeerStats {
	return nil
}
