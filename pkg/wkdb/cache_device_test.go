package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestDeviceCache(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)

	// 测试基本缓存操作
	uid := "test_user"
	deviceFlag := uint64(123)
	
	device := wkdb.Device{
		Id:          1,
		Uid:         uid,
		Token:       "test_token",
		DeviceFlag:  deviceFlag,
		DeviceLevel: 1,
		CreatedAt:   &time.Time{},
	}

	// 设置缓存
	cache.SetDevice(device)

	// 获取缓存
	cachedDevice, found := cache.GetDevice(uid, deviceFlag)
	assert.True(t, found)
	assert.Equal(t, device.Id, cachedDevice.Id)
	assert.Equal(t, device.Uid, cachedDevice.Uid)
	assert.Equal(t, device.Token, cachedDevice.Token)
	assert.Equal(t, device.DeviceFlag, cachedDevice.DeviceFlag)
	assert.Equal(t, device.DeviceLevel, cachedDevice.DeviceLevel)

	// 测试缓存失效
	cache.InvalidateDevice(uid, deviceFlag)
	_, found = cache.GetDevice(uid, deviceFlag)
	assert.False(t, found)
}

func TestDeviceCacheBatchOperations(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)

	devices := []wkdb.Device{
		{
			Id:          1,
			Uid:         "user1",
			Token:       "token1",
			DeviceFlag:  100,
			DeviceLevel: 1,
		},
		{
			Id:          2,
			Uid:         "user1",
			Token:       "token2",
			DeviceFlag:  200,
			DeviceLevel: 2,
		},
		{
			Id:          3,
			Uid:         "user2",
			Token:       "token3",
			DeviceFlag:  300,
			DeviceLevel: 1,
		},
	}

	// 批量设置设备
	cache.BatchSetDevices(devices)

	// 验证所有设备都已缓存
	for _, device := range devices {
		cachedDevice, found := cache.GetDevice(device.Uid, device.DeviceFlag)
		assert.True(t, found)
		assert.Equal(t, device.Id, cachedDevice.Id)
		assert.Equal(t, device.Token, cachedDevice.Token)
	}

	// 测试按用户失效
	cache.InvalidateUserDevices("user1")

	// 验证 user1 的设备已失效
	_, found1 := cache.GetDevice("user1", 100)
	_, found2 := cache.GetDevice("user1", 200)
	assert.False(t, found1)
	assert.False(t, found2)

	// 验证 user2 的设备仍然存在
	_, found3 := cache.GetDevice("user2", 300)
	assert.True(t, found3)
}

func TestDeviceCacheByDeviceId(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)

	devices := []wkdb.Device{
		{Id: 1, Uid: "user1", DeviceFlag: 100, Token: "token1"},
		{Id: 2, Uid: "user2", DeviceFlag: 200, Token: "token2"},
		{Id: 3, Uid: "user3", DeviceFlag: 300, Token: "token3"},
	}

	cache.BatchSetDevices(devices)

	// 测试按设备ID失效
	cache.InvalidateByDeviceId(2)

	// 验证设备ID=2的设备已失效
	_, found2 := cache.GetDevice("user2", 200)
	assert.False(t, found2)

	// 验证其他设备仍然存在
	_, found1 := cache.GetDevice("user1", 100)
	_, found3 := cache.GetDevice("user3", 300)
	assert.True(t, found1)
	assert.True(t, found3)
}

func TestDeviceCacheStats(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)

	devices := []wkdb.Device{
		{Id: 1, Uid: "user1", DeviceFlag: 100},
		{Id: 2, Uid: "user2", DeviceFlag: 200},
		{Id: 3, Uid: "user3", DeviceFlag: 300},
	}

	cache.BatchSetDevices(devices)

	// 获取缓存统计
	stats := cache.GetCacheStats()
	assert.Equal(t, 3, stats["device_cache_len"])
	assert.Equal(t, 1000, stats["device_cache_max"])
	assert.Equal(t, 300.0, stats["cache_ttl_seconds"]) // 5分钟 = 300秒

	// 测试其他统计方法
	assert.Equal(t, 3, cache.GetCacheSize())
	assert.Equal(t, 1000, cache.GetMaxCacheSize())
	assert.Equal(t, 5*time.Minute, cache.GetCacheTTL())
}

func TestDeviceCacheExpiration(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)
	
	// 设置较短的TTL用于测试
	cache.SetCacheTTL(100 * time.Millisecond)

	device := wkdb.Device{
		Id:         1,
		Uid:        "test_user",
		DeviceFlag: 123,
		Token:      "test_token",
	}

	// 设置缓存
	cache.SetDevice(device)

	// 立即获取应该成功
	_, found := cache.GetDevice("test_user", 123)
	assert.True(t, found)

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 过期后获取应该失败
	_, found = cache.GetDevice("test_user", 123)
	assert.False(t, found)
}

