package options

import "fmt"

type ReactorMsgType uint32

func (r ReactorMsgType) String() string {
	switch r {
	case ReactorUserMsgTypeMin:
		return "ReactorUserMsgTypeMin"
	case ReactorUserMsgTypeMax:
		return "ReactorUserMsgTypeMax"
	case ReactorChannelMsgTypeMin:
		return "ReactorChannelMsgTypeMin"
	case ReactorChannelMsgTypeMax:
		return "ReactorChannelMsgTypeMax"
	default:
		return fmt.Sprintf("ReactorMsgType(%d)", r)
	}
}

func (r ReactorMsgType) Uint32() uint32 {
	return uint32(r)
}

const (
	// reactor user的最小消息类型
	// [min,max)
	ReactorUserMsgTypeMin ReactorMsgType = 2000
	// reactor user的最大消息类型, 不包含max
	ReactorUserMsgTypeMax ReactorMsgType = 3000
	// reactor channel的最小消息类型
	ReactorChannelMsgTypeMin ReactorMsgType = 3001
	// reactor channel的最大消息类型，不包含
	ReactorChannelMsgTypeMax ReactorMsgType = 4000
	// diffuse
	ReactorDiffuseMsgTypeMin ReactorMsgType = 4001
	ReactorDiffuseMsgTypeMax ReactorMsgType = 5000
	// push
	ReactorPushMsgTypeMin ReactorMsgType = 5001
	ReactorPushMsgTypeMax ReactorMsgType = 6000
)
