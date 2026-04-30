package channelmeta

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

func BenchmarkSyncRefreshChannelMetaBusinessCacheHit(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	id := channel.ChannelID{ID: "bench-hot", Type: 2}
	syncer := benchmarkChannelMetaSync(id, now, now.Add(time.Hour))
	if _, err := syncer.RefreshChannelMeta(ctx, id); err != nil {
		b.Fatalf("prime channel meta cache: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := syncer.RefreshChannelMeta(ctx, id); err != nil {
			b.Fatalf("refresh channel meta: %v", err)
		}
	}
}

func BenchmarkSyncRefreshChannelMetaBusinessCacheHitParallel(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	id := channel.ChannelID{ID: "bench-hot-parallel", Type: 2}
	syncer := benchmarkChannelMetaSync(id, now, now.Add(time.Hour))
	if _, err := syncer.RefreshChannelMeta(ctx, id); err != nil {
		b.Fatalf("prime channel meta cache: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := syncer.RefreshChannelMeta(ctx, id); err != nil {
				b.Fatalf("refresh channel meta: %v", err)
			}
		}
	})
}

func BenchmarkSyncActivateByKeyFetchCacheHit(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	id := channel.ChannelID{ID: "bench-fetch", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	syncer := benchmarkChannelMetaSync(id, now, now.Add(time.Hour))
	syncer.cache.StorePositive(key, channel.Meta{
		Key:         key,
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channel.NodeID{1, 2, 3},
		ISR:         []channel.NodeID{1, 2, 3},
		MinISR:      2,
		LeaseUntil:  now.Add(time.Hour),
		Status:      channel.StatusActive,
	}, now)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := syncer.ActivateByKey(ctx, key, channelruntime.ActivationSourceFetch); err != nil {
			b.Fatalf("activate channel key: %v", err)
		}
	}
}

func BenchmarkSyncRefreshChannelMetaAuthoritativeRead(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	id := channel.ChannelID{ID: "bench-cold", Type: 2}
	syncer := benchmarkChannelMetaSync(id, now, now.Add(500*time.Millisecond))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := syncer.RefreshChannelMeta(ctx, id); err != nil {
			b.Fatalf("refresh channel meta: %v", err)
		}
	}
}

func BenchmarkSyncInvalidateAndRefreshChannelMeta(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	id := channel.ChannelID{ID: "bench-invalidate", Type: 2}
	syncer := benchmarkChannelMetaSync(id, now, now.Add(time.Hour))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		syncer.InvalidateChannelMeta(id)
		if _, err := syncer.RefreshChannelMeta(ctx, id); err != nil {
			b.Fatalf("refresh channel meta: %v", err)
		}
	}
}

func BenchmarkActivationCacheRunSingleflightUncontended(b *testing.B) {
	var cache ActivationCache
	key := channel.ChannelKey("channel/2/bench")
	meta := channel.Meta{Key: key, Leader: 1, Status: channel.StatusActive}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := cache.RunSingleflight(key, true, func(ActivationCacheGeneration) (channel.Meta, MetaRefreshResult, error) {
			return meta, MetaRefreshAuthoritativeRead, nil
		}); err != nil {
			b.Fatalf("run singleflight: %v", err)
		}
	}
}

func BenchmarkActivationCacheLoadPositive(b *testing.B) {
	var cache ActivationCache
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	key := channel.ChannelKey("channel/2/bench")
	cache.StorePositive(key, channel.Meta{Key: key, Leader: 1, Status: channel.StatusActive}, now)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := cache.LoadPositive(key, now); !ok {
			b.Fatal("positive cache entry missing")
		}
	}
}

func benchmarkChannelMetaSync(id channel.ChannelID, now time.Time, leaseUntil time.Time) *Sync {
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Leader:       1,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: leaseUntil.UnixMilli(),
		Features:     uint64(channel.MessageSeqFormatU64),
	}
	return NewSync(SyncOptions{
		Source:    benchmarkMetaSource{meta: meta},
		Runtime:   benchmarkRuntime{},
		LocalNode: 1,
		Now:       func() time.Time { return now },
	})
}

type benchmarkMetaSource struct {
	meta metadb.ChannelRuntimeMeta
}

func (s benchmarkMetaSource) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return s.meta, nil
}

func (s benchmarkMetaSource) ListChannelRuntimeMeta(context.Context) ([]metadb.ChannelRuntimeMeta, error) {
	return []metadb.ChannelRuntimeMeta{s.meta}, nil
}

type benchmarkRuntime struct{}

func (benchmarkRuntime) ApplyRoutingMeta(channel.Meta) error {
	return nil
}

func (benchmarkRuntime) EnsureLocalRuntime(channel.Meta) error {
	return nil
}

func (benchmarkRuntime) RemoveLocalRuntime(channel.ChannelKey) error {
	return nil
}

func (benchmarkRuntime) Channel(channel.ChannelKey) (ChannelObserver, bool) {
	return nil, false
}
