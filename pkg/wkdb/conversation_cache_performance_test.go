package wkdb_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

// 测试缓存对 GetLastConversations 性能的影响
func TestGetLastConversationsWithCache(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "cache_test_user"
	now := time.Now()

	// 创建大量会话数据
	conversations := make([]wkdb.Conversation, 0, 50)
	for i := 0; i < 50; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:           uint64(i + 1),
			Uid:          uid,
			ChannelId:    fmt.Sprintf("channel_%d", i),
			ChannelType:  1,
			Type:         wkdb.ConversationTypeChat,
			UnreadCount:  uint32(i + 1),
			ReadToMsgSeq: uint64(i + 1),
			CreatedAt:    &now,
			UpdatedAt:    &updatedAt,
		})
	}

	// 添加会话
	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 第一次查询（缓存未命中）
	start1 := time.Now()
	result1, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 20)
	duration1 := time.Since(start1)
	assert.NoError(t, err)
	assert.LessOrEqual(t, len(result1), 20)

	// 第二次查询（缓存命中）
	start2 := time.Now()
	result2, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 20)
	duration2 := time.Since(start2)
	assert.NoError(t, err)
	assert.Equal(t, len(result1), len(result2))

	// 缓存命中的查询应该更快
	t.Logf("First query (cache miss): %v", duration1)
	t.Logf("Second query (cache hit): %v", duration2)

	// 通常缓存命中应该比缓存未命中快很多
	if duration2 < duration1 {
		speedup := float64(duration1) / float64(duration2)
		t.Logf("Cache hit is %.2fx faster", speedup)
	}

	// 验证结果一致性
	assert.Equal(t, len(result1), len(result2))
	for i := range result1 {
		assert.Equal(t, result1[i].Id, result2[i].Id)
		assert.Equal(t, result1[i].ChannelId, result2[i].ChannelId)
	}
}

// 基准测试：对比有缓存和无缓存的性能
func BenchmarkGetLastConversationsWithCache(b *testing.B) {
	d := newTestDB(b)
	err := d.Open()
	assert.NoError(b, err)

	defer func() {
		err := d.Close()
		assert.NoError(b, err)
	}()

	uid := "bench_user"
	now := time.Now()

	// 创建测试数据
	conversations := make([]wkdb.Conversation, 0, 30)
	for i := 0; i < 30; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:           uint64(i + 1),
			Uid:          uid,
			ChannelId:    fmt.Sprintf("channel_%d", i),
			ChannelType:  1,
			Type:         wkdb.ConversationTypeChat,
			UnreadCount:  uint32(i + 1),
			ReadToMsgSeq: uint64(i + 1),
			CreatedAt:    &now,
			UpdatedAt:    &updatedAt,
		})
	}

	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
		assert.NoError(b, err)
	}
}

// 测试智能缓存更新的性能
func TestSmartCacheUpdatePerformance(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "smart_update_test_user"
	now := time.Now()

	// 创建测试数据
	conversations := make([]wkdb.Conversation, 0, 20)
	for i := 0; i < 20; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:           uint64(i + 1),
			Uid:          uid,
			ChannelId:    fmt.Sprintf("channel_%d", i),
			ChannelType:  1,
			Type:         wkdb.ConversationTypeChat,
			UnreadCount:  uint32(i + 1),
			ReadToMsgSeq: uint64(i + 1),
			CreatedAt:    &now,
			UpdatedAt:    &updatedAt,
		})
	}

	// 添加会话
	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 第一次查询（缓存未命中）
	result1, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	assert.NoError(t, err)
	assert.Len(t, result1, 10)

	// 第二次查询（缓存命中）
	start2 := time.Now()
	result2, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	duration2 := time.Since(start2)
	assert.NoError(t, err)
	assert.Equal(t, len(result1), len(result2))

	// 更新部分会话（智能更新缓存）
	conversations[0].UnreadCount = 100
	conversations[1].UnreadCount = 200
	updatedAt := now.Add(time.Hour)
	conversations[0].UpdatedAt = &updatedAt
	conversations[1].UpdatedAt = &updatedAt
	err = d.AddOrUpdateConversationsWithUser(uid, conversations[:2])
	assert.NoError(t, err)

	// 第三次查询（缓存应该被智能更新，而不是失效）
	start3 := time.Now()
	result3, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	duration3 := time.Since(start3)
	assert.NoError(t, err)

	t.Logf("Cached query: %v", duration2)
	t.Logf("Query after smart cache update: %v", duration3)

	// 验证缓存仍然有效且数据已更新
	found1, found2 := false, false
	for _, conv := range result3 {
		if conv.Id == 1 {
			assert.Equal(t, uint32(100), conv.UnreadCount)
			found1 = true
		}
		if conv.Id == 2 {
			assert.Equal(t, uint32(200), conv.UnreadCount)
			found2 = true
		}
	}
	assert.True(t, found1, "Updated conversation 1 should be found")
	assert.True(t, found2, "Updated conversation 2 should be found")

	// 智能更新后的查询应该仍然很快（缓存命中）
	if duration3 < duration2*2 {
		t.Logf("Smart cache update maintained good performance")
	}
}

