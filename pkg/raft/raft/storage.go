package raft

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type Storage interface {
	// AppendLogs 追加日志, 如果termStartIndex不为nil, 则需要保存termStartIndex，最好确保原子性
	AppendLogs(logs []types.Log, termStartIndex *types.TermStartIndexInfo) error
	// GetLogs 获取日志 start日志开始下标 maxSize最大数量，结果包含start
	GetLogs(start, maxSize uint64) ([]types.Log, error)
	// GetState 获取状态
	GetState() (RaftState, error)
	// GetTermStartIndex 获取指定任期的开始日志下标
	GetTermStartIndex(term uint32) (uint64, error)
	// TruncateLogTo 截断日志到指定下标，比如 1 2 3 4 5 6 7 8 9 10, TruncateLogTo(5) 会截断到 1 2 3 4 5
	TruncateLogTo(index uint64) error

	// DeleteLeaderTermStartIndexGreaterThanTerm 删除大于term的领导任期和开始索引
	DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error
}

type RaftState struct {
	// LastLogIndex 最后一个日志的下标
	LastLogIndex uint64
	// LastTerm 最后一个日志的任期
	LastTerm uint32
	// AppliedIndex 已应用的日志下标
	AppliedIndex uint64
}
