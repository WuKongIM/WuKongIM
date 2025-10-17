package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestPermissionCache(t *testing.T) {
	cache := wkdb.NewPermissionCache(1000)

	channelId := "test_channel"
	channelType := uint8(1)
	uid := "test_user"

	// 测试黑名单缓存
	t.Run("DenylistCache", func(t *testing.T) {
		// 设置黑名单缓存
		cache.SetDenylistExists(channelId, channelType, uid, true)

		// 获取黑名单缓存
		exists, found := cache.GetDenylistExists(channelId, channelType, uid)
		assert.True(t, found)
		assert.True(t, exists)
	})

	// 测试订阅者缓存
	t.Run("SubscriberCache", func(t *testing.T) {
		// 设置订阅者缓存
		cache.SetSubscriberExists(channelId, channelType, uid, true)

		// 获取订阅者缓存
		exists, found := cache.GetSubscriberExists(channelId, channelType, uid)
		assert.True(t, found)
		assert.True(t, exists)
	})

	// 测试白名单缓存
	t.Run("AllowlistCache", func(t *testing.T) {
		// 设置白名单缓存
		cache.SetAllowlistExists(channelId, channelType, uid, true)

		// 获取白名单缓存
		exists, found := cache.GetAllowlistExists(channelId, channelType, uid)
		assert.True(t, found)
		assert.True(t, exists)

		// 设置 HasAllowlist 缓存
		cache.SetHasAllowlist(channelId, channelType, true)

		// 获取 HasAllowlist 缓存
		hasAllowlist, found := cache.GetHasAllowlist(channelId, channelType)
		assert.True(t, found)
		assert.True(t, hasAllowlist)
	})
}

func TestPermissionCacheBatchOperations(t *testing.T) {
	cache := wkdb.NewPermissionCache(1000)

	channelId := "test_channel"
	channelType := uint8(1)
	uids := []string{"user1", "user2", "user3"}

	// 测试批量设置黑名单
	t.Run("BatchDenylist", func(t *testing.T) {
		cache.BatchSetDenylistExists(channelId, channelType, uids, true)

		for _, uid := range uids {
			exists, found := cache.GetDenylistExists(channelId, channelType, uid)
			assert.True(t, found)
			assert.True(t, exists)
		}
	})

	// 测试批量设置订阅者
	t.Run("BatchSubscriber", func(t *testing.T) {
		cache.BatchSetSubscriberExists(channelId, channelType, uids, true)

		for _, uid := range uids {
			exists, found := cache.GetSubscriberExists(channelId, channelType, uid)
			assert.True(t, found)
			assert.True(t, exists)
		}
	})

	// 测试批量设置白名单
	t.Run("BatchAllowlist", func(t *testing.T) {
		cache.BatchSetAllowlistExists(channelId, channelType, uids, true)

		for _, uid := range uids {
			exists, found := cache.GetAllowlistExists(channelId, channelType, uid)
			assert.True(t, found)
			assert.True(t, exists)
		}

		// 验证 HasAllowlist 也被设置为 true
		hasAllowlist, found := cache.GetHasAllowlist(channelId, channelType)
		assert.True(t, found)
		assert.True(t, hasAllowlist)
	})
}

