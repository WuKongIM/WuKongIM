package conversation

import (
	"context"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestActiveHintCacheDedupesByMessageSeqAndActiveAt(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL: time.Hour,
		Now:     func() time.Time { return now },
	})

	key := metadb.UserConversationActiveHint{UID: "u1", ChannelID: "c1", ChannelType: 2}
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		withHintValues(key, 100, 10),
		withHintValues(key, 90, 11),
		withHintValues(key, 110, 11),
		withHintValues(key, 120, 9),
	}))

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{withHintValues(key, 110, 11)}, hints)
}

func TestActiveHintCacheDeleteBarrierDropsOldHintsButAcceptsNewerHints(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:    time.Hour,
		BarrierTTL: time.Hour,
		Now:        func() time.Time { return now },
	})

	key := metadb.UserConversationActiveHint{UID: "u1", ChannelID: "c1", ChannelType: 2}
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{withHintValues(key, 100, 10)}))
	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "c1",
		ChannelType:  2,
		DeletedToSeq: 10,
	}}))

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Empty(t, hints)

	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		withHintValues(key, 101, 10),
		withHintValues(key, 102, 11),
	}))
	hints, err = cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{withHintValues(key, 102, 11)}, hints)
}

func TestActiveHintCacheListSynthesizesHotHintsInActiveOrder(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL: time.Hour,
		Now:     func() time.Time { return now },
	})

	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u2", ChannelID: "c9", ChannelType: 2, ActiveAt: 400, MessageSeq: 4},
		{UID: "u1", ChannelID: "c3", ChannelType: 1, ActiveAt: 300, MessageSeq: 5},
	}))

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 2)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c3", ChannelType: 1, ActiveAt: 300, MessageSeq: 5},
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
	}, hints)
}

func TestActiveHintCacheFlushBatchesAndKeepsUpsertSemantics(t *testing.T) {
	now := time.Unix(100, 0)
	store := newActiveHintStoreStub()
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		Store:          store,
		HintTTL:        time.Hour,
		FlushBatchSize: 2,
		Now:            func() time.Time { return now },
	})

	key := metadb.UserConversationActiveHint{UID: "u1", ChannelID: "c1", ChannelType: 2}
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		withHintValues(key, 100, 10),
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 20},
		{UID: "u2", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 30},
	}))

	store.blockFirstBatch()
	flushDone := make(chan error, 1)
	go func() { flushDone <- cache.Flush(context.Background()) }()
	store.waitFirstBatch(t)

	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{withHintValues(key, 150, 11)}))
	store.releaseFirstBatch()
	require.NoError(t, <-flushDone)

	store.requireBatches(t, [][]metadb.UserConversationActivePatch{
		{
			{UID: "u2", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 30},
			{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 20},
		},
		{
			{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 10},
		},
	})

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{withHintValues(key, 150, 11)}, hints)
}

func withHintValues(base metadb.UserConversationActiveHint, activeAt int64, messageSeq uint64) metadb.UserConversationActiveHint {
	base.ActiveAt = activeAt
	base.MessageSeq = messageSeq
	return base
}

type activeHintStoreStub struct {
	mu             sync.Mutex
	batches        [][]metadb.UserConversationActivePatch
	firstBatchSeen chan struct{}
	firstBatchGate chan struct{}
}

func newActiveHintStoreStub() *activeHintStoreStub {
	return &activeHintStoreStub{}
}

func (s *activeHintStoreStub) TouchUserConversationActiveAt(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	s.mu.Lock()
	batch := append([]metadb.UserConversationActivePatch(nil), patches...)
	s.batches = append(s.batches, batch)
	firstSeen := s.firstBatchSeen
	firstGate := s.firstBatchGate
	isFirst := len(s.batches) == 1 && firstGate != nil
	s.mu.Unlock()

	if isFirst {
		close(firstSeen)
		<-firstGate
	}
	return nil
}

func (s *activeHintStoreStub) blockFirstBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.firstBatchSeen = make(chan struct{})
	s.firstBatchGate = make(chan struct{})
}

func (s *activeHintStoreStub) waitFirstBatch(t *testing.T) {
	t.Helper()
	select {
	case <-s.firstBatchSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first flush batch")
	}
}

func (s *activeHintStoreStub) releaseFirstBatch() {
	s.mu.Lock()
	gate := s.firstBatchGate
	s.mu.Unlock()
	close(gate)
}

func (s *activeHintStoreStub) requireBatches(t *testing.T, want [][]metadb.UserConversationActivePatch) {
	t.Helper()
	s.mu.Lock()
	got := append([][]metadb.UserConversationActivePatch(nil), s.batches...)
	s.mu.Unlock()
	require.Equal(t, want, got)
}
