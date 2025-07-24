package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

// 辅助方法：通过反射或者添加测试方法来访问私有的 getLastConversationIds
// 这里我们通过 GetLastConversations 来间接测试，因为它内部调用了 getLastConversationIds

// TestDuplicateIdsInGetLastConversationIds 测试 getLastConversationIds 方法中出现重复 ID 的问题
func TestDuplicateIdsInGetLastConversationIds(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test_duplicate_user1"
	channelId := "test_channel"
	channelType := uint8(1)
	now := time.Now()

	// 场景1：测试同一个会话的多次快速更新
	t.Run("MultipleQuickUpdates", func(t *testing.T) {
		// 创建初始会话
		conversation := wkdb.Conversation{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   channelId + "_1",
			ChannelType: channelType,
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: 1,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		}

		err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
		assert.NoError(t, err)

		// 快速连续更新同一个会话多次
		for i := 0; i < 5; i++ {
			updatedTime := now.Add(time.Duration(i+1) * time.Millisecond)
			conversation.UnreadCount = uint32(i + 2)
			conversation.UpdatedAt = &updatedTime

			err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
			assert.NoError(t, err)
		}

		// 检查是否有重复的 ID
		ids, err := d.GetLastConversationIds(uid, 0, 10)
		assert.NoError(t, err)

		// 检查重复
		idMap := make(map[uint64]int)
		for _, id := range ids {
			idMap[id]++
		}

		for id, count := range idMap {
			if count > 1 {
				t.Logf("Found duplicate ID: %d, count: %d", id, count)
			}
		}
	})

	// 场景2：测试批量更新中包含重复会话
	t.Run("BatchUpdateWithDuplicates", func(t *testing.T) {
		channelId2 := channelId + "_2"
		baseTime := now.Add(time.Hour)

		// 创建包含重复会话的批量更新
		conversations := []wkdb.Conversation{
			{
				Id:          d.NextPrimaryKey(),
				Uid:         uid,
				ChannelId:   channelId2,
				ChannelType: channelType,
				Type:        wkdb.ConversationTypeChat,
				UnreadCount: 1,
				CreatedAt:   &baseTime,
				UpdatedAt:   &baseTime,
			},
			{
				Id:          d.NextPrimaryKey(),
				Uid:         uid,
				ChannelId:   channelId2,  // 相同的 channelId
				ChannelType: channelType, // 相同的 channelType
				Type:        wkdb.ConversationTypeChat,
				UnreadCount: 2, // 不同的 UnreadCount
				CreatedAt:   &baseTime,
				UpdatedAt:   &baseTime,
			},
		}

		err := d.AddOrUpdateConversationsWithUser(uid, conversations)
		assert.NoError(t, err)

		// 检查是否有重复的 ID
		ids, err := d.GetLastConversationIds(uid, 0, 10)
		assert.NoError(t, err)

		// 检查重复
		idMap := make(map[uint64]int)
		for _, id := range ids {
			idMap[id]++
		}

		for id, count := range idMap {
			if count > 1 {
				t.Logf("Found duplicate ID in batch update: %d, count: %d", id, count)
			}
		}
	})

	// 场景3：测试相同时间戳的更新
	t.Run("SameTimestampUpdates", func(t *testing.T) {
		channelId3 := channelId + "_3"
		sameTime := now.Add(2 * time.Hour)

		// 创建初始会话
		conversation := wkdb.Conversation{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   channelId3,
			ChannelType: channelType,
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: 1,
			CreatedAt:   &sameTime,
			UpdatedAt:   &sameTime,
		}

		err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
		assert.NoError(t, err)

		// 使用相同的时间戳更新多次
		for i := 0; i < 3; i++ {
			conversation.UnreadCount = uint32(i + 2)
			// 故意使用相同的 UpdatedAt 时间
			conversation.UpdatedAt = &sameTime

			err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
			assert.NoError(t, err)
		}

		// 检查是否有重复的 ID
		ids, err := d.GetLastConversationIds(uid, 0, 10)
		assert.NoError(t, err)

		// 检查重复
		idMap := make(map[uint64]int)
		for _, id := range ids {
			idMap[id]++
		}

		for id, count := range idMap {
			if count > 1 {
				t.Logf("Found duplicate ID with same timestamp: %d, count: %d", id, count)
			}
		}
	})

	// 场景4：测试并发更新（模拟）
	t.Run("ConcurrentUpdates", func(t *testing.T) {

		channelId4 := channelId + "_4"
		baseTime := now.Add(3 * time.Hour)

		// 创建初始会话
		conversation := wkdb.Conversation{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   channelId4,
			ChannelType: channelType,
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: 1,
			CreatedAt:   &baseTime,
			UpdatedAt:   &baseTime,
		}

		err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
		assert.NoError(t, err)

		// 模拟并发更新：快速连续的更新操作
		done := make(chan bool, 3)
		for i := 0; i < 3; i++ {
			go func(index int) {
				defer func() { done <- true }()

				for j := 0; j < 2; j++ {
					updateTime := baseTime.Add(time.Duration(index*10+j) * time.Microsecond)
					conv := wkdb.Conversation{
						Id:          d.NextPrimaryKey(),
						Uid:         uid,
						ChannelId:   channelId4,
						ChannelType: channelType,
						Type:        wkdb.ConversationTypeChat,
						UnreadCount: uint32(index*10 + j + 1),
						CreatedAt:   &baseTime,
						UpdatedAt:   &updateTime,
					}

					d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conv})
				}
			}(i)
		}

		// 等待所有 goroutine 完成
		for i := 0; i < 3; i++ {
			<-done
		}

		// 检查是否有重复的 ID
		ids, err := d.GetLastConversationIds(uid, 0, 10)
		assert.NoError(t, err)

		// 检查重复
		idMap := make(map[uint64]int)
		for _, id := range ids {
			idMap[id]++
		}

		for id, count := range idMap {
			if count > 1 {
				t.Logf("Found duplicate ID in concurrent updates: %d, count: %d", id, count)
			}
		}
	})

	// 最终检查：获取所有会话 ID 并分析重复情况
	t.Run("FinalAnalysis", func(t *testing.T) {
		ids, err := d.GetLastConversationIds(uid, 0, 100)
		assert.NoError(t, err)

		t.Logf("Total IDs returned: %d", len(ids))

		// 统计重复情况
		idMap := make(map[uint64]int)
		for _, id := range ids {
			idMap[id]++
		}

		duplicateCount := 0
		for id, count := range idMap {
			if count > 1 {
				duplicateCount++
				t.Logf("Duplicate ID: %d appears %d times", id, count)
			}
		}

		if duplicateCount > 0 {
			t.Logf("Found %d duplicate IDs out of %d unique IDs", duplicateCount, len(idMap))
		} else {
			t.Log("No duplicate IDs found")
		}

		// 验证去重逻辑是否工作
		assert.Equal(t, len(idMap), len(ids), "去重逻辑应该确保没有重复ID")
	})
}

