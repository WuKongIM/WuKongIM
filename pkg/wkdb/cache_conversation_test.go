package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestConversationCache(t *testing.T) {
	cache := wkdb.NewConversationCache(1000)

	// 测试 GetLastConversations 缓存
	conversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1, UnreadCount: 5},
		{Id: 2, Uid: "user1", ChannelId: "channel2", ChannelType: 1, UnreadCount: 3},
	}

	// 设置缓存
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10, conversations)

	// 获取缓存
	cached, found := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.True(t, found)
	assert.Len(t, cached, 2)
	assert.Equal(t, uint64(1), cached[0].Id)
	assert.Equal(t, uint64(2), cached[1].Id)

	// 测试缓存失效
	cache.InvalidateUserConversations("user1")
	_, found = cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.False(t, found)
}

func TestCacheStats(t *testing.T) {
	cache := wkdb.NewConversationCache(1000)

	// 添加一些数据
	conversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1},
	}
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10, conversations)

	// 获取缓存统计
	stats := cache.GetCacheStats()
	assert.Equal(t, 1, stats["last_conversations_cache_len"])
	assert.Equal(t, 1000, stats["last_conversations_cache_max"])
	assert.Equal(t, 120.0, stats["cache_ttl_seconds"]) // 2分钟 = 120秒
}

func TestCacheClear(t *testing.T) {
	cache := wkdb.NewConversationCache(1000)

	// 添加一些数据
	conversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1},
	}
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10, conversations)

	// 验证数据存在
	_, found := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.True(t, found)

	// 清空缓存
	cache.ClearCache()

	// 验证缓存已清空
	_, found = cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.False(t, found)

	stats := cache.GetCacheStats()
	assert.Equal(t, 0, stats["last_conversations_cache_len"])
}

func TestCacheKeyGeneration(t *testing.T) {
	cache := wkdb.NewConversationCache(1000)

	conversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1},
	}

	// 测试不同参数生成不同的缓存键
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10, conversations)
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 20, conversations)   // 不同的limit
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 100, nil, 10, conversations) // 不同的updatedAt

	// 验证不同参数的缓存是独立的
	cached1, found1 := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.True(t, found1)
	assert.Len(t, cached1, 1)

	cached2, found2 := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 20)
	assert.True(t, found2)
	assert.Len(t, cached2, 1)

	cached3, found3 := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 100, nil, 10)
	assert.True(t, found3)
	assert.Len(t, cached3, 1)

	// 验证缓存统计
	stats := cache.GetCacheStats()
	assert.Equal(t, 3, stats["last_conversations_cache_len"])
}

func TestCacheWithExcludeChannelTypes(t *testing.T) {
	cache := wkdb.NewConversationCache(1000)

	conversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1},
	}

	// 测试带有 excludeChannelTypes 的缓存
	excludeTypes1 := []uint8{1, 2}
	excludeTypes2 := []uint8{2, 1} // 相同内容但顺序不同，应该生成相同的键

	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, excludeTypes1, 10, conversations)

	// 验证顺序不同但内容相同的 excludeChannelTypes 能命中缓存
	cached, found := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, excludeTypes2, 10)
	assert.True(t, found)
	assert.Len(t, cached, 1)

	// 验证不同的 excludeChannelTypes 不会命中缓存
	excludeTypes3 := []uint8{3, 4}
	_, found = cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, excludeTypes3, 10)
	assert.False(t, found)
}

func TestUpdateConversationsInCache(t *testing.T) {
	cache := wkdb.NewConversationCache(1000)

	// 初始会话数据
	originalConversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1, UnreadCount: 5},
		{Id: 2, Uid: "user1", ChannelId: "channel2", ChannelType: 1, UnreadCount: 3},
		{Id: 3, Uid: "user1", ChannelId: "channel3", ChannelType: 1, UnreadCount: 1},
	}

	// 设置初始缓存
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10, originalConversations)

	// 验证初始缓存
	cached, found := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.True(t, found)
	assert.Len(t, cached, 3)
	assert.Equal(t, uint32(5), cached[0].UnreadCount)
	assert.Equal(t, uint32(3), cached[1].UnreadCount)

	// 更新部分会话数据
	updatedConversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1, UnreadCount: 10}, // 更新未读数
		{Id: 2, Uid: "user1", ChannelId: "channel2", ChannelType: 1, UnreadCount: 8},  // 更新未读数
	}

	// 智能更新缓存
	cache.UpdateConversationsInCache(updatedConversations)

	// 验证缓存已更新
	cachedAfterUpdate, found := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.True(t, found)
	assert.Len(t, cachedAfterUpdate, 3)

	// 验证更新的会话
	assert.Equal(t, uint32(10), cachedAfterUpdate[0].UnreadCount) // 已更新
	assert.Equal(t, uint32(8), cachedAfterUpdate[1].UnreadCount)  // 已更新
	assert.Equal(t, uint32(1), cachedAfterUpdate[2].UnreadCount)  // 未更新，保持原值
}

func TestUpdateConversationsInCacheWithNewConversation(t *testing.T) {
	cache := wkdb.NewConversationCache(1000)

	// 初始会话数据
	originalConversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1, UnreadCount: 5},
		{Id: 2, Uid: "user1", ChannelId: "channel2", ChannelType: 1, UnreadCount: 3},
	}

	// 设置初始缓存
	cache.SetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10, originalConversations)

	// 验证初始缓存
	cached, found := cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.True(t, found)
	assert.Len(t, cached, 2)

	// 更新数据，包含新增会话
	updatedConversations := []wkdb.Conversation{
		{Id: 1, Uid: "user1", ChannelId: "channel1", ChannelType: 1, UnreadCount: 10}, // 更新现有
		{Id: 3, Uid: "user1", ChannelId: "channel3", ChannelType: 1, UnreadCount: 7},  // 新增会话
	}

	// 智能更新缓存
	cache.UpdateConversationsInCache(updatedConversations)

	// 验证缓存已失效（因为有新增会话）
	_, found = cache.GetLastConversations("user1", wkdb.ConversationTypeChat, 0, nil, 10)
	assert.False(t, found, "Cache should be invalidated when new conversations are added")
}
