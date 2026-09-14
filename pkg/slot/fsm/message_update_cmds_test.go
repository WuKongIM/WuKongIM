package fsm

import (
	"context"
	"encoding/json"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"strings"
	"testing"
)

func TestMessageUpdateAtomicCASAndLatestIndex(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.UpsertChannel(11, metadb.Channel{ChannelID: "g", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	if err := wb.UpsertChannelRuntimeMeta(11, metadb.ChannelRuntimeMeta{ChannelID: "g", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, MinISR: 1, Replicas: []uint64{1}, ISR: []uint64{1}}); err != nil {
		t.Fatal(err)
	}
	if err := wb.Commit(); err != nil {
		t.Fatal(err)
	}
	q := metadb.MessageUpdateMutation{Op: "init", ChannelID: "g", ChannelType: 2, Generation: "generation"}
	index := uint64(0)
	command := func(q metadb.MessageUpdateMutation) multiraft.Command {
		data, err := EncodeMessageUpdateCommand(q)
		if err != nil {
			t.Fatal(err)
		}
		index++
		return multiraft.Command{SlotID: 11, HashSlot: 11, Index: index, Term: 1, Data: data}
	}
	apply := func(q metadb.MessageUpdateMutation) metadb.MessageUpdateMutationResult {
		data, err := sm.Apply(ctx, command(q))
		if err != nil {
			t.Fatal(err)
		}
		var out metadb.MessageUpdateMutationResult
		if err = json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := apply(q); out.Status != "ok" {
		t.Fatal(out)
	}
	q.Op = "update"
	q.MessageID = 100
	q.MessageSeq = 10
	q.ExpectedChannelEpoch = 1
	q.ExpectedRouteGeneration = 1
	q.RequestID = "first"
	q.Digest = strings.Repeat("a", 64)
	q.Payload = []byte("first")
	q2 := q
	q2.RequestID = "conflict"
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{command(q), command(q2)})
	if err != nil {
		t.Fatal(err)
	}
	var first, conflict metadb.MessageUpdateMutationResult
	if err = json.Unmarshal(results[0], &first); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(results[1], &conflict); err != nil {
		t.Fatal(err)
	}
	if first.Status != "ok" || first.Request.Version != 1 || conflict.Status != "version_conflict" {
		t.Fatalf("%+v %+v", first, conflict)
	}
	second := q
	second.MessageID = 200
	second.MessageSeq = 20
	second.RequestID = "second"
	second.Payload = []byte("B")
	if out := apply(second); out.Request.UpdateSeq != 2 {
		t.Fatal(out)
	}
	third := q
	third.ExpectedVersion = 1
	third.RequestID = "third"
	third.Digest = strings.Repeat("b", 64)
	third.Payload = []byte("A latest")
	if out := apply(third); out.Request.UpdateSeq != 3 {
		t.Fatal(out)
	}
	if out := apply(q); out.Request.Version != 1 || out.Request.UpdateSeq != 1 {
		t.Fatalf("retry lost original result: %+v", out)
	}
	q.Digest = strings.Repeat("c", 64)
	if out := apply(q); out.Status != "idempotency_conflict" {
		t.Fatal(out)
	}
	page, err := db.ForHashSlot(11).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: "g", ChannelType: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Updates) != 1 || page.Updates[0].MessageID != 200 || !page.More || page.Next != 2 {
		t.Fatalf("page=%+v", page)
	}
	next, err := db.ForHashSlot(11).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: "g", ChannelType: 2, After: page.Next, Through: page.Through, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Updates) != 1 || string(next.Updates[0].Payload) != "A latest" || next.More || next.Next != 3 {
		t.Fatalf("next=%+v", next)
	}
	ack := third
	ack.Op = "ack"
	ack.ExpectedVersion = 1
	if out := apply(ack); out.Status != "ok" {
		t.Fatal(out)
	}
	exact, err := db.ForHashSlot(11).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: "g", ChannelType: 2, IDs: []uint64{100}, IncludePending: true})
	if err != nil {
		t.Fatal(err)
	}
	if exact.Updates[0].Pending != 1 {
		t.Fatal("stale ack cleared newer notification")
	}
}

