package meta

import (
	"context"
	"errors"
	"testing"
)

func TestPersonDirectoryTaskLifecycleIsAtomicWithChannelMetadata(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	const hashSlot HashSlot = 7
	key := ChannelKey{ChannelID: "u1@u2", ChannelType: 1}

	batch := store.db.NewBatch()
	defer batch.Close()
	if _, err := batch.CreateChannelRuntimeMeta(hashSlot, ChannelRuntimeMeta{
		ChannelID: key.ChannelID, ChannelType: key.ChannelType,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1},
		ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, MinISR: 1,
	}); err != nil {
		t.Fatalf("CreateChannelRuntimeMeta(): %v", err)
	}
	if err := batch.EnsurePersonDirectoryTask(hashSlot, PersonDirectoryTask{ChannelID: key.ChannelID, ChannelType: key.ChannelType, CommittedTail: 9, CreatedAt: 123}); err != nil {
		t.Fatalf("EnsurePersonDirectoryTask(): %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(create): %v", err)
	}

	channel, ok, err := store.db.HashSlot(hashSlot).GetChannel(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || channel.DirectoryProjectionState != DirectoryProjectionPending {
		t.Fatalf("channel after create = (%+v, %v, %v), want pending", channel, ok, err)
	}
	task, ok, err := store.db.HashSlot(hashSlot).GetPersonDirectoryTask(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || task.CommittedTail != 9 || task.CreatedAt != 123 {
		t.Fatalf("task after create = (%+v, %v, %v), want tail=9 created_at=123", task, ok, err)
	}

	repeated := store.db.NewBatch()
	defer repeated.Close()
	if err := repeated.EnsurePersonDirectoryTask(hashSlot, PersonDirectoryTask{ChannelID: key.ChannelID, ChannelType: key.ChannelType, CommittedTail: 99, CreatedAt: 456}); err != nil {
		t.Fatalf("EnsurePersonDirectoryTask(repeated): %v", err)
	}
	if err := repeated.Commit(ctx); err != nil {
		t.Fatalf("Commit(repeated): %v", err)
	}
	task, ok, err = store.db.HashSlot(hashSlot).GetPersonDirectoryTask(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || task.CommittedTail != 9 || task.CreatedAt != 123 {
		t.Fatalf("task after repeated admission = (%+v, %v, %v), want original boundary", task, ok, err)
	}

	complete := store.db.NewBatch()
	defer complete.Close()
	if err := complete.CompletePersonDirectoryTask(hashSlot, PersonDirectoryTaskLocation{
		HashSlot: hashSlot, ChannelID: key.ChannelID, ChannelType: key.ChannelType, Generation: task.Generation,
	}); err != nil {
		t.Fatalf("CompletePersonDirectoryTask(): %v", err)
	}
	if err := complete.Commit(ctx); err != nil {
		t.Fatalf("Commit(complete): %v", err)
	}
	channel, ok, err = store.db.HashSlot(hashSlot).GetChannel(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || channel.DirectoryProjectionState != DirectoryProjectionReady {
		t.Fatalf("channel after completion = (%+v, %v, %v), want ready", channel, ok, err)
	}
	if _, ok, err := store.db.HashSlot(hashSlot).GetPersonDirectoryTask(ctx, key.ChannelID, key.ChannelType); err != nil || ok {
		t.Fatalf("task after completion ok=%v err=%v, want absent", ok, err)
	}

	replay := store.db.NewBatch()
	defer replay.Close()
	if err := replay.EnsurePersonDirectoryTask(hashSlot, PersonDirectoryTask{ChannelID: key.ChannelID, ChannelType: key.ChannelType, CommittedTail: 99, CreatedAt: 456}); err != nil {
		t.Fatalf("EnsurePersonDirectoryTask(replay): %v", err)
	}
	if err := replay.Commit(ctx); err != nil {
		t.Fatalf("Commit(replay): %v", err)
	}
	channel, ok, err = store.db.HashSlot(hashSlot).GetChannel(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || channel.DirectoryProjectionState != DirectoryProjectionReady {
		t.Fatalf("channel after replay = (%+v, %v, %v), want ready", channel, ok, err)
	}
	if _, ok, err := store.db.HashSlot(hashSlot).GetPersonDirectoryTask(ctx, key.ChannelID, key.ChannelType); err != nil || ok {
		t.Fatalf("task after replay ok=%v err=%v, want absent", ok, err)
	}
}

func TestPersonDirectoryTaskCompletionRequiresDurableTask(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	const hashSlot HashSlot = 7
	key := ChannelKey{ChannelID: "u1@u2", ChannelType: 1}

	if err := store.db.HashSlot(hashSlot).UpsertChannel(ctx, Channel{
		ChannelID: key.ChannelID, ChannelType: key.ChannelType,
		DirectoryProjectionState:      DirectoryProjectionPending,
		DirectoryProjectionGeneration: 1,
	}); err != nil {
		t.Fatalf("UpsertChannel(pending): %v", err)
	}
	batch := store.db.NewBatch()
	defer batch.Close()
	if err := batch.CompletePersonDirectoryTask(hashSlot, PersonDirectoryTaskLocation{
		HashSlot: hashSlot, ChannelID: key.ChannelID, ChannelType: key.ChannelType, Generation: 1,
	}); err != nil {
		t.Fatalf("CompletePersonDirectoryTask(stage): %v", err)
	}
	if err := batch.Commit(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Commit() error = %v, want missing durable task", err)
	}
	channel, ok, err := store.db.HashSlot(hashSlot).GetChannel(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || channel.DirectoryProjectionState != DirectoryProjectionPending {
		t.Fatalf("channel after rejected completion = (%+v, %v, %v), want pending", channel, ok, err)
	}
}

func TestDeletePersonChannelRemovesPendingDirectoryTaskAtomically(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	const hashSlot HashSlot = 7
	key := ChannelKey{ChannelID: "u1@u2", ChannelType: 1}

	batch := store.db.NewBatch()
	if err := batch.EnsurePersonDirectoryTask(hashSlot, PersonDirectoryTask{
		ChannelID: key.ChannelID, ChannelType: key.ChannelType, CreatedAt: 123, Generation: 1,
	}); err != nil {
		t.Fatalf("EnsurePersonDirectoryTask(): %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(admit): %v", err)
	}
	if err := store.db.HashSlot(hashSlot).DeleteChannel(ctx, key.ChannelID, key.ChannelType); err != nil {
		t.Fatalf("DeleteChannel(): %v", err)
	}

	if _, ok, err := store.db.HashSlot(hashSlot).GetChannel(ctx, key.ChannelID, key.ChannelType); err != nil || ok {
		t.Fatalf("GetChannel(deleted) = ok %v err %v, want absent", ok, err)
	}
	if _, ok, err := store.db.HashSlot(hashSlot).GetPersonDirectoryTask(ctx, key.ChannelID, key.ChannelType); err != nil || ok {
		t.Fatalf("GetPersonDirectoryTask(deleted) = ok %v err %v, want absent", ok, err)
	}
}

func TestPersonDirectoryTaskGenerationFencesDeleteRecreateAndLateCompletion(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	const hashSlot HashSlot = 7
	key := ChannelKey{ChannelID: "u1@u2", ChannelType: 1}

	first := store.db.NewBatch()
	defer first.Close()
	if _, err := first.CreateChannelRuntimeMeta(hashSlot, ChannelRuntimeMeta{
		ChannelID: key.ChannelID, ChannelType: key.ChannelType,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1},
		ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, MinISR: 1,
	}); err != nil {
		t.Fatalf("CreateChannelRuntimeMeta(): %v", err)
	}
	if err := first.EnsurePersonDirectoryTask(hashSlot, PersonDirectoryTask{
		ChannelID: key.ChannelID, ChannelType: key.ChannelType, CommittedTail: 9, CreatedAt: 123,
	}); err != nil {
		t.Fatalf("EnsurePersonDirectoryTask(first): %v", err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("Commit(first): %v", err)
	}
	firstTask, ok, err := store.db.HashSlot(hashSlot).GetPersonDirectoryTask(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || firstTask.Generation == 0 {
		t.Fatalf("first task = (%+v,%v,%v), want non-zero generation", firstTask, ok, err)
	}

	if err := store.db.HashSlot(hashSlot).DeleteChannel(ctx, key.ChannelID, key.ChannelType); err != nil {
		t.Fatalf("DeleteChannel(): %v", err)
	}
	recreated := store.db.NewBatch()
	defer recreated.Close()
	if err := recreated.EnsurePersonDirectoryTask(hashSlot, PersonDirectoryTask{
		ChannelID: key.ChannelID, ChannelType: key.ChannelType, CommittedTail: 19, CreatedAt: 456,
	}); err != nil {
		t.Fatalf("EnsurePersonDirectoryTask(recreated): %v", err)
	}
	if err := recreated.Commit(ctx); err != nil {
		t.Fatalf("Commit(recreated): %v", err)
	}
	secondTask, ok, err := store.db.HashSlot(hashSlot).GetPersonDirectoryTask(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || secondTask.Generation <= firstTask.Generation {
		t.Fatalf("second task = (%+v,%v,%v), want generation after %d", secondTask, ok, err, firstTask.Generation)
	}

	stale := store.db.NewBatch()
	defer stale.Close()
	if err := stale.CompletePersonDirectoryTask(hashSlot, PersonDirectoryTaskLocation{
		HashSlot: hashSlot, ChannelID: key.ChannelID, ChannelType: key.ChannelType, Generation: firstTask.Generation,
	}); err != nil {
		t.Fatalf("CompletePersonDirectoryTask(stale stage): %v", err)
	}
	if err := stale.Commit(ctx); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("Commit(stale) error = %v, want stale generation", err)
	}
	channel, ok, err := store.db.HashSlot(hashSlot).GetChannel(ctx, key.ChannelID, key.ChannelType)
	if err != nil || !ok || channel.DirectoryProjectionState != DirectoryProjectionPending || channel.DirectoryProjectionGeneration != secondTask.Generation {
		t.Fatalf("channel after stale completion = (%+v,%v,%v), want pending generation %d", channel, ok, err, secondTask.Generation)
	}

	current := store.db.NewBatch()
	defer current.Close()
	if err := current.CompletePersonDirectoryTask(hashSlot, PersonDirectoryTaskLocation{
		HashSlot: hashSlot, ChannelID: key.ChannelID, ChannelType: key.ChannelType, Generation: secondTask.Generation,
	}); err != nil {
		t.Fatalf("CompletePersonDirectoryTask(current stage): %v", err)
	}
	if err := current.Commit(ctx); err != nil {
		t.Fatalf("Commit(current): %v", err)
	}
}
