package replica

import (
	"time"
)

type messageWait struct {
	messageSendIntervalTickCount int // 消息发送间隔
	replicaMaxCount              int
	waits                        [][]*waitInfo

	syncIntervalTickCount               int // 同步消息发送间隔
	syncTickCount                       int
	pingTickCount                       int
	msgLeaderTermStartIndexReqTickCount int
}

func newMessageWait(messageSendInterval time.Duration, replicaMaxCount int) *messageWait {
	messageSendIntervalTickCount := 1
	syncIntervalTickCount := 20
	return &messageWait{
		messageSendIntervalTickCount:        messageSendIntervalTickCount, // 如果某个消息在指定时间内没有收到ack，则认为超时，超时后可以重发此消息
		waits:                               make([][]*waitInfo, MsgMaxValue),
		replicaMaxCount:                     replicaMaxCount,
		syncIntervalTickCount:               syncIntervalTickCount,
		syncTickCount:                       1,
		pingTickCount:                       messageSendIntervalTickCount,
		msgLeaderTermStartIndexReqTickCount: messageSendIntervalTickCount,
	}
}

func (m *messageWait) canPing() bool {

	return m.pingTickCount >= m.messageSendIntervalTickCount
}

func (m *messageWait) resetPing() {
	m.pingTickCount = 0
}

func (m *messageWait) canSync() bool {
	return m.syncTickCount >= m.syncIntervalTickCount
}

func (m *messageWait) resetSync() {
	m.syncTickCount = 0
}

func (m *messageWait) immediatelySync() {
	m.syncTickCount = m.syncIntervalTickCount
}

func (m *messageWait) canMsgLeaderTermStartIndex() bool {

	return m.msgLeaderTermStartIndexReqTickCount >= m.messageSendIntervalTickCount
}

func (m *messageWait) resetMsgLeaderTermStartIndex() {
	m.msgLeaderTermStartIndexReqTickCount = 0
}

func (m *messageWait) tick() {
	m.syncTickCount++
	m.pingTickCount++
	m.msgLeaderTermStartIndexReqTickCount++
}

type waitInfo struct {
	nodeId    uint64
	tickCount int
	wait      bool
}
