package node

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/pb"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

type EventType uint8

const (
	Unknown EventType = iota

	// RaftEvent raft事件
	RaftEvent
	// NodeAdd 节点添加
	NodeAdd
)

type Event struct {
	// Type 事件类型
	Type EventType

	From uint64

	To uint64

	// Nodes 节点集合
	Nodes []*pb.Node

	// Slots 槽位集合
	Slots []*pb.Slot

	Event types.Event
}
