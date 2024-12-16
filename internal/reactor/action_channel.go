package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

type ChannelActionType uint8

const (
	// 未知
	ChannelActionUnknown ChannelActionType = iota
	// 选举
	ChannelActionElection
	// 配置更新
	ChannelActionConfigUpdate
	// 加入
	ChannelActionJoin
	ChannelActionJoinResp
	// 心跳
	ChannelActionHeartbeatReq
	ChannelActionHeartbeatResp
	// 收件箱
	ChannelActionInboundAdd
	ChannelActionInbound

	// 发件箱
	ChannelActionOutboundAdd
	ChannelActionOutboundForward
	ChannelActionOutboundForwardResp

	// 关闭
	ChannelActionClose
)

func (c ChannelActionType) String() string {
	switch c {
	case ChannelActionElection:
		return "election"
	case ChannelActionConfigUpdate:
		return "config_update"
	case ChannelActionJoin:
		return "join"
	case ChannelActionJoinResp:
		return "join_resp"
	case ChannelActionHeartbeatReq:
		return "heartbeat_req"
	case ChannelActionHeartbeatResp:
		return "heartbeat_resp"
	case ChannelActionInboundAdd:
		return "inbound_add"
	case ChannelActionInbound:
		return "inbound"
	case ChannelActionOutboundAdd:
		return "outbound_add"
	case ChannelActionOutboundForward:
		return "outbound_forward"
	case ChannelActionOutboundForwardResp:
		return "outbound_forward_resp"
	case ChannelActionClose:
		return "close"
	default:
		return fmt.Sprintf("unknown ChannelActionType: %d", c)
	}
}

type ChannelAction struct {
	No            string
	Type          ChannelActionType
	Key           string
	FakeChannelId string
	ChannelType   uint8
	From          uint64
	To            uint64
	Messages      []*ChannelMessage
	// 频道配置
	Cfg  ChannelConfig
	Role Role
	// 频道信息
	ChannelInfo wkdb.ChannelInfo
}

func (c ChannelAction) Size() uint64 {
	return 0
}