func TestPermissionCacheInvalidation(t *testing.T) {
	cache := wkdb.NewPermissionCache(1000)

	channelId := "test_channel"
	channelType := uint8(1)
	uid := "test_user"

	// 设置所有类型的权限缓存
	cache.SetDenylistExists(channelId, channelType, uid, true)
	cache.SetSubscriberExists(channelId, channelType, uid, true)
	cache.SetAllowlistExists(channelId, channelType, uid, true)
	cache.SetHasAllowlist(channelId, channelType, true)

	// 验证缓存存在
	_, found1 := cache.GetDenylistExists(channelId, channelType, uid)
	_, found2 := cache.GetSubscriberExists(channelId, channelType, uid)
	_, found3 := cache.GetAllowlistExists(channelId, channelType, uid)
	_, found4 := cache.GetHasAllowlist(channelId, channelType)
	assert.True(t, found1)
	assert.True(t, found2)
	assert.True(t, found3)
	assert.True(t, found4)

	// 测试用户级失效
	t.Run("InvalidateUser", func(t *testing.T) {
		cache.InvalidateUser(channelId, channelType, uid)

		// 用户级权限应该失效
		_, found1 := cache.GetDenylistExists(channelId, channelType, uid)
		_, found2 := cache.GetSubscriberExists(channelId, channelType, uid)
		_, found3 := cache.GetAllowlistExists(channelId, channelType, uid)
		assert.False(t, found1)
		assert.False(t, found2)
		assert.False(t, found3)

		// 频道级权限应该仍然存在
		_, found4 := cache.GetHasAllowlist(channelId, channelType)
		assert.True(t, found4)
	})

	// 重新设置缓存
	cache.SetDenylistExists(channelId, channelType, uid, true)
	cache.SetSubscriberExists(channelId, channelType, uid, true)
	cache.SetAllowlistExists(channelId, channelType, uid, true)

	// 测试频道级失效
	t.Run("InvalidateChannel", func(t *testing.T) {
		cache.InvalidateChannel(channelId, channelType)

		// 所有权限都应该失效
		_, found1 := cache.GetDenylistExists(channelId, channelType, uid)
		_, found2 := cache.GetSubscriberExists(channelId, channelType, uid)
		_, found3 := cache.GetAllowlistExists(channelId, channelType, uid)
		_, found4 := cache.GetHasAllowlist(channelId, channelType)
		assert.False(t, found1)
		assert.False(t, found2)
		assert.False(t, found3)
		assert.False(t, found4)
	})
}

func TestPermissionCacheStats(t *testing.T) {
	cache := wkdb.NewPermissionCache(1000)

	channelId := "test_channel"
	channelType := uint8(1)
	uids := []string{"user1", "user2"}

	// 添加不同类型的权限缓存
	cache.BatchSetDenylistExists(channelId, channelType, uids, true)
	cache.BatchSetSubscriberExists(channelId, channelType, uids, true)
	cache.BatchSetAllowlistExists(channelId, channelType, uids, true)

	// 获取缓存统计
	stats := cache.GetCacheStats()
	assert.Equal(t, 7, stats["total_cache_len"])      // 2+2+2+1 = 7
	assert.Equal(t, 2, stats["denylist_cache_len"])   // 2个黑名单
	assert.Equal(t, 2, stats["subscriber_cache_len"]) // 2个订阅者
	assert.Equal(t, 2, stats["allowlist_cache_len"])  // 2个白名单
	assert.Equal(t, 1, stats["has_allowlist_cache_len"]) // 1个HasAllowlist
	assert.Equal(t, 1000, stats["cache_max"])
	assert.Equal(t, 300.0, stats["cache_ttl_seconds"]) // 5分钟 = 300秒

	// 测试其他统计方法
	assert.Equal(t, 7, cache.GetCacheSize())
	assert.Equal(t, 1000, cache.GetMaxCacheSize())
	assert.Equal(t, 5*time.Minute, cache.GetCacheTTL())
}

