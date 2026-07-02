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

func TestManagerV2HotToColdMigration(t *testing.T) {
	m := NewManagerV2(Options{})

	// 标记活跃（带 hashSlot）
	patches := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
		ReadSeq:     10,
	}}
	m.MarkActiveForHashSlot(context.Background(), 1, patches)

	// 验证在热缓存中
	_, hotOK := m.hot.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !hotOK {
		t.Fatal("entry should be in hot cache")
	}

	// 手动迁移到冷缓存（模拟刷盘后）
	m.moveHotToCold([]cacheAddress{{
		uid: "u1",
		key: conversationKey{
			kind:        metadb.ConversationKindNormal,
			channelID:   "ch1",
			channelType: 2,
		},
	}})

	// 验证已移到冷缓存
	_, hotOK = m.hot.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	_, coldOK := m.cold.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})

	if hotOK {
		t.Error("entry still in hot cache after migration")
	}
	if !coldOK {
		t.Error("entry not in cold cache after migration")
	}
}

func TestManagerV2ColdToHotPromotion(t *testing.T) {
	m := NewManagerV2(Options{})

	// 手动插入到冷缓存
	coldPatch := ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}
	m.cold.set(coldPatch)

	// 再次标记活跃（相同会话）
	patches := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  2000,
		ReadSeq:     20,
	}}
	m.MarkActive(context.Background(), patches)

	// 验证已提升到热缓存
	entry, ok := m.hot.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if !ok {
		t.Fatal("entry not promoted to hot cache")
	}
	if entry.patch.ActiveAtMS != 2000 {
		t.Errorf("ActiveAtMS = %d, want 2000", entry.patch.ActiveAtMS)
	}

	// 验证已从冷缓存移除
	_, coldOK := m.cold.get("u1", conversationKey{
		kind:        metadb.ConversationKindNormal,
		channelID:   "ch1",
		channelType: 2,
	})
	if coldOK {
		t.Error("entry still in cold cache after promotion")
	}
}

func TestManagerV2ListActiveViewBasic(t *testing.T) {
	ctx := context.Background()
	m := NewManagerV2(Options{})

	// 添加热数据
	patches := []ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hot1", ChannelType: 2, ActiveAtMS: 3000},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hot2", ChannelType: 2, ActiveAtMS: 2000},
	}
	m.MarkActive(ctx, patches)

	// 添加冷数据
	m.cold.set(ActivePatch{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "cold1", ChannelType: 2, ActiveAtMS: 1000,
	})

	// 查询
	page, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1",
		metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView error: %v", err)
	}

	// 验证返回了所有数据
	if len(page.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(page.Rows))
	}

	// 验证按 ActiveAt 降序排序
	if page.Rows[0].ActiveAt < page.Rows[1].ActiveAt {
		t.Error("rows not sorted by ActiveAt descending")
	}
	if page.Rows[1].ActiveAt < page.Rows[2].ActiveAt {
		t.Error("rows not sorted by ActiveAt descending")
	}
}

func TestManagerV2ListActiveViewPagination(t *testing.T) {
	ctx := context.Background()
	m := NewManagerV2(Options{})

	// 添加多个会话
	for i := 0; i < 20; i++ {
		patch := ActivePatch{
			UID:         "u1",
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   fmt.Sprintf("ch%d", i),
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		}
		m.MarkActive(ctx, []ActivePatch{patch})
	}

	// 查询第一页（限制 10 条）
	page, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1",
		metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView error: %v", err)
	}

	// 验证分页
	if len(page.Rows) != 10 {
		t.Errorf("got %d rows, want 10", len(page.Rows))
	}
	if page.Done {
		t.Error("page.Done should be false (more data available)")
	}
}

func TestManagerV2Metrics(t *testing.T) {
	ctx := context.Background()
	m := NewManagerV2(Options{})

	// 初始指标应该为 0
	snapshot := m.GetMetrics()
	if snapshot.MarkActiveOps != 0 {
		t.Errorf("initial MarkActiveOps = %d, want 0", snapshot.MarkActiveOps)
	}

	// 执行一些操作
	patches := []ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "ch1", ChannelType: 2, ActiveAtMS: 1000},
	}
	m.MarkActive(ctx, patches)

	// 验证指标可以正常获取
	snapshot = m.GetMetrics()
	if snapshot.CacheHitRate < 0 || snapshot.CacheHitRate > 1 {
		t.Errorf("invalid cache hit rate: %f", snapshot.CacheHitRate)
	}
}
