package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

// TestGetLastConversationsFullScan 测试全表扫描+过滤的 GetLastConversations 实现
func TestGetLastConversationsFullScan(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "full_scan_test_user"
	now := time.Now()

	// 创建多个不同类型和时间的会话
	conversations := []wkdb.Conversation{
		{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   "channel_1",
			ChannelType: 1,
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: 5,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		},
		{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   "channel_2",
			ChannelType: 2,
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: 3,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		},
		{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   "channel_3",
			ChannelType: 1,
			Type:        wkdb.ConversationTypeCMD, // 不同类型
			UnreadCount: 2,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		},
	}

	// 添加会话
	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 测试1：获取所有聊天类型的会话
	t.Run("GetAllChatConversations", func(t *testing.T) {
		// 先检查是否成功添加了会话
		allConversations, err := d.GetConversations(uid)
		assert.NoError(t, err)
		t.Logf("Total conversations added: %d", len(allConversations))
		for i, conv := range allConversations {
			t.Logf("Conversation %d: ID=%d, Type=%d, ChannelId=%s, ChannelType=%d",
				i, conv.Id, conv.Type, conv.ChannelId, conv.ChannelType)
		}

		result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
		assert.NoError(t, err)
		t.Logf("GetLastConversations returned: %d conversations", len(result))
		assert.Len(t, result, 2) // 应该有2个聊天类型的会话

		// 验证结果都是聊天类型
		for _, conv := range result {
			assert.Equal(t, wkdb.ConversationTypeChat, conv.Type)
		}
	})

	// 测试2：排除特定频道类型
	t.Run("ExcludeChannelTypes", func(t *testing.T) {
		excludeTypes := []uint8{2} // 排除频道类型2
		result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, excludeTypes, 10)
		assert.NoError(t, err)
		assert.Len(t, result, 1) // 应该只有1个会话（排除了频道类型2）

		// 验证结果不包含被排除的频道类型
		for _, conv := range result {
			assert.NotEqual(t, uint8(2), conv.ChannelType)
		}
	})

	// 测试3：限制数量
	t.Run("LimitResults", func(t *testing.T) {
		result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 1)
		assert.NoError(t, err)
		assert.Len(t, result, 1) // 应该只返回1个会话
	})

	// 测试4：按更新时间过滤
	t.Run("FilterByUpdatedAt", func(t *testing.T) {
		// 更新其中一个会话的时间
		futureTime := now.Add(time.Hour)
		updatedConv := conversations[0]
		updatedConv.UpdatedAt = &futureTime
		updatedConv.UnreadCount = 10

		err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{updatedConv})
		assert.NoError(t, err)

		// 使用未来时间作为过滤条件
		futureNano := uint64(futureTime.UnixNano())
		result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, futureNano, nil, 10)
		assert.NoError(t, err)
		assert.Len(t, result, 1) // 应该只有1个会话满足时间条件

		// 验证返回的是更新后的会话
		assert.Equal(t, uint32(10), result[0].UnreadCount)
	})

	// 测试5：时间排序
	t.Run("TimeOrdering", func(t *testing.T) {
		// 创建不同时间的会话
		time1 := now.Add(time.Minute)
		time2 := now.Add(2 * time.Minute)
		time3 := now.Add(3 * time.Minute)

		newConversations := []wkdb.Conversation{
			{
				Id:          d.NextPrimaryKey(),
				Uid:         uid,
				ChannelId:   "channel_time_1",
				ChannelType: 1,
				Type:        wkdb.ConversationTypeChat,
				UnreadCount: 1,
				CreatedAt:   &time1,
				UpdatedAt:   &time1,
			},
			{
				Id:          d.NextPrimaryKey(),
				Uid:         uid,
				ChannelId:   "channel_time_2",
				ChannelType: 1,
				Type:        wkdb.ConversationTypeChat,
				UnreadCount: 2,
				CreatedAt:   &time2,
				UpdatedAt:   &time2,
			},
			{
				Id:          d.NextPrimaryKey(),
				Uid:         uid,
				ChannelId:   "channel_time_3",
				ChannelType: 1,
				Type:        wkdb.ConversationTypeChat,
				UnreadCount: 3,
				CreatedAt:   &time3,
				UpdatedAt:   &time3,
			},
		}

		err := d.AddOrUpdateConversationsWithUser(uid, newConversations)
		assert.NoError(t, err)

		result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
		assert.NoError(t, err)
		assert.True(t, len(result) >= 3)

		// 验证按时间降序排列（最新的在前面）
		for i := 0; i < len(result)-1; i++ {
			if result[i].UpdatedAt != nil && result[i+1].UpdatedAt != nil {
				assert.True(t, result[i].UpdatedAt.After(*result[i+1].UpdatedAt) || result[i].UpdatedAt.Equal(*result[i+1].UpdatedAt))
			}
		}
	})
}

