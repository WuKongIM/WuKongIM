package cluster

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

func newDefaultSingleNode(t testing.TB) *Node {
	t.Helper()
	cfg := Config{NodeID: 1, ListenAddr: freeTCPAddr(t.(*testing.T)), DataDir: t.TempDir()}
	cfg.Control.ClusterID = "cluster-integration-single"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1
	cfg.Channel.TickInterval = time.Millisecond
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New(single-node cluster) error = %v", err)
	}
	return node
}

func newDefaultThreeNodeCluster(t testing.TB) []*Node {
	t.Helper()
	tb := t.(*testing.T)
	addrs := []string{freeTCPAddr(tb), freeTCPAddr(tb), freeTCPAddr(tb)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "cluster-integration-three"
		cfg.Control.Voters = voters
		cfg.Control.AllowBootstrap = true
		cfg.Slots.InitialSlotCount = 1
		cfg.Slots.HashSlotCount = 4
		cfg.Slots.ReplicaCount = 3
		cfg.Channel.TickInterval = time.Millisecond
		node, err := New(cfg)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", voter.NodeID, err)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func startNode(t testing.TB, node *Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start(node=%d) error = %v", node.NodeID(), err)
	}
}

func startNodes(t testing.TB, nodes ...*Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type startResult struct {
		nodeID uint64
		err    error
	}
	results := make(chan startResult, len(nodes))
	for _, node := range nodes {
		node := node
		go func() {
			results <- startResult{nodeID: node.NodeID(), err: node.Start(ctx)}
		}()
	}
	var firstFailure startResult
	for range nodes {
		result := <-results
		if result.err != nil && firstFailure.err == nil {
			firstFailure = result
			cancel()
		}
	}
	if firstFailure.err == nil {
		return
	}
	for i := len(nodes) - 1; i >= 0; i-- {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = nodes[i].Stop(stopCtx)
		stopCancel()
	}
	t.Fatalf("Start(node=%d) error = %v", firstFailure.nodeID, firstFailure.err)
}

func stopNodes(t testing.TB, nodes ...*Node) {
	t.Helper()
	for i := len(nodes) - 1; i >= 0; i-- {
		if nodes[i] == nil {
			continue
		}
		if err := nodes[i].Stop(context.Background()); err != nil {
			t.Fatalf("Stop(node=%d) error = %v", nodes[i].NodeID(), err)
		}
	}
}

func waitClusterReady(t testing.TB, nodes ...*Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := WaitClusterReady(ctx, nodes...); err != nil {
		t.Fatalf("WaitClusterReady() error = %v", err)
	}
}

// waitNodeWriteReady proves that the routed Slot runtime can commit a bounded metadata write.
func waitNodeWriteReady(t testing.TB, node *Node) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		lastErr = node.ProbeWriteReady(ctx)
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ProbeWriteReady(node=%d) error = %v", node.NodeID(), lastErr)
}

func waitRouteLeader(t testing.TB, node *Node, hashSlot uint16, want uint64) {
	t.Helper()
	waitUntil(t.(*testing.T), func() bool {
		route, err := node.RouteHashSlot(hashSlot)
		return err == nil && route.Leader == want
	})
}

func waitRouteKeyLeaderReady(t testing.TB, node *Node, key string) Route {
	t.Helper()
	var route Route
	waitUntil(t.(*testing.T), func() bool {
		var err error
		route, err = node.RouteKey(key)
		return err == nil && route.Leader != 0
	})
	return route
}

func waitRouteKeyLeaderConverged(t testing.TB, nodes []*Node, key string) Route {
	t.Helper()
	if len(nodes) == 0 {
		t.Fatal("no cluster nodes provided")
	}
	var route Route
	waitUntil(t.(*testing.T), func() bool {
		candidate, err := nodes[0].RouteKey(key)
		if err != nil || candidate.Leader == 0 {
			return false
		}
		for _, node := range nodes[1:] {
			observed, err := node.RouteKey(key)
			if err != nil || observed.HashSlot != candidate.HashSlot || observed.SlotID != candidate.SlotID || observed.Leader != candidate.Leader {
				return false
			}
		}
		route = candidate
		return true
	})
	return route
}

func waitAllHashSlotLeadersConverged(t testing.TB, nodes []*Node) {
	t.Helper()
	if len(nodes) == 0 {
		t.Fatal("no cluster nodes provided")
	}
	waitUntil(t.(*testing.T), func() bool {
		hashSlotCount := nodes[0].Snapshot().HashSlotCount
		if hashSlotCount == 0 {
			return false
		}
		for hashSlot := uint16(0); hashSlot < hashSlotCount; hashSlot++ {
			candidate, err := nodes[0].RouteHashSlot(hashSlot)
			if err != nil || candidate.Leader == 0 {
				return false
			}
			for _, node := range nodes[1:] {
				observed, err := node.RouteHashSlot(hashSlot)
				if err != nil || observed.SlotID != candidate.SlotID || observed.Leader != candidate.Leader {
					return false
				}
			}
		}
		return true
	})
}

func firstNonLeaderNode(t testing.TB, nodes []*Node, leader uint64) *Node {
	t.Helper()
	for _, node := range nodes {
		if node.NodeID() != leader {
			return node
		}
	}
	t.Fatalf("no follower node found for leader %d", leader)
	return nil
}

func requireChannelMessage(t testing.TB, node *Node, id channelruntime.ChannelID, seq uint64, messageID uint64, payload []byte) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	var lastMessages []channelruntime.Message
	for time.Now().Before(deadline) {
		messages, err := readDefaultChannelStore(node, id, seq)
		if err == nil && len(messages) > 0 {
			msg := messages[0]
			if msg.MessageSeq == seq && msg.MessageID == messageID && bytes.Equal(msg.Payload, payload) {
				return
			}
			t.Fatalf("node %d fetched message = %#v, want seq=%d messageID=%d payload=%q", node.NodeID(), msg, seq, messageID, payload)
		}
		lastErr = err
		if err == nil {
			lastMessages = append([]channelruntime.Message(nil), messages...)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not replicate channel %v seq %d; lastErr=%v lastMessages=%#v", node.NodeID(), id, seq, lastErr, lastMessages)
}

func readDefaultChannelStore(node *Node, id channelruntime.ChannelID, seq uint64) ([]channelruntime.Message, error) {
	if node == nil || node.defaultChannelStore == nil {
		return nil, ErrNotStarted
	}
	cs, err := node.defaultChannelStore.ChannelStore(channelruntime.ChannelKeyForID(id), id)
	if err != nil {
		return nil, err
	}
	read, err := cs.ReadCommitted(context.Background(), channelstore.ReadCommittedRequest{FromSeq: seq, MaxSeq: seq, Limit: 1, MaxBytes: 1024})
	if err != nil {
		return nil, err
	}
	return read.Messages, nil
}

func findRouteKeyWithDifferentHashSlot(t testing.TB, node *Node, avoid uint16, prefix string) string {
	t.Helper()
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("%s-%d", prefix, i)
		route := waitRouteKeyLeaderReady(t, node, key)
		if route.HashSlot != avoid {
			return key
		}
	}
	t.Fatalf("could not find key outside hash slot %d", avoid)
	return ""
}

func channelLatestKeyForHashSlot(t *testing.T, want, count uint16) string {
	t.Helper()
	for i := 0; i < 100000; i++ {
		key := fmt.Sprintf("latest-batch-%d", i)
		if routing.HashSlotForKey(key, count) == want {
			return key
		}
	}
	t.Fatalf("no channel key found for hash slot %d/%d", want, count)
	return ""
}
