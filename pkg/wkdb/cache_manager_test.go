package wkdb_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestCacheManager(t *testing.T) {
	// 创建各种缓存
	permissionCache := wkdb.NewPermissionCache(100)
	conversationCache := wkdb.NewConversationCache(100)
	channelInfoCache := wkdb.NewChannelInfoCache(100)
	clusterConfigCache := wkdb.NewChannelClusterConfigCache(100)
	deviceCache := wkdb.NewDeviceCache(100)

	// 创建缓存管理器
	cacheManager := wkdb.NewCacheManager(
		permissionCache,
		conversationCache,
		channelInfoCache,
		clusterConfigCache,
		deviceCache,
	)

	// 测试启动和停止
	cacheManager.Start()
	defer cacheManager.Stop()

	// 添加一些测试数据
	permissionCache.SetDenylistExists("channel1", 1, "user1", true)

	device := wkdb.Device{
		Id:         1,
		Uid:        "user1",
		DeviceFlag: 123,
		Token:      "token1",
	}
	deviceCache.SetDevice(device)

	// 测试获取缓存统计
	stats := cacheManager.GetCacheStats()
	assert.NotEmpty(t, stats)
	assert.Contains(t, stats, "permission_total_cache_len")
	assert.Contains(t, stats, "device_device_cache_len")

	// 测试获取总缓存大小
	totalSize := cacheManager.GetTotalCacheSize()
	assert.Greater(t, totalSize, 0)

	// 测试获取缓存利用率
	utilization := cacheManager.GetCacheUtilization()
	assert.GreaterOrEqual(t, utilization, 0.0)
	assert.LessOrEqual(t, utilization, 100.0)

	// 测试强制清理
	cacheManager.ForceCleanup()

	// 测试清空所有缓存
	cacheManager.ClearAllCaches()

	// 验证缓存已清空
	totalSizeAfterClear := cacheManager.GetTotalCacheSize()
	assert.Equal(t, 0, totalSizeAfterClear)
}

func TestCacheManagerExpiredItemsCleanup(t *testing.T) {
	// 创建缓存
	permissionCache := wkdb.NewPermissionCache(100)
	deviceCache := wkdb.NewDeviceCache(100)

	// 设置较短的TTL
	permissionCache.SetCacheTTL(50 * time.Millisecond)
	deviceCache.SetCacheTTL(50 * time.Millisecond)

	// 创建缓存管理器
	cacheManager := wkdb.NewCacheManager(
		permissionCache,
		nil, // conversationCache
		nil, // channelInfoCache
		nil, // clusterConfigCache
		deviceCache,
	)

	// 设置较短的清理间隔
	cacheManager.SetCleanupInterval(100 * time.Millisecond)

	// 启动缓存管理器
	cacheManager.Start()
	defer cacheManager.Stop()

	// 添加一些测试数据
	permissionCache.SetDenylistExists("channel1", 1, "user1", true)

	device := wkdb.Device{
		Id:         1,
		Uid:        "user1",
		DeviceFlag: 123,
		Token:      "token1",
	}
	deviceCache.SetDevice(device)

	// 验证数据存在
	initialSize := cacheManager.GetTotalCacheSize()
	assert.Greater(t, initialSize, 0)

	// 等待数据过期和清理
	time.Sleep(200 * time.Millisecond)

	// 验证过期数据被清理
	finalSize := cacheManager.GetTotalCacheSize()
	assert.LessOrEqual(t, finalSize, initialSize)
}

func TestCacheManagerWarmup(t *testing.T) {
	// 创建缓存
	channelInfoCache := wkdb.NewChannelInfoCache(100)
	clusterConfigCache := wkdb.NewChannelClusterConfigCache(100)
	deviceCache := wkdb.NewDeviceCache(100)

	// 创建缓存管理器
	cacheManager := wkdb.NewCacheManager(
		nil, // permissionCache
		nil, // conversationCache
		channelInfoCache,
		clusterConfigCache,
		deviceCache,
	)

	// 准备预热数据
	warmupData := &wkdb.CacheWarmupData{
		ChannelInfoData: []wkdb.ChannelInfo{
			{ChannelId: "channel1", ChannelType: 1},
			{ChannelId: "channel2", ChannelType: 1},
		},
		ClusterConfigData: []wkdb.ChannelClusterConfig{
			{ChannelId: "channel1", ChannelType: 1, LeaderId: 1},
			{ChannelId: "channel2", ChannelType: 1, LeaderId: 2},
		},
		DeviceData: []wkdb.Device{
			{Id: 1, Uid: "user1", DeviceFlag: 123},
			{Id: 2, Uid: "user2", DeviceFlag: 456},
		},
	}

	// 执行预热
	cacheManager.WarmUpAllCaches(warmupData)

	// 验证数据已被预热
	totalSize := cacheManager.GetTotalCacheSize()
	assert.Greater(t, totalSize, 0)

	// 验证具体数据
	channelInfo, found := channelInfoCache.GetChannelInfo("channel1", 1)
	assert.True(t, found)
	assert.Equal(t, "channel1", channelInfo.ChannelId)

	clusterConfig, found := clusterConfigCache.GetChannelClusterConfig("channel1", 1)
	assert.True(t, found)
	assert.Equal(t, uint64(1), clusterConfig.LeaderId)

	device, found := deviceCache.GetDevice("user1", 123)
	assert.True(t, found)
	assert.Equal(t, uint64(1), device.Id)
}

