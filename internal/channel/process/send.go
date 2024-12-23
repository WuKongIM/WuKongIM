package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/track"
)

func (c *Channel) processSend(channelId string, channelType uint8, role reactor.Role, msgs []*reactor.ChannelMessage) {
	if role == reactor.RoleFollower {
		// 如果是追随者则不处理发送消息,转给领导
		for _, m := range msgs {
			// 在线cmd消息进入makeTag环节
			if options.G.IsOnlineCmdChannel(channelId) {
				m.MsgType = reactor.ChannelMsgMakeTag
			} else {
				m.MsgType = reactor.ChannelMsgPermission
			}
		}
		reactor.Channel.AddMessagesToOutbound(channelId, channelType, msgs)
		return
	}

	for _, m := range msgs {
		// 记录轨迹
		m.Track.Record(track.PositionChannelOnSend)
		// 在线cmd消息进入makeTag环节
		if options.G.IsOnlineCmdChannel(channelId) {
			m.MsgType = reactor.ChannelMsgMakeTag
		} else {
			m.MsgType = reactor.ChannelMsgPermission
		}

	}
	reactor.Channel.AddMessages(channelId, channelType, msgs)

}
