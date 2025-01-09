package icluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

type Api interface {
	// RequestProposeBatchUntilApplied 向指定节点请求频道提按
	RequestChannelProposeBatchUntilApplied(nodeId uint64, channelId string, channelType uint8, reqs types.ProposeReqSet) (types.ProposeRespSet, error)
}
