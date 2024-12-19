package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"go.uber.org/zap"
)

// webhook通知队列
func (c *Channel) processStorageNotifyQueue(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {

	var err error
	if options.G.WebhookOn(types.EventMsgNotify) {
		err = service.Store.AppendMessageOfNotifyQueue(c.toStorageMessages(messages))
		if err != nil {
			c.Error("store notify queue message failed", zap.Error(err), zap.Int("msgs", len(messages)), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		}
	}
}
