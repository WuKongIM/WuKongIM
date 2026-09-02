package message

import (
	"context"
	"errors"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestCheckpointHWBatchPreservesFieldsAndCommitsAcrossPhysicalOwners(t *testing.T) {
	firstEngine := openCompatEngine(t)
	secondEngine := openCompatEngine(t)
	first := mustForChannel(t, firstEngine, "checkpoint-batch-a", channel.ChannelID{ID: "checkpoint-batch-a", Type: 1})
	second := mustForChannel(t, firstEngine, "checkpoint-batch-b", channel.ChannelID{ID: "checkpoint-batch-b", Type: 1})
	third := mustForChannel(t, secondEngine, "checkpoint-batch-c", channel.ChannelID{ID: "checkpoint-batch-c", Type: 1})
	defer first.Close()
	defer second.Close()
	defer third.Close()
	if err := first.StoreCheckpoint(channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}); err != nil {
		t.Fatalf("StoreCheckpoint(first) error = %v", err)
	}
	if err := third.StoreCheckpoint(channel.Checkpoint{Epoch: 9, LogStartOffset: 1, HW: 3}); err != nil {
		t.Fatalf("StoreCheckpoint(third) error = %v", err)
	}

	results := StoreCheckpointHWMonotonicBatch(context.Background(), []CheckpointHWBatchItem{
		{Store: first, HW: 8},
		{Store: second, HW: 4},
		{Store: third, HW: 6},
	})
	if len(results) != 3 {
		t.Fatalf("StoreCheckpointHWMonotonicBatch() result count = %d", len(results))
	}
	for index, result := range results {
		if result.Err != nil {
			t.Fatalf("result[%d] error = %v", index, result.Err)
		}
	}
	assertCompatCheckpoint(t, first, channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 8})
	assertCompatCheckpoint(t, second, channel.Checkpoint{HW: 4})
	assertCompatCheckpoint(t, third, channel.Checkpoint{Epoch: 9, LogStartOffset: 1, HW: 6})

	results = StoreCheckpointHWMonotonicBatch(context.Background(), []CheckpointHWBatchItem{
		{Store: first, HW: 7}, {Store: second, HW: 4}, {Store: third, HW: 2},
	})
	for index, result := range results {
		if result.Err != nil {
			t.Fatalf("no-op result[%d] error = %v", index, result.Err)
		}
	}
	assertCompatCheckpoint(t, first, channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 8})
	assertCompatCheckpoint(t, second, channel.Checkpoint{HW: 4})
	assertCompatCheckpoint(t, third, channel.Checkpoint{Epoch: 9, LogStartOffset: 1, HW: 6})
}

func TestCheckpointHWBatchRejectsDuplicateCanonicalEntryAndCancellation(t *testing.T) {
	engine := openCompatEngine(t)
	id := channel.ChannelID{ID: "checkpoint-batch-duplicate", Type: 1}
	first := mustForChannel(t, engine, "checkpoint-batch-duplicate", id)
	sibling := mustForChannel(t, engine, "checkpoint-batch-duplicate", id)
	defer first.Close()
	defer sibling.Close()
	results := StoreCheckpointHWMonotonicBatch(context.Background(), []CheckpointHWBatchItem{
		{Store: first, HW: 8}, {Store: sibling, HW: 9}, {Store: nil, HW: 10},
	})
	if len(results) != 3 {
		t.Fatalf("duplicate result count = %d", len(results))
	}
	for index, result := range results {
		if !errors.Is(result.Err, channel.ErrInvalidArgument) {
			t.Fatalf("duplicate result[%d] error = %v", index, result.Err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results = StoreCheckpointHWMonotonicBatch(ctx, []CheckpointHWBatchItem{{Store: first, HW: 1}})
	if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("canceled batch results = %#v", results)
	}
	if got := StoreCheckpointHWMonotonicBatch(context.Background(), nil); len(got) != 0 {
		t.Fatalf("empty batch results = %#v", got)
	}
}

func TestCheckpointHWCommitSetupReleasesTransferredLocksOnEarlyFailure(t *testing.T) {
	engine := openCompatEngine(t)
	store := mustForChannel(t, engine, "checkpoint-setup-failure", channel.ChannelID{ID: "checkpoint-setup-failure", Type: 1})
	entry := store.log.channelEntry
	prepared := []preparedCheckpointHW{{store: store, checkpoint: Checkpoint{HW: 1}}}
	assertReleased := func(name string) {
		t.Helper()
		if !entry.checkpointMu.TryLock() {
			t.Fatalf("checkpoint lock remained owned after %s", name)
		}
		entry.checkpointMu.Unlock()
	}

	entry.checkpointMu.Lock()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := commitPreparedCheckpointHWBatch(canceled, engine, prepared, []*channelEntry{entry}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commit error = %v", err)
	}
	assertReleased("cancellation")

	entry.checkpointMu.Lock()
	if err := commitPreparedCheckpointHWBatch(context.Background(), nil, prepared, []*channelEntry{entry}); !errors.Is(err, channel.ErrInvalidArgument) {
		t.Fatalf("nil owner commit error = %v", err)
	}
	assertReleased("nil owner")

	if err := engine.Close(); err != nil {
		t.Fatalf("Engine.Close(): %v", err)
	}
	entry.checkpointMu.Lock()
	if err := commitPreparedCheckpointHWBatch(context.Background(), engine, prepared, []*channelEntry{entry}); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("closed owner commit error = %v", err)
	}
	assertReleased("closed owner")
}

func assertCompatCheckpoint(t *testing.T, store *ChannelStore, want channel.Checkpoint) {
	t.Helper()
	got, err := store.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if got != want {
		t.Fatalf("checkpoint = %#v, want %#v", got, want)
	}
}
