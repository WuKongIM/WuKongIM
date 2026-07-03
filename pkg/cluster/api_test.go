package cluster_test

import (
	"context"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestPublicAPICompile(t *testing.T) {
	cfg := cluster.Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
	node, err := cluster.New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_ = node.NodeID()
	_ = node.Snapshot()
	_, _ = node.RouteKey("u1")
	_, _ = node.RouteKeys([]string{"u1", "u2"})
	_, _ = node.RouteHashSlot(0)
	_ = node.Propose(context.Background(), cluster.ProposeRequest{Key: "u1", Command: []byte("cmd")})
	_, _ = node.AppendChannel(context.Background(), channelruntime.AppendRequest{})
	_, _ = node.AppendChannelBatch(context.Background(), channelruntime.AppendBatchRequest{})
	_, _ = node.ReadChannelCommitted(context.Background(), channelruntime.ChannelID{}, channelstore.ReadCommittedRequest{})
	_ = node.Stop(context.Background())
}

func TestProposeTargetAllowsHashSlotZero(t *testing.T) {
	req := cluster.ProposeRequest{Command: []byte("cmd"), Target: cluster.ProposeTarget{HashSlot: 0, HasHashSlot: true}}
	if !req.Target.HasHashSlot || req.Target.HashSlot != 0 {
		t.Fatal("hash slot zero must be explicitly representable")
	}
}
