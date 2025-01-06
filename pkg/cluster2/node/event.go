package node

import "github.com/WuKongIM/WuKongIM/pkg/cluster2/node/pb"

type EventType uint8

const (
	Unknown EventType = iota
	// NodeAdd 节点添加
	NodeAdd
)

type Event struct {
	// Type 事件类型
	Type EventType

	// Nodes 节点集合
	Nodes []*pb.Node

	// Slots 槽位集合
	Slots []*pb.Slot
}
