package reactor

import "fmt"

type DiffuseActionType uint8

const (
	// 未知
	DiffuseActionUnknown DiffuseActionType = iota
	// 收件箱
	DiffuseActionInboundAdd
	DiffuseActionInbound
	// 发件箱
	DiffuseActionOutboundAdd
	DiffuseActionOutboundForward
)

func (d DiffuseActionType) String() string {
	switch d {
	case DiffuseActionInboundAdd:
		return "DiffuseActionInboundAdd"
	case DiffuseActionInbound:
		return "DiffuseActionInbound"
	case DiffuseActionOutboundAdd:
		return "DiffuseActionOutboundAdd"
	case DiffuseActionOutboundForward:
		return "DiffuseActionOutboundForward"
	}
	return fmt.Sprintf("unknown DiffuseActionType: %d", d)
}

type DiffuseAction struct {
	// 如果没指定，则随机分配
	WorkerId int
	Type     DiffuseActionType
	// 消息
	Messages []*ChannelMessage
	To       uint64
}

func (d DiffuseAction) Size() uint64 {
	var size uint64
	for _, m := range d.Messages {
		size += m.Size()
	}
	return size
}
