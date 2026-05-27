package clusterv2_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestPublicAPICompile(t *testing.T) {
	cfg := clusterv2.Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
	node, err := clusterv2.New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_ = node.NodeID()
	_ = node.Snapshot()
	_, _ = node.RouteKey("u1")
	_, _ = node.RouteHashSlot(0)
	_ = node.Propose(context.Background(), clusterv2.ProposeRequest{Key: "u1", Command: []byte("cmd")})
	_, _ = node.AppendChannel(context.Background(), channelv2.AppendRequest{})
	_, _ = node.AppendChannelBatch(context.Background(), channelv2.AppendBatchRequest{})
	_ = node.Stop(context.Background())
}

func TestProposeTargetAllowsHashSlotZero(t *testing.T) {
	req := clusterv2.ProposeRequest{Command: []byte("cmd"), Target: clusterv2.ProposeTarget{HashSlot: 0, HasHashSlot: true}}
	if !req.Target.HasHashSlot || req.Target.HashSlot != 0 {
		t.Fatal("hash slot zero must be explicitly representable")
	}
}
