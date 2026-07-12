package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

func TestMessageDBStoreAdapterContract(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	cleanupFactory := cleanupChannelStoreFactory{t: t, Factory: factory}
	testStoreContract(t, cleanupFactory)
	testStoreCheckpointHWMonotonic(t, cleanupFactory)
}

func TestMessageDBStoreAdapterCheckpointPreservesExistingFields(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	cs, err := factory.ChannelStore(ch.ChannelKey("1:preserve"), ch.ChannelID{ID: "preserve", Type: 1})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)
	adapter := cs.(*messageDBChannelStoreAdapter)
	require.NoError(t, adapter.store.StoreCheckpoint(channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}))

	require.NoError(t, adapter.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 3}))
	current, err := adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}, current)

	require.NoError(t, adapter.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 8}))
	current, err = adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 8}, current)
}

func TestMessageDBTraceMetadataIsNotStoredInDBCompatibleMessage(t *testing.T) {
	ctx := context.Background()
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	id := ch.ChannelID{ID: "trace-not-persisted", Type: 1}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{{ID: 10, Payload: []byte("payload"), SizeBytes: len("payload")}},
		Sync:    true,
	})
	require.NoError(t, err)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, committed.Messages, 1)
	require.Empty(t, committed.Messages[0].TraceID)
	require.Empty(t, committed.Messages[0].ChannelKey)

	lookup, ok := cs.(MessageLookup)
	require.True(t, ok)
	msg, found, err := lookup.LookupMessageByID(ctx, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, msg.TraceID)
	require.Empty(t, msg.ChannelKey)
}

func TestMessageDBStoreAdapterPreservesConversationDisplayFields(t *testing.T) {
	ctx := context.Background()
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	id := ch.ChannelID{ID: "display-fields", Type: 2}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{{
			ID:                10,
			Setting:           2,
			FromUID:           "u1",
			ClientMsgNo:       "client-1",
			Payload:           []byte("payload"),
			SizeBytes:         len("payload"),
			ServerTimestampMS: 1234,
		}},
		Sync: true,
	})
	require.NoError(t, err)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, committed.Messages, 1)
	require.Equal(t, "u1", committed.Messages[0].FromUID)
	require.Equal(t, "client-1", committed.Messages[0].ClientMsgNo)
	require.Equal(t, uint8(2), committed.Messages[0].Setting)
	require.Equal(t, []byte("payload"), committed.Messages[0].Payload)
	require.Equal(t, int64(1234), committed.Messages[0].ServerTimestampMS)

	lookup, ok := cs.(MessageLookup)
	require.True(t, ok)
	msg, found, err := lookup.LookupMessageByID(ctx, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "u1", msg.FromUID)
	require.Equal(t, "client-1", msg.ClientMsgNo)
	require.Equal(t, uint8(2), msg.Setting)
	require.Equal(t, []byte("payload"), msg.Payload)
	require.Equal(t, int64(1234), msg.ServerTimestampMS)
}

func TestMessageDBStoreAdapterLookupIdempotency(t *testing.T) {
	ctx := context.Background()
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	id := ch.ChannelID{ID: "idempotency", Type: 2}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{{
			ID:          10,
			FromUID:     "u1",
			ClientMsgNo: "client-1",
			Payload:     []byte("payload"),
			SizeBytes:   len("payload"),
		}},
		Sync: true,
	})
	require.NoError(t, err)

	lookup, ok := cs.(IdempotencyLookup)
	require.True(t, ok)
	hit, found, err := lookup.LookupIdempotency(ctx, "u1", "client-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(10), hit.Message.MessageID)
	require.Equal(t, uint64(1), hit.Message.MessageSeq)
	require.Equal(t, "u1", hit.Message.FromUID)
	require.Equal(t, "client-1", hit.Message.ClientMsgNo)
	require.Equal(t, []byte("payload"), hit.Message.Payload)
	require.Equal(t, hashPayload([]byte("payload")), hit.PayloadHash)

	_, found, err = lookup.LookupIdempotency(ctx, "u1", "missing")
	require.NoError(t, err)
	require.False(t, found)
}

