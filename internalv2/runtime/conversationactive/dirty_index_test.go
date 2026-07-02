package conversationactive

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestDirtyIndexBasicOperations(t *testing.T) {
	index := newDirtyIndex()

	addr := cacheAddress{
		uid: "u1",
		key: conversationKey{
			kind:        metadb.ConversationKindNormal,
			channelID:   "ch1",
			channelType: 2,
		},
	}

	entry := flushEntry{
		uid:     "u1",
		key:     addr.key,
		patch:   ActivePatch{ActiveAtMS: 1000},
		version: 1,
	}

	// 测试添加
	index.add(10, addr, entry)
	if index.count(10) != 1 {
		t.Errorf("count = %d, want 1", index.count(10))
	}

	// 测试 pop
	entries := index.popN(10, 10)
	if len(entries) != 1 {
		t.Errorf("popN returned %d entries, want 1", len(entries))
	}
	if entries[0].uid != "u1" {
		t.Errorf("uid = %s, want u1", entries[0].uid)
	}

	// 验证已删除
	if index.count(10) != 0 {
		t.Errorf("count after pop = %d, want 0", index.count(10))
	}
}

func TestDirtyIndexHashSlotIsolation(t *testing.T) {
	index := newDirtyIndex()

	// 添加到不同的 hashSlot
	index.add(1, cacheAddress{uid: "u1", key: conversationKey{channelID: "ch1"}},
		flushEntry{uid: "u1", patch: ActivePatch{ActiveAtMS: 1000}})
	index.add(2, cacheAddress{uid: "u2", key: conversationKey{channelID: "ch2"}},
		flushEntry{uid: "u2", patch: ActivePatch{ActiveAtMS: 2000}})

	// 验证隔离
	entries1 := index.popN(1, 100)
	if len(entries1) != 1 || entries1[0].uid != "u1" {
		t.Errorf("slot 1 entries = %v, want u1 only", entries1)
	}

	entries2 := index.popN(2, 100)
	if len(entries2) != 1 || entries2[0].uid != "u2" {
		t.Errorf("slot 2 entries = %v, want u2 only", entries2)
	}
}

func TestDirtyIndexRemove(t *testing.T) {
	index := newDirtyIndex()

	addr := cacheAddress{
		uid: "u1",
		key: conversationKey{
			kind:        metadb.ConversationKindNormal,
			channelID:   "ch1",
			channelType: 2,
		},
	}

	entry := flushEntry{
		uid:     "u1",
		key:     addr.key,
		patch:   ActivePatch{ActiveAtMS: 1000},
		version: 1,
	}

	// 添加
	index.add(10, addr, entry)
	if index.count(10) != 1 {
		t.Errorf("count = %d, want 1", index.count(10))
	}

	// 删除
	index.remove(10, addr)
	if index.count(10) != 0 {
		t.Errorf("count after remove = %d, want 0", index.count(10))
	}

	// popN 应该返回空
	entries := index.popN(10, 10)
	if len(entries) != 0 {
		t.Errorf("popN after remove returned %d entries, want 0", len(entries))
	}
}

func TestDirtyIndexPopNLimit(t *testing.T) {
	index := newDirtyIndex()

	// 添加 10 个条目
	for i := 0; i < 10; i++ {
		addr := cacheAddress{
			uid: "u1",
			key: conversationKey{
				kind:        metadb.ConversationKindNormal,
				channelID:   string(rune('a' + i)),
				channelType: 2,
			},
		}
		entry := flushEntry{
			uid:     "u1",
			key:     addr.key,
			patch:   ActivePatch{ActiveAtMS: int64(1000 + i)},
			version: uint64(i),
		}
		index.add(5, addr, entry)
	}

	// 只 pop 3 个
	entries := index.popN(5, 3)
	if len(entries) != 3 {
		t.Errorf("popN(3) returned %d entries, want 3", len(entries))
	}

	// 剩余 7 个
	if index.count(5) != 7 {
		t.Errorf("count after popN(3) = %d, want 7", index.count(5))
	}
}
