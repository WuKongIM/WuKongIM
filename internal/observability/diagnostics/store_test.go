package diagnostics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventContainsMessageSeq(t *testing.T) {
	event := Event{MessageSeq: 7, RangeStart: 10, RangeEnd: 12}
	if !event.ContainsMessageSeq(7) {
		t.Fatal("expected direct message seq match")
	}
	if !event.ContainsMessageSeq(11) {
		t.Fatal("expected range message seq match")
	}
	if event.ContainsMessageSeq(9) {
		t.Fatal("did not expect message seq outside direct and range match")
	}
}

func TestNormalizeEventSetsDefaultsAndTruncatesError(t *testing.T) {
	event := normalizeEvent(Event{Stage: Stage("message.send_durable"), Error: string(make([]byte, 300))}, time.Unix(10, 0), 256)
	if event.Result != ResultOK {
		t.Fatalf("expected default result %q, got %q", ResultOK, event.Result)
	}
	if !event.At.Equal(time.Unix(10, 0)) {
		t.Fatalf("expected default timestamp, got %s", event.At)
	}
	if len(event.Error) != 256 {
		t.Fatalf("expected truncated error length 256, got %d", len(event.Error))
	}
}

func TestRedactEventRemovesSensitiveFieldsFromResponses(t *testing.T) {
	event := redactEvent(Event{FromUID: "u1", Error: "boom"})
	if event.FromUID != "" {
		t.Fatalf("expected FromUID to be redacted, got %q", event.FromUID)
	}
	if event.Error != "boom" {
		t.Fatalf("expected Error to be preserved, got %q", event.Error)
	}
}

func TestStoreQueriesByTraceAndClientMsgNo(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 4, MaxEventsPerKey: 4, MaxKeysPerIndex: 8, Now: func() time.Time { return time.Unix(1, 0) }})
	store.Record(Event{TraceID: "trace-1", ClientMsgNo: "c1", Stage: "gateway.messages_send", Result: ResultOK})
	store.Record(Event{TraceID: "trace-1", ClientMsgNo: "c1", Stage: "message.send_durable", MessageSeq: 9, Result: ResultOK})

	byTrace := store.Query(context.Background(), Query{TraceID: "trace-1", Limit: 10})
	require.Equal(t, StatusOK, byTrace.Status)
	require.Len(t, byTrace.Events, 2)

	byClient := store.Query(context.Background(), Query{ClientMsgNo: "c1", Limit: 10})
	require.Equal(t, StatusOK, byClient.Status)
	require.Len(t, byClient.Events, 2)
}

func TestStoreQueriesByPhysicalSlotID(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8, MaxEventsPerKey: 8, MaxKeysPerIndex: 8})
	store.Record(Event{
		Stage:             "slot.preferred_leader_reconcile",
		SlotID:            7,
		Decision:          "preferred_lagging",
		ActualLeaderID:    1,
		PreferredLeaderID: 2,
		RaftTerm:          11,
		ConfigEpoch:       4,
		Result:            ResultSkipped,
	})
	store.Record(Event{Stage: "slot.preferred_leader_reconcile", SlotID: 8, Decision: "match", Result: ResultOK})

	result := store.Query(context.Background(), Query{SlotID: 7, Limit: 10})

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, uint32(7), result.Query.SlotID)
	require.Len(t, result.Events, 1)
	require.Equal(t, uint32(7), result.Events[0].SlotID)
	require.Equal(t, "preferred_lagging", result.Events[0].Decision)
	require.Equal(t, uint64(1), result.Events[0].ActualLeaderID)
	require.Equal(t, uint64(2), result.Events[0].PreferredLeaderID)
	require.Equal(t, uint64(11), result.Events[0].RaftTerm)
	require.Equal(t, uint64(4), result.Events[0].ConfigEpoch)
}

