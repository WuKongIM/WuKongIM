package reactor

import "fmt"

type PushActionType uint8

const (
	// 未知
	PushActionUnknown PushActionType = iota
	// 收件箱
	PushActionInboundAdd
	PushActionInbound
	// 发件箱
	PushActionOutboundAdd
	PushActionOutboundForward
)

func (d PushActionType) String() string {
	switch d {
	case PushActionInboundAdd:
		return "PushActionInboundAdd"
	case PushActionInbound:
		return "PushActionInbound"
	case PushActionOutboundAdd:
		return "PushActionOutboundAdd"
	case PushActionOutboundForward:
		return "PushActionOutboundForward"
	}
	return fmt.Sprintf("unknown PushActionType: %d", d)
}

type PushAction struct {
	// 如果没指定，则随机分配
	WorkerId int
	Type     PushActionType
	// 消息
	Messages []*ChannelMessage
	To       uint64
}

func (d PushAction) Size() uint64 {
	var size uint64
	for _, m := range d.Messages {
		size += m.Size()
	}
	return size
}
