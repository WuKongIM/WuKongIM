package channelv2_test

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/service"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/testkit"
)

func BenchmarkAppendSingleNodeManyChannels(b *testing.B) {
	cluster := newBenchSingleNode(b)
	ctx := context.Background()
	for i := 0; i < 1024; i++ {
		meta := benchMeta(fmt.Sprintf("c-%d", i))
		if err := cluster.ApplyMeta(meta); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ch.ChannelID{ID: fmt.Sprintf("c-%d", i%1024), Type: 1}
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: uint64(i + 1), Payload: []byte("hello")}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendSingleNodeHotChannel(b *testing.B) {
	cluster := newBenchSingleNode(b)
	ctx := context.Background()
	meta := benchMeta("hot")
	if err := cluster.ApplyMeta(meta); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: uint64(i + 1), Payload: []byte("hello")}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendThreeNodeManyChannelsMemoryTransport(b *testing.B) {
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	ctx := context.Background()
	for i := 0; i < 128; i++ {
		h.ApplyMetaToAll(benchMetaThreeNode(fmt.Sprintf("c-%d", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ch.ChannelID{ID: fmt.Sprintf("c-%d", i%128), Type: 1}
		if _, err := h.Nodes[1].Append(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: uint64(i + 1), Payload: []byte("hello")}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFetchCommittedManyChannels(b *testing.B) {
	cluster := newBenchSingleNode(b)
	ctx := context.Background()
	for i := 0; i < 1024; i++ {
		meta := benchMeta(fmt.Sprintf("c-%d", i))
		if err := cluster.ApplyMeta(meta); err != nil {
			b.Fatal(err)
		}
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: uint64(i + 1), Payload: []byte("hello")}}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ch.ChannelID{ID: fmt.Sprintf("c-%d", i%1024), Type: 1}
		if _, err := cluster.Fetch(ctx, ch.FetchRequest{ChannelID: id, FromSeq: 1, Limit: 1, MaxBytes: 1024}); err != nil {
			b.Fatal(err)
		}
	}
}

func newBenchSingleNode(b *testing.B) ch.Cluster {
	b.Helper()
	cluster, err := service.New(service.Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 4})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cluster.Close() })
	return cluster
}

func benchMeta(id string) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	return ch.Meta{Key: ch.ChannelKeyForID(channelID), ID: channelID, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
}

func benchMetaThreeNode(id string) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	return ch.Meta{Key: ch.ChannelKeyForID(channelID), ID: channelID, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
}
