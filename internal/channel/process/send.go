package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
)

func (c *Channel) processSend(role reactor.Role, m *reactor.ChannelMessage) {
	if role == reactor.RoleFollower {
		// 如果是追随者则不处理发送消息,转给领导
		reactor.Channel.AddMessageToOutbound(m)
		return
	}
	// 如果是系统发送的消息则不做权限判断，直接存储
	if options.G.IsSystemDevice(m.Conn.DeviceId) {
		m.MsgType = reactor.ChannelMsgStorage
	} else {
		m.MsgType = reactor.ChannelMsgPermission
	}
	reactor.Channel.AddMessage(m)
}
