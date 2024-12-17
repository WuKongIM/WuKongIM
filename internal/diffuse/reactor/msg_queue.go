package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type msgQueue struct {
	messages       []*reactor.ChannelMessage
	offsetMsgIndex uint64
	wklog.Log
	lastIndex uint64 // 最新下标
}

func newMsgQueue(prefix string) *msgQueue {
	return &msgQueue{
		Log:            wklog.NewWKLog(fmt.Sprintf("diffuse.msgQueue[%s]", prefix)),
		offsetMsgIndex: 1, // 消息下标是从1开始的 所以offset初始化值为1
	}
}

func (m *msgQueue) append(message *reactor.ChannelMessage) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

func (m *msgQueue) len() int {
	return len(m.messages)
}

// [lo,hi)
func (m *msgQueue) slice(startMsgIndex uint64, endMsgIndex uint64) []*reactor.ChannelMessage {

	return m.messages[startMsgIndex-m.offsetMsgIndex : endMsgIndex-m.offsetMsgIndex : endMsgIndex-m.offsetMsgIndex]
}

func (m *msgQueue) sliceWithSize(startMsgIndex uint64, endMsgIndex uint64, maxSize uint64) []*reactor.ChannelMessage {
	if startMsgIndex == endMsgIndex {
		return nil
	}
	if maxSize == 0 {
		return m.slice(startMsgIndex, endMsgIndex)
	}
	if startMsgIndex >= m.offsetMsgIndex {
		logs := m.slice(startMsgIndex, endMsgIndex)
		return limitSize(logs, maxSize)
	}
	return nil
}

// truncateTo 裁剪index之前的消息,不包含index
func (m *msgQueue) truncateTo(msgIndex uint64) {
	num := m.getArrayIndex(msgIndex)
	m.messages = m.messages[num:]
	m.offsetMsgIndex = msgIndex
	m.shrinkMessagesArray()
}

func (m *msgQueue) getArrayIndex(msgIndex uint64) int {

	return int(msgIndex - m.offsetMsgIndex)
}

func (m *msgQueue) reset() {
	m.messages = m.messages[:0]
	m.offsetMsgIndex = 1
	m.lastIndex = 0
}

func (m *msgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if len(m.messages)*lenMultiple < cap(m.messages) {
		newMessages := make([]*reactor.ChannelMessage, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

func limitSize(messages []*reactor.ChannelMessage, maxSize uint64) []*reactor.ChannelMessage {
	if len(messages) == 0 {
		return messages
	}
	size := messages[0].Size()
	for limit := 1; limit < len(messages); limit++ {
		size += messages[limit].Size()
		if size > maxSize {
			return messages[:limit]
		}
	}
	return messages
}
