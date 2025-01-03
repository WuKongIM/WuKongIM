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
}
