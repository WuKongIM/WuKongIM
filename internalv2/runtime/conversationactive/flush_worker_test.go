package conversationactive

import (
	"context"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// mockStore 用于测试刷盘
type mockFlushStore struct {
	mu      sync.Mutex
	touches [][]ActivePatch
}

func (m *mockFlushStore) TouchConversationActives(ctx context.Context, patches []ActivePatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touches = append(m.touches, patches)
	return nil
}

func (m *mockFlushStore) ListConversationActivePage(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	return nil, metadb.ConversationActiveCursor{}, false, nil
}

func (m *mockFlushStore) GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	return metadb.ConversationState{}, false, nil
}

func (m *mockFlushStore) GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	return nil, nil
}

func (m *mockFlushStore) TouchConversationActiveAt(ctx context.Context, patches []metadb.ConversationActivePatch) error {
	return nil
}

func TestFlushWorkerAutoFlush(t *testing.T) {
	store := &mockFlushStore{}
	m := NewManagerV2(Options{
		Store:         store,
		FlushInterval: 100 * time.Millisecond,
	})

	// 启动刷盘协程
	m.StartFlushWorker()
	defer m.StopFlushWorker()

	// 写入脏数据
	patches := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}
	m.MarkActiveForHashSlot(context.Background(), 1, patches)

	// 等待自动刷盘
	time.Sleep(250 * time.Millisecond)

	// 验证已刷盘
	store.mu.Lock()
	touchCount := len(store.touches)
	store.mu.Unlock()

	if touchCount == 0 {
		t.Error("no flush occurred after interval")
	}
}

func TestFlushWorkerSignal(t *testing.T) {
	store := &mockFlushStore{}
	m := NewManagerV2(Options{
		Store:         store,
		FlushInterval: 10 * time.Second, // 长间隔
	})

	m.StartFlushWorker()
	defer m.StopFlushWorker()

	// 写入脏数据
	patches := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}
	m.MarkActiveForHashSlot(context.Background(), 1, patches)

	// 立即触发刷盘
	m.SignalFlush()
	time.Sleep(200 * time.Millisecond)

	// 验证已刷盘
	store.mu.Lock()
	touchCount := len(store.touches)
	store.mu.Unlock()

	if touchCount == 0 {
		t.Error("no flush occurred after signal")
	}
}

func TestFlushWorkerStop(t *testing.T) {
	store := &mockFlushStore{}
	m := NewManagerV2(Options{
		Store:         store,
		FlushInterval: 50 * time.Millisecond,
	})

	// 启动
	m.StartFlushWorker()

	// 写入数据
	patches := []ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "ch1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}
	m.MarkActiveForHashSlot(context.Background(), 1, patches)

	// 等待一次刷盘
	time.Sleep(100 * time.Millisecond)

	// 停止
	m.StopFlushWorker()

	// 记录当前刷盘次数
	store.mu.Lock()
	countBeforeStop := len(store.touches)
	store.mu.Unlock()

	// 再等待一段时间，验证没有新的刷盘
	time.Sleep(200 * time.Millisecond)

	store.mu.Lock()
	countAfterStop := len(store.touches)
	store.mu.Unlock()

	// 停止后不应该有新的刷盘（允许有一次刷盘在停止前已经启动）
	if countAfterStop > countBeforeStop+1 {
		t.Errorf("flush continued after stop: before=%d, after=%d", countBeforeStop, countAfterStop)
	}
}
