package clusterv2

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestClusterV2SingleNodeDefaultProposeAppliesSlotCommand(t *testing.T) {
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

func TestClusterV2ThreeNodeDefaultChannelsReplicateQuorumAppend(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room-default-quorum", Type: 1}
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	applyChannelMetaToAll(t, nodes, defaultThreeNodeChannelMeta(channelID))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := nodes[0].AppendChannel(ctx, channelv2.AppendRequest{
		ChannelID:  channelID,
		CommitMode: channelv2.CommitModeQuorum,
		Message:    channelv2.Message{MessageID: 1001, Payload: []byte("hello-default")},
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

func TestClusterV2ThreeNodeDefaultChannelsReplicateToFollowerStore(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room-default-follower-store", Type: 1}
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	applyChannelMetaToAll(t, nodes, defaultThreeNodeChannelMeta(channelID))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := nodes[0].AppendChannel(ctx, channelv2.AppendRequest{
		ChannelID:  channelID,
		CommitMode: channelv2.CommitModeQuorum,
		Message:    channelv2.Message{MessageID: 1002, Payload: []byte("follower-fetch")},
	})
	if err != nil {
		t.Fatalf("AppendChannel(default channels) error = %v", err)
	}

	requireChannelMessage(t, nodes[1], channelID, res.MessageSeq, 1002, []byte("follower-fetch"))
}

func newDefaultSingleNode(t testing.TB) *Node {
	t.Helper()
	cfg := Config{NodeID: 1, ListenAddr: freeTCPAddr(t.(*testing.T)), DataDir: t.TempDir()}
	cfg.Control.ClusterID = "clusterv2-integration-single"
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
		cfg.Control.ClusterID = "clusterv2-integration-three"
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

func waitRouteLeader(t testing.TB, node *Node, hashSlot uint16, want uint64) {
	t.Helper()
	waitUntil(t.(*testing.T), func() bool {
		route, err := node.RouteHashSlot(hashSlot)
		return err == nil && route.Leader == want
	})
}

func applyChannelMetaToAll(t testing.TB, nodes []*Node, meta channelv2.Meta) {
	t.Helper()
	for _, node := range nodes {
		svc, ok := node.channels.(*channels.Service)
		if !ok {
			t.Fatalf("default channels(node=%d) type = %T, want *channels.Service", node.NodeID(), node.channels)
		}
		if err := svc.ApplyMeta(meta); err != nil {
			t.Fatalf("ApplyMeta(node=%d) error = %v", node.NodeID(), err)
		}
	}
}

func defaultThreeNodeChannelMeta(id channelv2.ChannelID) channelv2.Meta {
	return channelv2.Meta{
		Key:         channelv2.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelv2.NodeID{1, 2, 3},
		ISR:         []channelv2.NodeID{1, 2, 3},
		MinISR:      2,
		Status:      channelv2.StatusActive,
	}
}

func requireChannelMessage(t testing.TB, node *Node, id channelv2.ChannelID, seq uint64, messageID uint64, payload []byte) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	var lastMessages []channelv2.Message
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
			lastMessages = append([]channelv2.Message(nil), messages...)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not replicate channel %v seq %d; lastErr=%v lastMessages=%#v", node.NodeID(), id, seq, lastErr, lastMessages)
}

func readDefaultChannelStore(node *Node, id channelv2.ChannelID, seq uint64) ([]channelv2.Message, error) {
	if node == nil || node.defaultChannelStore == nil {
		return nil, ErrNotStarted
	}
	cs, err := node.defaultChannelStore.ChannelStore(channelv2.ChannelKeyForID(id), id)
	if err != nil {
		return nil, err
	}
	read, err := cs.ReadCommitted(context.Background(), channelstore.ReadCommittedRequest{FromSeq: seq, MaxSeq: seq, Limit: 1, MaxBytes: 1024})
	if err != nil {
		return nil, err
	}
	return read.Messages, nil
}
