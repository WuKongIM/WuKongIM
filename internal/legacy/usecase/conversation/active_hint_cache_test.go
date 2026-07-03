package conversation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

func TestActiveHintCacheExpiresHintsAndBarriers(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:    10 * time.Second,
		BarrierTTL: 10 * time.Second,
		Now:        func() time.Time { return now },
	})

	key := metadb.UserConversationActiveHint{UID: "u1", ChannelID: "c1", ChannelType: 2}
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{withHintValues(key, 100, 10)}))
	now = now.Add(11 * time.Second)

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Empty(t, hints)

	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "c1",
		ChannelType:  2,
		DeletedToSeq: 10,
	}}))
	now = now.Add(11 * time.Second)
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{withHintValues(key, 120, 10)}))

	hints, err = cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{withHintValues(key, 120, 10)}, hints)
}

func TestActiveHintCacheEvictsLowestActiveAtWhenOverCapacity(t *testing.T) {
	now := time.Unix(100, 0)
	perUIDCache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:        time.Hour,
		MaxHints:       10,
		MaxHintsPerUID: 2,
		Now:            func() time.Time { return now },
	})
	require.NoError(t, perUIDCache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", ChannelID: "c3", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	}))
	hints, err := perUIDCache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", ChannelID: "c3", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	}, hints)

	globalCache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:        time.Hour,
		MaxHints:       3,
		MaxHintsPerUID: 10,
		Now:            func() time.Time { return now },
	})
	require.NoError(t, globalCache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
		{UID: "u2", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		{UID: "u3", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u4", ChannelID: "c4", ChannelType: 2, ActiveAt: 400, MessageSeq: 4},
	}))
	hints, err = globalCache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Empty(t, hints)
}

func TestActiveHintCacheZeroValueOptionsAreSafe(t *testing.T) {
	cache := NewActiveHintCache(ActiveHintCacheOptions{})

	require.NoError(t, cache.Flush(context.Background()))
	require.NoError(t, cache.SubmitHints(context.Background(), nil))
	require.NoError(t, cache.RemoveHints(context.Background(), nil))
	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Empty(t, hints)
	require.NoError(t, cache.Stop())
}

func TestActiveHintCacheStartStopLifecycle(t *testing.T) {
	store := newActiveHintStoreStub()
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		Store:          store,
		FlushInterval:  5 * time.Millisecond,
		HintTTL:        time.Hour,
		FlushBatchSize: 1,
		Now:            func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
	}))

	require.NoError(t, cache.Start())
	require.NoError(t, cache.Start())
	store.waitBatchCount(t, 1)
	require.NoError(t, cache.Stop())
	require.NoError(t, cache.Stop())
}

func TestActiveHintCacheBackgroundFlushIsBoundedToOneBatch(t *testing.T) {
	store := newActiveHintStoreStub()
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		Store:          store,
		HintTTL:        time.Hour,
		FlushBatchSize: 2,
		Now:            func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		{UID: "u1", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", ChannelID: "c4", ChannelType: 2, ActiveAt: 400, MessageSeq: 4},
		{UID: "u1", ChannelID: "c5", ChannelType: 2, ActiveAt: 500, MessageSeq: 5},
	}))

	require.NoError(t, cache.flushBackgroundBatch(context.Background()))
	store.requireBatches(t, [][]metadb.UserConversationActivePatch{
		{
			{UID: "u1", ChannelID: "c5", ChannelType: 2, ActiveAt: 500, MessageSeq: 5},
			{UID: "u1", ChannelID: "c4", ChannelType: 2, ActiveAt: 400, MessageSeq: 4},
		},
	})
	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
	}, hints)
}

