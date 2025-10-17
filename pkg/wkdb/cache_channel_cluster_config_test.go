package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestChannelClusterConfigCache(t *testing.T) {
	cache := wkdb.NewChannelClusterConfigCache(1000)

	// 测试基本缓存操作
	channelId := "test_channel"
	channelType := uint8(1)
	
	config := wkdb.ChannelClusterConfig{
		ChannelId:        channelId,
		ChannelType:      channelType,
		ReplicaMaxCount:  3,
		Replicas:         []uint64{1, 2, 3},
		Learners:         []uint64{4, 5},
		LeaderId:         1,
		Term:             10,
		ConfVersion:      100,
	}

	// 设置缓存
	cache.SetChannelClusterConfig(config)

	// 获取缓存
	cachedConfig, found := cache.GetChannelClusterConfig(channelId, channelType)
	assert.True(t, found)
	assert.Equal(t, config.ChannelId, cachedConfig.ChannelId)
	assert.Equal(t, config.ChannelType, cachedConfig.ChannelType)
	assert.Equal(t, config.LeaderId, cachedConfig.LeaderId)
	assert.Equal(t, config.ConfVersion, cachedConfig.ConfVersion)

	// 测试缓存失效
	cache.InvalidateChannelClusterConfig(channelId, channelType)
	_, found = cache.GetChannelClusterConfig(channelId, channelType)
	assert.False(t, found)
}

func TestChannelClusterConfigCacheBatchOperations(t *testing.T) {
	cache := wkdb.NewChannelClusterConfigCache(1000)

	configs := []wkdb.ChannelClusterConfig{
		{
			ChannelId:   "channel1",
			ChannelType: 1,
			LeaderId:    1,
			ConfVersion: 100,
		},
		{
			ChannelId:   "channel2",
			ChannelType: 1,
			LeaderId:    2,
			ConfVersion: 200,
		},
		{
			ChannelId:   "channel3",
			ChannelType: 2,
			LeaderId:    1,
			ConfVersion: 300,
		},
	}

	// 批量设置配置
	cache.BatchSetChannelClusterConfigs(configs)

	// 验证所有配置都已缓存
	for _, config := range configs {
		cachedConfig, found := cache.GetChannelClusterConfig(config.ChannelId, config.ChannelType)
		assert.True(t, found)
		assert.Equal(t, config.ChannelId, cachedConfig.ChannelId)
		assert.Equal(t, config.LeaderId, cachedConfig.LeaderId)
	}

	// 测试按 LeaderId 失效
	cache.InvalidateByLeaderId(1)

	// 验证 LeaderId=1 的配置已失效
	_, found1 := cache.GetChannelClusterConfig("channel1", 1)
	_, found3 := cache.GetChannelClusterConfig("channel3", 2)
	assert.False(t, found1)
	assert.False(t, found3)

	// 验证 LeaderId=2 的配置仍然存在
	_, found2 := cache.GetChannelClusterConfig("channel2", 1)
	assert.True(t, found2)
}

func TestChannelClusterConfigCacheBySlotId(t *testing.T) {
	cache := wkdb.NewChannelClusterConfigCache(1000)

	// 模拟 channelSlotId 函数
	channelSlotIdFunc := func(channelId string) uint32 {
		switch channelId {
		case "channel1":
			return 100
		case "channel2":
			return 200
		case "channel3":
			return 100
		default:
			return 0
		}
	}

	configs := []wkdb.ChannelClusterConfig{
		{ChannelId: "channel1", ChannelType: 1, LeaderId: 1},
		{ChannelId: "channel2", ChannelType: 1, LeaderId: 2},
		{ChannelId: "channel3", ChannelType: 2, LeaderId: 3},
	}

	cache.BatchSetChannelClusterConfigs(configs)

	// 测试按 SlotId 获取配置
	slotConfigs := cache.GetConfigsBySlotId(100, channelSlotIdFunc)
	assert.Len(t, slotConfigs, 2) // channel1 和 channel3

	// 测试按 SlotId 失效
	cache.InvalidateBySlotId(100, channelSlotIdFunc)

	// 验证 SlotId=100 的配置已失效
	_, found1 := cache.GetChannelClusterConfig("channel1", 1)
	_, found3 := cache.GetChannelClusterConfig("channel3", 2)
	assert.False(t, found1)
	assert.False(t, found3)

	// 验证 SlotId=200 的配置仍然存在
	_, found2 := cache.GetChannelClusterConfig("channel2", 1)
	assert.True(t, found2)
}

func TestChannelClusterConfigCacheStats(t *testing.T) {
	cache := wkdb.NewChannelClusterConfigCache(1000)

	configs := []wkdb.ChannelClusterConfig{
		{ChannelId: "channel1", ChannelType: 1},
		{ChannelId: "channel2", ChannelType: 1},
		{ChannelId: "channel3", ChannelType: 2},
	}

	cache.BatchSetChannelClusterConfigs(configs)

	// 获取缓存统计
	stats := cache.GetCacheStats()
	assert.Equal(t, 3, stats["cluster_config_cache_len"])
	assert.Equal(t, 1000, stats["cluster_config_cache_max"])
	assert.Equal(t, 600.0, stats["cache_ttl_seconds"]) // 10分钟 = 600秒

	// 测试其他统计方法
	assert.Equal(t, 3, cache.GetCacheSize())
	assert.Equal(t, 1000, cache.GetMaxCacheSize())
	assert.Equal(t, 10*time.Minute, cache.GetCacheTTL())
}

