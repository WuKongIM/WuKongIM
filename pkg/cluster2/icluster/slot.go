package icluster

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type Slot interface {
	// SlotLeaderId 获取槽领导节点的id
	SlotLeaderId(slotId uint32) (nodeId uint64)
	//  GetSlotId 获取槽ID
	GetSlotId(v string) uint32
	// ProposeUntilApplied 提交数据直到应用
	ProposeUntilApplied(slotId uint32, data []byte) (*types.ProposeResp, error)
	// Propose 提交数据
	Propose(slotId uint32, data []byte) (*types.ProposeResp, error)
}
