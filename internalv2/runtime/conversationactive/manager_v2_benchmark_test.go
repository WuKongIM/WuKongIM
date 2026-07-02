package conversationactive

import (
	"context"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// generatePatches 生成测试数据
func generatePatches(numUsers, numChannels int) []ActivePatch {
	patches := make([]ActivePatch, numUsers*numChannels)
	idx := 0
	for u := 0; u < numUsers; u++ {
		for c := 0; c < numChannels; c++ {
			patches[idx] = ActivePatch{
				UID:         fmt.Sprintf("u%d", u),
				Kind:        metadb.ConversationKindNormal,
				ChannelID:   fmt.Sprintf("ch%d", c),
				ChannelType: 2,
				ActiveAtMS:  int64(1000 + idx),
				ReadSeq:     uint64(idx),
			}
			idx++
		}
	}
	return patches
}

// BenchmarkManagerV1MarkActive V1 写入性能
func BenchmarkManagerV1MarkActive(b *testing.B) {
	m := NewManager(Options{})
	patches := generatePatches(100, 10) // 1000 个 patches

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.MarkActive(context.Background(), patches)
	}
}

// BenchmarkManagerV2MarkActive V2 写入性能
func BenchmarkManagerV2MarkActive(b *testing.B) {
	m := NewManagerV2(Options{})
	patches := generatePatches(100, 10) // 1000 个 patches

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.MarkActive(context.Background(), patches)
	}
}

// BenchmarkManagerV1MarkActive_Parallel V1 并发写入
func BenchmarkManagerV1MarkActive_Parallel(b *testing.B) {
	m := NewManager(Options{})
	patches := generatePatches(10, 10) // 100 个 patches per goroutine

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			m.MarkActive(context.Background(), patches)
		}
	})
}

// BenchmarkManagerV2MarkActive_Parallel V2 并发写入
func BenchmarkManagerV2MarkActive_Parallel(b *testing.B) {
	m := NewManagerV2(Options{})
	patches := generatePatches(10, 10) // 100 个 patches per goroutine

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			m.MarkActive(context.Background(), patches)
		}
	})
}

// BenchmarkManagerV2ListActiveView 查询性能
func BenchmarkManagerV2ListActiveView(b *testing.B) {
	m := NewManagerV2(Options{})

	// 预填充数据
	patches := generatePatches(100, 100) // 10000 个 patches
	m.MarkActive(context.Background(), patches)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = m.ListActiveView(context.Background(), metadb.ConversationKindNormal,
			"u1", metadb.ConversationActiveCursor{}, 20)
	}
}

// BenchmarkManagerV2HotCacheGet 热缓存读取性能
func BenchmarkManagerV2HotCacheGet(b *testing.B) {
	m := NewManagerV2(Options{})

	// 预填充数据
	patch := ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}
	m.MarkActive(context.Background(), []ActivePatch{patch})

	key := conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = m.hot.get("u1", key)
	}
}

// BenchmarkManagerV2ColdCacheGet 冷缓存读取性能
func BenchmarkManagerV2ColdCacheGet(b *testing.B) {
	m := NewManagerV2(Options{})

	// 预填充冷数据
	patch := ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}
	m.cold.set(patch)

	key := conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = m.cold.get("u1", key)
	}
}

// BenchmarkShardIndexCalculation 分片索引计算性能
func BenchmarkShardIndexCalculation(b *testing.B) {
	uids := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		uids[i] = fmt.Sprintf("user-%d", i)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		uid := uids[i%1000]
		_ = shardIndex(uid, 16)
	}
}
