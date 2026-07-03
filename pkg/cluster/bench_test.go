package cluster

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var clusterBenchPayload = []byte("hello-cluster")

func BenchmarkRouteKey(b *testing.B) {
	router := newBenchRouter(b, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := router.RouteKey("bench-user"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouteKeys64(b *testing.B) {
	router := newBenchRouter(b, 1)
	keys := make([]string, 64)
	for i := range keys {
		keys[i] = "bench-user-" + strconv.Itoa(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := router.RouteKeys(keys); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocalPropose(b *testing.B) {
	router := newBenchRouter(b, 1)
	slots := &benchmarkSlotRuntime{localNode: 1, leader: 1}
	service := propose.NewService(propose.Config{LocalNode: 1, Router: router, Slots: slots})
	req := propose.Request{Key: "bench-user", Command: clusterBenchPayload}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := service.Propose(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForwardPropose(b *testing.B) {
	router := newBenchRouter(b, 2)
	network := clusternet.NewLocalNetwork()
	remoteSlots := &benchmarkSlotRuntime{localNode: 2, leader: 2}
	network.Register(2, clusternet.RPCSlotForwardPropose, propose.NewForwardHandler(remoteSlots))
	localSlots := &benchmarkSlotRuntime{localNode: 1, leader: 2}
	service := propose.NewService(propose.Config{LocalNode: 1, Router: router, Slots: localSlots, Forward: propose.NewNetworkForwardClient(network)})
	req := propose.Request{Key: "bench-user", Command: clusterBenchPayload}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := service.Propose(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChannelAppendLocal(b *testing.B) {
	node, channelID := newBenchChannelAppendNode(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := node.AppendChannel(ctx, channelruntime.AppendRequest{
			ChannelID:            channelID,
			CommitMode:           channelruntime.CommitModeLocal,
			ExpectedChannelEpoch: 1,
			ExpectedLeaderEpoch:  1,
			Message:              channelruntime.Message{MessageID: uint64(i + 1), Payload: clusterBenchPayload},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChannelAppendLocalParallel(b *testing.B) {
	node, channelID := newBenchChannelAppendNode(b)
	ctx := context.Background()
	var nextMessageID atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			messageID := nextMessageID.Add(1)
			_, err := node.AppendChannel(ctx, channelruntime.AppendRequest{
				ChannelID:            channelID,
				CommitMode:           channelruntime.CommitModeLocal,
				ExpectedChannelEpoch: 1,
				ExpectedLeaderEpoch:  1,
				Message:              channelruntime.Message{MessageID: messageID, Payload: clusterBenchPayload},
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkListPluginBindingsByPluginNo(b *testing.B) {
	node := newBenchPluginBindingNode(b)
	startNode(b, node)
	b.Cleanup(func() { stopNodes(b, node) })
	waitBenchRouteKeyLeaderReady(b, node, "bench-plugin-ready")

	ctx := context.Background()
	for i := 0; i < 1024; i++ {
		uid := "bench-plugin-user-" + strconv.Itoa(i)
		route, err := node.RouteKey(uid)
		if err != nil {
			b.Fatalf("RouteKey(%d) error = %v", i, err)
		}
		if err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).BindPluginUser(ctx, metadb.PluginUserBinding{UID: uid, PluginNo: "bench.receive", CreatedAtMS: int64(i), UpdatedAtMS: int64(i)}); err != nil {
			b.Fatalf("BindPluginUser(%d) error = %v", i, err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cursor := ""
		for {
			_, next, hasMore, err := node.ListPluginBindingsByPluginNo(ctx, "bench.receive", cursor, 100)
			if err != nil {
				b.Fatalf("ListPluginBindingsByPluginNo() error = %v", err)
			}
			if !hasMore {
				break
			}
			cursor = next
		}
	}
}

func newBenchPluginBindingNode(b *testing.B) *Node {
	b.Helper()
	cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: b.TempDir()}
	cfg.Control.ClusterID = "cluster-bench-plugin-binding"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1
	cfg.Channel.TickInterval = time.Millisecond
	node, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	return node
}

func waitBenchRouteKeyLeaderReady(b *testing.B, node *Node, key string) {
	b.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		route, err := node.RouteKey(key)
		if err == nil && route.Leader != 0 {
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	b.Fatalf("route key %q leader not ready: %v", key, lastErr)
}

func newBenchChannelAppendNode(b *testing.B) (*Node, channelruntime.ChannelID) {
	b.Helper()
	channelID := channelruntime.ChannelID{ID: "bench-local", Type: 1}
	meta := channelruntime.Meta{
		Key:         channelruntime.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelruntime.NodeID{1},
		ISR:         []channelruntime.NodeID{1},
		MinISR:      1,
		Status:      channelruntime.StatusActive,
	}
	service, err := channels.NewService(channels.Config{LocalNode: 1, Store: channelstore.NewMemoryFactory(), MetaSource: channels.NewStaticMetaSource([]channelruntime.Meta{meta})})
	if err != nil {
		b.Fatal(err)
	}
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: b.TempDir()}, WithChannels(service))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := node.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	return node, channelID
}

func newBenchRouter(b *testing.B, leader uint64) *routing.Router {
	b.Helper()
	router := routing.NewRouter()
	if err := router.UpdateControlSnapshot(benchControlSnapshot()); err != nil {
		b.Fatal(err)
	}
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: leader}})
	return router
}

func benchControlSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:10001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:10002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:10003", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
}

type benchmarkSlotRuntime struct {
	localNode uint64
	leader    uint64
	calls     int
}

func (r *benchmarkSlotRuntime) IsLocalLeader(uint32) bool { return r.localNode == r.leader }

func (r *benchmarkSlotRuntime) Propose(context.Context, uint32, []byte) error {
	r.calls++
	return nil
}
