package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// 不稳定的消息（也就是还没存储的消息）
type channelMsgQueue struct {
	messages         []*ReactorChannelMessage
	offset           uint64
	offsetInProgress uint64
	wklog.Log

	lastIndex       uint64 // 最新下标
	storagingIndex  uint64 // 正在存储的下标
	storagedIndex   uint64 // 已存储的下标
	deliveringIndex uint64 // 正在投递的下标
}

func newChannelMsgQueue(prefix string) *channelMsgQueue {
	return &channelMsgQueue{
		Log:              wklog.NewWKLog(fmt.Sprintf("channel.msgQueue[%s]", prefix)),
		offset:           1, // 消息下标是从1开始的 所以offset初始化值为1
		offsetInProgress: 1,
	}
}

// deliverTo 投递了index之前的消息
func (m *channelMsgQueue) deliverTo(index uint64) {
	num := int(index + 1 - m.offset)
	m.messages = m.messages[num:]
	m.offset = index + 1
	m.offsetInProgress = max(m.offsetInProgress, m.offset)
	m.shrinkMessagesArray()
}

func (m *channelMsgQueue) appendMessage(message *ReactorChannelMessage) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

func (m *channelMsgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if len(m.messages)*lenMultiple < cap(m.messages) {
		newMessages := make([]*ReactorChannelMessage, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

func (m *channelMsgQueue) acceptInProgress() {
	if len(m.messages) > 0 {
		m.offsetInProgress = m.messages[len(m.messages)-1].Index + 1
	}
}

func (m *channelMsgQueue) slice(lo uint64, hi uint64) []*ReactorChannelMessage {

	return m.messages[lo-m.offset : hi-m.offset : hi-m.offset]
}

func (m *channelMsgQueue) sliceWithSize(lo uint64, hi uint64, maxSize uint64) []*ReactorChannelMessage {
	if lo == hi {
		return nil
	}
	if lo >= m.offset {
		logs := m.slice(lo, hi)
		return limitSize(logs, maxSize)
	}
	return nil
}

func (m *channelMsgQueue) first() *ReactorChannelMessage {
	if len(m.messages) == 0 {
		return nil
	}
	return m.messages[0]
}

func limitSize(messages []*ReactorChannelMessage, maxSize uint64) []*ReactorChannelMessage {
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

type userMsgQueue struct {
	messages         []*ReactorUserMessage
	offset           uint64
	offsetInProgress uint64
	wklog.Log

	lastIndex uint64 // 最新下标
}

func newUserMsgQueue(prefix string) *userMsgQueue {
	return &userMsgQueue{
		Log:              wklog.NewWKLog(fmt.Sprintf("user.msgQueue[%s]", prefix)),
		offset:           1, // 消息下标是从1开始的 所以offset初始化值为1
		offsetInProgress: 1,
	}
}

// deliverTo 投递了index之前的消息
func (m *userMsgQueue) processTo(index uint64) {
	num := int(index + 1 - m.offset)
	m.messages = m.messages[num:]
	m.offset = index + 1
	m.offsetInProgress = max(m.offsetInProgress, m.offset)
	m.shrinkMessagesArray()
}

// nextMessages 返回未持久化的消息
func (m *userMsgQueue) nextMessages() []*ReactorUserMessage {
	inProgress := int(m.offsetInProgress - m.offset)
	if len(m.messages) == inProgress {
		return nil
	}
	return m.messages[inProgress:]
}

// hasNextMessages 是否有未持久化的消息
func (m *userMsgQueue) hasNextMessages() bool {
	return int(m.offsetInProgress-m.offset) < len(m.messages)
}

func (m *userMsgQueue) appendMessage(message *ReactorUserMessage) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

func (m *userMsgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if len(m.messages)*lenMultiple < cap(m.messages) {
		newMessages := make([]*ReactorUserMessage, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

func (m *userMsgQueue) acceptInProgress() {
	if len(m.messages) > 0 {
		lastMsg := m.messages[len(m.messages)-1]
		m.offsetInProgress = lastMsg.Index + 1
		m.processTo(lastMsg.Index)
	}
}

func (m *userMsgQueue) slice(lo uint64, hi uint64) []*ReactorUserMessage {

	return m.messages[lo-m.offset : hi-m.offset : hi-m.offset]
}

func (m *userMsgQueue) sliceWithSize(lo uint64, hi uint64, maxSize uint64) []*ReactorUserMessage {
	if lo == hi {
		return nil
	}
	if lo >= m.offset {
		logs := m.slice(lo, hi)
		return limitSizeWithUser(logs, maxSize)
	}
	return nil
}

func (m *userMsgQueue) first() *ReactorUserMessage {
	if len(m.messages) == 0 {
		return nil
	}
	return m.messages[0]
}

func limitSizeWithUser(messages []*ReactorUserMessage, maxSize uint64) []*ReactorUserMessage {
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
