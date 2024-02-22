package replica

type IStorage interface {
	// AppendLog 追加日志
	AppendLog(logs []Log) error
	// TruncateLog 截断日志, 从index开始截断,index不能等于0 （保留下来的内容不包含index）
	// [1,2,3,4,5,6] truncate to 4 = [1,2,3]
	TruncateLogTo(logIndex uint64) error
	// GetLogs 获取日志
	// startLogIndex 开始日志索引(结果包含startLogIndex)
	// endLogIndex 结束日志索引(结果不包含endLogIndex) endLogIndex=0表示不限制
	Logs(startLogIndex uint64, endLogIndex uint64, limit uint32) ([]Log, error)
	// LastIndex 最后一条日志的索引
	LastIndex() (uint64, error)

	// SetLeaderTermStartIndex 设置领导任期开始的第一条日志索引
	SetLeaderTermStartIndex(term uint32, index uint64) error
	// LeaderLastTerm 获取最新的本地保存的领导任期
	LeaderLastTerm() (uint32, error)
	// LeaderTermStartIndex 获取领导任期开始的第一条日志索引
	LeaderTermStartIndex(term uint32) (uint64, error)

	// 删除比传入的term大的的LeaderTermStartIndex记录
	DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error
}

type MemoryStorage struct {
	logs              []Log
	termStartIndexMap map[uint32]uint64
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		termStartIndexMap: make(map[uint32]uint64),
	}
}

func (m *MemoryStorage) AppendLog(logs []Log) error {
	m.logs = append(m.logs, logs...)
	return nil
}

func (m *MemoryStorage) Logs(startLogIndex uint64, endLogIndex uint64, limit uint32) ([]Log, error) {
	if len(m.logs) == 0 {
		return nil, nil
	}
	if startLogIndex > uint64(len(m.logs)) {
		return nil, nil
	}
	if endLogIndex == 0 {
		return m.logs[startLogIndex-1:], nil
	}
	if endLogIndex > uint64(len(m.logs)) {
		return m.logs[startLogIndex-1:], nil
	}
	return m.logs[startLogIndex-1 : endLogIndex-1], nil
}

func (m *MemoryStorage) TruncateLogTo(index uint64) error {
	if index > uint64(len(m.logs)) {
		return nil
	}
	if index == 0 {
		m.logs = nil
		return nil
	}
	m.logs = m.logs[:index-1]
	return nil
}

func (m *MemoryStorage) LastIndex() (uint64, error) {
	return uint64(len(m.logs)), nil
}

func (m *MemoryStorage) SetLeaderTermStartIndex(term uint32, index uint64) error {
	m.termStartIndexMap[term] = index
	return nil
}

func (m *MemoryStorage) LeaderLastTerm() (uint32, error) {
	var lastTerm uint32
	for term := range m.termStartIndexMap {
		if term > lastTerm {
			lastTerm = term
		}
	}
	return lastTerm, nil
}

func (m *MemoryStorage) LeaderTermStartIndex(term uint32) (uint64, error) {
	return m.termStartIndexMap[term], nil
}

func (m *MemoryStorage) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	for t := range m.termStartIndexMap {
		if t > term {
			delete(m.termStartIndexMap, t)
		}
	}
	return nil
}
