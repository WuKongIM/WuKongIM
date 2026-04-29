package channelmeta

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestActivationCacheRunSingleflightCoalescesConcurrentActivations(t *testing.T) {
	var cache ActivationCache
	key := channel.ChannelKey("1:hot")
	want := channel.Meta{Key: key, Leader: 2}
	call := &activationCall{done: make(chan struct{}), meta: want}
	var calls int32

	cache.mu.Lock()
	cache.calls = map[activationCallKey]*activationCall{{key: key}: call}
	cache.mu.Unlock()
	close(call.done)

	got, result, err := cache.RunSingleflight(key, false, func(ActivationCacheGeneration) (channel.Meta, MetaRefreshResult, error) {
		atomic.AddInt32(&calls, 1)
		return channel.Meta{Key: key, Leader: 9}, MetaRefreshAuthoritativeRead, nil
	})
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Empty(t, result)
	require.Equal(t, int32(0), atomic.LoadInt32(&calls))
}

func TestActivationCacheRunSingleflightCleansUpAfterPanic(t *testing.T) {
	var cache ActivationCache
	key := channel.ChannelKey("1:panic")
	want := channel.Meta{Key: key, Leader: 3}

	func() {
		defer func() {
			require.Equal(t, "boom", recover())
		}()
		_, _, _ = cache.RunSingleflight(key, false, func(ActivationCacheGeneration) (channel.Meta, MetaRefreshResult, error) {
			panic("boom")
		})
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		got, _, err := cache.RunSingleflight(key, false, func(ActivationCacheGeneration) (channel.Meta, MetaRefreshResult, error) {
			return want, MetaRefreshAuthoritativeRead, nil
		})
		require.NoError(t, err)
		require.Equal(t, want, got)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "activation call remained registered after panic")
	}
}
