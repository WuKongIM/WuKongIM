package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type msgQueue struct {
	messages []Message
	offset   uint64
	wklog.Log
	lastIndex uint64 // 最新下标
}

func newMsgQueue(prefix string) *msgQueue {
	return &msgQueue{
		Log: wklog.NewWKLog(fmt.Sprintf("user.msgQueue[%s]", prefix)),
	}
}

func (m *msgQueue) append(message Message) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

// [lo,hi)
func (m *msgQueue) slice(lo uint64, hi uint64) []Message {

	return m.messages[lo-m.offset : hi-m.offset : hi-m.offset]
}

func (m *msgQueue) sliceWithSize(lo uint64, hi uint64, maxSize uint64) []Message {
	if lo == hi {
		return nil
	}
	if maxSize == 0 {
		return m.slice(lo, hi)
	}
	if lo >= m.offset {
		logs := m.slice(lo, hi)
		return limitSize(logs, maxSize)
	}
	return nil
}

// truncateTo 裁剪index之前的消息,不包含index
func (m *msgQueue) truncateTo(index uint64) {
	num := int(index - m.offset)
	m.messages = m.messages[num:]
	m.offset = index
	m.shrinkMessagesArray()
}

func (m *msgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if len(m.messages)*lenMultiple < cap(m.messages) {
		newMessages := make([]Message, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

func limitSize(messages []Message, maxSize uint64) []Message {
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