func TestMessageDBStoreAdapterPreservesSyncOnceFlag(t *testing.T) {
	ctx := context.Background()
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	id := ch.ChannelID{ID: "sync-once", Type: 2}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{{
			ID:        10,
			Payload:   []byte("cmd"),
			SizeBytes: len("cmd"),
			SyncOnce:  true,
		}},
		Sync: true,
	})
	require.NoError(t, err)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, committed.Messages, 1)
	require.True(t, committed.Messages[0].SyncOnce)

	log, err := cs.ReadLog(ctx, ReadLogRequest{FromOffset: 1, MaxOffset: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, log.Records, 1)
	require.True(t, log.Records[0].SyncOnce)

	lookup, ok := cs.(MessageLookup)
	require.True(t, ok)
	msg, found, err := lookup.LookupMessageByID(ctx, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, msg.SyncOnce)
}

func TestChannelStoreAdapterRetentionAdoptAndTrim(t *testing.T) {
	ctx := context.Background()
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	id := ch.ChannelID{ID: "retention-adapter", Type: 1}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{
			{ID: 21, Payload: []byte("one"), SizeBytes: len("one")},
			{ID: 22, Payload: []byte("two"), SizeBytes: len("two")},
			{ID: 23, Payload: []byte("three"), SizeBytes: len("three")},
		},
		Sync: true,
	})
	require.NoError(t, err)

	retained, err := cs.AdoptRetentionBoundary(ctx, 2, "committed")
	require.NoError(t, err)
	require.Equal(t, uint64(3), retained)

	state, err := cs.LoadRetentionState(ctx)
	require.NoError(t, err)
	require.Equal(t, RetentionState{LocalRetentionThroughSeq: 2, RetainedMaxSeq: 3}, state)

	trim, err := cs.TrimMessagesThrough(ctx, 2, RetentionTrimOptions{MaxMessages: 1})
	require.NoError(t, err)
	require.Equal(t, RetentionTrimResult{DeletedThroughSeq: 1, Deleted: 1, More: true}, trim)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Equal(t, []uint64{2, 3}, messageSeqs(committed.Messages))

	trim, err = cs.TrimMessagesThrough(ctx, 2, RetentionTrimOptions{MaxMessages: 10})
	require.NoError(t, err)
	require.Equal(t, RetentionTrimResult{DeletedThroughSeq: 2, Deleted: 1}, trim)

	state, err = cs.LoadRetentionState(ctx)
	require.NoError(t, err)
	require.Equal(t, RetentionState{LocalRetentionThroughSeq: 2, PhysicalRetentionThroughSeq: 2, RetainedMaxSeq: 3}, state)
}

func TestMessageDBChannelStoreCloseReclaimsLeaseAndIsIdempotent(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	key := ch.ChannelKey("lease-reclaim:1")
	first, err := factory.ChannelStore(key, ch.ChannelID{ID: "first", Type: 1})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, first)
	require.NoError(t, first.Close())
	require.NoError(t, first.Close())

	second, err := factory.ChannelStore(key, ch.ChannelID{ID: "second", Type: 2})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, second)
}

func TestMessageDBChannelStoreMethodsReturnErrClosedAfterClose(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	cs, err := factory.ChannelStore("adapter-post-close:1", ch.ChannelID{ID: "adapter-post-close", Type: 1})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)
	require.NoError(t, cs.Close())
	ctx := context.Background()

	_, err = cs.Load(ctx)
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{})
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = cs.ApplyFollower(ctx, ApplyFollowerRequest{})
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 0, MinSeq: 1, Reverse: true})
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = cs.ReadLog(ctx, ReadLogRequest{})
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = cs.LoadRetentionState(ctx)
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = cs.AdoptRetentionBoundary(ctx, 0, "")
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = cs.TrimMessagesThrough(ctx, 0, RetentionTrimOptions{})
	require.ErrorIs(t, err, ch.ErrClosed)
	require.ErrorIs(t, cs.StoreCheckpoint(ctx, ch.Checkpoint{}), ch.ErrClosed)

	lookup := cs.(MessageLookup)
	_, _, err = lookup.LookupMessageByID(ctx, 0)
	require.ErrorIs(t, err, ch.ErrClosed)
	idempotency := cs.(IdempotencyLookup)
	_, _, err = idempotency.LookupIdempotency(ctx, "", "")
	require.ErrorIs(t, err, ch.ErrClosed)
	require.NoError(t, cs.Close())
}

