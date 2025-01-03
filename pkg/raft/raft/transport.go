package raft

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type Transport interface {
	// Send 发送事件
	Send(event types.Event)
}
