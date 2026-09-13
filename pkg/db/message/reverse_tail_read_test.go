package message

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

// The common conversation tail already has an exact persisted sequence. Its
// single-record read must avoid the allocation cost of constructing a range scan.
func TestReverseTailWarmAllocationBudget(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	_, err := log.Append(context.Background(), testRecords(1, strings.Repeat("x", 256)), AppendOptions{})
	require.NoError(t, err)
	allocations := testing.AllocsPerRun(100, func() {
		rows, err := log.ReadReverse(context.Background(), 1, ReadOptions{Limit: 1, MaxBytes: 1 << 20})
		if err != nil || len(rows) != 1 || rows[0].MessageSeq != 1 {
			t.Fatalf("read=%v,%v", rows, err)
		}
	})
	require.LessOrEqual(t, allocations, float64(27), "single persisted tail read allocations")
}

func TestReverseTailMatchesRangeAtBoundaries(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	_, err := log.Append(context.Background(), testRecords(1, "one", "two", "three", "four"), AppendOptions{})
	require.NoError(t, err)
	// Delete one primary row to model a missing requested bound. A point miss
	// must still find the closest surviving predecessor through the bounded scan.
	b := s.engine.NewBatch()
	require.NoError(t, b.Delete(encodeMessageRowKey(log.key, 3, messageHeaderFamilyID)))
	require.NoError(t, b.Commit(true))
	require.NoError(t, b.Close())
	for _, from := range []uint64{0, 1, 2, 3, 4, 5, math.MaxUint64} {
		want, err := log.ReadReverse(context.Background(), from, ReadOptions{Limit: 2, MaxBytes: 1})
		require.NoError(t, err)
		got, err := log.ReadReverse(context.Background(), from, ReadOptions{Limit: 1, MaxBytes: 1})
		require.NoError(t, err)
		if len(want) > 1 {
			want = want[:1]
		}
		require.Equal(t, want, got, "from=%d", from)
	}
	_, err = log.TrimPrefixThrough(context.Background(), 2)
	require.NoError(t, err)
	rows, err := log.ReadReverse(context.Background(), 2, ReadOptions{Limit: 1})
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestReverseTailRejectsCorruptionAndUnavailableStore(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	_, err := log.Append(context.Background(), testRecords(1, "one", "two"), AppendOptions{})
	require.NoError(t, err)
	b := s.engine.NewBatch()
	require.NoError(t, b.Set(encodeMessageRowKey(log.key, 2, messageHeaderFamilyID), []byte{1}))
	require.NoError(t, b.Commit(true))
	require.NoError(t, b.Close())
	rows, err := log.ReadReverse(context.Background(), 2, ReadOptions{Limit: 1})
	require.Error(t, err)
	require.Empty(t, rows, "corrupt exact row must not fall back to an older message")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = log.ReadReverse(ctx, 1, ReadOptions{Limit: 1})
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, s.engine.Close())
	_, err = log.ReadReverse(context.Background(), 1, ReadOptions{Limit: 1})
	require.ErrorIs(t, err, dberrors.ErrClosed)
}
