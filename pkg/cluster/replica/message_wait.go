package replica

import (
	"time"
)

type messageWait struct {
	waitMap             map[MsgType]map[uint64]waitInfo
	messageSendInterval time.Duration
	syncTimeout         time.Duration
}

func newMessageWait(messageSendInterval time.Duration) *messageWait {
	return &messageWait{
		waitMap:             make(map[MsgType]map[uint64]waitInfo),
		messageSendInterval: messageSendInterval, // 如果某个消息在指定时间内没有收到ack，则认为超时，超时后可以重发此消息
		syncTimeout:         time.Second * 20,
	}
}

func (m *messageWait) start(nodeId uint64, msgType MsgType) {
	nodeWaitMap := m.waitMap[msgType]
	if nodeWaitMap == nil {
		nodeWaitMap = make(map[uint64]waitInfo)
		m.waitMap[msgType] = nodeWaitMap
	}
	nodeWaitMap[nodeId] = waitInfo{
		createdAt: time.Now(),
		wait:      true,
	}
}

func (m *messageWait) finish(nodeId uint64, msgType MsgType) {
	m.finishWithForce(nodeId, msgType, false)
}

func (m *messageWait) finishWithForce(nodeId uint64, msgType MsgType, force bool) {
	nodeWaitMap := m.waitMap[msgType]
	if nodeWaitMap == nil {
		return
	}

	if v, ok := nodeWaitMap[nodeId]; ok {

		w := waitInfo{
			seq:       v.seq,
			createdAt: time.Now(),
			wait:      false,
		}

		if !force && msgType == MsgSync {
			if time.Since(v.createdAt) < m.messageSendInterval { // 如果同步消息发送间隔小于messageSendInterval，则至少等到messageSendInterval后才能再次发送
				w.createdAt = v.createdAt.Add(-m.syncTimeout + m.messageSendInterval)
				w.wait = true
			}
		}
		nodeWaitMap[nodeId] = w
	}
}

func (m *messageWait) has(nodeId uint64, msgType MsgType) bool {
	nodeWaitMap := m.waitMap[msgType]
	if nodeWaitMap == nil {
		return false
	}
	if v, ok := nodeWaitMap[nodeId]; ok {

		if v.wait {
			if msgType == MsgSync {
				return time.Since(v.createdAt) < m.syncTimeout
			}
			return time.Since(v.createdAt) < m.messageSendInterval
		}

		return false
	}
	return false
}

type waitInfo struct {
	seq       uint64
	createdAt time.Time
	wait      bool
}
