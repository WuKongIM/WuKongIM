package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// 不稳定的消息（也就是还没存储的消息）
type channelMsgQueue struct {
	messages         []ReactorChannelMessage
	offset           uint64
	offsetInProgress uint64
	wklog.Log

	lastIndex uint64 // 最新下标

	payloadDecryptingIndex uint64 // 正在解密的下标
	// payloadDecryptedIndex  uint64 // 已解密的下标

	permissionCheckingIndex uint64 // 正在检查权限的下标
	// permissionCheckedIndex  uint64 // 已检查权限的下标

	storagingIndex uint64 // 正在存储的下标
	// storagedIndex  uint64 // 已存储的下标

	sendackingIndex uint64 // 正在发送回执的下标

	deliveringIndex uint64 // 正在投递的下标
	// deliveredIndex  uint64 // 已投递的下标

	forwardingIndex uint64 // 转发中的下标
	// forwardedIndex  uint64 // 已转发下标
}

func newChannelMsgQueue(prefix string) *channelMsgQueue {
	return &channelMsgQueue{
		Log:              wklog.NewWKLog(fmt.Sprintf("channel.msgQueue[%s]", prefix)),
		offset:           1, // 消息下标是从1开始的 所以offset初始化值为1
		offsetInProgress: 1,
	}
}

// truncateTo 裁剪index之前的消息
func (m *channelMsgQueue) truncateTo(index uint64) {
	num := int(index + 1 - m.offset)
	m.messages = m.messages[num:]

	m.offset = index + 1
	m.offsetInProgress = max(m.offsetInProgress, m.offset)
	m.shrinkMessagesArray()
}

func (m *channelMsgQueue) getArrayIndex(index uint64) int {
	return int(index + 1 - m.offset)
}

func (m *channelMsgQueue) appendMessage(message ReactorChannelMessage) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

