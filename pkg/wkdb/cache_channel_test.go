package wkdb_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestChannelInfoCache(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	// 测试 ChannelInfo 缓存
	now := time.Now()
	channelInfo := wkdb.ChannelInfo{
		Id:          1,
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         false,
		Large:       true,
		Disband:     false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// 设置缓存
	cache.SetChannelInfo(channelInfo)

	// 获取缓存
	cached, found := cache.GetChannelInfo("channel1", 1)
	assert.True(t, found)
	assert.Equal(t, channelInfo.Id, cached.Id)
	assert.Equal(t, channelInfo.ChannelId, cached.ChannelId)
	assert.Equal(t, channelInfo.ChannelType, cached.ChannelType)
	assert.Equal(t, channelInfo.Large, cached.Large)

	// 测试缓存失效
	cache.InvalidateChannelInfo("channel1", 1)
	_, found = cache.GetChannelInfo("channel1", 1)
	assert.False(t, found)
}

func TestChannelInfoCacheUpdate(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	now := time.Now()
	originalChannelInfo := wkdb.ChannelInfo{
		Id:          1,
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         false,
		Large:       false,
		Disband:     false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// 设置初始缓存
	cache.SetChannelInfo(originalChannelInfo)

	// 验证初始缓存
	cached, found := cache.GetChannelInfo("channel1", 1)
	assert.True(t, found)
	assert.False(t, cached.Large)

	// 更新频道信息
	updatedChannelInfo := originalChannelInfo
	updatedChannelInfo.Large = true
	updatedChannelInfo.Ban = true
	cache.UpdateChannelInfo(updatedChannelInfo)

	// 验证缓存已更新
	cachedAfterUpdate, found := cache.GetChannelInfo("channel1", 1)
	assert.True(t, found)
	assert.True(t, cachedAfterUpdate.Large)
	assert.True(t, cachedAfterUpdate.Ban)
}

func TestChannelInfoCacheUpdateNonExistent(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	now := time.Now()
	channelInfo := wkdb.ChannelInfo{
		Id:          1,
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         false,
		Large:       true,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// 尝试更新不存在的频道信息（应该不会添加到缓存）
	cache.UpdateChannelInfo(channelInfo)

	// 验证缓存中没有该频道信息
	_, found := cache.GetChannelInfo("channel1", 1)
	assert.False(t, found)
}

func TestChannelInfoCacheBatchOperations(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	now := time.Now()
	channelInfos := []wkdb.ChannelInfo{
		{Id: 1, ChannelId: "channel1", ChannelType: 1, Large: true, CreatedAt: &now},
		{Id: 2, ChannelId: "channel2", ChannelType: 1, Large: false, CreatedAt: &now},
		{Id: 3, ChannelId: "channel3", ChannelType: 2, Large: true, CreatedAt: &now},
	}

	// 批量设置频道信息
	cache.BatchSetChannelInfos(channelInfos)

	// 验证所有频道信息都已缓存
	cached1, found1 := cache.GetChannelInfo("channel1", 1)
	assert.True(t, found1)
	assert.True(t, cached1.Large)

	cached2, found2 := cache.GetChannelInfo("channel2", 1)
	assert.True(t, found2)
	assert.False(t, cached2.Large)

	cached3, found3 := cache.GetChannelInfo("channel3", 2)
	assert.True(t, found3)
	assert.True(t, cached3.Large)

	// 批量更新频道信息
	updatedChannelInfos := []wkdb.ChannelInfo{
		{Id: 1, ChannelId: "channel1", ChannelType: 1, Large: false, Ban: true, CreatedAt: &now}, // 更新已存在的
		{Id: 4, ChannelId: "channel4", ChannelType: 1, Large: true, CreatedAt: &now},             // 新的频道（不应该被添加）
	}
	cache.BatchUpdateChannelInfos(updatedChannelInfos)

	// 验证已存在的频道信息被更新
	cached1Updated, found1Updated := cache.GetChannelInfo("channel1", 1)
	assert.True(t, found1Updated)
	assert.False(t, cached1Updated.Large) // 已更新
	assert.True(t, cached1Updated.Ban)    // 已更新

	// 验证新频道信息没有被添加
	_, found4 := cache.GetChannelInfo("channel4", 1)
	assert.False(t, found4)

	// 批量使缓存失效
	channels := []wkdb.Channel{
		{ChannelId: "channel1", ChannelType: 1},
		{ChannelId: "channel2", ChannelType: 1},
	}
	cache.BatchInvalidateChannelInfos(channels)

	// 验证指定频道的缓存已失效
	_, found1After := cache.GetChannelInfo("channel1", 1)
	assert.False(t, found1After)

	_, found2After := cache.GetChannelInfo("channel2", 1)
	assert.False(t, found2After)

	// 验证未指定的频道缓存仍然存在
	_, found3After := cache.GetChannelInfo("channel3", 2)
	assert.True(t, found3After)
}

func TestChannelInfoCacheStats(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	// 添加一些数据
	now := time.Now()
	channelInfos := []wkdb.ChannelInfo{
		{Id: 1, ChannelId: "channel1", ChannelType: 1, CreatedAt: &now},
		{Id: 2, ChannelId: "channel2", ChannelType: 1, CreatedAt: &now},
		{Id: 3, ChannelId: "channel3", ChannelType: 2, CreatedAt: &now},
	}
	cache.BatchSetChannelInfos(channelInfos)

	// 获取缓存统计
	stats := cache.GetCacheStats()
	assert.Equal(t, 3, stats["channel_info_cache_len"])
	assert.Equal(t, 1000, stats["channel_info_cache_max"])
	assert.Equal(t, 600.0, stats["cache_ttl_seconds"]) // 10分钟 = 600秒

	// 测试其他统计方法
	assert.Equal(t, 3, cache.GetCacheSize())
	assert.Equal(t, 1000, cache.GetMaxCacheSize())
	assert.Equal(t, 10*time.Minute, cache.GetCacheTTL())
}

func TestChannelInfoCacheClear(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	// 添加一些数据
	now := time.Now()
	channelInfo := wkdb.ChannelInfo{
		Id:          1,
		ChannelId:   "channel1",
		ChannelType: 1,
		CreatedAt:   &now,
	}
	cache.SetChannelInfo(channelInfo)

	// 验证数据存在
	_, found := cache.GetChannelInfo("channel1", 1)
	assert.True(t, found)

	// 清空缓存
	cache.ClearCache()

	// 验证缓存已清空
	_, found = cache.GetChannelInfo("channel1", 1)
	assert.False(t, found)

	stats := cache.GetCacheStats()
	assert.Equal(t, 0, stats["channel_info_cache_len"])
}

func TestChannelInfoCacheExpiration(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	// 设置较短的TTL用于测试
	cache.SetCacheTTL(100 * time.Millisecond)

	now := time.Now()
	channelInfo := wkdb.ChannelInfo{
		Id:          1,
		ChannelId:   "channel1",
		ChannelType: 1,
		CreatedAt:   &now,
	}

	// 设置缓存
	cache.SetChannelInfo(channelInfo)

	// 立即获取应该成功
	_, found := cache.GetChannelInfo("channel1", 1)
	assert.True(t, found)

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 过期后获取应该失败
	_, found = cache.GetChannelInfo("channel1", 1)
	assert.False(t, found)
}

func TestChannelInfoCacheRemoveExpiredItems(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	// 设置较短的TTL
	cache.SetCacheTTL(50 * time.Millisecond)

	now := time.Now()
	channelInfos := []wkdb.ChannelInfo{
		{Id: 1, ChannelId: "channel1", ChannelType: 1, CreatedAt: &now},
		{Id: 2, ChannelId: "channel2", ChannelType: 1, CreatedAt: &now},
	}

	// 批量设置缓存
	cache.BatchSetChannelInfos(channelInfos)

	// 验证缓存大小
	assert.Equal(t, 2, cache.GetCacheSize())

	// 等待过期
	time.Sleep(100 * time.Millisecond)

	// 清理过期项
	removedCount := cache.RemoveExpiredItems()
	assert.Equal(t, 2, removedCount)
	assert.Equal(t, 0, cache.GetCacheSize())
}

func TestChannelInfoCacheWarmUp(t *testing.T) {
	cache := wkdb.NewChannelInfoCache(1000)

	now := time.Now()
	channelInfos := []wkdb.ChannelInfo{
		{Id: 1, ChannelId: "channel1", ChannelType: 1, CreatedAt: &now},
		{Id: 2, ChannelId: "channel2", ChannelType: 1, CreatedAt: &now},
		{Id: 3, ChannelId: "channel3", ChannelType: 2, CreatedAt: &now},
	}

	// 预热缓存
	cache.WarmUpCache(channelInfos)

	// 验证所有频道信息都已缓存
	assert.Equal(t, 3, cache.GetCacheSize())

	for _, channelInfo := range channelInfos {
		cached, found := cache.GetChannelInfo(channelInfo.ChannelId, channelInfo.ChannelType)
		assert.True(t, found)
		assert.Equal(t, channelInfo.Id, cached.Id)
	}
}

// TestChannelInfoCacheIntegration 测试数据库操作与缓存的集成
func TestChannelInfoCacheIntegration(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	now := time.Now()
	channelInfo := wkdb.ChannelInfo{
		ChannelId:   "test_channel",
		ChannelType: 1,
		Ban:         false,
		Large:       true,
		Disband:     false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// 测试添加频道（应该自动缓存）
	id, err := d.AddChannel(channelInfo)
	assert.NoError(t, err)
	assert.Greater(t, id, uint64(0))

	// 第一次获取（应该从缓存获取）
	start1 := time.Now()
	retrieved1, err := d.GetChannel("test_channel", 1)
	duration1 := time.Since(start1)
	assert.NoError(t, err)
	assert.Equal(t, channelInfo.ChannelId, retrieved1.ChannelId)
	assert.Equal(t, channelInfo.Large, retrieved1.Large)

	// 第二次获取（应该从缓存获取，更快）
	start2 := time.Now()
	retrieved2, err := d.GetChannel("test_channel", 1)
	duration2 := time.Since(start2)
	assert.NoError(t, err)
	assert.Equal(t, retrieved1.Id, retrieved2.Id)

	t.Logf("First get (cache miss): %v", duration1)
	t.Logf("Second get (cache hit): %v", duration2)

	// 测试更新频道（应该更新缓存）
	channelInfo.Ban = true
	channelInfo.Large = false
	err = d.UpdateChannel(channelInfo)
	assert.NoError(t, err)

	// 获取更新后的频道信息（应该从缓存获取更新后的数据）
	updated, err := d.GetChannel("test_channel", 1)
	assert.NoError(t, err)
	assert.True(t, updated.Ban)
	assert.False(t, updated.Large)

	// 测试删除频道（应该使缓存失效）
	err = d.DeleteChannel("test_channel", 1)
	assert.NoError(t, err)

	// 删除后获取应该返回空
	deleted, err := d.GetChannel("test_channel", 1)
	assert.NoError(t, err)
	assert.True(t, wkdb.IsEmptyChannelInfo(deleted))
}

// TestChannelInfoCachePerformance 测试缓存性能
func TestChannelInfoCachePerformance(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	now := time.Now()

	// 创建多个频道
	channelCount := 50
	for i := 0; i < channelCount; i++ {
		channelInfo := wkdb.ChannelInfo{
			ChannelId:   fmt.Sprintf("channel_%d", i),
			ChannelType: 1,
			Ban:         i%2 == 0,
			Large:       i%3 == 0,
			Disband:     false,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		}

		_, err := d.AddChannel(channelInfo)
		assert.NoError(t, err)
	}

	// 测试批量查询性能（第一次，缓存未命中）
	start1 := time.Now()
	for i := 0; i < channelCount; i++ {
		_, err := d.GetChannel(fmt.Sprintf("channel_%d", i), 1)
		assert.NoError(t, err)
	}
	duration1 := time.Since(start1)

	// 测试批量查询性能（第二次，缓存命中）
	start2 := time.Now()
	for i := 0; i < channelCount; i++ {
		_, err := d.GetChannel(fmt.Sprintf("channel_%d", i), 1)
		assert.NoError(t, err)
	}
	duration2 := time.Since(start2)

	t.Logf("First batch query (%d channels, cache miss): %v", channelCount, duration1)
	t.Logf("Second batch query (%d channels, cache hit): %v", channelCount, duration2)

	// 缓存命中应该更快
	if duration2 < duration1 {
		speedup := float64(duration1) / float64(duration2)
		t.Logf("Cache hit is %.2fx faster", speedup)
	}
}

// BenchmarkChannelInfoCache 基准测试：缓存性能
func BenchmarkChannelInfoCache(b *testing.B) {
	cache := wkdb.NewChannelInfoCache(10000)

	now := time.Now()
	channelInfo := wkdb.ChannelInfo{
		Id:          1,
		ChannelId:   "benchmark_channel",
		ChannelType: 1,
		Ban:         false,
		Large:       true,
		Disband:     false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// 设置缓存
	cache.SetChannelInfo(channelInfo)

	b.Run("CacheGet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, found := cache.GetChannelInfo("benchmark_channel", 1)
			if !found {
				b.Fatal("cache miss")
			}
		}
	})

	b.Run("CacheSet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.SetChannelInfo(channelInfo)
		}
	})

	b.Run("CacheUpdate", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.UpdateChannelInfo(channelInfo)
		}
	})
}
