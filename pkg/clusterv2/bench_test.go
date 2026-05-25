package clusterv2

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

var clusterV2BenchPayload = []byte("hello-clusterv2")

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

func BenchmarkLocalPropose(b *testing.B) {
	router := newBenchRouter(b, 1)
	slots := &benchmarkSlotRuntime{localNode: 1, leader: 1}
	service := propose.NewService(propose.Config{LocalNode: 1, Router: router, Slots: slots})
	req := propose.Request{Key: "bench-user", Command: clusterV2BenchPayload}
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
	req := propose.Request{Key: "bench-user", Command: clusterV2BenchPayload}
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
	channelID := channelv2.ChannelID{ID: "bench-local", Type: 1}
	meta := channelv2.Meta{
		Key:         channelv2.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelv2.NodeID{1},
		ISR:         []channelv2.NodeID{1},
		MinISR:      1,
		Status:      channelv2.StatusActive,
	}
	service, err := channels.NewService(channels.Config{LocalNode: 1, Store: channelstore.NewMemoryFactory(), MetaSource: channels.NewStaticMetaSource([]channelv2.Meta{meta})})
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
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := node.AppendChannel(ctx, channelv2.AppendRequest{
			ChannelID:            channelID,
			CommitMode:           channelv2.CommitModeLocal,
			ExpectedChannelEpoch: 1,
			ExpectedLeaderEpoch:  1,
			Message:              channelv2.Message{MessageID: uint64(i + 1), Payload: clusterV2BenchPayload},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
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
