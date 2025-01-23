package raft

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type Storage interface {
	// AppendLogs 追加日志, 如果termStartIndex不为nil, 则需要保存termStartIndex，最好确保原子性
	AppendLogs(logs []types.Log, termStartIndex *types.TermStartIndexInfo) error
	// GetLogs 获取日志 startLogIndex日志开始下标,endLogIndex结束日志下标 limitSize限制每次查询日志大小，0表示不限制，结果包含startLogIndex不包含 endLogIndex
	GetLogs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]types.Log, error)
	// GetState 获取状态
	GetState() (types.RaftState, error)
	// GetTermStartIndex 获取指定任期的开始日志下标
	GetTermStartIndex(term uint32) (uint64, error)

	//  LeaderTermGreaterEqThan 获取大于或等于term的lastTerm
	LeaderTermGreaterEqThan(term uint32) (uint32, error)

	// LeaderLastTerm 获取领导者的最后一个日志任期
	LeaderLastTerm() (uint32, error)

	// TruncateLogTo 截断日志到指定下标，比如 1 2 3 4 5 6 7 8 9 10, TruncateLogTo(5) 会截断到 1 2 3 4 5
	TruncateLogTo(index uint64) error
	// DeleteLeaderTermStartIndexGreaterThanTerm 删除大于term的领导任期和开始索引
	DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error
	// Apply 应用日志
	Apply(logs []types.Log) error
	// SaveConfig 保存配置
	SaveConfig(cfg types.Config) error
}
