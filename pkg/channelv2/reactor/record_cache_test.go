package reactor

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfigEnablesLeaderRecentRecordCache(t *testing.T) {
	cfg := defaultConfig(Config{LocalNode: 1, Store: store.NewMemoryFactory()})

	require.Equal(t, 10, cfg.LeaderRecentRecordCacheSize)
	require.Equal(t, cfg.PullMaxBytes, cfg.LeaderRecentRecordCacheBytes)
}

func TestDefaultReactorConfigEnablesLeaderRecentRecordCache(t *testing.T) {
	cfg := defaultReactorConfig(ReactorConfig{LocalNode: 1, Store: store.NewMemoryFactory()})

	require.Equal(t, 10, cfg.LeaderRecentRecordCacheSize)
	require.Equal(t, cfg.PullMaxBytes, cfg.LeaderRecentRecordCacheBytes)
}

func TestNewGroupPassesLeaderRecentRecordCacheConfig(t *testing.T) {
	g, err := NewGroup(Config{
		LocalNode:                    1,
		Store:                        store.NewMemoryFactory(),
		ReactorCount:                 1,
		LeaderRecentRecordCacheSize:  7,
		LeaderRecentRecordCacheBytes: 4096,
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, g.Close())
	}()

	require.Len(t, g.reactors, 1)
	require.Equal(t, 7, g.reactors[0].cfg.LeaderRecentRecordCacheSize)
	require.Equal(t, 4096, g.reactors[0].cfg.LeaderRecentRecordCacheBytes)
}

func TestDefaultConfigCapsLeaderRecentRecordCacheBytesAt256KiB(t *testing.T) {
	cfg := defaultConfig(Config{LocalNode: 1, Store: store.NewMemoryFactory(), PullMaxBytes: 512 * 1024})

	require.Equal(t, 256*1024, cfg.LeaderRecentRecordCacheBytes)
}

func TestPublicFacadeConfigExposesLeaderRecentRecordCacheKnobs(t *testing.T) {
	cfg := ch.Config{
		PullMaxBytes:                 64 * 1024,
		LeaderRecentRecordCacheSize:  7,
		LeaderRecentRecordCacheBytes: 4096,
	}

	require.Equal(t, 7, cfg.LeaderRecentRecordCacheSize)
	require.Equal(t, 4096, cfg.LeaderRecentRecordCacheBytes)
}

func TestDefaultConfigPreservesDisabledLeaderRecentRecordCache(t *testing.T) {
	cfg := defaultConfig(Config{LocalNode: 1, Store: store.NewMemoryFactory(), LeaderRecentRecordCacheSize: -1})

	require.Equal(t, -1, cfg.LeaderRecentRecordCacheSize)
	require.Zero(t, cfg.LeaderRecentRecordCacheBytes)
}

func TestRecentRecordCacheRetainsNewestContinuousSuffix(t *testing.T) {
	cache := newRecentRecordCache(3, 1024)

	cache.append([]ch.Record{cacheRecord(1, "a"), cacheRecord(2, "b")})
	cache.append([]ch.Record{cacheRecord(3, "c"), cacheRecord(4, "d")})

	require.True(t, cache.enabled())
	require.False(t, cache.empty())
	require.Equal(t, uint64(2), cache.base())
	require.Equal(t, uint64(4), cache.lastOffset())
	require.True(t, cache.hasSuffixAfter(1))
	records, ok := cache.slice(2, 4, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{2, 3, 4}, recordIndexes(records))
}

func TestRecentRecordCacheEnforcesByteCap(t *testing.T) {
	cache := newRecentRecordCache(10, 5)

	cache.append([]ch.Record{cacheRecord(1, "aa"), cacheRecord(2, "bb"), cacheRecord(3, "cc")})

	require.Equal(t, uint64(2), cache.base())
	require.Equal(t, 4, cache.bytes)
	records, ok := cache.slice(2, 3, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{2, 3}, recordIndexes(records))

	cache.append([]ch.Record{cacheRecord(4, "oversized")})

	require.Equal(t, uint64(4), cache.base())
	require.Equal(t, uint64(4), cache.lastOffset())
	require.Equal(t, len("oversized"), cache.bytes)
	records, ok = cache.slice(4, 4, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{4}, recordIndexes(records))
}