// 测试缓存失效后的性能
func TestCacheInvalidationPerformance(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "invalidation_test_user"
	now := time.Now()

	// 创建测试数据
	conversations := make([]wkdb.Conversation, 0, 20)
	for i := 0; i < 20; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:           uint64(i + 1),
			Uid:          uid,
			ChannelId:    fmt.Sprintf("channel_%d", i),
			ChannelType:  1,
			Type:         wkdb.ConversationTypeChat,
			UnreadCount:  uint32(i + 1),
			ReadToMsgSeq: uint64(i + 1),
			CreatedAt:    &now,
			UpdatedAt:    &updatedAt,
		})
	}

	// 添加会话
	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 第一次查询（缓存未命中）
	result1, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	assert.NoError(t, err)
	assert.Len(t, result1, 10)

	// 第二次查询（缓存命中）
	start2 := time.Now()
	result2, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	duration2 := time.Since(start2)
	assert.NoError(t, err)
	assert.Equal(t, len(result1), len(result2))

	// 更新会话（这会使缓存失效）
	conversations[0].UnreadCount = 100
	updatedAt := now.Add(time.Hour)
	conversations[0].UpdatedAt = &updatedAt
	err = d.AddOrUpdateConversationsWithUser(uid, conversations[:1])
	assert.NoError(t, err)

	// 第三次查询（缓存失效后，需要重新从数据库查询）
	start3 := time.Now()
	result3, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	duration3 := time.Since(start3)
	assert.NoError(t, err)

	t.Logf("Cached query: %v", duration2)
	t.Logf("Query after cache invalidation: %v", duration3)

	// 验证缓存失效后数据是最新的
	found := false
	for _, conv := range result3 {
		if conv.Id == 1 {
			assert.Equal(t, uint32(100), conv.UnreadCount)
			found = true
			break
		}
	}
	assert.True(t, found, "Updated conversation should be found")
}

// 压力测试：大量并发查询
func TestCacheConcurrentQueries(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "concurrent_test_user"
	now := time.Now()

	// 创建测试数据
	conversations := make([]wkdb.Conversation, 0, 15)
	for i := 0; i < 15; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:           uint64(i + 1),
			Uid:          uid,
			ChannelId:    fmt.Sprintf("channel_%d", i),
			ChannelType:  1,
			Type:         wkdb.ConversationTypeChat,
			UnreadCount:  uint32(i + 1),
			ReadToMsgSeq: uint64(i + 1),
			CreatedAt:    &now,
			UpdatedAt:    &updatedAt,
		})
	}

	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 并发查询测试
	done := make(chan bool, 10)
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 5; j++ {
				_, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 5)
				if err != nil {
					errors <- err
					return
				}
			}
			done <- true
		}(i)
	}

	// 等待所有goroutine完成
	for i := 0; i < 10; i++ {
		select {
		case <-done:
			// 成功完成
		case err := <-errors:
			t.Fatalf("Concurrent query failed: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("Concurrent query timeout")
		}
	}
}

// 基准测试：缓存性能
func BenchmarkConversationCacheOperations(b *testing.B) {
	cache := wkdb.NewConversationCache(10000)

	// 预填充缓存
	conversations := make([]wkdb.Conversation, 100)
	for i := 0; i < 100; i++ {
		conversations[i] = wkdb.Conversation{
			Id:          uint64(i),
			Uid:         fmt.Sprintf("user%d", i%10),
			ChannelId:   fmt.Sprintf("channel%d", i),
			ChannelType: 1,
		}
	}

	b.Run("SetLastConversations", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			uid := fmt.Sprintf("user%d", i%10)
			cache.SetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10, conversations)
		}
	})

	// 预填充一些数据用于读取测试
	for i := 0; i < 10; i++ {
		uid := fmt.Sprintf("user%d", i)
		cache.SetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10, conversations)
	}

	b.Run("GetLastConversations", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			uid := fmt.Sprintf("user%d", i%10)
			cache.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
		}
	})
}