func TestDeviceCacheUserOperations(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)

	devices := []wkdb.Device{
		{Id: 1, Uid: "user1", DeviceFlag: 100, DeviceLevel: 1},
		{Id: 2, Uid: "user1", DeviceFlag: 200, DeviceLevel: 2},
		{Id: 3, Uid: "user2", DeviceFlag: 300, DeviceLevel: 1},
	}

	cache.BatchSetDevices(devices)

	// 测试获取用户设备数量
	count1 := cache.GetUserDeviceCount("user1")
	count2 := cache.GetUserDeviceCount("user2")
	assert.Equal(t, 2, count1)
	assert.Equal(t, 1, count2)

	// 测试获取用户设备列表
	userDevices := cache.GetDevicesByUser("user1")
	assert.Len(t, userDevices, 2)

	// 测试获取缓存用户列表
	users := cache.GetCachedUsers()
	assert.Contains(t, users, "user1")
	assert.Contains(t, users, "user2")
}

func TestDeviceCacheUpdateOperations(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)

	device := wkdb.Device{
		Id:          1,
		Uid:         "test_user",
		DeviceFlag:  123,
		Token:       "old_token",
		DeviceLevel: 1,
	}

	cache.SetDevice(device)

	// 测试更新Token
	cache.UpdateDeviceToken("test_user", 123, "new_token")
	cachedDevice, found := cache.GetDevice("test_user", 123)
	assert.True(t, found)
	assert.Equal(t, "new_token", cachedDevice.Token)

	// 测试更新设备级别
	cache.UpdateDeviceLevel("test_user", 123, 2)
	cachedDevice, found = cache.GetDevice("test_user", 123)
	assert.True(t, found)
	assert.Equal(t, uint8(2), cachedDevice.DeviceLevel)
}

func TestDeviceCacheQueryOperations(t *testing.T) {
	cache := wkdb.NewDeviceCache(1000)

	devices := []wkdb.Device{
		{Id: 1, Uid: "user1", DeviceFlag: 100, DeviceLevel: 1, Token: "token1"},
		{Id: 2, Uid: "user2", DeviceFlag: 200, DeviceLevel: 2, Token: "token2"},
		{Id: 3, Uid: "user3", DeviceFlag: 300, DeviceLevel: 1, Token: "token3"},
	}

	cache.BatchSetDevices(devices)

	// 测试按级别查询
	level1Devices := cache.GetDevicesByLevel(1)
	assert.Len(t, level1Devices, 2)

	level2Devices := cache.GetDevicesByLevel(2)
	assert.Len(t, level2Devices, 1)

	// 测试按Token查询
	tokenDevices := cache.GetDevicesByToken("token2")
	assert.Len(t, tokenDevices, 1)
	assert.Equal(t, "user2", tokenDevices[0].Uid)
}

// TestDeviceCacheIntegration 测试数据库操作与缓存的集成
func TestDeviceCacheIntegration(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test_user"
	deviceFlag := uint64(123)
	
	device := wkdb.Device{
		Id:          1,
		Uid:         uid,
		Token:       "test_token",
		DeviceFlag:  deviceFlag,
		DeviceLevel: 1,
		CreatedAt:   &time.Time{},
	}

	// 添加设备
	err = d.AddDevice(device)
	assert.NoError(t, err)

	// 第一次查询（应该从缓存获取）
	start1 := time.Now()
	device1, err := d.GetDevice(uid, deviceFlag)
	duration1 := time.Since(start1)
	assert.NoError(t, err)
	assert.Equal(t, device.Id, device1.Id)
	assert.Equal(t, device.Token, device1.Token)

	// 第二次查询（应该从缓存获取，更快）
	start2 := time.Now()
	device2, err := d.GetDevice(uid, deviceFlag)
	duration2 := time.Since(start2)
	assert.NoError(t, err)
	assert.Equal(t, device.Id, device2.Id)
	assert.Equal(t, device.Token, device2.Token)

	t.Logf("First query (cache miss): %v", duration1)
	t.Logf("Second query (cache hit): %v", duration2)

	// 更新设备
	device.Token = "updated_token"
	device.DeviceLevel = 2
	err = d.UpdateDevice(device)
	assert.NoError(t, err)

	// 查询应该返回更新后的设备（缓存已更新）
	device3, err := d.GetDevice(uid, deviceFlag)
	assert.NoError(t, err)
	assert.Equal(t, "updated_token", device3.Token)
	assert.Equal(t, uint8(2), device3.DeviceLevel)
}

// BenchmarkDeviceCache 基准测试：设备缓存性能
func BenchmarkDeviceCache(b *testing.B) {
	cache := wkdb.NewDeviceCache(10000)
	
	device := wkdb.Device{
		Id:         1,
		Uid:        "benchmark_user",
		DeviceFlag: 123,
		Token:      "benchmark_token",
	}

	// 设置缓存
	cache.SetDevice(device)

	b.Run("CacheGet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, found := cache.GetDevice("benchmark_user", 123)
			if !found {
				b.Fatal("cache miss")
			}
		}
	})

	b.Run("CacheSet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.SetDevice(device)
		}
	})

	b.Run("CacheInvalidate", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.InvalidateDevice("benchmark_user", 123)
			cache.SetDevice(device) // 重新设置以便下次测试
		}
	})
}
