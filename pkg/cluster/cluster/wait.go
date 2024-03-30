package cluster

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type commitWait struct {
	waitList []waitItem
	sync.Mutex
	wklog.Log
}

func newCommitWait() *commitWait {
	return &commitWait{
		Log: wklog.NewWKLog("commitWait"),
	}
}

type waitItem struct {
	logIndex uint64
	waitC    chan struct{}
}

func (c *commitWait) addWaitIndex(logIndex uint64) (<-chan struct{}, error) {
	c.Lock()
	defer c.Unlock()

	// for _, item := range c.waitList {
	// 	if item.logIndex == logIndex {
	// 		return nil, errors.New("logIndex already exists")
	// 	}
	// }

	waitC := make(chan struct{}, 1)
	c.waitList = append(c.waitList, waitItem{logIndex: logIndex, waitC: waitC})
	return waitC, nil
}

func (c *commitWait) commitIndex(logIndex uint64) {
	c.Lock()
	defer c.Unlock()

	maxIndex := 0
	exist := false
	for i, item := range c.waitList {
		if item.logIndex <= logIndex {
			select {
			case item.waitC <- struct{}{}:
			default:
				c.Warn("commitIndex notify failed", zap.Uint64("logIndex", logIndex))
			}
			maxIndex = i
			exist = true
		}
	}
	if exist {
		c.waitList = c.waitList[maxIndex+1:]
	}

}

var emptyMessageItem = messageItem{}

type messageItem struct {
	messageId  uint64
	messageSeq uint64
	proposed   bool // 是否已提案，已提案的消息有messageSeq
	committed  bool // 是否已提交
}

type messageWaitItem struct {
	messageItems []messageItem
	waitC        chan []messageItem
	ctx          context.Context
}

type messageWait struct {
	items []messageWaitItem
	mu    sync.Mutex
}

func newMessageWait() *messageWait {
	return &messageWait{}
}

func (m *messageWait) addWait(ctx context.Context, messageIds []uint64) chan []messageItem {

	waitC := make(chan []messageItem, 1)

	messageItems := make([]messageItem, len(messageIds))
	for i, messageId := range messageIds {
		messageItems[i] = messageItem{
			messageId: messageId,
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, messageWaitItem{
		messageItems: messageItems,
		waitC:        waitC,
		ctx:          ctx,
	})

	return waitC
}

// waitItemsWithRange 获取[startMessageSeq, endMessageSeq)范围的等待项
func (m *messageWait) waitItemsWithRange(startMessageSeq, endMessageSeq uint64) []messageWaitItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []messageWaitItem
	for _, item := range m.items {
		for _, messageItem := range item.messageItems {
			if messageItem.messageSeq >= startMessageSeq && messageItem.messageSeq < endMessageSeq {
				items = append(items, item)
				break
			}
		}
	}
	return items
}

// 获取大于等于startMessageSeq的messageWaitItem
func (m *messageWait) waitItemsWithStartSeq(startMessageSeq uint64) []messageWaitItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []messageWaitItem
	for _, item := range m.items {
		for _, messageItem := range item.messageItems {
			if messageItem.messageSeq >= startMessageSeq {
				items = append(items, item)
				break
			}
		}
	}
	return items
}

func (m *messageWait) didPropose(messageId uint64, messageSeq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.items {
		for i, messageItem := range item.messageItems {
			if messageItem.messageId == messageId {
				item.messageItems[i].messageSeq = messageSeq
				item.messageItems[i].proposed = true
			}
		}
	}
}

// didCommit 提交[startMessaageSeq, endMessageSeq)范围的消息
func (m *messageWait) didCommit(startMessaageSeq uint64, endMessageSeq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for j := len(m.items) - 1; j >= 0; j-- { // 采用倒序删除，避免删除后索引错位
		item := m.items[j]
		hasCommitted := false
		for i, messageItem := range item.messageItems {
			if !messageItem.committed {
				if messageItem.messageSeq >= startMessaageSeq && messageItem.messageSeq < endMessageSeq {
					item.messageItems[i].committed = true
					hasCommitted = true
				}
			}
		}

		if hasCommitted {
			itemAllCommitted := true
			for _, messageItem := range item.messageItems {
				if !messageItem.committed {
					itemAllCommitted = false
					break
				}
			}
			if itemAllCommitted {
				select {
				case item.waitC <- item.messageItems:
				default:
				}
				m.items = append(m.items[:j], m.items[j+1:]...)
			}
		}
	}
}
