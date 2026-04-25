package channelmeta

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestActivationCacheStoresPositiveUntilExpiry(t *testing.T) {
	var cache ActivationCache
	key := channel.ChannelKey("1:hot")
	now := time.Unix(100, 0)
	want := channel.Meta{Key: key, Leader: 2}

	cache.StorePositive(key, want, now)

	got, ok := cache.LoadPositive(key, now.Add(4*time.Second))
	require.True(t, ok)
	require.Equal(t, want, got)

	got, ok = cache.LoadPositive(key, now.Add(6*time.Second))
	require.False(t, ok)
	require.Equal(t, channel.Meta{}, got)
}

func TestActivationCacheStoresOnlyNotFoundNegativeUntilExpiry(t *testing.T) {
	var cache ActivationCache
	key := channel.ChannelKey("1:missing")
	now := time.Unix(200, 0)

	cache.StoreNegative(key, errors.New("temporary transport error"), now)
	require.NoError(t, cache.LoadNegative(key, now))

	cache.StoreNegative(key, metadb.ErrNotFound, now)
	require.ErrorIs(t, cache.LoadNegative(key, now.Add(500*time.Millisecond)), metadb.ErrNotFound)
	require.NoError(t, cache.LoadNegative(key, now.Add(2*time.Second)))
}

func TestActivationCacheClearDropsPositiveAndNegativeEntries(t *testing.T) {
	var cache ActivationCache
	positiveKey := channel.ChannelKey("1:positive")
	negativeKey := channel.ChannelKey("1:negative")
	now := time.Unix(300, 0)

	cache.StorePositive(positiveKey, channel.Meta{Key: positiveKey, Leader: 2}, now)
	cache.StoreNegative(negativeKey, metadb.ErrNotFound, now)

	cache.Clear()

	_, ok := cache.LoadPositive(positiveKey, now)
	require.False(t, ok)
	require.NoError(t, cache.LoadNegative(negativeKey, now))
}