func TestRecentRecordCacheSliceRespectsMaxBytesAndClonesPayload(t *testing.T) {
	input := []ch.Record{cacheRecord(1, "abc"), cacheRecord(2, "def"), cacheRecord(3, "ghi")}
	cache := newRecentRecordCache(10, 1024)

	cache.append(input)
	input[0].Payload[0] = 'x'

	records, ok := cache.slice(1, 3, 5)
	require.True(t, ok)
	require.Equal(t, []uint64{1}, recordIndexes(records))
	require.Equal(t, []byte("abc"), records[0].Payload)

	records[0].Payload[0] = 'y'
	again, ok := cache.slice(1, 1, 1024)
	require.True(t, ok)
	require.Equal(t, []byte("abc"), again[0].Payload)
}

func TestRecentRecordCacheMissesUncoveredOffsets(t *testing.T) {
	cache := newRecentRecordCache(10, 1024)
	cache.append([]ch.Record{cacheRecord(5, "e"), cacheRecord(6, "f")})

	_, ok := cache.slice(4, 6, 1024)
	require.False(t, ok)
	require.False(t, cache.covers(4))

	_, ok = cache.slice(7, 7, 1024)
	require.False(t, ok)
	require.False(t, cache.covers(7))
}

func TestRecentRecordCacheGapAppendResetsSuffix(t *testing.T) {
	cache := newRecentRecordCache(10, 1024)

	cache.append([]ch.Record{cacheRecord(1, "a"), cacheRecord(2, "b")})
	cache.append([]ch.Record{cacheRecord(4, "d"), cacheRecord(5, "e")})

	require.Equal(t, uint64(4), cache.base())
	records, ok := cache.slice(4, 5, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{4, 5}, recordIndexes(records))
	_, ok = cache.slice(2, 5, 1024)
	require.False(t, ok)

	cache.append([]ch.Record{cacheRecord(6, "f"), cacheRecord(8, "h"), cacheRecord(9, "i")})
	records, ok = cache.slice(8, 9, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{8, 9}, recordIndexes(records))
}

func TestRecentRecordCacheAllZeroAppendResets(t *testing.T) {
	cache := newRecentRecordCache(10, 1024)
	cache.append([]ch.Record{cacheRecord(1, "a"), cacheRecord(2, "b")})

	cache.append([]ch.Record{{ID: 3, Payload: []byte("unindexed"), SizeBytes: len("unindexed")}})

	require.True(t, cache.empty())
	require.Equal(t, uint64(0), cache.base())
	require.Equal(t, uint64(0), cache.lastOffset())
	_, ok := cache.slice(1, 2, 1024)
	require.False(t, ok)
}

func TestRecentRecordCacheTrailingUnindexedAppendResets(t *testing.T) {
	cache := newRecentRecordCache(10, 1024)
	cache.append([]ch.Record{cacheRecord(1, "a"), cacheRecord(2, "b")})

	cache.append([]ch.Record{cacheRecord(4, "d"), {ID: 5, Payload: []byte("pending"), SizeBytes: len("pending")}})

	require.True(t, cache.empty())
	require.Equal(t, uint64(0), cache.base())
	require.Equal(t, uint64(0), cache.lastOffset())
	_, ok := cache.slice(1, 2, 1024)
	require.False(t, ok)
}

func TestRecentRecordCacheEmptyAppendIsNoop(t *testing.T) {
	cache := newRecentRecordCache(10, 1024)
	cache.append([]ch.Record{cacheRecord(1, "a"), cacheRecord(2, "b")})

	cache.append(nil)
	cache.append([]ch.Record{})

	require.Equal(t, uint64(1), cache.base())
	require.Equal(t, uint64(2), cache.lastOffset())
	records, ok := cache.slice(1, 2, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{1, 2}, recordIndexes(records))
}

func TestRecentRecordCacheDisabled(t *testing.T) {
	cache := newRecentRecordCache(0, 1024)

	cache.append([]ch.Record{cacheRecord(1, "a")})

	require.False(t, cache.enabled())
	require.True(t, cache.empty())
	require.Equal(t, uint64(0), cache.base())
	require.Equal(t, uint64(0), cache.lastOffset())
	_, ok := cache.slice(1, 1, 1024)
	require.False(t, ok)
}

func cacheRecord(index uint64, payload string) ch.Record {
	return ch.Record{ID: index, Index: index, Payload: []byte(payload), SizeBytes: len(payload)}
}

func recordIndexes(records []ch.Record) []uint64 {
	indexes := make([]uint64, 0, len(records))
	for _, record := range records {
		indexes = append(indexes, record.Index)
	}
	return indexes
}
