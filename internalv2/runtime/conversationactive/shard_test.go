package conversationactive

import (
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestShardIndexDistribution(t *testing.T) {
	const numShards = 16
	counts := make(map[uint32]int)

	for i := 0; i < 10000; i++ {
		uid := fmt.Sprintf("user-%d", i)
		idx := shardIndex(uid, numShards)
		if idx >= numShards {
			t.Fatalf("shard index %d >= numShards %d", idx, numShards)
		}
		counts[idx]++
	}

	// 验证分布均匀性（允许 20% 偏差）
	expected := 10000 / numShards
	for idx, count := range counts {
		if count < expected*80/100 || count > expected*120/100 {
			t.Errorf("shard %d has %d items, expected ~%d", idx, count, expected)
		}
	}
}

func TestCacheShardBasicOperations(t *testing.T) {
	shard := newCacheShard()

	patch := ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
		ReadSeq:     10,
	}

	// 测试插入
	shard.set(patch, 1, 100, true)

	// 测试读取
	entry, ok := shard.get("u1", conversationKey{
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
	if entry.version != 1 {
		t.Errorf("version = %d, want 1", entry.version)
	}
}

func TestCacheShardRemove(t *testing.T) {
	shard := newCacheShard()

	patch := ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}

	shard.set(patch, 1, 0, false)

	// 验证存在
	_, ok := shard.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry should exist before remove")
	}

	// 删除
	shard.remove("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})

	// 验证已删除
	_, ok = shard.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if ok {
		t.Error("entry should not exist after remove")
	}
}

func TestCacheShardCount(t *testing.T) {
	shard := newCacheShard()

	if count := shard.count(); count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	// 添加多个条目
	for i := 0; i < 5; i++ {
		patch := ActivePatch{
			UID:         fmt.Sprintf("u%d", i),
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "ch1",
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		}
		shard.set(patch, uint64(i), 0, false)
	}

	if count := shard.count(); count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
}