func TestMessageDBStoreAdapterCheckpointInitialZeroIsDurable(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	cs, err := factory.ChannelStore("checkpoint-zero-adapter:1", ch.ChannelID{ID: "checkpoint-zero-adapter", Type: 1})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)
	adapter := cs.(*messageDBChannelStoreAdapter)

	require.NoError(t, cs.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 0}))
	checkpoint, err := adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, channel.Checkpoint{}, checkpoint)
}

func TestMessageDBStoreAdapterCheckpointDistinctAdaptersNeverRegress(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	key := ch.ChannelKey("checkpoint-distinct-adapters:1")
	id := ch.ChannelID{ID: "checkpoint-distinct-adapters", Type: 1}
	first, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, first)
	second, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, second)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for store, hw := range map[ChannelStore]uint64{first: 12, second: 4} {
		store, hw := store, hw
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: hw})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	checkpoint, err := first.(*messageDBChannelStoreAdapter).store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, uint64(12), checkpoint.HW)
}

func TestMessageDBFactoryBatchesReleaseLeasesOnSuccess(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	appendKeys := []ch.ChannelKey{"batch-append-success-a:1", "batch-append-success-b:1"}
	appendResults := factory.AppendLeaderBatch(context.Background(), []AppendLeaderBatchItem{
		batchAppendItem(appendKeys[0], "append-success-a", 101, 0),
		batchAppendItem(appendKeys[1], "append-success-b", 102, 0),
	})
	require.Len(t, appendResults, 2)
	require.NoError(t, appendResults[0].Err)
	require.NoError(t, appendResults[1].Err)
	requireBatchKeysReclaimed(t, factory, appendKeys)

	applyKeys := []ch.ChannelKey{"batch-apply-success-a:1", "batch-apply-success-b:1"}
	applyResults := factory.ApplyFollowerBatch(context.Background(), []ApplyFollowerBatchItem{
		batchApplyItem(applyKeys[0], "apply-success-a", 201, 1),
		batchApplyItem(applyKeys[1], "apply-success-b", 202, 1),
	})
	require.Len(t, applyResults, 2)
	require.NoError(t, applyResults[0].Err)
	require.NoError(t, applyResults[1].Err)

	checkpointResults := factory.StoreCheckpointBatch(context.Background(), []StoreCheckpointBatchItem{
		{ChannelKey: applyKeys[0], ChannelID: ch.ChannelID{ID: "apply-success-a", Type: 1}, Checkpoint: ch.Checkpoint{HW: 1}},
		{ChannelKey: applyKeys[1], ChannelID: ch.ChannelID{ID: "apply-success-b", Type: 1}, Checkpoint: ch.Checkpoint{HW: 1}},
	})
	require.Len(t, checkpointResults, 2)
	require.NoError(t, checkpointResults[0].Err)
	require.NoError(t, checkpointResults[1].Err)
	for i, key := range applyKeys {
		cs, err := factory.ChannelStore(key, ch.ChannelID{ID: "apply-success-" + string(rune('a'+i)), Type: 1})
		require.NoError(t, err)
		loaded, err := cs.Load(context.Background())
		require.NoError(t, err)
		require.Equal(t, uint64(1), loaded.CheckpointHW)
		require.NoError(t, cs.Close())
	}
	requireBatchKeysReclaimed(t, factory, applyKeys)
}

