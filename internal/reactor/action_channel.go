package reactor

type ChannelActionType uint8

const (
	// 未知
	ChannelActionUnknown ChannelActionType = iota
	// 选举
	ChannelActionElection
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
)

type ChannelAction struct {
}
