package testkit

import (
	"context"
	"sort"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/service"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/stretchr/testify/require"
)

// ClusterHarness is an in-memory multi-node channelv2 test cluster.
type ClusterHarness struct {
	Nodes   map[ch.NodeID]ch.Cluster
	Network *transport.LocalNetwork
	stop    chan struct{}
}

// NewClusterHarness creates nodes backed by memory stores and local transport.
func NewClusterHarness(t testing.TB, nodeIDs []ch.NodeID) *ClusterHarness {
	t.Helper()
	network := transport.NewLocalNetwork()
	h := &ClusterHarness{Nodes: make(map[ch.NodeID]ch.Cluster), Network: network, stop: make(chan struct{})}
	for _, nodeID := range nodeIDs {
		cluster, err := service.New(service.Config{LocalNode: nodeID, Store: store.NewMemoryFactory(), ReactorCount: 1, Transport: network.Client()})
		require.NoError(t, err)
		h.Nodes[nodeID] = cluster
		if server, ok := cluster.(transport.Server); ok {
			network.Register(nodeID, server)
		}
	}
	h.startTicks()
	return h
}

// ApplyMetaToAll applies authoritative metadata to every harness node.
func (h *ClusterHarness) ApplyMetaToAll(meta ch.Meta) {
	for _, node := range h.Nodes {
		_ = node.ApplyMeta(meta)
	}
}

// WaitCommitted polls a node until Fetch exposes seq as committed.
func (h *ClusterHarness) WaitCommitted(t testing.TB, nodeID ch.NodeID, id ch.ChannelID, seq uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := h.Nodes[nodeID].Fetch(context.Background(), ch.FetchRequest{ChannelID: id, FromSeq: seq, Limit: 1, MaxBytes: 1024})
		if err == nil && res.CommittedSeq >= seq && len(res.Messages) > 0 {
			return
		}
		_ = h.Nodes[nodeID].Tick(context.Background())
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not commit seq %d", nodeID, seq)
}

// TickAll advances every harness node once in stable node-id order.
func (h *ClusterHarness) TickAll(ctx context.Context) error {
	ids := make([]ch.NodeID, 0, len(h.Nodes))
	for id := range h.Nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := h.Nodes[id].Tick(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Close closes all nodes.
func (h *ClusterHarness) Close() {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
	for _, node := range h.Nodes {
		_ = node.Close()
	}
}

func (h *ClusterHarness) startTicks() {
	for _, node := range h.Nodes {
		node := node
		go func() {
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-h.stop:
					return
				case <-ticker.C:
					_ = node.Tick(context.Background())
				}
			}
		}()
	}
}