func (m *channelMsgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if cap(m.messages) >= len(m.messages)*lenMultiple {
		newMessages := make([]ReactorChannelMessage, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

func (m *channelMsgQueue) slice(lo uint64, hi uint64) []ReactorChannelMessage {

	return m.messages[lo-m.offset : hi-m.offset : hi-m.offset]
}

func (m *channelMsgQueue) sliceWithSize(lo uint64, hi uint64, maxSize uint64) []ReactorChannelMessage {
	if lo == hi {
		return nil
	}
	if lo >= m.offset {
		msgs := m.slice(lo, hi)
		if maxSize == 0 {
			return msgs
		}
		return limitSize(msgs, maxSize)
	}
	return nil
}

func (m *channelMsgQueue) resetIndex() {
	newIndex := m.offset - 1
	m.payloadDecryptingIndex = newIndex
	m.permissionCheckingIndex = newIndex
	m.storagingIndex = newIndex
	m.sendackingIndex = newIndex
	m.deliveringIndex = newIndex
	m.forwardingIndex = newIndex
}

// func (m *channelMsgQueue) first() ReactorChannelMessage {
// 	if len(m.messages) == 0 {
// 		return EmptyReactorChannelMessage
// 	}
// 	return m.messages[0]
// }

func (m *channelMsgQueue) String() string {
	return fmt.Sprintf("channelMsgQueue{offset=%d, lastIndex=%d payloadDecryptingIndex=%d, permissionCheckingIndex=%d, storagingIndex=%d, sendackingIndex=%d, deliveringIndex=%d,  forwardingIndex=%d len(messages)=%d}",
		m.offset, m.lastIndex, m.payloadDecryptingIndex, m.permissionCheckingIndex, m.storagingIndex, m.sendackingIndex, m.deliveringIndex, m.forwardingIndex, len(m.messages))
}

func limitSize(messages []ReactorChannelMessage, maxSize uint64) []ReactorChannelMessage {
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
	messages         []ReactorUserMessage
	offset           uint64
	offsetInProgress uint64
	wklog.Log

	processingIndex uint64 // 正在处理的下标
	lastIndex       uint64 // 最新下标
}

func newUserMsgQueue(prefix string) *userMsgQueue {
	return &userMsgQueue{
		Log:              wklog.NewWKLog(fmt.Sprintf("user.msgQueue[%s]", prefix)),
		offset:           1, // 消息下标是从1开始的 所以offset初始化值为1
		offsetInProgress: 1,
	}
}

func (m *userMsgQueue) resetIndex() {
	if m.offset <= 0 {
		return
	}
	m.processingIndex = m.offset - 1
}

// truncateTo 裁剪index之前的消息
func (m *userMsgQueue) truncateTo(index uint64) {
	num := int(index + 1 - m.offset)
	m.messages = m.messages[num:]
	m.offset = index + 1
	m.offsetInProgress = max(m.offsetInProgress, m.offset)
	m.shrinkMessagesArray()
}

// // nextMessages 返回未持久化的消息
// func (m *userMsgQueue) nextMessages() []ReactorUserMessage {
// 	inProgress := int(m.offsetInProgress - m.offset)
// 	if len(m.messages) == inProgress {
// 		return nil
// 	}
// 	return m.messages[inProgress:]
// }

// // hasNextMessages 是否有未持久化的消息
// func (m *userMsgQueue) hasNextMessages() bool {
// 	return int(m.offsetInProgress-m.offset) < len(m.messages)
// }

func (m *userMsgQueue) appendMessage(message ReactorUserMessage) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

func (m *userMsgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if len(m.messages)*lenMultiple < cap(m.messages) {
		newMessages := make([]ReactorUserMessage, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

// func (m *userMsgQueue) acceptInProgress() {
// 	if len(m.messages) > 0 {
// 		lastMsg := m.messages[len(m.messages)-1]
// 		m.offsetInProgress = lastMsg.Index + 1
// 		m.processTo(lastMsg.Index)
// 	}
// }

func (m *userMsgQueue) slice(lo uint64, hi uint64) []ReactorUserMessage {

	return m.messages[lo-m.offset : hi-m.offset : hi-m.offset]
}

func (m *userMsgQueue) sliceWithSize(lo uint64, hi uint64, maxSize uint64) []ReactorUserMessage {
	if lo == hi {
		return nil
	}
	if maxSize == 0 {
		return m.slice(lo, hi)
	}
	if lo >= m.offset {
		logs := m.slice(lo, hi)
		return limitSizeWithUser(logs, maxSize)
	}
	return nil
}

// func (m *userMsgQueue) first() ReactorUserMessage {
// 	if len(m.messages) == 0 {
// 		return EmptyReactorUserMessage
// 	}
// 	return m.messages[0]
// }

func limitSizeWithUser(messages []ReactorUserMessage, maxSize uint64) []ReactorUserMessage {
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

// 投递消息队列
type deliverMsgQueue struct {
	messages         []ReactorChannelMessage
	offset           uint64
	offsetInProgress uint64
	wklog.Log

	lastIndex uint64 // 最新下标

	deliveringIndex uint64 // 正在投递的下标

}

func newDeliverMsgQueue(prefix string) *deliverMsgQueue {
	return &deliverMsgQueue{
		Log:              wklog.NewWKLog(fmt.Sprintf("node.msgQueue[%s]", prefix)),
		offset:           1, // 消息下标是从1开始的 所以offset初始化值为1
		offsetInProgress: 1,
	}
}

// truncateTo 裁剪index之前的消息
func (m *deliverMsgQueue) truncateTo(index uint64) {
	num := int(index + 1 - m.offset)
	m.messages = m.messages[num:]
	m.offset = index + 1
	m.offsetInProgress = max(m.offsetInProgress, m.offset)
	m.shrinkMessagesArray()
}

func (m *deliverMsgQueue) appendMessage(message ReactorChannelMessage) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

func (m *deliverMsgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if len(m.messages)*lenMultiple < cap(m.messages) {
		newMessages := make([]ReactorChannelMessage, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

// func (m *deliverMsgQueue) acceptInProgress() {
// 	if len(m.messages) > 0 {
// 		m.offsetInProgress = m.messages[len(m.messages)-1].Index + 1
// 	}
// }

func (m *deliverMsgQueue) slice(lo uint64, hi uint64) []ReactorChannelMessage {

	return m.messages[lo-m.offset : hi-m.offset : hi-m.offset]
}

func (m *deliverMsgQueue) sliceWithSize(lo uint64, hi uint64, maxSize uint64) []ReactorChannelMessage {
	if lo == hi {
		return nil
	}
	if lo >= m.offset {
		msgs := m.slice(lo, hi)
		if maxSize == 0 {
			return msgs
		}
		return limitSize(msgs, maxSize)
	}
	return nil
}

// func (m *deliverMsgQueue) first() ReactorChannelMessage {
// 	if len(m.messages) == 0 {
// 		return EmptyReactorChannelMessage

// 	}
// 	return m.messages[0]
// }

type streamMsgQueue struct {
	messages               []reactorStreamMessage
	lastIndex              uint64 // 最新下标
	offset                 uint64
	offsetInProgress       uint64
	payloadDecryptingIndex uint64 // 正在解密的下标
	deliveringIndex        uint64 // 正在投递的下标
	forwardingIndex        uint64 // 转发中的下标
	wklog.Log
}

func newStreamMsgQueue() *streamMsgQueue {
	return &streamMsgQueue{
		Log: wklog.NewWKLog("stream.msgQueue"),
	}
}

// truncateTo 裁剪index之前的消息
func (m *streamMsgQueue) truncateTo(index uint64) {
	num := int(index + 1 - m.offset)
	m.messages = m.messages[num:]

	m.offset = index + 1
	m.offsetInProgress = max(m.offsetInProgress, m.offset)
	m.shrinkMessagesArray()
}

func (m *streamMsgQueue) getArrayIndex(index uint64) int {
	return int(index + 1 - m.offset)
}

func (m *streamMsgQueue) appendMessage(message reactorStreamMessage) {
	m.messages = append(m.messages, message)
	m.lastIndex++
}

func (m *streamMsgQueue) shrinkMessagesArray() {
	const lenMultiple = 2
	if len(m.messages) == 0 {
		m.messages = nil
	} else if len(m.messages)*lenMultiple < cap(m.messages) {
		newMessages := make([]reactorStreamMessage, len(m.messages))
		copy(newMessages, m.messages)
		m.messages = newMessages
	}
}

func (m *streamMsgQueue) slice(lo uint64, hi uint64) []reactorStreamMessage {

	return m.messages[lo-m.offset : hi-m.offset : hi-m.offset]
}

func (m *streamMsgQueue) sliceWithSize(lo uint64, hi uint64, maxSize uint64) []reactorStreamMessage {
	if lo == hi {
		return nil
	}
	if lo >= m.offset {
		msgs := m.slice(lo, hi)
		if maxSize == 0 {
			return msgs
		}
		return limitSizeWithStream(msgs, maxSize)
	}
	return nil
}

func (m *streamMsgQueue) resetIndex() {
	newIndex := m.offset - 1
	m.payloadDecryptingIndex = newIndex
	m.deliveringIndex = newIndex
	m.forwardingIndex = newIndex
}

func limitSizeWithStream(messages []reactorStreamMessage, maxSize uint64) []reactorStreamMessage {
	if len(messages) == 0 {
		return messages
	}
	size := messages[0].size()
	for limit := 1; limit < len(messages); limit++ {
		size += messages[limit].size()
		if size > maxSize {
			return messages[:limit]
		}
	}
	return messages
}