func TestPermissionCacheExpiration(t *testing.T) {
	cache := wkdb.NewPermissionCache(1000)
	
	// 设置较短的TTL用于测试
	cache.SetCacheTTL(100 * time.Millisecond)

	channelId := "test_channel"
	channelType := uint8(1)
	uid := "test_user"

	// 设置各种权限缓存
	cache.SetDenylistExists(channelId, channelType, uid, true)
	cache.SetSubscriberExists(channelId, channelType, uid, true)
	cache.SetAllowlistExists(channelId, channelType, uid, true)
	cache.SetHasAllowlist(channelId, channelType, true)

	// 立即获取应该成功
	_, found1 := cache.GetDenylistExists(channelId, channelType, uid)
	_, found2 := cache.GetSubscriberExists(channelId, channelType, uid)
	_, found3 := cache.GetAllowlistExists(channelId, channelType, uid)
	_, found4 := cache.GetHasAllowlist(channelId, channelType)
	assert.True(t, found1)
	assert.True(t, found2)
	assert.True(t, found3)
	assert.True(t, found4)

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 过期后获取应该失败
	_, found1 = cache.GetDenylistExists(channelId, channelType, uid)
	_, found2 = cache.GetSubscriberExists(channelId, channelType, uid)
	_, found3 = cache.GetAllowlistExists(channelId, channelType, uid)
	_, found4 = cache.GetHasAllowlist(channelId, channelType)
	assert.False(t, found1)
	assert.False(t, found2)
	assert.False(t, found3)
	assert.False(t, found4)
}

// TestPermissionCacheIntegration 测试数据库操作与统一缓存的集成
func TestPermissionCacheIntegration(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "test_channel"
	channelType := uint8(1)
	uid := "test_user"

	// 测试黑名单缓存集成
	t.Run("DenylistIntegration", func(t *testing.T) {
		// 添加到黑名单
		members := []wkdb.Member{{Uid: uid}}
		err := d.AddDenylist(channelId, channelType, members)
		assert.NoError(t, err)

		// 查询应该从缓存获取
		exists, err := d.ExistDenylist(channelId, channelType, uid)
		assert.NoError(t, err)
		assert.True(t, exists)
	})

	// 测试订阅者缓存集成
	t.Run("SubscriberIntegration", func(t *testing.T) {
		// 添加订阅者
		members := []wkdb.Member{{Uid: uid}}
		err := d.AddSubscribers(channelId, channelType, members)
		assert.NoError(t, err)

		// 查询应该从缓存获取
		exists, err := d.ExistSubscriber(channelId, channelType, uid)
		assert.NoError(t, err)
		assert.True(t, exists)
	})

	// 测试白名单缓存集成
	t.Run("AllowlistIntegration", func(t *testing.T) {
		// 添加到白名单
		members := []wkdb.Member{{Uid: uid}}
		err := d.AddAllowlist(channelId, channelType, members)
		assert.NoError(t, err)

		// 查询应该从缓存获取
		exists, err := d.ExistAllowlist(channelId, channelType, uid)
		assert.NoError(t, err)
		assert.True(t, exists)

		hasAllowlist, err := d.HasAllowlist(channelId, channelType)
		assert.NoError(t, err)
		assert.True(t, hasAllowlist)
	})
}

// BenchmarkPermissionCache 基准测试：统一权限缓存性能
func BenchmarkPermissionCache(b *testing.B) {
	cache := wkdb.NewPermissionCache(10000)
	
	channelId := "benchmark_channel"
	channelType := uint8(1)
	uid := "benchmark_user"

	// 设置各种权限缓存
	cache.SetDenylistExists(channelId, channelType, uid, true)
	cache.SetSubscriberExists(channelId, channelType, uid, true)
	cache.SetAllowlistExists(channelId, channelType, uid, true)
	cache.SetHasAllowlist(channelId, channelType, true)

	b.Run("DenylistGet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, found := cache.GetDenylistExists(channelId, channelType, uid)
			if !found {
				b.Fatal("cache miss")
			}
		}
	})

	b.Run("SubscriberGet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, found := cache.GetSubscriberExists(channelId, channelType, uid)
			if !found {
				b.Fatal("cache miss")
			}
		}
	})

	b.Run("AllowlistGet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, found := cache.GetAllowlistExists(channelId, channelType, uid)
			if !found {
				b.Fatal("cache miss")
			}
		}
	})

	b.Run("HasAllowlistGet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, found := cache.GetHasAllowlist(channelId, channelType)
			if !found {
				b.Fatal("cache miss")
			}
		}
	})
}
