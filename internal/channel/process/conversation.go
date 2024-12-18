package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"go.uber.org/zap"
)

func (c *Channel) processConversation(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {
	fmt.Println("processConversation-->")
	tagKey := messages[0].TagKey
	// 获取和缓存tag
	_, err := c.commonService.GetOrRequestAndMakeTag(fakeChannelId, channelType, tagKey)
	if err != nil {
		c.Error("processConversation: get tag failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}
	service.ConversationManager.Push(fakeChannelId, channelType, tagKey, messages)

	for _, message := range messages {
		message.MsgType = reactor.ChannelMsgDiffuse
	}
	reactor.Channel.AddMessages(fakeChannelId, channelType, messages)

	// fmt.Println("processConversation--->")
	// tag, err := c.getOrMakeTag(fakeChannelId, channelType)
	// if err != nil {
	// 	c.Error("processConversation: get or make tag failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
	// 	return
	// }
	// err := c.makeTagIfNotExist(fakeChannelId, channelType)
	// if err != nil {
	// 	c.Error("processConversation: makeChannelTag failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
	// 	return
	// }

	// fmt.Println("get tag------>", tag.String())

	// localNodeExist := false
	// for _, node := range tag.Nodes {
	// 	if options.G.IsLocalNode(node.LeaderId) {
	// 		localNodeExist = true
	// 		break
	// 	}
	// }
	// // 如果本节点存在订阅者，则更新最近会话
	// if localNodeExist {
	// 	service.ConversationManager.Push(fakeChannelId, channelType, tag.Key, messages)
	// }

	// for _, node := range tag.Nodes {
	// 	if !options.G.IsLocalNode(node.LeaderId) {
	// 		for _, m := range messages {
	// 			cloneMsg := m.Clone()
	// 			cloneMsg.TagKey = tag.Key
	// 			cloneMsg.MsgType = reactor.ChannelMsgDiffuse
	// 			cloneMsg.ToNode = node.LeaderId
	// 			// 如果不是当前节点的消息，则发送到对应节点
	// 			fmt.Println("forward--->", cloneMsg.ToNode, node.Uids)
	// 			reactor.Diffuse.AddMessageToOutbound(cloneMsg)
	// 		}
	// 	}
	// }
}
