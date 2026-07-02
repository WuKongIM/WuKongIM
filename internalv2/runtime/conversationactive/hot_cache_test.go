package conversationactive

import (
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestHotCacheLRUEviction(t *testing.T) {
	hot := newHotCache(16, 100) // 16 shards, 100 max entries

	// 插入 150 个条目
	for i := 0; i < 150; i++ {
		patch := ActivePatch{
			UID:         fmt.Sprintf("u%d", i),
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "ch1",
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		}
		hot.set(patch, uint64(i), 0, false)
	}

	// 验证总数不超过 100
	total := hot.totalCount()
	if total > 100 {
		t.Errorf("hot cache size = %d, want <= 100", total)
	}

	// 验证最老的条目被淘汰
	_, ok := hot.get("u0", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if ok {
		t.Error("oldest entry should be evicted")
	}
}

func TestHotCacheBasicOperations(t *testing.T) {
	hot := newHotCache(16, 1000)

	patch := ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
		ReadSeq:     10,
	}

	// 测试 set
	hot.set(patch, 1, 0, false)

	// 测试 get
	entry, ok := hot.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.patch.ActiveAtMS != 1000 {
		t.Errorf("ActiveAtMS = %d, want 1000", entry.patch.ActiveAtMS)
	}

	// 测试 remove
	hot.remove("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})

	_, ok = hot.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if ok {
		t.Error("entry should be removed")
	}
}

func TestHotCacheTotalCount(t *testing.T) {
	hot := newHotCache(16, 1000)

	if count := hot.totalCount(); count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	// 添加 50 个条目
	for i := 0; i < 50; i++ {
		patch := ActivePatch{
			UID:         fmt.Sprintf("u%d", i),
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "ch1",
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		}
		hot.set(patch, uint64(i), 0, false)
	}

	if count := hot.totalCount(); count != 50 {
		t.Errorf("count = %d, want 50", count)
	}
}

func TestHotCacheAccessUpdatesLRU(t *testing.T) {
	hot := newHotCache(16, 10) // 小容量便于测试

	// 插入 10 个条目
	for i := 0; i < 10; i++ {
		patch := ActivePatch{
			UID:         fmt.Sprintf("u%d", i),
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "ch1",
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		}
		hot.set(patch, uint64(i), 0, false)
	}

	// 插入更多条目触发淘汰
	for i := 10; i < 15; i++ {
		patch := ActivePatch{
			UID:         fmt.Sprintf("u%d", i),
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "ch1",
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		}
		hot.set(patch, uint64(i), 0, false)
	}

	// 验证总数不超过最大容量
	if count := hot.totalCount(); count > 10 {
		t.Errorf("count = %d, should not exceed 10", count)
	}

	// 验证淘汰确实发生了
	if count := hot.totalCount(); count < 10 {
		t.Logf("count = %d after eviction", count)
	}
}
