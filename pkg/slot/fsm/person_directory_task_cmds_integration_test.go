//go:build integration

package fsm

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestStateMachinePersonDirectoryTaskAdmissionProjectionAndCompletion(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5, 7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	admit, err := EncodeAdmitPersonDirectoryTaskBatchCommandChecked([]PersonDirectoryAdmissionBatchItem{{
		HashSlot: 7,
		Task:     metadb.PersonDirectoryTask{ChannelID: channelID, ChannelType: 1, CommittedTail: 9, CreatedAt: 123},
		RuntimeMeta: metadb.ChannelRuntimeMeta{
			ChannelID: channelID, ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
			Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1,
		},
	}})
	if err != nil {
		t.Fatalf("EncodeAdmitPersonDirectoryTaskBatchCommandChecked(): %v", err)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 7, Index: 1, Term: 1, Data: admit}); err != nil {
		t.Fatalf("Apply(admit): %v", err)
	}
	task, ok, err := db.ForHashSlot(7).GetPersonDirectoryTask(ctx, channelID, 1)
	if err != nil || !ok || task.CommittedTail != 9 {
		t.Fatalf("task = (%+v,%v,%v), want durable tail 9", task, ok, err)
	}
	channel, err := db.ForHashSlot(7).GetChannel(ctx, channelID, 1)
	if err != nil || channel.DirectoryProjectionState != metadb.DirectoryProjectionPending {
		t.Fatalf("channel = %+v err=%v, want pending", channel, err)
	}

	preexisting := metadb.UserChannelMembership{
		UID: "u1", ChannelID: channelID, ChannelType: 1,
		ReadSeq: 44, DeletedToSeq: 33, ActivatedAt: 22, Tombstone: true, TombstoneAt: 11,
	}
	if err := db.ForHashSlot(5).UpsertUserChannelMembership(ctx, preexisting); err != nil {
		t.Fatalf("UpsertUserChannelMembership(preexisting): %v", err)
	}
	ensure, err := EncodeEnsureUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{
		{HashSlot: 5, Membership: metadb.UserChannelMembership{UID: "u1", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, ReadSeq: 9, DeletedToSeq: 9, SourceVersion: 1, UpdatedAt: 123}},
		{HashSlot: 7, Membership: metadb.UserChannelMembership{UID: "u2", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, ReadSeq: 9, DeletedToSeq: 9, SourceVersion: 1, UpdatedAt: 123}},
	})
	if err != nil {
		t.Fatalf("EncodeEnsureUserChannelMembershipBatchCommandChecked(): %v", err)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 5, Index: 2, Term: 1, Data: ensure}); err != nil {
		t.Fatalf("Apply(ensure): %v", err)
	}
	got, err := db.ForHashSlot(5).GetUserChannelMembership(ctx, "u1", channelID, 1)
	wantPreexisting := preexisting
	wantPreexisting.JoinSeq = task.CommittedTail + 1
	wantPreexisting.SourceVersion = task.Generation
	wantPreexisting.UpdatedAt = task.CreatedAt
	if err != nil || got != wantPreexisting {
		t.Fatalf("preexisting membership = %+v err=%v, want personal state with generation fence %+v", got, err, wantPreexisting)
	}
	if _, err := db.ForHashSlot(7).GetUserChannelMembership(ctx, "u2", channelID, 1); err != nil {
		t.Fatalf("GetUserChannelMembership(u2): %v", err)
	}

	complete, err := EncodeCompletePersonDirectoryTaskBatchCommandChecked([]PersonDirectoryCompletionBatchItem{{HashSlot: 7, ChannelID: channelID, ChannelType: 1, Generation: task.Generation}})
	if err != nil {
		t.Fatalf("EncodeCompletePersonDirectoryTaskBatchCommandChecked(): %v", err)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 7, Index: 3, Term: 1, Data: complete}); err != nil {
		t.Fatalf("Apply(complete): %v", err)
	}
	channel, err = db.ForHashSlot(7).GetChannel(ctx, channelID, 1)
	if err != nil || channel.DirectoryProjectionState != metadb.DirectoryProjectionReady {
		t.Fatalf("completed channel = %+v err=%v, want ready", channel, err)
	}
	if _, ok, err := db.ForHashSlot(7).GetPersonDirectoryTask(ctx, channelID, 1); err != nil || ok {
		t.Fatalf("task after completion ok=%v err=%v, want absent", ok, err)
	}
}

func TestStateMachineDeleteRecreateFencesStalePersonDirectoryCompletion(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	runtimeMeta := metadb.ChannelRuntimeMeta{
		ChannelID: channelID, ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1,
	}
	admit := func(index uint64, createdAt int64) metadb.PersonDirectoryTask {
		t.Helper()
		command, encodeErr := EncodeAdmitPersonDirectoryTaskBatchCommandChecked([]PersonDirectoryAdmissionBatchItem{{
			HashSlot: 7, Task: metadb.PersonDirectoryTask{ChannelID: channelID, ChannelType: 1, CommittedTail: index, CreatedAt: createdAt}, RuntimeMeta: runtimeMeta,
		}})
		if encodeErr != nil {
			t.Fatalf("EncodeAdmitPersonDirectoryTaskBatchCommandChecked(): %v", encodeErr)
		}
		if _, applyErr := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 7, Index: index, Term: 1, Data: command}); applyErr != nil {
			t.Fatalf("Apply(admit generation at index %d): %v", index, applyErr)
		}
		task, ok, getErr := db.ForHashSlot(7).GetPersonDirectoryTask(ctx, channelID, 1)
		if getErr != nil || !ok {
			t.Fatalf("GetPersonDirectoryTask(index %d) = (%+v,%v,%v), want present", index, task, ok, getErr)
		}
		return task
	}

	first := admit(1, 100)
	if first.Generation != 1 {
		t.Fatalf("first task generation = %d, want 1", first.Generation)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 7, Index: 2, Term: 1, Data: EncodeDeleteChannelCommand(channelID, 1),
	}); err != nil {
		t.Fatalf("Apply(delete): %v", err)
	}
	runtimeAfterDelete, err := db.ForHashSlot(7).GetChannelRuntimeMeta(ctx, channelID, 1)
	if err != nil || runtimeAfterDelete.DirectoryGeneration != 2 {
		t.Fatalf("runtime after delete = %+v err=%v, want directory generation 2", runtimeAfterDelete, err)
	}

	second := admit(3, 200)
	if second.Generation != 2 {
		t.Fatalf("recreated task generation = %d, want 2", second.Generation)
	}
	staleCompletion, err := EncodeCompletePersonDirectoryTaskBatchCommandChecked([]PersonDirectoryCompletionBatchItem{{
		HashSlot: 7, ChannelID: channelID, ChannelType: 1, Generation: first.Generation,
	}})
	if err != nil {
		t.Fatalf("EncodeCompletePersonDirectoryTaskBatchCommandChecked(): %v", err)
	}
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 7, Index: 4, Term: 1, Data: staleCompletion})
	if err != nil {
		t.Fatalf("Apply(stale completion): %v", err)
	}
	if got := string(result); got != ApplyResultStaleMeta {
		t.Fatalf("stale completion result = %q, want %q", got, ApplyResultStaleMeta)
	}
	current, ok, err := db.ForHashSlot(7).GetPersonDirectoryTask(ctx, channelID, 1)
	if err != nil || !ok || current.Generation != second.Generation {
		t.Fatalf("task after stale completion = (%+v,%v,%v), want generation %d", current, ok, err, second.Generation)
	}
}
