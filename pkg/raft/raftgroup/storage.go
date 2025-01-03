package raftgroup

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type IStorage interface {
	// AppendLogs 追加日志, 如果termStartIndex不为nil, 则需要保存termStartIndex，最好确保原子性
	AppendLogs(raft IRaft, logs []types.Log, termStartIndexInfo *types.TermStartIndexInfo) error
	// GetTermStartIndex 获取指定任期的开始日志下标
	GetTermStartIndex(raft IRaft, term uint32) (uint64, error)
	// LeaderLastLogTerm 获取领导的最后一个日志的任期
	LeaderLastLogTerm(raft IRaft) (uint32, error)
	// GetLogs 获取日志 start日志开始下标 maxSize最大数量，结果包含start
	GetLogs(raft IRaft, start, maxSize uint64) ([]types.Log, error)
}
