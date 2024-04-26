package clusterevent

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
)

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
	// EventTypeApiServerAddrUpdate api服务地址更新
	EventTypeApiServerAddrUpdate
	// EventTypeNodeOnlieChange 节点在线状态改变
	EventTypeNodeOnlieChange
	// EventTypeSlotNeedElection 槽需要重新选举
	EventTypeSlotNeedElection
	// EventTypeSlotLeaderTransfer 槽leader转移
	EventTypeSlotLeaderTransfer
	// EventTypeSlotElectionStart 槽开始选举
	EventTypeSlotElectionStart
	// EventTypeSlotLeaderTransferCheck 槽leader转移检查(检查槽是否可以进行leader转移了)
	EventTypeSlotLeaderTransferCheck
	// EventTypeSlotLeaderTransferStart 开始槽领导的转移
	EventTypeSlotLeaderTransferStart
	// EventTypeSlotMigratePrepared 槽迁移已准备
	EventTypeSlotMigratePrepared
	// EventTypeSlotLearnerCheck 检查槽的学习者是否达到要求
	EventTypeSlotLearnerCheck
	// EventTypeSlotLearnerToReplica 学习者转换成副本
	EventTypeSlotLearnerToReplica
	// EventTypeNodeJoinSuccess 节点加入成功
	EventTypeNodeJoinSuccess
)

func (e EventType) String() string {
	switch e {
	case EventTypeNodeAdd:
		return "EventTypeNodeAdd"
	case EventTypeNodeUpdate:
		return "EventTypeNodeUpdate"
	case EventTypeNodeDelete:
		return "EventTypeNodeDelete"
	case EventTypeNodeAddResp:
		return "EventTypeNodeAddResp"
	case EventTypeSlotAdd:
		return "EventTypeSlotAdd"
	case EventTypeSlotUpdate:
		return "EventTypeSlotUpdate"
	case EventTypeSlotDelete:
		return "EventTypeSlotDelete"
	case EventTypeApiServerAddrUpdate:
		return "EventTypeApiServerAddrUpdate"
	case EventTypeNodeOnlieChange:
		return "EventTypeNodeOnlieChange"
	case EventTypeSlotNeedElection:
		return "EventTypeSlotNeedElection"
	case EventTypeSlotLeaderTransfer:
		return "EventTypeSlotLeaderTransfer"
	case EventTypeSlotElectionStart:
		return "EventTypeSlotElectionStart"
	case EventTypeSlotLeaderTransferCheck:
		return "EventTypeSlotLeaderTransferCheck"
	case EventTypeSlotLeaderTransferStart:
		return "EventTypeSlotLeaderTransferStart"
	case EventTypeSlotMigratePrepared:
		return "EventTypeSlotMigratePrepared"
	case EventTypeSlotLearnerCheck:
		return "EventTypeSlotLearnerCheck"
	case EventTypeSlotLearnerToReplica:
		return "EventTypeSlotLearnerToReplica"
	case EventTypeNodeJoinSuccess:
		return "EventTypeNodeJoinSuccess"
	}
	return fmt.Sprintf("EventTypeNone[%d]", e)

}

type Message struct {
	Type         EventType         // 事件类型
	Nodes        []*pb.Node        // 节点
	Slots        []*pb.Slot        // 槽
	SlotMigrates []*pb.SlotMigrate // 槽迁移
}
