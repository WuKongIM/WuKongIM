package replica

type messageWait struct {
	messageSendIntervalTickCount int // 消息发送间隔
	replicaMaxCount              int

	syncMaxIntervalTickCount            int // 同步消息最大间隔
	syncIntervalTickCount               int // 同步消息发送间隔
	syncTickCount                       int // 同步消息发送间隔计数
	pingTickCount                       int // ping消息发送间隔计数
	appendLogTickCount                  int // 追加日志到磁盘的间隔计数
	msgLeaderTermStartIndexReqTickCount int
}

func newMessageWait(replicaMaxCount int) *messageWait {
	messageSendIntervalTickCount := 1
	syncMaxIntervalTickCount := 20
	syncIntervalTickCount := 1
	return &messageWait{
		messageSendIntervalTickCount:        messageSendIntervalTickCount, // 如果某个消息在指定时间内没有收到ack，则认为超时，超时后可以重发此消息
		replicaMaxCount:                     replicaMaxCount,
		syncMaxIntervalTickCount:            syncMaxIntervalTickCount,
		syncIntervalTickCount:               syncIntervalTickCount,
		syncTickCount:                       syncIntervalTickCount,
		pingTickCount:                       messageSendIntervalTickCount,
		msgLeaderTermStartIndexReqTickCount: messageSendIntervalTickCount,
		appendLogTickCount:                  messageSendIntervalTickCount,
	}
}

// 是否可以发送ping
func (m *messageWait) canPing() bool {

	return m.pingTickCount >= m.messageSendIntervalTickCount
}

func (m *messageWait) resetPing() {
	m.pingTickCount = 0
}

// 是否可以同步
func (m *messageWait) canSync() bool {
	if m.syncIntervalTickCount > m.syncMaxIntervalTickCount {
		return m.syncTickCount >= m.syncIntervalTickCount
	}
	return m.syncTickCount >= m.syncMaxIntervalTickCount
}

func (m *messageWait) resetSync() {
	m.syncTickCount = 0
}

// 立马进行下次同步
func (m *messageWait) immediatelySync() {
	if m.syncMaxIntervalTickCount > m.syncIntervalTickCount {
		m.syncTickCount = m.syncMaxIntervalTickCount
	} else {
		m.syncTickCount = m.syncIntervalTickCount
	}
}

// 快速进行下次同步
func (m *messageWait) quickSync() {
	if m.syncMaxIntervalTickCount > m.syncIntervalTickCount {
		m.syncTickCount = m.syncMaxIntervalTickCount - m.syncIntervalTickCount
	}
}

func (m *messageWait) canMsgLeaderTermStartIndex() bool {

	return m.msgLeaderTermStartIndexReqTickCount >= m.messageSendIntervalTickCount
}

func (m *messageWait) resetMsgLeaderTermStartIndex() {
	m.msgLeaderTermStartIndexReqTickCount = 0
}

func (m *messageWait) canAppendLog() bool {
	return m.appendLogTickCount >= m.messageSendIntervalTickCount
}

func (m *messageWait) resetAppendLog() {
	m.appendLogTickCount = 0
}

func (m *messageWait) setSyncIntervalTickCount(syncIntervalTickCount int) {
	m.syncIntervalTickCount = syncIntervalTickCount
}

func (m *messageWait) tick() {
	m.syncTickCount++
	m.pingTickCount++
	m.msgLeaderTermStartIndexReqTickCount++
	m.appendLogTickCount++
}
