package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

func TestMessageDBStoreAdapterContract(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	testStoreContract(t, factory)
	testStoreCheckpointHWMonotonic(t, factory)
}

func TestMessageDBStoreAdapterCheckpointPreservesExistingFields(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	cs, err := factory.ChannelStore(ch.ChannelKey("1:preserve"), ch.ChannelID{ID: "preserve", Type: 1})
	require.NoError(t, err)
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

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{{
			ID:                10,
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
	require.Equal(t, []byte("payload"), committed.Messages[0].Payload)
	require.Equal(t, int64(1234), committed.Messages[0].ServerTimestampMS)

	lookup, ok := cs.(MessageLookup)
	require.True(t, ok)
	msg, found, err := lookup.LookupMessageByID(ctx, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "u1", msg.FromUID)
	require.Equal(t, "client-1", msg.ClientMsgNo)
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
