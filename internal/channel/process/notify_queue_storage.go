package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"go.uber.org/zap"
)

func (c *Channel) processStorageNotifyQueue(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {

	var err error
	if options.G.WebhookOn(types.EventMsgNotify) {
		err = service.Store.AppendMessageOfNotifyQueue(c.toStorageMessages(messages))
		if err != nil {
			c.Error("store notify queue message failed", zap.Error(err), zap.Int("msgs", len(messages)), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		}
	}

	// 通知队列的消息不管有没有存储成功，都可以发送回执，因为消息本身已经存储成功
	for _, m := range messages {
		m.MsgType = reactor.ChannelMsgSendack
	}
	reactor.Channel.AddMessages(fakeChannelId, channelType, messages)
}
