package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"go.uber.org/zap"
)

// 扩散消息
func (c *Channel) processDiffuse(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {
	fmt.Println("processDiffuse--->", fakeChannelId, channelType, len(messages))
	tagKey := messages[0].TagKey
	tag, err := c.commonService.GetOrRequestAndMakeTag(fakeChannelId, channelType, tagKey)
	if err != nil {
		c.Error("processDiffuse: get tag failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType), zap.String("tagKey", tagKey))
		return
	}
	if tag == nil {
		c.Error("processDiffuse: tag not found", zap.String("tagKey", tagKey), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	fmt.Println("processDiffuse-2-->", tag.String())

	// 根据用户所在节点，将消息推送到对应节点
	var pubshMessages []*reactor.ChannelMessage
	for _, node := range tag.Nodes {
		if !options.G.IsLocalNode(node.LeaderId) {
			continue
		}
		for _, msg := range messages {
			for _, uid := range node.Uids {
				if pubshMessages == nil {
					pubshMessages = make([]*reactor.ChannelMessage, 0, len(messages)*len(node.Uids))
				}
				cloneMsg := msg.Clone()
				cloneMsg.ToUid = uid
				cloneMsg.MsgType = reactor.ChannelMsgPush
				pubshMessages = append(pubshMessages, cloneMsg)

			}
		}
	}
	fmt.Println("processDiffuse--->", len(pubshMessages))
	if len(pubshMessages) > 0 {
		reactor.Push.PushMessages(pubshMessages)
	}

}
