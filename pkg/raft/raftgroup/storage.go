package raftgroup

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type IStorage interface {
	// AppendLogs 追加日志, 如果termStartIndex不为nil, 则需要保存termStartIndex，最好确保原子性
	AppendLogs(raft IRaft, logs []types.Log, termStartIndexInfo *types.TermStartIndexInfo) error
	// GetTermStartIndex 获取指定任期的开始日志下标
	GetTermStartIndex(raft IRaft, term uint32) (uint64, error)
	// LeaderLastLogTerm 获取领导的最后一个日志的任期
	LeaderLastLogTerm(raft IRaft) (uint32, error)
	// GetLogs 获取日志 start日志开始下标 limit限制查询数量，结果包含start
	GetLogs(raft IRaft, startLogIndex, limit uint64) ([]types.Log, error)
	// GetState 获取raft状态
	GetState(raft IRaft) (*types.RaftState, error)
	// Apply 应用日志
	Apply(raft IRaft, logs []types.Log) error
	// TruncateLogTo 截断日志到指定下标，比如 1 2 3 4 5 6 7 8 9 10, TruncateLogTo(r,5) 会截断到 1 2 3 4 5
	TruncateLogTo(raft IRaft, index uint64) error
	// DeleteLeaderTermStartIndexGreaterThanTerm 删除大于term的领导任期和开始索引
	DeleteLeaderTermStartIndexGreaterThanTerm(raft IRaft, term uint32) error
}