func TestMessageDBFactoryBatchesReleaseLeasesOnCancellation(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	appendKeys := []ch.ChannelKey{"batch-append-cancel-a:1", "batch-append-cancel-b:1"}
	appendResults := factory.AppendLeaderBatch(ctx, []AppendLeaderBatchItem{
		batchAppendItem(appendKeys[0], "append-cancel-a", 301, 0),
		batchAppendItem(appendKeys[1], "append-cancel-b", 302, 0),
	})
	require.ErrorIs(t, appendResults[0].Err, context.Canceled)
	require.ErrorIs(t, appendResults[1].Err, context.Canceled)
	requireBatchKeysReclaimed(t, factory, appendKeys)

	applyKeys := []ch.ChannelKey{"batch-apply-cancel-a:1", "batch-apply-cancel-b:1"}
	applyResults := factory.ApplyFollowerBatch(ctx, []ApplyFollowerBatchItem{
		batchApplyItem(applyKeys[0], "apply-cancel-a", 401, 1),
		batchApplyItem(applyKeys[1], "apply-cancel-b", 402, 1),
	})
	require.ErrorIs(t, applyResults[0].Err, context.Canceled)
	require.ErrorIs(t, applyResults[1].Err, context.Canceled)
	requireBatchKeysReclaimed(t, factory, applyKeys)
	checkpointKeys := []ch.ChannelKey{"batch-checkpoint-cancel-a:1", "batch-checkpoint-cancel-b:1"}
	checkpointResults := factory.StoreCheckpointBatch(ctx, []StoreCheckpointBatchItem{
		{ChannelKey: checkpointKeys[0], ChannelID: ch.ChannelID{ID: "checkpoint-cancel-a", Type: 1}, Checkpoint: ch.Checkpoint{HW: 1}},
		{ChannelKey: checkpointKeys[1], ChannelID: ch.ChannelID{ID: "checkpoint-cancel-b", Type: 1}, Checkpoint: ch.Checkpoint{HW: 1}},
	})
	require.ErrorIs(t, checkpointResults[0].Err, context.Canceled)
	require.ErrorIs(t, checkpointResults[1].Err, context.Canceled)
	requireBatchKeysReclaimed(t, factory, checkpointKeys)
}