func TestActiveHintCacheSubmitWithinCapacityAvoidsCountMapAllocations(t *testing.T) {
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:        time.Hour,
		MaxHints:       100,
		MaxHintsPerUID: 100,
		Now:            func() time.Time { return time.Unix(100, 0) },
	})
	hints := []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
	}
	require.NoError(t, cache.SubmitHints(context.Background(), hints))

	ctx := context.Background()
	seq := uint64(1)
	allocs := testing.AllocsPerRun(100, func() {
		seq++
		hints[0].ActiveAt = int64(100 + seq)
		hints[0].MessageSeq = seq
		if err := cache.SubmitHints(ctx, hints); err != nil {
			t.Fatalf("SubmitHints: %v", err)
		}
	})
	require.Zero(t, allocs)
}

func TestActiveHintCacheMaintainsUIDHintCounts(t *testing.T) {
	store := newActiveHintStoreStub()
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		Store:          store,
		HintTTL:        time.Hour,
		FlushBatchSize: 1,
		Now:            func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		{UID: "u2", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
	}))
	require.Equal(t, 2, activeHintCountForUID(cache, "u1"))
	require.Equal(t, 1, activeHintCountForUID(cache, "u2"))

	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 150, MessageSeq: 4},
	}))
	require.Equal(t, 2, activeHintCountForUID(cache, "u1"))

	require.NoError(t, cache.flushBackgroundBatch(context.Background()))
	require.Equal(t, 2, activeHintCountForUID(cache, "u1"))
	require.Zero(t, activeHintCountForUID(cache, "u2"))

	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{
		{UID: "u1", ChannelID: "c2", ChannelType: 2, DeletedToSeq: 10},
	}))
	require.Equal(t, 1, activeHintCountForUID(cache, "u1"))

	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, DeletedToSeq: 10},
	}))
	require.Zero(t, activeHintCountForUID(cache, "u1"))
}

func TestActiveHintCacheStopContextBoundsBlockedFlush(t *testing.T) {
	store := newContextBlockingActiveHintStore()
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		Store:         store,
		FlushInterval: time.Millisecond,
		HintTTL:       time.Hour,
		Now:           func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
	}))
	require.NoError(t, cache.Start())
	store.waitEntered(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, cache.StopContext(ctx), context.DeadlineExceeded)
}

func TestActiveHintCacheFlushNoopsWithNilStoreOrNoHints(t *testing.T) {
	nilStoreCache := NewActiveHintCache(ActiveHintCacheOptions{Now: func() time.Time { return time.Unix(100, 0) }})
	require.NoError(t, nilStoreCache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
	}))
	require.NoError(t, nilStoreCache.Flush(context.Background()))
	hints, err := nilStoreCache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Len(t, hints, 1)

	store := newActiveHintStoreStub()
	emptyCache := NewActiveHintCache(ActiveHintCacheOptions{Store: store})
	require.NoError(t, emptyCache.Flush(context.Background()))
	store.requireBatches(t, nil)
}

func TestActiveHintCacheFlushErrorRemovesSuccessfulBatchesOnly(t *testing.T) {
	flushErr := errors.New("flush failed")
	store := newActiveHintStoreStub()
	store.failBatch(2, flushErr)
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		Store:          store,
		HintTTL:        time.Hour,
		FlushBatchSize: 2,
		Now:            func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		{UID: "u1", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
	}))

	require.ErrorIs(t, cache.Flush(context.Background()), flushErr)
	store.requireBatches(t, [][]metadb.UserConversationActivePatch{
		{
			{UID: "u1", ChannelID: "c3", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
			{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		},
		{
			{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
		},
	})

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1},
	}, hints)
}

func TestActiveHintCacheRemoveHintsKeepsHighestDeleteBarrier(t *testing.T) {
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:    time.Hour,
		BarrierTTL: time.Hour,
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "c1",
		ChannelType:  2,
		DeletedToSeq: 10,
	}}))
	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "c1",
		ChannelType:  2,
		DeletedToSeq: 5,
	}}))

	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 70, MessageSeq: 7},
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 110, MessageSeq: 11},
	}))
	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 110, MessageSeq: 11},
	}, hints)
}

