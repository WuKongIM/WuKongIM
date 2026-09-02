package message

import (
	"context"
	"errors"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestStoreApplyFetchTrustedBatchIsolatesInvalidAndNoopChannels(t *testing.T) {
	engine := openCompatEngine(t)
	valid := mustForChannel(t, engine, "apply-batch-valid:1", channel.ChannelID{ID: "apply-batch-valid", Type: 1})
	invalid := mustForChannel(t, engine, "apply-batch-invalid:1", channel.ChannelID{ID: "apply-batch-invalid", Type: 1})
	noop := mustForChannel(t, engine, "apply-batch-noop:1", channel.ChannelID{ID: "apply-batch-noop", Type: 1})
	defer valid.Close()
	defer invalid.Close()
	defer noop.Close()
	ahead := uint64(2)
	zero := uint64(0)

	results := StoreApplyFetchTrustedBatch(nil, []ApplyFetchBatchItem{
		{Store: valid, Request: channel.ApplyFetchStoreRequest{
			Records: []channel.Record{compatTestRecord(t, 9_301, "apply-batch-valid", "client")},
		}},
		{Store: invalid, Request: channel.ApplyFetchStoreRequest{
			Records:      []channel.Record{compatTestRecord(t, 9_302, "apply-batch-invalid", "client")},
			CheckpointHW: &ahead,
		}},
		{Store: noop, Request: channel.ApplyFetchStoreRequest{CheckpointHW: &zero}},
	})
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	if results[0].Err != nil || results[0].LEO != 1 {
		t.Fatalf("valid result = %+v", results[0])
	}
	if !errors.Is(results[1].Err, channel.ErrCorruptState) || results[1].LEO != 0 {
		t.Fatalf("invalid result = %+v", results[1])
	}
	if results[2].Err != nil || results[2].LEO != 0 {
		t.Fatalf("no-op result = %+v", results[2])
	}
	if got := valid.LEO(); got != 1 {
		t.Fatalf("valid LEO = %d, want 1", got)
	}
	if got := invalid.LEO(); got != 0 {
		t.Fatalf("invalid LEO = %d, want 0", got)
	}
	if got := noop.LEO(); got != 0 {
		t.Fatalf("no-op LEO = %d, want 0", got)
	}
	if snapshot := engine.ChannelEntryMetricsSnapshot(); snapshot.BackgroundPins != 0 {
		t.Fatalf("background pins = %d, want zero", snapshot.BackgroundPins)
	}
}

func TestStoreApplyFetchTrustedBatchRejectsAmbiguousAndUnavailableItems(t *testing.T) {
	engine := openCompatEngine(t)
	id := channel.ChannelID{ID: "apply-batch-duplicate", Type: 1}
	first := mustForChannel(t, engine, "apply-batch-duplicate:1", id)
	sibling := mustForChannel(t, engine, "apply-batch-duplicate:1", id)
	defer first.Close()
	defer sibling.Close()

	results := StoreApplyFetchTrustedBatch(context.Background(), []ApplyFetchBatchItem{
		{Store: first}, {Store: sibling}, {Store: nil},
	})
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	for index, result := range results {
		if !errors.Is(result.Err, channel.ErrInvalidArgument) {
			t.Fatalf("result[%d] = %+v, want invalid argument", index, result)
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	results = StoreApplyFetchTrustedBatch(canceled, []ApplyFetchBatchItem{{Store: first}})
	if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("canceled results = %+v", results)
	}
	if got := StoreApplyFetchTrustedBatch(context.Background(), nil); len(got) != 0 {
		t.Fatalf("empty results = %+v", got)
	}
}

func TestBatchLockAdmissionHonorsCanceledContextWithoutLeakingLocks(t *testing.T) {
	engine := openCompatEngine(t)
	store := mustForChannel(t, engine, "batch-lock-canceled:1", channel.ChannelID{ID: "batch-lock-canceled", Type: 1})
	defer store.Close()
	entry := store.log.channelEntry
	entry.appendMu.Lock()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lockCommitEntriesWithoutHoldAndWait(canceled, []*channelEntry{entry}, nil); !errors.Is(err, context.Canceled) {
		entry.appendMu.Unlock()
		t.Fatalf("lock admission error = %v, want canceled", err)
	}
	entry.appendMu.Unlock()
	if !entry.appendMu.TryLock() {
		t.Fatal("canceled admission leaked append lock ownership")
	}
	entry.appendMu.Unlock()
}