func TestMessageDBFactoryBatchAdmittedCancellationReclaimsAfterTerminalCommit(t *testing.T) {
	observer := &commitAdmissionObserver{admitted: make(chan struct{})}
	factory := NewMessageDBFactoryWithOptions(t.TempDir(), MessageDBFactoryOptions{
		CommitFlushWindow: 200 * time.Millisecond,
		CommitMaxRequests: 2,
		CommitObserver:    observer,
	})
	t.Cleanup(func() { _ = factory.Close() })
	key := ch.ChannelKey("batch-admitted-cancel:1")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	resultCh := make(chan []AppendLeaderBatchResult, 1)
	go func() {
		resultCh <- factory.AppendLeaderBatch(ctx, []AppendLeaderBatchItem{
			batchAppendItem(key, "batch-admitted-cancel", 451, 0),
		})
	}()

	select {
	case <-observer.admitted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for commit admission")
	}
	cancel()
	var results []AppendLeaderBatchResult
	select {
	case results = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled adapter batch")
	}
	require.Len(t, results, 1)
	require.ErrorIs(t, results[0].Err, context.Canceled)

	_, err := factory.ChannelStore(key, ch.ChannelID{ID: "replacement-before-terminal", Type: 2})
	require.Error(t, err, "background commit pin should retain the canonical identity")

	deadline := time.Now().Add(2 * time.Second)
	for {
		replacement, acquireErr := factory.ChannelStore(key, ch.ChannelID{ID: "replacement-after-terminal", Type: 2})
		if acquireErr == nil {
			require.NoError(t, replacement.Close())
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("channel lease was not reclaimed after terminal commit: %v", acquireErr)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMessageDBFactoryBatchesReleaseSuccessfulAcquisitionsWhenOneAcquireFails(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	conflictKey := ch.ChannelKey("batch-acquire-conflict:1")
	held, err := factory.ChannelStore(conflictKey, ch.ChannelID{ID: "held", Type: 1})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, held)
	uniqueAppendKey := ch.ChannelKey("batch-acquire-append-unique:1")
	appendResults := factory.AppendLeaderBatch(context.Background(), []AppendLeaderBatchItem{
		batchAppendItem(conflictKey, "conflicting", 501, 0),
		batchAppendItem(uniqueAppendKey, "append-unique", 502, 0),
	})
	require.Error(t, appendResults[0].Err)
	require.NoError(t, appendResults[1].Err)
	requireBatchKeysReclaimed(t, factory, []ch.ChannelKey{uniqueAppendKey})

	uniqueApplyKey := ch.ChannelKey("batch-acquire-apply-unique:1")
	applyResults := factory.ApplyFollowerBatch(context.Background(), []ApplyFollowerBatchItem{
		batchApplyItem(conflictKey, "conflicting", 601, 1),
		batchApplyItem(uniqueApplyKey, "apply-unique", 602, 1),
	})
	require.Error(t, applyResults[0].Err)
	require.NoError(t, applyResults[1].Err)
	requireBatchKeysReclaimed(t, factory, []ch.ChannelKey{uniqueApplyKey})
	require.NoError(t, held.Close())

	replacement, err := factory.ChannelStore(conflictKey, ch.ChannelID{ID: "replacement", Type: 2})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, replacement)
}

func TestMessageDBFactoryBatchesReleaseLeasesOnDBItemErrors(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	appendKeys := []ch.ChannelKey{"batch-append-error:1", "batch-append-after-error:1"}
	appendResults := factory.AppendLeaderBatch(context.Background(), []AppendLeaderBatchItem{
		batchAppendItem(appendKeys[0], "append-error", 701, 2),
		batchAppendItem(appendKeys[1], "append-after-error", 702, 0),
	})
	require.Error(t, appendResults[0].Err)
	require.NoError(t, appendResults[1].Err)
	requireBatchKeysReclaimed(t, factory, appendKeys)

	applyKeys := []ch.ChannelKey{"batch-apply-error:1", "batch-apply-after-error:1"}
	applyResults := factory.ApplyFollowerBatch(context.Background(), []ApplyFollowerBatchItem{
		batchApplyItem(applyKeys[0], "apply-error", 801, 2),
		batchApplyItem(applyKeys[1], "apply-after-error", 802, 1),
	})
	require.Error(t, applyResults[0].Err)
	require.NoError(t, applyResults[1].Err)
	requireBatchKeysReclaimed(t, factory, applyKeys)
}

func TestMessageDBFactoryCloseMapsClosedAndUnopenedFactoryStaysInvalid(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	cs, err := factory.ChannelStore("factory-close-existing:1", ch.ChannelID{ID: "factory-close-existing", Type: 1})
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)
	require.NoError(t, factory.Close())

	_, err = factory.ChannelStore("factory-close-new:1", ch.ChannelID{ID: "factory-close-new", Type: 1})
	require.ErrorIs(t, err, ch.ErrClosed)
	_, _, _, err = factory.ListChannelsPage(context.Background(), "", 10)
	require.ErrorIs(t, err, ch.ErrClosed)
	appendResults := factory.AppendLeaderBatch(context.Background(), []AppendLeaderBatchItem{
		batchAppendItem("factory-close-append:1", "factory-close-append", 901, 0),
	})
	require.ErrorIs(t, appendResults[0].Err, ch.ErrClosed)
	applyResults := factory.ApplyFollowerBatch(context.Background(), []ApplyFollowerBatchItem{
		batchApplyItem("factory-close-apply:1", "factory-close-apply", 902, 1),
	})
	require.ErrorIs(t, applyResults[0].Err, ch.ErrClosed)
	checkpointResults := factory.StoreCheckpointBatch(context.Background(), []StoreCheckpointBatchItem{{}})
	require.ErrorIs(t, checkpointResults[0].Err, ch.ErrClosed)
	_, err = cs.ReadCommitted(context.Background(), ReadCommittedRequest{FromSeq: 0, MinSeq: 1, Reverse: true})
	require.ErrorIs(t, err, ch.ErrClosed)
	require.NoError(t, cs.Close())

	unopened := &MessageDBFactory{}
	_, err = unopened.ChannelStore("unopened:1", ch.ChannelID{ID: "unopened", Type: 1})
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	unopenedAppend := unopened.AppendLeaderBatch(context.Background(), []AppendLeaderBatchItem{{}})
	require.ErrorIs(t, unopenedAppend[0].Err, ch.ErrInvalidConfig)
	unopenedApply := unopened.ApplyFollowerBatch(context.Background(), []ApplyFollowerBatchItem{{}})
	require.ErrorIs(t, unopenedApply[0].Err, ch.ErrInvalidConfig)
	unopenedCheckpoint := unopened.StoreCheckpointBatch(context.Background(), []StoreCheckpointBatchItem{{}})
	require.ErrorIs(t, unopenedCheckpoint[0].Err, ch.ErrInvalidConfig)

	var nilFactory *MessageDBFactory
	_, err = nilFactory.ChannelStore("nil:1", ch.ChannelID{ID: "nil", Type: 1})
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	nilAppend := nilFactory.AppendLeaderBatch(context.Background(), []AppendLeaderBatchItem{{}})
	require.ErrorIs(t, nilAppend[0].Err, ch.ErrInvalidConfig)
	nilApply := nilFactory.ApplyFollowerBatch(context.Background(), []ApplyFollowerBatchItem{{}})
	require.ErrorIs(t, nilApply[0].Err, ch.ErrInvalidConfig)
	nilCheckpoint := nilFactory.StoreCheckpointBatch(context.Background(), []StoreCheckpointBatchItem{{}})
	require.ErrorIs(t, nilCheckpoint[0].Err, ch.ErrInvalidConfig)
}

