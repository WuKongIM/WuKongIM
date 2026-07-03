package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExpiredTombstonesDoNotBlockChannelLookup(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-2")
	now := time.Now()

	rt.tombstones.add(meta.Key, 1, now.Add(-time.Second))
	rt.tombstones.add(meta.Key, 2, now.Add(time.Second))
	require.True(t, rt.tombstones.contains(meta.Key, 1))
	require.True(t, rt.tombstones.contains(meta.Key, 2))

	rt.tombstones.dropExpired(now)

	require.False(t, rt.tombstones.contains(meta.Key, 1))
	require.True(t, rt.tombstones.contains(meta.Key, 2))
	_, ok := rt.Channel(meta.Key)
	require.False(t, ok)
}

func TestRuntimeCleanupExpiresTombstones(t *testing.T) {
	rt := newTestRuntimeWithOptions(
		t,
		withTombstoneTTL(25*time.Millisecond),
		withTombstoneCleanupInterval(5*time.Millisecond),
	)
	meta := testMeta("room-cleanup")

	require.NoError(t, rt.EnsureChannel(meta))
	require.NoError(t, rt.RemoveChannel(meta.Key))
	require.True(t, rt.tombstones.contains(meta.Key, 1))

	require.Eventually(t, func() bool {
		return !rt.tombstones.contains(meta.Key, 1)
	}, 400*time.Millisecond, 10*time.Millisecond)
}