func TestActiveHintCacheRemoveHintsOnlyDeletesPendingHintsAtOrBelowBarrier(t *testing.T) {
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:    time.Hour,
		BarrierTTL: time.Hour,
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 110, MessageSeq: 11},
		{UID: "u1", ChannelID: "c2", ChannelType: 2, ActiveAt: 100, MessageSeq: 10},
	}))

	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, DeletedToSeq: 10},
		{UID: "u1", ChannelID: "c2", ChannelType: 2, DeletedToSeq: 10},
	}))

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 110, MessageSeq: 11},
	}, hints)
}

func TestActiveHintCacheRemoveHintsConservativelyDeletesZeroSeqPendingHint(t *testing.T) {
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:    time.Hour,
		BarrierTTL: time.Hour,
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c0", ChannelType: 2, ActiveAt: 100, MessageSeq: 0},
	}))

	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "c0",
		ChannelType:  2,
		DeletedToSeq: 10,
	}}))

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Empty(t, hints)
}

func TestActiveHintCacheLowerBarrierDoesNotDeleteNewerPendingHint(t *testing.T) {
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:    time.Hour,
		BarrierTTL: time.Hour,
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "c1",
		ChannelType:  2,
		DeletedToSeq: 10,
	}}))
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 110, MessageSeq: 11},
	}))
	require.NoError(t, cache.RemoveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "c1",
		ChannelType:  2,
		DeletedToSeq: 5,
	}}))

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "c1", ChannelType: 2, ActiveAt: 110, MessageSeq: 11},
	}, hints)
}

func withHintValues(base metadb.UserConversationActiveHint, activeAt int64, messageSeq uint64) metadb.UserConversationActiveHint {
	base.ActiveAt = activeAt
	base.MessageSeq = messageSeq
	return base
}

func activeHintCountForUID(cache *ActiveHintCache, uid string) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.hintCountsByUID[uid]
}

type activeHintStoreStub struct {
	mu             sync.Mutex
	batches        [][]metadb.UserConversationActivePatch
	firstBatchSeen chan struct{}
	firstBatchGate chan struct{}
	failOnBatch    int
	failErr        error
}

type contextBlockingActiveHintStore struct {
	entered chan struct{}
	once    sync.Once
}

func newContextBlockingActiveHintStore() *contextBlockingActiveHintStore {
	return &contextBlockingActiveHintStore{entered: make(chan struct{})}
}

func (s *contextBlockingActiveHintStore) TouchUserConversationActiveAt(ctx context.Context, _ []metadb.UserConversationActivePatch) error {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func (s *contextBlockingActiveHintStore) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-s.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked active hint flush")
	}
}

func newActiveHintStoreStub() *activeHintStoreStub {
	return &activeHintStoreStub{}
}

func (s *activeHintStoreStub) TouchUserConversationActiveAt(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	s.mu.Lock()
	batch := append([]metadb.UserConversationActivePatch(nil), patches...)
	s.batches = append(s.batches, batch)
	callCount := len(s.batches)
	failErr := s.failErr
	shouldFail := s.failOnBatch == callCount && failErr != nil
	firstSeen := s.firstBatchSeen
	firstGate := s.firstBatchGate
	isFirst := len(s.batches) == 1 && firstGate != nil
	s.mu.Unlock()

	if isFirst {
		close(firstSeen)
		<-firstGate
	}
	if shouldFail {
		return failErr
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

func (s *activeHintStoreStub) waitBatchCount(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		got := len(s.batches)
		s.mu.Unlock()
		if got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d flush batches, got %d", want, got)
		case <-ticker.C:
		}
	}
}

func (s *activeHintStoreStub) failBatch(batch int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failOnBatch = batch
	s.failErr = err
}

func (s *activeHintStoreStub) requireBatches(t *testing.T, want [][]metadb.UserConversationActivePatch) {
	t.Helper()
	s.mu.Lock()
	got := append([][]metadb.UserConversationActivePatch(nil), s.batches...)
	s.mu.Unlock()
	require.Equal(t, want, got)
}
