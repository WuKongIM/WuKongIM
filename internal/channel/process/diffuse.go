package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"go.uber.org/zap"
)

// 扩散消息
func (c *Channel) processDiffuse(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {
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

	// 更新最近会话
	var conversationMessages []*reactor.ChannelMessage
	for _, m := range messages {
		if m.SendPacket.NoPersist {
			continue
		}
		if conversationMessages == nil {
			conversationMessages = make([]*reactor.ChannelMessage, 0, len(messages))
		}
		m.MsgType = reactor.ChannelMsgConversationUpdate
		conversationMessages = append(conversationMessages, m)
	}

	if len(conversationMessages) > 0 {
		reactor.Channel.AddMessages(fakeChannelId, channelType, conversationMessages)
	}

	// 推送属于自己节点的用户消息
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
	if len(pubshMessages) > 0 {
		reactor.Push.PushMessages(pubshMessages)
	}

}