func TestStoreQueryFiltersByResultBeforeLimit(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	store := NewStore(StoreOptions{NodeID: 1, Capacity: 16, Now: func() time.Time { return now }})

	store.Record(Event{TraceID: "tr-ok-1", Stage: Stage("gateway_send"), Result: ResultOK, At: now.Add(time.Second)})
	store.Record(Event{TraceID: "tr-err-1", Stage: Stage("channel_append"), Result: ResultError, ErrorCode: ErrorCodeUnknown, At: now.Add(2 * time.Second)})
	store.Record(Event{TraceID: "tr-ok-2", Stage: Stage("delivery"), Result: ResultOK, At: now.Add(3 * time.Second)})
	store.Record(Event{TraceID: "tr-err-2", Stage: Stage("replica_quorum"), Result: ResultError, ErrorCode: ErrorCodeUnknown, At: now.Add(4 * time.Second)})
	store.Record(Event{TraceID: "tr-ok-3", Stage: Stage("ack"), Result: ResultOK, At: now.Add(5 * time.Second)})

	got := store.Query(context.Background(), Query{Result: ResultError, Limit: 1})

	require.Equal(t, StatusError, got.Status)
	require.Len(t, got.Events, 1)
	require.Equal(t, "tr-err-2", got.Events[0].TraceID)
	require.Equal(t, ResultError, got.Events[0].Result)
}

func TestStoreQueryFiltersByStageAndResult(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	store := NewStore(StoreOptions{NodeID: 1, Capacity: 16, Now: func() time.Time { return now }})

	store.Record(Event{Stage: Stage("channel_append"), Result: ResultOK, At: now.Add(time.Second)})
	store.Record(Event{Stage: Stage("channel_append"), Result: ResultTimeout, At: now.Add(2 * time.Second)})
	store.Record(Event{Stage: Stage("delivery"), Result: ResultTimeout, At: now.Add(3 * time.Second)})

	got := store.Query(context.Background(), Query{Stage: Stage("channel_append"), Result: ResultTimeout, Limit: 10})

	require.Equal(t, StatusError, got.Status)
	require.Len(t, got.Events, 1)
	require.Equal(t, Stage("channel_append"), got.Events[0].Stage)
	require.Equal(t, ResultTimeout, got.Events[0].Result)
}

func TestStoreQueryAppliesTimeWindowBeforeLimit(t *testing.T) {
	now := time.Date(2026, 7, 24, 4, 0, 0, 0, time.UTC)
	store := NewStore(StoreOptions{NodeID: 1, Capacity: 8, Now: func() time.Time { return now }})
	store.Record(Event{TraceID: "in-window-old", Stage: "send", At: now.Add(-3 * time.Minute)})
	store.Record(Event{TraceID: "outside-new", Stage: "send", At: now.Add(-time.Minute)})

	got := store.Query(context.Background(), Query{
		Start: now.Add(-4 * time.Minute),
		End:   now.Add(-2 * time.Minute),
		Limit: 1,
	})
	if len(got.Events) != 1 || got.Events[0].TraceID != "in-window-old" {
		t.Fatalf("events = %#v, want older in-window event", got.Events)
	}
}

func TestStoreReturnsStableNotFound(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 4})
	result := store.Query(context.Background(), Query{TraceID: "missing"})
	require.Equal(t, StatusNotFound, result.Status)
	require.Empty(t, result.Events)
	require.NotEmpty(t, result.Notes)
}

func TestStoreMatchesRangeEventsWithoutPerSeqExpansion(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 4, MaxEventsPerKey: 4, MaxKeysPerIndex: 8})
	store.Record(Event{ChannelKey: "person:u1@u2", RangeStart: 10, RangeEnd: 20, Stage: "replica.follower.apply_durable", Result: ResultOK})

	result := store.Query(context.Background(), Query{ChannelKey: "person:u1@u2", MessageSeq: 15, Limit: 10})
	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
}

func TestStoreQueryByUIDRedactsFromUID(t *testing.T) {
	store := NewStore(StoreOptions{NodeID: 1})
	store.Record(Event{TraceID: "trace-u1", FromUID: "u1", Stage: "message.send_durable", Result: ResultOK})
	store.Record(Event{TraceID: "trace-u2", FromUID: "u2", Stage: "message.send_durable", Result: ResultOK})

	result := store.Query(context.Background(), Query{UID: "u1", Limit: 10})

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, "u1", result.UID)
	require.Len(t, result.Events, 1)
	require.Equal(t, "trace-u1", result.Events[0].TraceID)
	require.Empty(t, result.Events[0].FromUID)
}

func TestStoreBoundsIndexKeys(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 16, MaxEventsPerKey: 2, MaxKeysPerIndex: 2})
	store.Record(Event{TraceID: "trace-1", Stage: "s1"})
	store.Record(Event{TraceID: "trace-2", Stage: "s1"})
	store.Record(Event{TraceID: "trace-3", Stage: "s1"})

	require.LessOrEqual(t, store.index.trace.Len(), 2)
}

