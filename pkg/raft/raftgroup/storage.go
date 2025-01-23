package raftgroup

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type IStorage interface {
	// AppendLogs 追加日志, 如果termStartIndex不为nil, 则需要保存termStartIndex，最好确保原子性
	AppendLogs(key string, logs []types.Log, termStartIndexInfo *types.TermStartIndexInfo) error
	// GetTermStartIndex 获取指定任期的开始日志下标
	GetTermStartIndex(key string, term uint32) (uint64, error)
	// LeaderLastLogTerm 获取领导的最后一个日志的任期
	LeaderLastLogTerm(key string) (uint32, error)
	//  LeaderTermGreaterEqThan 获取大于或等于term的lastTerm
	LeaderTermGreaterEqThan(key string, term uint32) (uint32, error)

	// GetLogs  获取日志 startLogIndex日志开始下标,endLogIndex结束日志下标 limitSize限制每次查询日志大小,0表示不限制，结果包含startLogIndex不包含 endLogIndex
	GetLogs(key string, startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]types.Log, error)
	// Apply 应用日志
	Apply(key string, logs []types.Log) error
	// TruncateLogTo 截断日志到指定下标，比如 1 2 3 4 5 6 7 8 9 10, TruncateLogTo(r,5) 会截断到 1 2 3 4 5
	TruncateLogTo(key string, index uint64) error
	// DeleteLeaderTermStartIndexGreaterThanTerm 删除大于term的领导任期和开始索引
	DeleteLeaderTermStartIndexGreaterThanTerm(key string, term uint32) error
	// SaveConfig 保存配置
	SaveConfig(key string, cfg types.Config) error
}