func TestChannelClusterConfigCacheExpiration(t *testing.T) {
	cache := wkdb.NewChannelClusterConfigCache(1000)
	
	// 设置较短的TTL用于测试
	cache.SetCacheTTL(100 * time.Millisecond)

	config := wkdb.ChannelClusterConfig{
		ChannelId:   "test_channel",
		ChannelType: 1,
		LeaderId:    1,
	}

	// 设置缓存
	cache.SetChannelClusterConfig(config)

	// 立即获取应该成功
	_, found := cache.GetChannelClusterConfig("test_channel", 1)
	assert.True(t, found)

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 过期后获取应该失败
	_, found = cache.GetChannelClusterConfig("test_channel", 1)
	assert.False(t, found)
}

func TestChannelClusterConfigCacheVersionOperations(t *testing.T) {
	cache := wkdb.NewChannelClusterConfigCache(1000)

	config := wkdb.ChannelClusterConfig{
		ChannelId:   "test_channel",
		ChannelType: 1,
		ConfVersion: 100,
	}

	cache.SetChannelClusterConfig(config)

	// 测试获取配置版本
	version, found := cache.GetConfigVersion("test_channel", 1)
	assert.True(t, found)
	assert.Equal(t, uint64(100), version)

	// 测试更新配置版本
	cache.UpdateConfigVersion("test_channel", 1, 200)

	// 验证版本已更新
	version, found = cache.GetConfigVersion("test_channel", 1)
	assert.True(t, found)
	assert.Equal(t, uint64(200), version)

	// 验证完整配置也已更新
	cachedConfig, found := cache.GetChannelClusterConfig("test_channel", 1)
	assert.True(t, found)
	assert.Equal(t, uint64(200), cachedConfig.ConfVersion)
}

// TestChannelClusterConfigCacheIntegration 测试数据库操作与缓存的集成
func TestChannelClusterConfigCacheIntegration(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "test_channel"
	channelType := uint8(1)
	
	config := wkdb.ChannelClusterConfig{
		ChannelId:        channelId,
		ChannelType:      channelType,
		ReplicaMaxCount:  3,
		Replicas:         []uint64{1, 2, 3},
		LeaderId:         1,
		Term:             10,
		ConfVersion:      100,
	}

	// 保存配置
	err = d.SaveChannelClusterConfig(config)
	assert.NoError(t, err)

	// 第一次查询（应该从缓存获取）
	start1 := time.Now()
	config1, err := d.GetChannelClusterConfig(channelId, channelType)
	duration1 := time.Since(start1)
	assert.NoError(t, err)
	assert.Equal(t, config.ChannelId, config1.ChannelId)
	assert.Equal(t, config.LeaderId, config1.LeaderId)

	// 第二次查询（应该从缓存获取，更快）
	start2 := time.Now()
	config2, err := d.GetChannelClusterConfig(channelId, channelType)
	duration2 := time.Since(start2)
	assert.NoError(t, err)
	assert.Equal(t, config.ChannelId, config2.ChannelId)
	assert.Equal(t, config.LeaderId, config2.LeaderId)

	t.Logf("First query (cache miss): %v", duration1)
	t.Logf("Second query (cache hit): %v", duration2)

	// 更新配置
	config.LeaderId = 2
	config.ConfVersion = 200
	err = d.SaveChannelClusterConfig(config)
	assert.NoError(t, err)

	// 查询应该返回更新后的配置（缓存已更新）
	config3, err := d.GetChannelClusterConfig(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), config3.LeaderId)
	assert.Equal(t, uint64(200), config3.ConfVersion)
}

// BenchmarkChannelClusterConfigCache 基准测试：集群配置缓存性能
func BenchmarkChannelClusterConfigCache(b *testing.B) {
	cache := wkdb.NewChannelClusterConfigCache(10000)
	
	config := wkdb.ChannelClusterConfig{
		ChannelId:   "benchmark_channel",
		ChannelType: 1,
		LeaderId:    1,
		ConfVersion: 100,
	}

	// 设置缓存
	cache.SetChannelClusterConfig(config)

	b.Run("CacheGet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, found := cache.GetChannelClusterConfig("benchmark_channel", 1)
			if !found {
				b.Fatal("cache miss")
			}
		}
	})

	b.Run("CacheSet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.SetChannelClusterConfig(config)
		}
	})

	b.Run("CacheInvalidate", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.InvalidateChannelClusterConfig("benchmark_channel", 1)
			cache.SetChannelClusterConfig(config) // 重新设置以便下次测试
		}
	})
}
