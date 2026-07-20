package cluster

import (
	"bytes"
	"context"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestClusterSingleNodeDefaultProposeAppliesSlotCommand(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	waitRouteLeader(t, node, 0, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.Propose(ctx, ProposeRequest{
		Key: "user-a",
		Command: metafsm.EncodeUpsertUserCommand(metadb.User{
			UID:         "user-a",
			Token:       "token-a",
			DeviceFlag:  1,
			DeviceLevel: 2,
		}),
	}); err != nil {
		t.Fatalf("Propose(default slot command) error = %v", err)
	}
}

func TestClusterSingleNodeChannelSubscriberMetadataFacade(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "g1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.UpsertChannelMetadata(ctx, metadb.Channel{
		ChannelID:     "g1",
		ChannelType:   2,
		AllowStranger: 1,
	}); err != nil {
		t.Fatalf("UpsertChannelMetadata() error = %v", err)
	}
	channel, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannel(ctx, "g1", 2)
	if err != nil {
		t.Fatalf("GetChannel() error = %v, want channel", err)
	}
	if channel.AllowStranger != 1 {
		t.Fatalf("channel = %#v, want persisted flags", channel)
	}
	if err := node.AddChannelSubscribers(ctx, "g1", 2, []string{"u2", "u1", "u1"}, 7); err != nil {
		t.Fatalf("AddChannelSubscribers() error = %v", err)
	}

	first, cursor, done, err := node.ListChannelSubscribersPage(ctx, "g1", 2, "", 1)
	if err != nil {
		t.Fatalf("ListChannelSubscribersPage(first) error = %v", err)
	}
	if len(first) != 1 || first[0] != "u1" || cursor != "u1" || done {
		t.Fatalf("first page = %#v cursor=%q done=%t, want u1 and continuation", first, cursor, done)
	}
	second, cursor, done, err := node.ListChannelSubscribersPage(ctx, "g1", 2, cursor, 10)
	if err != nil {
		t.Fatalf("ListChannelSubscribersPage(second) error = %v", err)
	}
	if len(second) != 1 || second[0] != "u2" || cursor != "" || !done {
		t.Fatalf("second page = %#v cursor=%q done=%t, want final u2", second, cursor, done)
	}
	channel, err = node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannel(ctx, "g1", 2)
	if err != nil || channel.SubscriberMutationVersion != 7 {
		t.Fatalf("channel after subscribers = %#v err=%v, want mutation version 7", channel, err)
	}
}

func TestClusterThreeNodeDefaultChannelsReplicateQuorumAppend(t *testing.T) {
	channelID := channelruntime.ChannelID{ID: "room-default-quorum", Type: 1}
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := nodes[0].AppendChannel(ctx, channelruntime.AppendRequest{
		ChannelID:  channelID,
		CommitMode: channelruntime.CommitModeQuorum,
		Message:    channelruntime.Message{MessageID: 1001, Payload: []byte("hello-default")},
	})
	if err != nil {
		t.Fatalf("AppendChannel(default channels) error = %v", err)
	}
	if res.MessageSeq == 0 {
		t.Fatal("AppendChannel(default channels) MessageSeq = 0, want committed sequence")
	}

	for _, node := range nodes {
		requireChannelMessage(t, node, channelID, res.MessageSeq, 1001, []byte("hello-default"))
	}
}

func TestClusterThreeNodeDefaultChannelsReplicateToFollowerStore(t *testing.T) {
	channelID := channelruntime.ChannelID{ID: "room-default-follower-store", Type: 1}
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	route := waitRouteKeyLeaderConverged(t, nodes, channelID.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := nodes[0].AppendChannel(ctx, channelruntime.AppendRequest{
		ChannelID:  channelID,
		CommitMode: channelruntime.CommitModeQuorum,
		Message:    channelruntime.Message{MessageID: 1002, Payload: []byte("follower-fetch")},
	})
	if err != nil {
		t.Fatalf("AppendChannel(default channels) error = %v", err)
	}

	follower := firstNonLeaderNode(t, nodes, route.Leader)
	requireChannelMessage(t, follower, channelID, res.MessageSeq, 1002, []byte("follower-fetch"))
}

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
		t.Fatalf("New(single node) error = %v", err)
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	errs := make(chan error, len(nodes))
	for _, node := range nodes {
		node := node
		go func() { errs <- node.Start(ctx) }()
	}
	for range nodes {
		if err := <-errs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}
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
