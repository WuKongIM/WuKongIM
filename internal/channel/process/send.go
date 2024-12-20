package process

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/track"
)

func (c *Channel) processSend(channelId string, channelType uint8, role reactor.Role, msgs []*reactor.ChannelMessage) {
	if role == reactor.RoleFollower {
		// 如果是追随者则不处理发送消息,转给领导
		reactor.Channel.AddMessagesToOutbound(channelId, channelType, msgs)
		return
	}

	for _, m := range msgs {
		// 记录轨迹
		m.Track.Record(track.PositionChannelOnSend)

		// 进入权限判断
		m.MsgType = reactor.ChannelMsgPermission

	}
	reactor.Channel.AddMessages(channelId, channelType, msgs)

}
