package reactor

import (
	"context"
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

var emptyMessageItem = messageItem{}

type messageItem struct {
	messageId  uint64
	messageSeq uint64
	committed  bool // 是否已提交
}

type messageWaitItem struct {
	waitC chan []messageItem
	ctx   context.Context

	messageSeqAndIdMap map[uint64]uint64
	messageIds         [][]uint64
	waits              []chan []messageItem
}

type proposeWait struct {
	mu sync.RWMutex
	wklog.Log

	proposeResultMap map[string][]ProposeResult
	proposeWaitMap   map[string]chan []ProposeResult
	hasAdd           atomic.Bool
}

func newProposeWait(key string) *proposeWait {
	return &proposeWait{
		Log:              wklog.NewWKLog(fmt.Sprintf("proposeWait[%s]", key)),
		proposeWaitMap:   make(map[string]chan []ProposeResult),
		proposeResultMap: make(map[string][]ProposeResult),
	}
}

// TODO: 此方法返回创建ProposeResult导致内存过高，需要优化
func (m *proposeWait) add(ctx context.Context, key string, ids []uint64) chan []ProposeResult {
	m.hasAdd.Store(true)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(ids) == 0 {
		m.Panic("addWait ids is empty")
	}
	waitC := make(chan []ProposeResult, 1)
	items := make([]ProposeResult, len(ids))
	for i, id := range ids {
		items[i] = ProposeResult{
			Id: id,
		}
	}

	// m.Debug("addWait", zap.String("key", key), zap.Int("ids", len(ids)))

	m.proposeResultMap[key] = items
	m.proposeWaitMap[key] = waitC

	return waitC
}

// waitItemsWithRange 获取[startMessageSeq, endMessageSeq)范围的等待项
func (m *proposeWait) waitItemsWithRange(startMessageSeq, endMessageSeq uint64) []messageWaitItem {
	// m.mu.Lock()
	// defer m.mu.Unlock()
	// var items []messageWaitItem
	// for _, item := range m.items {
	// 	for _, messageItem := range item.messageItems {
	// 		if messageItem.messageSeq >= startMessageSeq && messageItem.messageSeq < endMessageSeq {
	// 			items = append(items, item)
	// 			break
	// 		}
	// 	}
	// }
	// return items

	return nil
}

// 获取大于等于startMessageSeq的messageWaitItem
func (m *proposeWait) waitItemsWithStartSeq(startMessageSeq uint64) []messageWaitItem {
	// m.mu.Lock()
	// defer m.mu.Unlock()
	// var items []messageWaitItem
	// for _, item := range m.items {
	// 	for _, messageItem := range item.messageItems {
	// 		if messageItem.messageSeq >= startMessageSeq {
	// 			items = append(items, item)
	// 			break
	// 		}
	// 	}
	// }
	// return items

	return nil
}

// TODO 此方法startMessageSeq 至 endMessageSeq的跨度十万需要10来秒 很慢 需要优化
func (m *proposeWait) didPropose(key string, logId uint64, logIndex uint64) {
	if logIndex == 0 {
		m.Panic("didPropose logIndex is 0")
	}
	// m.Debug("didPropose", zap.String("key", key), zap.Uint64("logId", logId), zap.Uint64("logIndex", logIndex))
	m.mu.RLock()
	items := m.proposeResultMap[key]
	m.mu.RUnlock()
	for i, item := range items {
		if item.Id == logId {
			items[i].Index = logIndex
			break
		}
	}
	m.mu.Lock()
	m.proposeResultMap[key] = items
	m.mu.Unlock()
}

// didCommit 提交[startLogIndex, endLogIndex)范围的消息
func (m *proposeWait) didCommit(startLogIndex uint64, endLogIndex uint64) {

	m.mu.Lock()
	defer m.mu.Unlock()

	if startLogIndex == 0 {
		m.Panic("didCommit startLogIndex is 0")
	}

	if endLogIndex == 0 {
		m.Panic("didCommit endLogIndex is 0")
	}

	keysToDelete := make([]string, 0, 500)
	for key, items := range m.proposeResultMap {
		shouldCommit := true
		for i, item := range items {
			if item.Index >= startLogIndex && item.Index < endLogIndex {
				items[i].committed = true
			}
			if !items[i].committed {
				shouldCommit = false
			}
		}
		if shouldCommit {
			m.Debug("didCommit", zap.String("key", key), zap.Uint64("startLogIndex", startLogIndex), zap.Uint64("endLogIndex", endLogIndex))
			waitC := m.proposeWaitMap[key]
			waitC <- items
			close(waitC)
			keysToDelete = append(keysToDelete, key)

		}
	}
	for _, key := range keysToDelete {
		delete(m.proposeResultMap, key)
		delete(m.proposeWaitMap, key)
	}

}

func (m *proposeWait) remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.proposeResultMap, key)
	delete(m.proposeWaitMap, key)
}

func (m *proposeWait) exist(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.proposeResultMap[key]
	return ok

}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b uint64) uint64 {
	if a > b {
		return b
	}
	return a
}
