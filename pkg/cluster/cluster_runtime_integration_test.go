//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
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
