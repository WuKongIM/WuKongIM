package raftgroup

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type IRaft interface {
	// Key 获取当前节点的key
	Key() string
	// HasReady 是否有事件
	HasReady() bool
	// Ready 获取当前节点的事件
	Ready() []types.Event
	// Step 处理事件
	Step(event types.Event) error
	// Tick 时钟周期
	Tick()
	// LeaderId 获得领导者ID
	LeaderId() uint64
	// IsLeader 是否是领导者
	IsLeader() bool
	// LastLogIndex 获取最后一个日志的索引
	LastLogIndex() uint64
	// LastLogTerm 获取最新的任期
	LastTerm() uint32
	// CommittedIndex 已提交的下标
	CommittedIndex() uint64
	// AppliedIndex 已应用的日志下标
	AppliedIndex() uint64

	// NodeId 当前节点id
	NodeId() uint64
	// Lock 锁
	Lock()
	Unlock()
	// 获取raft配置
	Config() types.Config
}
