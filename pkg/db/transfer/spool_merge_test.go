package transfer

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpoolWalkMergePairsRelativeKeysAndRetainsBothSides(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"), "merge-test", 4096)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.Put(ctx, []SpoolRow{
		{Key: []byte("long-left/b"), Value: []byte("left-b")},
		{Key: []byte("long-left/d"), Value: []byte("left-d")},
		{Key: []byte("long-left/f")},
		{Key: []byte("r/a"), Value: []byte("right-a")},
		{Key: []byte("r/b"), Value: []byte("right-b")},
		{Key: []byte("r/e"), Value: []byte("right-e")},
		{Key: []byte("r/f")},
	}))
	var pairs [][2]string
	var retained []SpoolRow
	require.NoError(t, s.WalkMerge(ctx, []byte("long-left/"), []byte("r/"), func(left, right SpoolRow) error {
		pairs = append(pairs, [2]string{string(left.Key), string(right.Key)})
		retained = append(retained, left, right)
		for _, row := range []SpoolRow{left, right} {
			if len(row.Key) > 0 {
				value, found, err := s.Get(ctx, row.Key)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, row.Value, value)
			}
		}
		return nil
	}))
	require.Equal(t, [][2]string{{"", "r/a"}, {"long-left/b", "r/b"}, {"long-left/d", ""}, {"", "r/e"}, {"long-left/f", "r/f"}}, pairs)
	require.Equal(t, "left-b", string(retained[2].Value), "callbacks receive owned rows")
	require.Equal(t, "right-a", string(retained[1].Value))
}

func TestSpoolWalkMergeEmptySidesCancellationAndErrors(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"), "merge-test", 4096)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	calls := 0
	visit := func(SpoolRow, SpoolRow) error { calls++; return nil }
	require.NoError(t, s.WalkMerge(ctx, []byte("l/"), []byte("r/"), visit))
	require.Zero(t, calls)
	require.NoError(t, s.Put(ctx, []SpoolRow{{Key: []byte("l/a"), Value: []byte("a")}}))
	require.NoError(t, s.WalkMerge(ctx, []byte("l/"), []byte("r/"), visit))
	require.Equal(t, 1, calls)
	boom := errors.New("visitor failure")
	require.ErrorIs(t, s.WalkMerge(ctx, []byte("l/"), []byte("r/"), func(SpoolRow, SpoolRow) error { return boom }), boom)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, s.WalkMerge(canceled, []byte("l/"), []byte("r/"), visit), context.Canceled)
	for _, prefixes := range [][2]string{{"", "r/"}, {"l/", ""}, {"l/", "l/"}, {"l/", "l/nested/"}} {
		require.Error(t, s.WalkMerge(ctx, []byte(prefixes[0]), []byte(prefixes[1]), visit))
	}
	require.Error(t, s.WalkMerge(nil, []byte("l/"), []byte("r/"), visit))
	require.Error(t, s.WalkMerge(ctx, []byte("l/"), []byte("r/"), nil))
	require.NoError(t, s.Close())
	require.Error(t, s.WalkMerge(ctx, []byte("l/"), []byte("r/"), visit))
}
