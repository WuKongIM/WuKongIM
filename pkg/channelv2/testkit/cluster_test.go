package testkit

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/service"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeClusterCommitsWithMinISR2(t *testing.T) {
	h := NewClusterHarness(t, []ch.NodeID{1, 2, 3})
	defer h.Close()
	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	h.ApplyMetaToAll(meta)

	res, err := h.Nodes[1].Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
	h.WaitCommitted(t, 2, meta.ID, 1, time.Second)
}


func TestLeaderAppendNotifyCommitsWithoutBackgroundTicks(t *testing.T) {
	network := transport.NewLocalNetwork()
	nodes := make(map[ch.NodeID]ch.Cluster)
	for _, nodeID := range []ch.NodeID{1, 2, 3} {
		cluster, err := service.New(service.Config{
			LocalNode:                   nodeID,
			Store:                       store.NewMemoryFactory(),
			ReactorCount:                1,
			Transport:                   network.Client(),
			ReplicationIdlePollInterval: time.Hour,
		})
		require.NoError(t, err)
		defer cluster.Close()
		nodes[nodeID] = cluster
		server, ok := cluster.(transport.Server)
		require.True(t, ok)
		network.Register(nodeID, server)
	}
	meta := ch.Meta{Key: ch.ChannelKey("1:notify"), ID: ch.ChannelID{ID: "notify", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	for _, node := range nodes {
		require.NoError(t, node.ApplyMeta(meta))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := nodes[1].Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
}
