package replica

import (
	"time"
)

type messageWait struct {
	waitMap             map[MsgType]map[uint64]waitInfo
	messageSendInterval time.Duration
}

func newMessageWait(messageSendInterval time.Duration) *messageWait {
	return &messageWait{
		waitMap:             make(map[MsgType]map[uint64]waitInfo),
		messageSendInterval: messageSendInterval, // 如果某个消息在指定时间内没有收到ack，则认为超时，超时后可以重发此消息
	}
}

func (m *messageWait) next(nodeId uint64, msgType MsgType) uint64 {
	nodeWaitMap := m.waitMap[msgType]
	if nodeWaitMap == nil {
		nodeWaitMap = make(map[uint64]waitInfo)
		m.waitMap[msgType] = nodeWaitMap
	}
	seq := nodeWaitMap[nodeId].seq + 1
	nodeWaitMap[nodeId] = waitInfo{
		seq:       seq,
		createdAt: time.Now(),
		wait:      true,
	}
	return seq
}

func (m *messageWait) finish(nodeId uint64, msgType MsgType) {
	nodeWaitMap := m.waitMap[msgType]
	if nodeWaitMap == nil {
		return
	}
	if v, ok := nodeWaitMap[nodeId]; ok {
		nodeWaitMap[nodeId] = waitInfo{
			seq:       v.seq,
			createdAt: time.Now(),
			wait:      false,
		}
	}
}

func (m *messageWait) has(nodeId uint64, msgType MsgType) bool {
	nodeWaitMap := m.waitMap[msgType]
	if nodeWaitMap == nil {
		return false
	}
	if v, ok := nodeWaitMap[nodeId]; ok {

		if v.wait {
			return !v.createdAt.Add(m.messageSendInterval).Before(time.Now())
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
