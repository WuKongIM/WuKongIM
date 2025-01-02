package raft

type Storage interface {
	// AppendLogs 追加日志, 如果termStartIndex不为nil, 则需要保存termStartIndex，最好确保原子性
	AppendLogs(logs []Log, termStartIndex *TermStartIndex) error
	// GetLogs 获取日志 [start, end)
	GetLogs(start, end uint64) ([]Log, error)
	// GetState 获取状态
	GetState() (RaftState, error)
	// GetTermStartIndex 获取指定任期的开始日志下标
	GetTermStartIndex(term uint32) (uint64, error)
	// TruncateLogTo 截断日志到指定下标，比如 1 2 3 4 5 6 7 8 9 10, TruncateLogTo(5) 会截断到 1 2 3 4 5
	TruncateLogTo(index uint64) error
}

type RaftState struct {
	// LastLogIndex 最后一个日志的下标
	LastLogIndex uint64
	// LastTerm 最后一个日志的任期
	LastTerm uint32
	// AppliedIndex 已应用的日志下标
	AppliedIndex uint64
}
