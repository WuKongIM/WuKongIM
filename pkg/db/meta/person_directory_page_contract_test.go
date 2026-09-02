package meta

import (
	"context"
	"errors"
	"testing"
)

func TestPersonDirectoryTaskPageResumesWithoutSkippingPendingProjection(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const hashSlot uint16 = 22

	tasks := []PersonDirectoryTask{
		{ChannelID: "u1@u2", ChannelType: 1, CommittedTail: 10, CreatedAt: 100, Generation: 1},
		{ChannelID: "u3@u4", ChannelType: 1, CommittedTail: 20, CreatedAt: 200, Generation: 1},
	}
	batch := db.NewWriteBatch()
	defer batch.Close()
	for _, task := range tasks {
		if err := batch.EnsurePersonDirectoryTask(hashSlot, task); err != nil {
			t.Fatalf("EnsurePersonDirectoryTask(%+v): %v", task, err)
		}
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}

	shard := db.ForHashSlot(hashSlot)
	firstPage, cursor, done, err := shard.ListPersonDirectoryTaskPage(ctx, PersonDirectoryTaskCursor{}, 1)
	if err != nil || len(firstPage) != 1 || done || cursor.ChannelID == "" {
		t.Fatalf("ListPersonDirectoryTaskPage(first) = (%+v, %+v, %v, %v)", firstPage, cursor, done, err)
	}
	secondPage, next, _, err := shard.ListPersonDirectoryTaskPage(ctx, cursor, 10)
	if err != nil || len(secondPage) != 1 || secondPage[0].ChannelID == firstPage[0].ChannelID {
		t.Fatalf("ListPersonDirectoryTaskPage(resume) = (%+v, %+v, %v)", secondPage, next, err)
	}
	seen := map[string]uint64{
		firstPage[0].ChannelID:  firstPage[0].CommittedTail,
		secondPage[0].ChannelID: secondPage[0].CommittedTail,
	}
	if seen["u1@u2"] != 10 || seen["u3@u4"] != 20 {
		t.Fatalf("paged tasks = %+v, want both durable projection boundaries", seen)
	}

	got, ok, err := shard.GetPersonDirectoryTask(ctx, firstPage[0].ChannelID, firstPage[0].ChannelType)
	if err != nil || !ok || got.Generation != 1 {
		t.Fatalf("GetPersonDirectoryTask() = (%+v, %v, %v)", got, ok, err)
	}
	complete := db.NewWriteBatch()
	defer complete.Close()
	if err := complete.CompletePersonDirectoryTask(hashSlot, PersonDirectoryTaskLocation{
		HashSlot: hashSlot, ChannelID: got.ChannelID, ChannelType: got.ChannelType, Generation: got.Generation,
	}); err != nil {
		t.Fatalf("CompletePersonDirectoryTask(): %v", err)
	}
	if err := complete.Commit(); err != nil {
		t.Fatalf("Commit(complete): %v", err)
	}
	if _, ok, err := shard.GetPersonDirectoryTask(ctx, got.ChannelID, got.ChannelType); err != nil || ok {
		t.Fatalf("GetPersonDirectoryTask(after completion) = (ok %v, err %v), want absent", ok, err)
	}
	channel, err := shard.GetChannel(ctx, got.ChannelID, got.ChannelType)
	if err != nil || channel.DirectoryProjectionState != DirectoryProjectionReady || channel.DirectoryProjectionGeneration != got.Generation {
		t.Fatalf("GetChannel(after completion) = (%+v, %v), want ready generation %d", channel, err, got.Generation)
	}

	_, _, _, err = shard.ListPersonDirectoryTaskPage(ctx, PersonDirectoryTaskCursor{ChannelID: "group", ChannelType: 2}, 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ListPersonDirectoryTaskPage(invalid cursor) error = %v, want invalid argument", err)
	}
}
