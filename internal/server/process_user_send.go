package server

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (p *processUser) handleSend(msg *reactor.UserMessage) {
	sendPacket := msg.Frame.(*wkproto.SendPacket)
	channelId := sendPacket.ChannelID
	channelType := sendPacket.ChannelType

	// 根据需要唤醒频道
	reactor.Channel.WakeIfNeed(channelId, channelType)

	// 添加消息到频道
	reactor.Channel.AddMessage(channelId, channelType, &reactor.ChannelMessage{
		Conn:       msg.Conn,
		SendPacket: sendPacket,
		MsgType:    reactor.ChannelMsgSend,
	})
}
