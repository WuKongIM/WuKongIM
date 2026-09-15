package message

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

// Ordinary channels are the common conversation-read workload. Keep a warm
// count's allocation budget below the former two-independent-rank implementation.
func TestOrdinaryCountWarmAllocationBudget(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	appendBadgeRows(t, log, 1, false, false, false, false)
	assertBadgeCount(t, log, 0, 3, 3)
	allocations := testing.AllocsPerRun(100, func() {
		got, err := log.CountOrdinaryMessages(context.Background(), 0, 3)
		if err != nil || got != 3 {
			t.Fatalf("count = %d, %v", got, err)
		}
	})
	require.LessOrEqual(t, allocations, float64(32), "warm ordinary count allocations")
}

func TestOrdinaryCountMatchesVisibleRowsAfterRetention(t *testing.T) {
	for _, flags := range [][]bool{
		{false, false, false, false, false, false, false, false},
		{true, true, true, true, true, true, true, true},
		{false, true, true, false, true, false, false, true},
	} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			s := openTestMessageStore(t)
			defer s.close(t)
			log := testChannelLog(s)
			appendBadgeRows(t, log, 1, false, flags...)
			for _, retained := range []uint64{0, 1, 3, 5, 8} {
				if retained > 0 {
					_, err := log.TrimPrefixThrough(context.Background(), retained)
					require.NoError(t, err)
				}
				for after := retained; after <= uint64(len(flags)); after++ {
					for through := after; through <= uint64(len(flags)); through++ {
						var want uint64
						for seq := after + 1; seq <= through; seq++ {
							if !flags[seq-1] {
								want++
							}
						}
						assertBadgeCount(t, log, after, through, want)
					}
				}
			}
		})
	}
}

func TestOrdinaryCountMaximumSequenceAndRetainedBaseline(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	// Model a retained index whose first surviving ordinal is above one.
	batch := s.engine.NewBatch()
	require.NoError(t, batch.Set(nonBusinessVersionKey(log.key), []byte{1}))
	require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, ^uint64(0)-2), encodeUint64(7)))
	require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, ^uint64(0)), encodeUint64(8)))
	require.NoError(t, batch.Commit(true))
	require.NoError(t, batch.Close())
	assertBadgeCount(t, log, ^uint64(0)-4, ^uint64(0)-3, 1)
	assertBadgeCount(t, log, ^uint64(0)-4, ^uint64(0), 2)
	assertBadgeCount(t, log, ^uint64(0)-2, ^uint64(0), 1)
	assertBadgeCount(t, log, ^uint64(0)-1, ^uint64(0), 0)
	assertBadgeCount(t, log, ^uint64(0), ^uint64(0), 0)
}

func TestOrdinaryCountRejectsCorruptRank(t *testing.T) {
	for _, c := range []struct {
		name  string
		seq   uint64
		value []byte
		err   error
	}{
		{"zero", 2, encodeUint64(0), dberrors.ErrCorruptValue},
		{"short", 2, []byte{1}, dberrors.ErrCorruptValue},
		{"decreasing", 4, encodeUint64(1), dberrors.ErrCorruptState},
		{"excessive", 4, encodeUint64(99), dberrors.ErrCorruptState},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := openTestMessageStore(t)
			defer s.close(t)
			log := testChannelLog(s)
			batch := s.engine.NewBatch()
			require.NoError(t, batch.Set(nonBusinessVersionKey(log.key), []byte{1}))
			require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, 2), encodeUint64(2)))
			require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, c.seq), c.value))
			require.NoError(t, batch.Commit(true))
			require.NoError(t, batch.Close())
			_, err := log.CountOrdinaryMessages(context.Background(), 2, 4)
			require.ErrorIs(t, err, c.err)
		})
	}
}

