//go:build integration

package cluster

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"strings"
	"testing"
	"time"
)

func TestMessageUpdateThreeNodeQuorumAndLeaderTransfer(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	for _, node := range nodes {
		node.cfg.HealthReport.Interval = 200 * time.Millisecond
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "edit-quorum", Type: 2}
	if err := nodes[0].UpsertChannelMetadata(ctx, metadb.Channel{ChannelID: id.ID, ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	original, err := nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 1001, Payload: []byte("original")}})
	if err != nil {
		t.Fatal(err)
	}
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	origin := firstNonLeaderNode(t, nodes, route.Leader)
	init, err := origin.ApplyMessageUpdate(ctx, metadb.MessageUpdateMutation{Op: "init", ChannelID: id.ID, ChannelType: 2, Generation: "generation"})
	if err != nil || init.Status != "ok" || init.Head.ReplicaSet == "" {
		t.Fatalf("init=%+v err=%v", init, err)
	}
	edit := metadb.MessageUpdateMutation{Op: "update", ChannelID: id.ID, ChannelType: 2, Generation: init.Head.Generation, ReplicaSet: init.Head.ReplicaSet, MessageID: 1001, MessageSeq: original.MessageSeq, RequestID: "first", Digest: strings.Repeat("a", 64), Payload: []byte("edited")}
	apply := func() {
		t.Helper()
		runtime, e := origin.GetChannelRuntimeMeta(ctx, id.ID, 2)
		if e != nil {
			t.Fatal(e)
		}
		edit.ExpectedChannelEpoch = runtime.ChannelEpoch
		edit.ExpectedRouteGeneration = runtime.RouteGeneration
		result, e := origin.ApplyMessageUpdate(ctx, edit)
		if e != nil || result.Status != "ok" {
			t.Fatalf("edit=%+v err=%v", result, e)
		}
	}
	apply()
	query := []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1024}}}
	for _, node := range nodes {
		result, e := node.ReadChannelCommittedBatch(ctx, query)
		if e != nil || len(result) != 1 || result[0].Err != nil || len(result[0].Read.Messages) != 1 || string(result[0].Read.Messages[0].Payload) != "edited" || result[0].Read.Messages[0].Version != 1 {
			t.Fatalf("node=%d result=%+v err=%v", node.NodeID(), result, e)
		}
	}
	// Move Slot authority while the Channel log remains immutable.
	transferSlotLeaderAndWait(t, nodes, route.SlotID, origin.NodeID())
	// Wait for published routing as well as Raft leadership before injecting the failure.
	waitUntil(t, func() bool {
		for _, node := range nodes {
			current, err := node.RouteKey(id.ID)
			if err != nil || current.SlotID != route.SlotID || current.Leader != origin.NodeID() {
				return false
			}
		}
		return true
	})
	victim := nodes[route.Leader-1]
	stopNodes(t, victim)
	edit.ExpectedVersion = 1
	edit.RequestID = "second"
	edit.Payload = []byte("second edit")
	apply() // The durable activation proof permits quorum writes with one replica down.
	pages, err := origin.ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: id.ID, ChannelType: 2, After: 1, Limit: 10}})
	if err != nil || len(pages) != 1 || len(pages[0].Updates) != 1 || pages[0].Updates[0].Version != 2 {
		t.Fatalf("after failover=%+v err=%v", pages, err)
	}
	// Model a process restart: reconstruct adapters against the same durable directory.
	restarted, e := New(victim.cfg)
	if e != nil {
		t.Fatal(e)
	}
	nodes[victim.NodeID()-1] = restarted
	victim = restarted
	startNode(t, victim)
	waitClusterReady(t, nodes...)
	pages, err = victim.ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: id.ID, ChannelType: 2, IDs: []uint64{1001}}})
	if err != nil || len(pages) != 1 || len(pages[0].Updates) != 1 || string(pages[0].Updates[0].Payload) != "second edit" {
		t.Fatalf("restart=%+v err=%v", pages, err)
	}
	raw, err := victim.ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1024})
	if err != nil || len(raw.Messages) != 1 || string(raw.Messages[0].Payload) != "original" {
		t.Fatalf("immutable log=%+v err=%v", raw, err)
	}
	// No read or edit may succeed when the physical Slot loses its quorum.
	for _, node := range nodes {
		if node.NodeID() != victim.NodeID() {
			stopNodes(t, node)
		}
	}
	blocked, blockedCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer blockedCancel()
	if _, e = victim.ReadMessageUpdatesBatch(blocked, []metadb.MessageUpdateRead{{ChannelID: id.ID, ChannelType: 2, IDs: []uint64{1001}}}); e == nil {
		t.Fatal("read succeeded without quorum")
	}
	blockedWrite, writeCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer writeCancel()
	edit.ExpectedVersion = 2
	edit.RequestID = "no-quorum"
	if _, e = victim.ApplyMessageUpdate(blockedWrite, edit); e == nil {
		t.Fatal("edit succeeded without quorum")
	}

}