// TestGetLastConversationsPerformance 测试全表扫描的性能
func TestGetLastConversationsPerformance(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "performance_test_user"
	now := time.Now()

	// 创建大量会话数据
	conversations := make([]wkdb.Conversation, 0, 100)
	for i := 0; i < 100; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   "channel_" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
			ChannelType: uint8(1 + i%3), // 频道类型 1, 2, 3
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: uint32(i + 1),
			CreatedAt:   &now,
			UpdatedAt:   &updatedAt,
		})
	}

	// 批量添加会话
	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 测试性能
	start := time.Now()
	result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 20)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Len(t, result, 20)
	t.Logf("Full scan query took: %v for 100 conversations", duration)

	// 验证结果正确性
	assert.True(t, len(result) <= 20)
	for _, conv := range result {
		assert.Equal(t, wkdb.ConversationTypeChat, conv.Type)
	}
}

// TestGetLastConversationsNoDuplicates 测试全表扫描不会产生重复结果
func TestGetLastConversationsNoDuplicates(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "no_duplicates_test_user"
	now := time.Now()

	// 创建会话并多次更新
	conversation := wkdb.Conversation{
		Id:          d.NextPrimaryKey(),
		Uid:         uid,
		ChannelId:   "test_channel",
		ChannelType: 1,
		Type:        wkdb.ConversationTypeChat,
		UnreadCount: 1,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// 添加初始会话
	err = d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
	assert.NoError(t, err)

	// 多次更新同一个会话
	for i := 0; i < 5; i++ {
		updateTime := now.Add(time.Duration(i+1) * time.Minute)
		conversation.UnreadCount = uint32(i + 2)
		conversation.UpdatedAt = &updateTime

		err = d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
		assert.NoError(t, err)
	}

	// 获取会话列表
	result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	assert.NoError(t, err)

	// 验证没有重复
	channelMap := make(map[string]int)
	for _, conv := range result {
		key := conv.ChannelId + ":" + string(rune(conv.ChannelType))
		channelMap[key]++
	}

	for channel, count := range channelMap {
		assert.Equal(t, 1, count, "Channel %s should appear only once, but appeared %d times", channel, count)
	}

	// 验证返回的是最新的数据
	if len(result) > 0 {
		assert.Equal(t, uint32(6), result[0].UnreadCount) // 最后一次更新的值
	}
}

// TestGetLastConversationsCache 测试缓存功能
func TestGetLastConversationsCache(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "cache_test_user"
	now := time.Now()

	// 创建测试数据
	conversations := []wkdb.Conversation{
		{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   "channel_1",
			ChannelType: 1,
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: 5,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		},
	}

	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 第一次查询（缓存未命中）
	start1 := time.Now()
	result1, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	duration1 := time.Since(start1)
	assert.NoError(t, err)
	assert.Len(t, result1, 1)

	// 第二次查询（缓存命中）
	start2 := time.Now()
	result2, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	duration2 := time.Since(start2)
	assert.NoError(t, err)
	assert.Len(t, result2, 1)

	// 验证结果一致
	assert.Equal(t, result1[0].Id, result2[0].Id)
	assert.Equal(t, result1[0].UnreadCount, result2[0].UnreadCount)

	t.Logf("First query (cache miss): %v", duration1)
	t.Logf("Second query (cache hit): %v", duration2)

	// 缓存命中应该更快
	if duration2 < duration1 {
		t.Logf("Cache hit is faster by: %v", duration1-duration2)
	}
}
