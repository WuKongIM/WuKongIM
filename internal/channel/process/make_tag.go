package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 给消息打标签
func (c *Channel) processMakeTag(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {
	// fmt.Println("processConversation--->")
	tag, err := c.getOrMakeTag(fakeChannelId, channelType)
	if err != nil {
		c.Error("processConversation: get or make tag failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	fmt.Println("get tag------>", tag.String())
	// 给消息打标签
	for _, m := range messages {
		m.TagKey = tag.Key
	}

	// 如果不是本地节点，则转发消息
	for _, node := range tag.Nodes {
		for _, m := range messages {
			if m.SendPacket.NoPersist {
				m.MsgType = reactor.ChannelMsgDiffuse
			} else {
				m.MsgType = reactor.ChannelMsgConversationUpdate
			}
			// 如果非本地节点，则发送到对应节点
			if !options.G.IsLocalNode(node.LeaderId) {
				cloneMsg := m.Clone()
				cloneMsg.ToNode = node.LeaderId
				reactor.Channel.AddMessageToOutboundNoAdvance(cloneMsg)
			}
		}
	}
	reactor.Channel.AddMessages(fakeChannelId, channelType, messages)
}

func (c *Channel) getOrMakeTag(fakeChannelId string, channelType uint8) (*types.Tag, error) {
	var (
		tag              *types.Tag
		err              error
		orgFakeChannelId string = fakeChannelId
	)
	if options.G.IsCmdChannel(fakeChannelId) {
		orgFakeChannelId = options.G.CmdChannelConvertOrginalChannel(fakeChannelId)
	}
	tagKey := service.TagManager.GetChannelTag(orgFakeChannelId, channelType)
	if tagKey != "" {
		tag = service.TagManager.Get(tagKey)
	}
	if tag == nil {
		tag, err = c.makeChannelTag(orgFakeChannelId, channelType)
		if err != nil {
			c.Error("diffuse: makeTag failed", zap.Error(err), zap.String("tagKey", tagKey))
			return nil, err
		}
	}
	return tag, nil
}

func (c *Channel) makeChannelTag(fakeChannelId string, channelType uint8) (*types.Tag, error) {

	var (
		subscribers      []string
		orgFakeChannelId string = fakeChannelId
	)

	if options.G.IsCmdChannel(fakeChannelId) {
		// 处理命令频道
		orgFakeChannelId = options.G.CmdChannelConvertOrginalChannel(fakeChannelId)
	}
	if channelType == wkproto.ChannelTypePerson { // 个人频道
		u1, u2 := options.GetFromUIDAndToUIDWith(orgFakeChannelId)
		subscribers = append(subscribers, u1, u2)
	} else {
		members, err := service.Store.GetSubscribers(orgFakeChannelId, channelType)
		if err != nil {
			c.Error("diffuse: getSubscribers failed", zap.Error(err), zap.String("orgFakeChannelId", orgFakeChannelId), zap.Uint8("channelType", channelType))
			return nil, err
		}
		for _, member := range members {
			subscribers = append(subscribers, member.Uid)
		}
	}
	tag, err := service.TagManager.MakeTag(subscribers)
	if err != nil {
		c.Error("diffuse: makeTag failed", zap.Error(err), zap.String("orgFakeChannelId", orgFakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}
	fmt.Println("MakeTag--->", tag.Key, orgFakeChannelId, channelType)
	service.TagManager.SetChannelTag(orgFakeChannelId, channelType, tag.Key)
	return tag, nil
}