func TestMessageDBFactoryConcurrentCloseErrorMappingStaysPublic(t *testing.T) {
	factory := &MessageDBFactory{}
	factory.closed.Store(true)

	require.ErrorIs(t, factory.mapError(errors.New("physical engine closed")), ch.ErrClosed)
	require.ErrorIs(t, factory.mapError(context.Canceled), context.Canceled)
	require.ErrorIs(t, factory.mapError(context.DeadlineExceeded), context.DeadlineExceeded)
}

func TestDBCompatibleLegacyTimestampDoesNotBecomeServerTimestampMS(t *testing.T) {
	payload, err := encodeDBCompatibleMessage(channel.Message{
		MessageID: 10,
		Timestamp: 1234,
		Payload:   []byte("payload"),
	})
	require.NoError(t, err)
	msg, err := decodeDBCompatibleMessage(payload)
	require.NoError(t, err)
	require.Equal(t, int32(1234), msg.Timestamp)
	require.Zero(t, msg.ServerTimestampMS)
}

func TestMessageDBFactoryOptionsDoesNotExposeCommitNoSync(t *testing.T) {
	_, ok := reflect.TypeOf(MessageDBFactoryOptions{}).FieldByName("CommitNoSync")
	require.False(t, ok)
}

func TestNewMessageDBFactoryWithOptionsConfiguresCommitCoordinatorTuning(t *testing.T) {
	factory := NewMessageDBFactoryWithOptions(t.TempDir(), MessageDBFactoryOptions{
		CommitFlushWindow: 500 * time.Microsecond,
		CommitMaxRequests: 32,
		CommitMaxRecords:  512,
		CommitMaxBytes:    256 * 1024,
		CommitShards:      4,
	})
	t.Cleanup(func() { _ = factory.Close() })

	require.NotNil(t, factory.engine)
	cfg := factory.engine.CommitCoordinatorConfig()
	require.Equal(t, 500*time.Microsecond, cfg.FlushWindow)
	require.Equal(t, 32, cfg.MaxRequests)
	require.Equal(t, 512, cfg.MaxRecords)
	require.Equal(t, 256*1024, cfg.MaxBytes)
	require.Equal(t, 4, cfg.Shards)
}

func TestMessageDBFactoryMetricsSnapshotReportsPhysicalStore(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })

	snapshot := factory.MetricsSnapshot()
	require.Greater(t, snapshot.DiskSpaceUsageBytes, uint64(0))
	require.GreaterOrEqual(t, snapshot.ReadAmplification, 0)
}

