package conversationactive

import (
	"context"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerV2Creation(t *testing.T) {
	m := NewManagerV2(Options{})
	if m == nil {
		t.Fatal("NewManagerV2 returned nil")
	}
	if len(m.shards) != 16 {
		t.Errorf("shards count = %d, want 16", len(m.shards))
	}
	if m.dirtyIndex == nil {
		t.Error("dirtyIndex is nil")
	}
}

func TestManagerV2MarkActiveBasic(t *testing.T) {
	m := NewManagerV2(Options{})

	patches := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
		ReadSeq:     10,
	}}

	err := m.MarkActive(context.Background(), patches)
	if err != nil {
		t.Fatalf("MarkActive error: %v", err)
	}

	// 验证数据已缓存
	entry, ok := m.getEntryForTest("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry not found after MarkActive")
	}
	if entry.patch.ActiveAtMS != 1000 {
		t.Errorf("ActiveAtMS = %d, want 1000", entry.patch.ActiveAtMS)
	}
	if entry.patch.ReadSeq != 10 {
		t.Errorf("ReadSeq = %d, want 10", entry.patch.ReadSeq)
	}
}

func TestManagerV2MarkActiveForHashSlot(t *testing.T) {
	m := NewManagerV2(Options{})

	patches := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}

	err := m.MarkActiveForHashSlot(context.Background(), 10, patches)
	if err != nil {
		t.Fatalf("MarkActiveForHashSlot error: %v", err)
	}

	// 验证数据已缓存且带 hashSlot
	entry, ok := m.getEntryForTest("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry not found")
	}
	if !entry.hasHashSlot {
		t.Error("hasHashSlot should be true")
	}
	if entry.hashSlot != 10 {
		t.Errorf("hashSlot = %d, want 10", entry.hashSlot)
	}
}

func TestManagerV2MarkActiveMultipleUsers(t *testing.T) {
	m := NewManagerV2(Options{})

	patches := make([]ActivePatch, 100)
	for i := 0; i < 100; i++ {
		patches[i] = ActivePatch{
			UID:         fmt.Sprintf("u%d", i),
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "ch1",
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		}
	}

	err := m.MarkActive(context.Background(), patches)
	if err != nil {
		t.Fatalf("MarkActive error: %v", err)
	}

	// 验证所有用户的数据都已缓存
	for i := 0; i < 100; i++ {
		uid := fmt.Sprintf("u%d", i)
		entry, ok := m.getEntryForTest(uid, conversationKey{
			kind:        metadb.ConversationKindNormal,
			channelID:   "ch1",
			channelType: 2,
		})
		if !ok {
			t.Errorf("entry not found for %s", uid)
			continue
		}
		if entry.patch.ActiveAtMS != int64(1000+i) {
			t.Errorf("%s: ActiveAtMS = %d, want %d", uid, entry.patch.ActiveAtMS, 1000+i)
		}
	}
}

func TestManagerV2MarkActiveCoalesces(t *testing.T) {
	m := NewManagerV2(Options{})

	// 第一次标记
	patches1 := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
		ReadSeq:     5,
	}}

	err := m.MarkActive(context.Background(), patches1)
	if err != nil {
		t.Fatalf("MarkActive(1) error: %v", err)
	}

	entry1, _ := m.getEntryForTest("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	version1 := entry1.version

	// 第二次标记（更新）
	patches2 := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  2000,
		ReadSeq:     10,
	}}

	err = m.MarkActive(context.Background(), patches2)
	if err != nil {
		t.Fatalf("MarkActive(2) error: %v", err)
	}

	entry2, ok := m.getEntryForTest("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry not found after update")
	}

	// 验证合并逻辑：取最大值
	if entry2.patch.ActiveAtMS != 2000 {
		t.Errorf("ActiveAtMS = %d, want 2000", entry2.patch.ActiveAtMS)
	}
	if entry2.patch.ReadSeq != 10 {
		t.Errorf("ReadSeq = %d, want 10", entry2.patch.ReadSeq)
	}
	if entry2.version == version1 {
		t.Error("version should change after update")
	}
}