func TestCacheManagerReporting(t *testing.T) {
	// 创建缓存
	permissionCache := wkdb.NewPermissionCache(100)
	deviceCache := wkdb.NewDeviceCache(100)

	// 创建缓存管理器
	cacheManager := wkdb.NewCacheManager(
		permissionCache,
		nil, // conversationCache
		nil, // channelInfoCache
		nil, // clusterConfigCache
		deviceCache,
	)

	// 添加一些测试数据
	permissionCache.SetDenylistExists("channel1", 1, "user1", true)
	permissionCache.SetSubscriberExists("channel1", 1, "user1", true)

	device := wkdb.Device{
		Id:         1,
		Uid:        "user1",
		DeviceFlag: 123,
		Token:      "token1",
	}
	deviceCache.SetDevice(device)

	// 测试日志报告（这里只是确保不会崩溃）
	cacheManager.LogCacheReport()

	// 测试获取统计信息
	stats := cacheManager.GetCacheStats()
	assert.NotEmpty(t, stats)

	// 验证权限缓存统计
	assert.Contains(t, stats, "permission_total_cache_len")
	assert.Equal(t, 2, stats["permission_total_cache_len"]) // denylist + subscriber

	// 验证设备缓存统计
	assert.Contains(t, stats, "device_device_cache_len")
	assert.Equal(t, 1, stats["device_device_cache_len"])

	// 验证清理间隔
	assert.Contains(t, stats, "cleanup_interval_seconds")
	assert.Equal(t, 600.0, stats["cleanup_interval_seconds"]) // 10分钟 = 600秒
}

func TestCacheManagerIntegration(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	// 获取缓存管理器
	cacheManager := d.GetCacheManager()
	assert.NotNil(t, cacheManager)

	// 执行一些数据库操作
	channelId := "test_channel"
	channelType := uint8(1)
	uid := "test_user"

	// 添加权限数据
	members := []wkdb.Member{{Uid: uid}}
	err = d.AddDenylist(channelId, channelType, members)
	assert.NoError(t, err)

	// 添加设备数据
	device := wkdb.Device{
		Id:         1,
		Uid:        uid,
		DeviceFlag: 123,
		Token:      "test_token",
	}
	err = d.AddDevice(device)
	assert.NoError(t, err)

	// 执行查询操作（这些会填充缓存）
	_, err = d.ExistDenylist(channelId, channelType, uid)
	assert.NoError(t, err)

	_, err = d.GetDevice(uid, 123)
	assert.NoError(t, err)

	// 验证缓存中有数据
	totalSize := cacheManager.GetTotalCacheSize()
	assert.Greater(t, totalSize, 0)

	// 测试强制清理
	cacheManager.ForceCleanup()

	// 测试缓存报告
	cacheManager.LogCacheReport()

	// 测试清空所有缓存
	cacheManager.ClearAllCaches()

	// 验证缓存已清空
	totalSizeAfterClear := cacheManager.GetTotalCacheSize()
	assert.Equal(t, 0, totalSizeAfterClear)
}

func TestCacheManagerCleanupInterval(t *testing.T) {
	// 创建缓存
	permissionCache := wkdb.NewPermissionCache(100)

	// 创建缓存管理器
	cacheManager := wkdb.NewCacheManager(
		permissionCache,
		nil, nil, nil, nil,
	)

	// 测试设置清理间隔
	originalInterval := 10 * time.Minute
	newInterval := 5 * time.Minute

	// 验证默认间隔
	stats := cacheManager.GetCacheStats()
	assert.Equal(t, originalInterval.Seconds(), stats["cleanup_interval_seconds"])

	// 设置新的清理间隔
	cacheManager.SetCleanupInterval(newInterval)

	// 验证新间隔
	stats = cacheManager.GetCacheStats()
	assert.Equal(t, newInterval.Seconds(), stats["cleanup_interval_seconds"])
}

// BenchmarkCacheManagerOverhead 测试缓存管理器的开销
func BenchmarkCacheManagerOverhead(b *testing.B) {
	// 创建缓存
	permissionCache := wkdb.NewPermissionCache(1000)
	deviceCache := wkdb.NewDeviceCache(1000)

	// 创建缓存管理器
	cacheManager := wkdb.NewCacheManager(
		permissionCache,
		nil, nil, nil,
		deviceCache,
	)

	// 添加一些数据
	for i := 0; i < 100; i++ {
		permissionCache.SetDenylistExists("channel", 1, fmt.Sprintf("user%d", i), true)
		deviceCache.SetDevice(wkdb.Device{
			Id:         uint64(i),
			Uid:        fmt.Sprintf("user%d", i),
			DeviceFlag: uint64(i),
		})
	}

	b.Run("GetCacheStats", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = cacheManager.GetCacheStats()
		}
	})

	b.Run("GetTotalCacheSize", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = cacheManager.GetTotalCacheSize()
		}
	})

	b.Run("ForceCleanup", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cacheManager.ForceCleanup()
		}
	})
}
