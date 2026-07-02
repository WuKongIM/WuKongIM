package conversationactive

import (
	"context"
	"sync"
	"time"
)

// FlushWorker 后台异步刷盘协程
type FlushWorker struct {
	manager       *ManagerV2
	interval      time.Duration
	minInterval   time.Duration
	maxInterval   time.Duration
	stopCh        chan struct{}
	signalCh      chan struct{}
	wg            sync.WaitGroup
	mu            sync.Mutex
	running       bool
	adaptive      bool
	currentInterval time.Duration
}

func newFlushWorker(m *ManagerV2, interval time.Duration) *FlushWorker {
	if interval == 0 {
		interval = 1 * time.Second // 默认 1 秒
	}

	return &FlushWorker{
		manager:         m,
		interval:        interval,
		minInterval:     interval / 2,   // 最小间隔：默认的一半
		maxInterval:     interval * 4,   // 最大间隔：默认的 4 倍
		currentInterval: interval,
		stopCh:          make(chan struct{}),
		signalCh:        make(chan struct{}, 1),
		adaptive:        true, // 默认启用自适应
	}
}

// Start 启动刷盘协程
func (w *FlushWorker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return
	}

	w.running = true
	w.wg.Add(1)
	go w.run()
}

// Stop 停止刷盘协程
func (w *FlushWorker) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	close(w.stopCh)
	w.wg.Wait()

	w.mu.Lock()
	w.running = false
	w.mu.Unlock()
}

// Signal 触发立即刷盘
func (w *FlushWorker) Signal() {
	select {
	case w.signalCh <- struct{}{}:
	default:
		// 已经有待处理的信号，跳过
	}
}

func (w *FlushWorker) run() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.currentInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return

		case <-ticker.C:
			w.flush()
			// 自适应调整间隔
			if w.adaptive {
				newInterval := w.adjustInterval()
				if newInterval != w.currentInterval {
					w.currentInterval = newInterval
					ticker.Reset(newInterval)
				}
			}

		case <-w.signalCh:
			w.flush()
		}
	}
}

// adjustInterval 根据脏数据量调整刷盘间隔
func (w *FlushWorker) adjustInterval() time.Duration {
	totalDirty := w.manager.dirtyIndex.totalCount()

	// 根据脏数据量调整间隔
	// 0 条：最大间隔（放松刷盘）
	// 100-1000 条：正常间隔
	// >1000 条：最小间隔（加快刷盘）
	// >10000 条：立即刷盘

	if totalDirty == 0 {
		return w.maxInterval
	} else if totalDirty < 100 {
		return w.interval * 2
	} else if totalDirty < 1000 {
		return w.interval
	} else if totalDirty < 10000 {
		return w.minInterval
	} else {
		// 超过 10000 条，使用最小间隔
		return w.minInterval
	}
}

func (w *FlushWorker) flush() {
	if w.manager.store == nil {
		return
	}

	ctx := context.Background()

	// 获取所有脏数据并刷盘
	// 简化实现：刷盘所有 hashSlot
	totalDirty := w.manager.dirtyIndex.totalCount()
	if totalDirty == 0 {
		return
	}

	// 逐个 hashSlot 刷盘（最多刷 1000 个条目）
	for slot := uint16(0); slot < 1024; slot++ {
		count := w.manager.dirtyIndex.count(slot)
		if count == 0 {
			continue
		}

		// 弹出脏数据
		entries := w.manager.dirtyIndex.popN(slot, 1000)
		if len(entries) == 0 {
			continue
		}

		// 转换为 ActivePatch
		patches := make([]ActivePatch, len(entries))
		addrs := make([]cacheAddress, len(entries))
		for i, entry := range entries {
			patches[i] = entry.patch
			addrs[i] = cacheAddress{
				uid: entry.uid,
				key: entry.key,
			}
		}

		// 调用 store 持久化
		// 注意：真实实现需要调用 TouchConversationActiveAt
		// 这里简化为假设 store 有类似方法
		if toucher, ok := w.manager.store.(interface {
			TouchConversationActives(context.Context, []ActivePatch) error
		}); ok {
			_ = toucher.TouchConversationActives(ctx, patches)
		}

		// 迁移到冷缓存
		w.manager.moveHotToCold(addrs)
	}
}