func TestMessageUpdateSnapshotCleanupAndChannelIncarnation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.UpsertChannel(11, metadb.Channel{ChannelID: "g", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	runtime := metadb.ChannelRuntimeMeta{ChannelID: "g", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, MinISR: 1, Replicas: []uint64{1}, ISR: []uint64{1}}
	if err := wb.UpsertChannelRuntimeMeta(11, runtime); err != nil {
		t.Fatal(err)
	}
	if err := wb.Commit(); err != nil {
		t.Fatal(err)
	}
	index := uint64(0)
	apply := func(q metadb.MessageUpdateMutation) metadb.MessageUpdateMutationResult {
		t.Helper()
		data, err := EncodeMessageUpdateCommand(q)
		if err != nil {
			t.Fatal(err)
		}
		index++
		raw, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 11, Index: index, Term: 1, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		var result metadb.MessageUpdateMutationResult
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	init := metadb.MessageUpdateMutation{Op: "init", ChannelID: "g", ChannelType: 2, Generation: "old"}
	if result := apply(init); result.Status != "ok" {
		t.Fatal(result)
	}
	edit := init
	edit.Op = "update"
	edit.MessageID = 100
	edit.MessageSeq = 10
	edit.RequestID = "r"
	edit.Digest = strings.Repeat("a", 64)
	edit.Payload = []byte("new")
	edit.ExpectedChannelEpoch = 1
	edit.ExpectedRouteGeneration = 1
	if result := apply(edit); result.Status != "ok" {
		t.Fatal(result)
	}
	progress := init
	progress.Op = "progress"
	progress.MessageID = 100
	progress.ExpectedVersion = 1
	progress.AfterUID = strings.Repeat("u", 65535)
	if result := apply(progress); result.Status != "ok" {
		t.Fatal(result)
	}
	snap, err := sm.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := openTestDB(t)
	restoredSM := mustNewStateMachine(t, restored, 11)
	if err = restoredSM.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}
	page, err := restored.ForHashSlot(11).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: "g", ChannelType: 2, IDs: []uint64{100}, IncludePending: true})
	if err != nil || len(page.Updates) != 1 || len(page.Updates[0].Payload) != 0 || page.Updates[0].PendingAfterUID != progress.AfterUID {
		t.Fatalf("restored=%+v err=%v", page, err)
	}
	content, e := restored.ForHashSlot(11).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: "g", ChannelType: 2, IDs: []uint64{100}})
	if e != nil || len(content.Updates) != 1 || string(content.Updates[0].Payload) != "new" {
		t.Fatalf("content=%+v err=%v", content, e)
	}
	pending, _, _, err := restored.ForHashSlot(11).ListPendingMessageUpdates(ctx, metadb.MessageUpdatePendingCursor{}, 8)
	if err != nil || len(pending) != 1 || len(pending[0].Payload) != 0 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	candidates, _, done, err := db.ForHashSlot(11).ListMessageUpdateRetentionCandidates(ctx, metadb.MessageUpdateRetentionCursor{}, 8)
	if err != nil || !done || len(candidates) != 1 || candidates[0].MessageSeq != 10 || len(candidates[0].Payload) != 0 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	prune := init
	prune.Op = "prune"
	prune.MessageID = 100
	if result := apply(prune); result.Status != "ok" {
		t.Fatal(result)
	}
	page, err = db.ForHashSlot(11).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: "g", ChannelType: 2, Limit: 10})
	if err != nil || len(page.Updates) != 1 {
		t.Fatal("premature pruning", err)
	}
	runtime.RetentionThroughSeq = 10
	wb2 := db.NewWriteBatch()
	defer wb2.Close()
	if err = wb2.UpsertChannelRuntimeMeta(11, runtime); err != nil {
		t.Fatal(err)
	}
	if err = wb2.Commit(); err != nil {
		t.Fatal(err)
	}
	if result := apply(prune); result.Status != "ok" {
		t.Fatal(result)
	}
	page, err = db.ForHashSlot(11).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: "g", ChannelType: 2, Limit: 10})
	if err != nil || len(page.Updates) != 0 || page.Head.UpdateSeq != 1 {
		t.Fatalf("pruned=%+v err=%v", page, err)
	}
	if result := apply(edit); result.Status != "message_not_found" {
		t.Fatal("retention resurrected original", result)
	}
	// Delete and recreate in one metadata batch must invalidate the old head overlay.
	wb3 := db.NewWriteBatch()
	defer wb3.Close()
	if _, err = wb3.ApplyMessageUpdate(11, init); err != nil {
		t.Fatal(err)
	}
	if err = wb3.DeleteChannel(11, "g", 2); err != nil {
		t.Fatal(err)
	}
	if err = wb3.UpsertChannel(11, metadb.Channel{ChannelID: "g", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	newInit := init
	newInit.Generation = "new"
	result, err := wb3.ApplyMessageUpdate(11, newInit)
	if err != nil {
		t.Fatal(err)
	}
	if err = wb3.Commit(); err != nil {
		t.Fatal(err)
	}
	if result.Head.Generation != "new" || result.Head.UpdateSeq != 0 {
		t.Fatalf("recreated=%+v", result)
	}
	if old := apply(edit); old.Status != "reset_required" {
		t.Fatal("old generation accepted", old)
	}
}