func TestStoreOverwritesOldEventsAndLazyCleanupSkipsTombstones(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 2, MaxEventsPerKey: 4, MaxKeysPerIndex: 8})
	store.Record(Event{TraceID: "old", Stage: "s1"})
	store.Record(Event{TraceID: "new-1", Stage: "s1"})
	store.Record(Event{TraceID: "new-2", Stage: "s1"})

	oldResult := store.Query(context.Background(), Query{TraceID: "old"})
	require.Equal(t, StatusNotFound, oldResult.Status)
}

func TestStoreMatchesExactChannelSeq(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 4})
	store.Record(Event{ChannelKey: "person:u1@u2", MessageSeq: 9, Stage: "message.send_durable"})

	result := store.Query(context.Background(), Query{ChannelKey: "person:u1@u2", MessageSeq: 9})

	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
}

func TestStoreQueryHonorsCanceledContext(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 4})
	store.Record(Event{TraceID: "trace-1", Stage: "s1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := store.Query(ctx, Query{TraceID: "trace-1"})

	require.Equal(t, StatusNotFound, result.Status)
}

func TestStoreConcurrentRecordAndQuery(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 1024})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			store.Record(Event{TraceID: fmt.Sprintf("trace-%d", i), Stage: "s1"})
		}
	}()
	for i := 0; i < 1000; i++ {
		_ = store.Query(context.Background(), Query{Stage: "s1", Limit: 10})
	}
	<-done
}

func BenchmarkStoreRecord(b *testing.B) {
	store := NewStore(StoreOptions{Capacity: 50000})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store.Record(Event{TraceID: "trace-1", Stage: "message.send_durable", Result: ResultOK})
	}
}

func BenchmarkStoreOverwrite(b *testing.B) {
	store := NewStore(StoreOptions{Capacity: 128})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store.Record(Event{TraceID: fmt.Sprintf("trace-%d", i), Stage: "message.send_durable", Result: ResultOK})
	}
}

func TestStoreMultiKeyQueryDoesNotDropLaterIndexedCandidate(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 16, MaxEventsPerKey: 16, MaxKeysPerIndex: 16})
	for i := 0; i < 4; i++ {
		store.Record(Event{TraceID: "trace-1", Stage: Stage(fmt.Sprintf("trace-only-%d", i))})
	}
	store.Record(Event{TraceID: "trace-1", ClientMsgNo: "client-1", Stage: "target"})

	result := store.Query(context.Background(), Query{TraceID: "trace-1", ClientMsgNo: "client-1", Limit: 1})

	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, Stage("target"), result.Events[0].Stage)
}

func TestStoreChannelOnlyQueryFindsRetainedEventOutsideRecentWindow(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 16, MaxEventsPerKey: 16, MaxKeysPerIndex: 16})
	store.Record(Event{ChannelKey: "person:u1@u2", Stage: "target"})
	for i := 0; i < 8; i++ {
		store.Record(Event{ChannelKey: fmt.Sprintf("person:other-%d", i), Stage: "other"})
	}

	result := store.Query(context.Background(), Query{ChannelKey: "person:u1@u2", Limit: 1})

	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, "person:u1@u2", result.Events[0].ChannelKey)
}

func TestStoreMessageSeqOnlyQueryScansRetainedRing(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 16, MaxEventsPerKey: 16, MaxKeysPerIndex: 16})
	store.Record(Event{ChannelKey: "person:u1@u2", MessageSeq: 42, Stage: "target"})
	for i := 0; i < 8; i++ {
		store.Record(Event{ChannelKey: fmt.Sprintf("person:other-%d", i), MessageSeq: uint64(i + 1), Stage: "other"})
	}

	result := store.Query(context.Background(), Query{MessageSeq: 42, Limit: 1})

	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, uint64(42), result.Events[0].MessageSeq)
}

func TestStoreCanceledContextBeforeQueryAvoidsCandidateWork(t *testing.T) {
	nowCalls := 0
	store := NewStore(StoreOptions{Capacity: 4, Now: func() time.Time {
		nowCalls++
		return time.Unix(10, 0)
	}})
	store.Record(Event{TraceID: "trace-1", Stage: "s1"})
	nowCalls = 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := store.Query(ctx, Query{TraceID: "trace-1"})

	require.Equal(t, StatusNotFound, result.Status)
	require.Zero(t, nowCalls)
}
