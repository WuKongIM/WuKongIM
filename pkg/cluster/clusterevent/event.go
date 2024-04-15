package clusterevent

import "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"

type EventType int

const (
	EventTypeNone EventType = iota
	// EventTypeNodeAdd 节点添加
	EventTypeNodeAdd
	// EventTypeNodeUpdate 节点更新
	EventTypeNodeUpdate
	// EventTypeNodeDelete 节点删除
	EventTypeNodeDelete
	// EventTypeNodeAddResp 节点添加响应
	EventTypeNodeAddResp
	// EventTypeSlotAdd 槽添加
	EventTypeSlotAdd
	// EventTypeSlotUpdate 槽更新
	EventTypeSlotUpdate
	// EventTypeSlotDelete 槽删除
	EventTypeSlotDelete
)

type Message struct {
	Type         EventType
	Nodes        []*pb.Node
	Slots        []*pb.Slot
	SlotMigrates []*pb.SlotMigrate
}