func TestMessageDBFactoryMetricsSnapshotTracksChannelEntryOwnership(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })

	first, err := factory.ChannelStore("metrics:1", ch.ChannelID{ID: "metrics", Type: 1})
	require.NoError(t, err)
	second, err := factory.ChannelStore("metrics:1", ch.ChannelID{ID: "metrics", Type: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	t.Cleanup(func() { _ = second.Close() })

	require.Equal(t, messagedb.ChannelEntryMetricsSnapshot{
		ActiveEntries:     1,
		OutstandingLeases: 2,
		AcquireTotal:      2,
	}, factory.MetricsSnapshot().ChannelEntries)
	require.Equal(t, factory.MetricsSnapshot().ChannelEntries, factory.ChannelEntryMetricsSnapshot())

	require.NoError(t, first.Close())
	require.Equal(t, messagedb.ChannelEntryMetricsSnapshot{
		ActiveEntries:     1,
		OutstandingLeases: 1,
		AcquireTotal:      2,
		ReleaseTotal:      1,
	}, factory.ChannelEntryMetricsSnapshot())

	require.NoError(t, second.Close())
	require.Equal(t, messagedb.ChannelEntryMetricsSnapshot{
		AcquireTotal: 2,
		ReleaseTotal: 2,
		ReclaimTotal: 1,
	}, factory.ChannelEntryMetricsSnapshot())
}

func TestStoreApplyFetchRecordsPrefersTrustedPath(t *testing.T) {
	store := &trustedApplyFetchRecorder{leo: 7}

	leo, err := storeApplyFetchRecords(store, channel.ApplyFetchStoreRequest{
		Records: []channel.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(7), leo)
	require.Equal(t, 1, store.trustedCalls)
	require.Zero(t, store.strictCalls)
}

func TestFollowerApplyCheckpointCapsLeaderHWAtAppliedTail(t *testing.T) {
	records := []channel.Record{{Index: 4}, {Index: 5}}
	checkpoint := followerApplyCheckpointHW(records, 9)
	require.NotNil(t, checkpoint)
	require.Equal(t, uint64(5), *checkpoint)
	require.Nil(t, followerApplyCheckpointHW(nil, 9))
}

func TestFollowerApplyCheckpointPreservesEpochAndLogStart(t *testing.T) {
	ctx := context.Background()
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	id := ch.ChannelID{ID: "follower-checkpoint-fields", Type: 1}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	closeChannelStoreOnCleanup(t, cs)

	_, err = cs.ApplyFollower(ctx, ApplyFollowerRequest{
		Records:  []ch.Record{{ID: 1, Index: 1}, {ID: 2, Index: 2}},
		LeaderHW: 2,
	})
	require.NoError(t, err)
	adapter := cs.(*messageDBChannelStoreAdapter)
	require.NoError(t, adapter.store.StoreCheckpoint(channel.Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 2}))

	_, err = cs.ApplyFollower(ctx, ApplyFollowerRequest{
		Records:  []ch.Record{{ID: 3, Index: 3}},
		LeaderHW: 3,
	})
	require.NoError(t, err)
	checkpoint, err := adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 3}, checkpoint)
}

type trustedApplyFetchRecorder struct {
	leo          uint64
	strictCalls  int
	trustedCalls int
}

func (s *trustedApplyFetchRecorder) StoreApplyFetch(channel.ApplyFetchStoreRequest) (uint64, error) {
	s.strictCalls++
	return 0, errors.New("strict path should not be used")
}

func (s *trustedApplyFetchRecorder) StoreApplyFetchTrusted(channel.ApplyFetchStoreRequest) (uint64, error) {
	s.trustedCalls++
	return s.leo, nil
}

type cleanupChannelStoreFactory struct {
	t *testing.T
	Factory
}

type commitAdmissionObserver struct {
	once     sync.Once
	admitted chan struct{}
}

func (o *commitAdmissionObserver) SetCommitCoordinatorQueueDepth(int) {
	o.once.Do(func() { close(o.admitted) })
}

func (o *commitAdmissionObserver) ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent) {
}

func (f cleanupChannelStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error) {
	store, err := f.Factory.ChannelStore(key, id)
	if err == nil {
		closeChannelStoreOnCleanup(f.t, store)
	}
	return store, err
}

func closeChannelStoreOnCleanup(t *testing.T, store ChannelStore) {
	t.Helper()
	t.Cleanup(func() { _ = store.Close() })
}

func batchAppendItem(key ch.ChannelKey, id string, messageID uint64, index uint64) AppendLeaderBatchItem {
	return AppendLeaderBatchItem{
		ChannelKey: key,
		ChannelID:  ch.ChannelID{ID: id, Type: 1},
		Request: AppendLeaderRequest{Records: []ch.Record{{
			ID:        messageID,
			Index:     index,
			Payload:   []byte("payload"),
			SizeBytes: len("payload"),
		}}},
	}
}

func batchApplyItem(key ch.ChannelKey, id string, messageID uint64, index uint64) ApplyFollowerBatchItem {
	return ApplyFollowerBatchItem{
		ChannelKey: key,
		ChannelID:  ch.ChannelID{ID: id, Type: 1},
		Request: ApplyFollowerRequest{Records: []ch.Record{{
			ID:        messageID,
			Index:     index,
			Payload:   []byte("payload"),
			SizeBytes: len("payload"),
		}}},
	}
}

func requireBatchKeysReclaimed(t *testing.T, factory *MessageDBFactory, keys []ch.ChannelKey) {
	t.Helper()
	for _, key := range keys {
		store, err := factory.ChannelStore(key, ch.ChannelID{ID: "replacement-" + string(key), Type: 2})
		require.NoError(t, err, "key %q still owns its prior batch lease", key)
		closeChannelStoreOnCleanup(t, store)
	}
}
