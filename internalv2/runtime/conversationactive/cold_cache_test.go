package conversationactive

import (
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestColdCacheMemoryEfficiency(t *testing.T) {
	cold := newColdCache()

	// 插入 10000 个条目
	for i := 0; i < 10000; i++ {
		patch := ActivePatch{
			UID:         fmt.Sprintf("u%d", i%100),
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   fmt.Sprintf("ch%d", i),
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
			ReadSeq:     uint64(i),
		}
		cold.set(patch)
	}

	// 验证可以读取
	patch, ok := cold.get("u50", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch50",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry not found in cold cache")
	}
	if patch.ActiveAtMS != 1050 {
		t.Errorf("ActiveAtMS = %d, want 1050", patch.ActiveAtMS)
	}
}

func TestColdCacheThreadSafety(t *testing.T) {
	cold := newColdCache()
	done := make(chan bool)

	// 并发写入
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				patch := ActivePatch{
					UID:         fmt.Sprintf("u%d", id),
					ChannelID:   fmt.Sprintf("ch%d", j),
					ChannelType: 2,
					ActiveAtMS:  int64(1000 + j),
				}
				cold.set(patch)
			}
			done <- true
		}(i)
	}

	// 等待完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证数据完整性
	count := cold.count()
	if count != 1000 {
		t.Errorf("count = %d, want 1000", count)
	}
}

func TestColdCacheBasicOperations(t *testing.T) {
	cold := newColdCache()

	patch := ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
		ReadSeq:     10,
	}

	// 测试 set
	cold.set(patch)

	// 测试 get
	retrieved, ok := cold.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry not found")
	}
	if retrieved.ActiveAtMS != 1000 {
		t.Errorf("ActiveAtMS = %d, want 1000", retrieved.ActiveAtMS)
	}
	if retrieved.ReadSeq != 10 {
		t.Errorf("ReadSeq = %d, want 10", retrieved.ReadSeq)
	}

	// 测试 remove
	cold.remove("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})

	_, ok = cold.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if ok {
		t.Error("entry should be removed")
	}
}

func TestColdCacheBatchRemove(t *testing.T) {
	cold := newColdCache()

	// 添加多个条目
	patches := []ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "ch1", ChannelType: 2, ActiveAtMS: 1000},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "ch2", ChannelType: 2, ActiveAtMS: 2000},
		{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "ch1", ChannelType: 2, ActiveAtMS: 3000},
	}

	for _, p := range patches {
		cold.set(p)
	}

	// 批量删除
	addrs := []cacheAddress{
		{uid: "u1", key: conversationKey{kind: metadb.ConversationKindNormal, channelID: "ch1", channelType: 2}},
		{uid: "u2", key: conversationKey{kind: metadb.ConversationKindNormal, channelID: "ch1", channelType: 2}},
	}

	cold.batchRemove(addrs)

	// 验证已删除
	_, ok := cold.get("u1", conversationKey{kind: metadb.ConversationKindNormal, channelID: "ch1", channelType: 2})
	if ok {
		t.Error("u1/ch1 should be removed")
	}

	_, ok = cold.get("u2", conversationKey{kind: metadb.ConversationKindNormal, channelID: "ch1", channelType: 2})
	if ok {
		t.Error("u2/ch1 should be removed")
	}

	// u1/ch2 应该还在
	_, ok = cold.get("u1", conversationKey{kind: metadb.ConversationKindNormal, channelID: "ch2", channelType: 2})
	if !ok {
		t.Error("u1/ch2 should still exist")
	}
}
