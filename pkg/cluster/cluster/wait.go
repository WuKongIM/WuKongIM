package cluster

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
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

type messageWait struct {
	mu sync.Mutex
	wklog.Log

	messageResultMap map[string][]*messageItem
	messageWaitMap   map[string]chan []*messageItem
	messageSeqs      []uint64
	messageIds       []uint64
	offset           uint64
	hasAdd           atomic.Bool
}

func newMessageWait() *messageWait {
	return &messageWait{
		Log:              wklog.NewWKLog("messageWait"),
		messageWaitMap:   make(map[string]chan []*messageItem),
		messageResultMap: make(map[string][]*messageItem),
	}
}

// TODO: 此方法返回创建messageItem导致内存过高，需要优化
func (m *messageWait) addWait(ctx context.Context, key string, messageIds []uint64) chan []*messageItem {
	m.hasAdd.Store(true)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(messageIds) == 0 {
		m.Panic("addWait messageIds is empty")
	}
	waitC := make(chan []*messageItem, 1)
	items := make([]*messageItem, len(messageIds))
	for i, messageId := range messageIds {
		items[i] = &messageItem{
			messageId: messageId,
		}
	}

	m.messageResultMap[key] = items
	m.messageWaitMap[key] = waitC

	return waitC
}

// waitItemsWithRange 获取[startMessageSeq, endMessageSeq)范围的等待项
func (m *messageWait) waitItemsWithRange(startMessageSeq, endMessageSeq uint64) []messageWaitItem {
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
func (m *messageWait) waitItemsWithStartSeq(startMessageSeq uint64) []messageWaitItem {
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
func (m *messageWait) didPropose(key string, messageId uint64, messageSeq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if messageSeq == 0 {
		m.Panic("didPropose messageSeq is 0")
	}
	items := m.messageResultMap[key]
	for _, item := range items {
		if item.messageId == messageId {
			item.messageSeq = messageSeq
			break
		}
	}
}

// didCommit 提交[startMessaageSeq, endMessageSeq)范围的消息
func (m *messageWait) didCommit(startMessageSeq uint64, endMessageSeq uint64) {

	m.mu.Lock()
	defer m.mu.Unlock()

	if startMessageSeq == 0 {
		m.Panic("didCommit startMessaageSeq is 0")
	}

	if endMessageSeq == 0 {
		m.Panic("didCommit endMessageSeq is 0")
	}

	keysToDelete := make([]string, 0, 500)
	for key, items := range m.messageResultMap {
		shouldCommit := true
		for _, item := range items {
			if item.messageSeq >= startMessageSeq && item.messageSeq < endMessageSeq {
				item.committed = true
			}
			if !item.committed {
				shouldCommit = false
			}
		}
		if shouldCommit {
			waitC := m.messageWaitMap[key]
			waitC <- items
			close(waitC)
			keysToDelete = append(keysToDelete, key)
		}
	}
	for _, key := range keysToDelete {
		delete(m.messageResultMap, key)
		delete(m.messageWaitMap, key)
	}

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