func TestOrdinaryCountConcurrentAppendKeepsFixedFrontier(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	flags := make([]bool, 32)
	for i := range flags {
		flags[i] = i%2 == 0
	}
	appendBadgeRows(t, log, 1, false, flags...)
	var wg sync.WaitGroup
	start := make(chan struct{})
	failures := make(chan error, 4)
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				got, err := log.CountOrdinaryMessages(context.Background(), 3, 32)
				if err != nil || got != 15 {
					failures <- fmt.Errorf("count = %d, %v", got, err)
					return
				}
			}
		}()
	}
	close(start)
	for base := uint64(33); base < 161; base += 32 {
		appendBadgeRows(t, log, base, false, flags...)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
}

func TestOrdinaryCountRejectsMalformedIndexKey(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	batch := s.engine.NewBatch()
	require.NoError(t, batch.Set(nonBusinessVersionKey(log.key), []byte{1}))
	// A truncated sequence is within the bounded index span but cannot be a rank.
	key := append(encodeMessageIndexPrefix(log.key, messageIndexIDNonBusinessSeq), byte(0))
	require.NoError(t, batch.Set(key, encodeUint64(1)))
	require.NoError(t, batch.Commit(true))
	require.NoError(t, batch.Close())
	_, err := log.CountOrdinaryMessages(context.Background(), 0, 3)
	require.ErrorIs(t, err, dberrors.ErrCorruptValue)
}

func TestOrdinaryCountWarmReadFailsWhenStoreUnavailable(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	appendBadgeRows(t, log, 1, false, false, false)
	assertBadgeCount(t, log, 0, 2, 2)
	require.NoError(t, s.engine.Close())
	_, err := log.CountOrdinaryMessages(context.Background(), 0, 2)
	require.ErrorIs(t, err, dberrors.ErrClosed)
}

func TestOrdinaryCountZeroFloorRetainedOrdinal(t *testing.T) {
	for _, seq := range []uint64{0, 1, 5, ^uint64(0)} {
		t.Run(fmt.Sprint(seq), func(t *testing.T) {
			s := openTestMessageStore(t)
			defer s.close(t)
			log := testChannelLog(s)
			batch := s.engine.NewBatch()
			require.NoError(t, batch.Set(nonBusinessVersionKey(log.key), []byte{1}))
			require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, seq), encodeUint64(7)))
			require.NoError(t, batch.Commit(true))
			require.NoError(t, batch.Close())
			for _, through := range []uint64{1, 4, 5, ^uint64(0)} {
				want := through
				if seq > 0 && seq <= through {
					want--
				}
				assertBadgeCount(t, log, 0, through, want)
			}
		})
	}
}

func TestOrdinaryCountZeroFloorRejectsCorruptFirstOrdinal(t *testing.T) {
	for _, value := range [][]byte{nil, {1}, encodeUint64(0)} {
		t.Run(fmt.Sprintf("%x", value), func(t *testing.T) {
			s := openTestMessageStore(t)
			defer s.close(t)
			log := testChannelLog(s)
			batch := s.engine.NewBatch()
			require.NoError(t, batch.Set(nonBusinessVersionKey(log.key), []byte{1}))
			// This ordinal lies beyond the upper frontier but must still be
			// validated because it defines the retained zero-floor baseline.
			require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, 5), value))
			require.NoError(t, batch.Commit(true))
			require.NoError(t, batch.Close())
			_, err := log.CountOrdinaryMessages(context.Background(), 0, 3)
			require.ErrorIs(t, err, dberrors.ErrCorruptValue)
		})
	}
}

func TestOrdinaryCountZeroFloorRejectsMalformedKeyAfterZero(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	batch := s.engine.NewBatch()
	require.NoError(t, batch.Set(nonBusinessVersionKey(log.key), []byte{1}))
	require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, 0), encodeUint64(7)))
	require.NoError(t, batch.Set(append(nonBusinessIndexKey(log.key, 0), 0), encodeUint64(7)))
	require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, 2), encodeUint64(8)))
	require.NoError(t, batch.Commit(true))
	require.NoError(t, batch.Close())
	_, err := log.CountOrdinaryMessages(context.Background(), 0, 3)
	require.ErrorIs(t, err, dberrors.ErrCorruptValue)
}
