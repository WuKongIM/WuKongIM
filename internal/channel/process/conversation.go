package process

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"go.uber.org/zap"
)

func (c *Channel) processConversation(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {
	tagKey := messages[0].TagKey
	// 获取和缓存tag
	_, err := c.commonService.GetOrRequestAndMakeTagWithLocal(fakeChannelId, channelType, tagKey)
	if err != nil {
		c.Error("processConversation: get tag failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}
	service.ConversationManager.Push(fakeChannelId, channelType, tagKey, messages)
}