// TestGetLastConversationIdsDeduplication 专门测试去重逻辑
func TestGetLastConversationIdsDeduplication(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "dedup_test_user"
	now := time.Now()

	// 创建多个会话，然后多次更新以增加重复 ID 的可能性
	for i := 0; i < 5; i++ {
		channelId := "channel_" + string(rune('A'+i))

		// 创建初始会话
		conversation := wkdb.Conversation{
			Id:          d.NextPrimaryKey(),
			Uid:         uid,
			ChannelId:   channelId,
			ChannelType: 1,
			Type:        wkdb.ConversationTypeChat,
			UnreadCount: 1,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		}

		err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
		assert.NoError(t, err)

		// 多次更新每个会话
		for j := 0; j < 3; j++ {
			updateTime := now.Add(time.Duration(i*100+j) * time.Millisecond)
			conversation.UnreadCount = uint32(j + 2)
			conversation.UpdatedAt = &updateTime

			err := d.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
			assert.NoError(t, err)
		}
	}

	// 获取 ID 列表并检查去重效果
	ids, err := d.GetLastConversationIds(uid, 0, 20)
	assert.NoError(t, err)

	// 验证去重逻辑
	idMap := make(map[uint64]int)
	for _, id := range ids {
		idMap[id]++
	}

	// 检查是否有重复
	hasDeduplication := false
	for id, count := range idMap {
		if count > 1 {
			t.Errorf("ID %d appears %d times, deduplication failed", id, count)
		}
		if count == 1 && len(ids) > len(idMap) {
			hasDeduplication = true
		}
	}

	if hasDeduplication {
		t.Logf("Deduplication worked: %d raw IDs reduced to %d unique IDs", len(ids), len(idMap))
	}

	t.Logf("Final result: %d unique conversation IDs", len(idMap))
}
