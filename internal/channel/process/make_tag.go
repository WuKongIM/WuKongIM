package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 给消息打标签
func (c *Channel) processMakeTag(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {

	// 记录消息轨迹
	for _, m := range messages {
		m.Track.Record(track.PositionChannelMakeTag)
	}

	// 获取或创建tag
	tag, err := c.getOrMakeTag(fakeChannelId, channelType)
	if err != nil {
		c.Error("processConversation: get or make tag failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	hasLocalNode := false // 如果有本地节点投递任务
	for _, node := range tag.Nodes {
		if options.G.IsLocalNode(node.LeaderId) {
			hasLocalNode = true
			break
		}
	}
	// 给消息打标签
	for _, m := range messages {
		// 打上标签
		m.TagKey = tag.Key
		// 去分发
		m.MsgType = reactor.ChannelMsgDiffuse
	}

	// 标签包含本地节点，才需要分发
	if hasLocalNode {
		reactor.Channel.AddMessages(fakeChannelId, channelType, messages)
	}

	// 发送消息给对应节点去分发
	var nodeMessages []*reactor.ChannelMessage
	for _, node := range tag.Nodes {
		for _, m := range messages {
			if !options.G.IsLocalNode(node.LeaderId) {
				if nodeMessages == nil {
					nodeMessages = make([]*reactor.ChannelMessage, 0, len(messages))
				}
				cloneMsg := m.Clone()
				cloneMsg.ToNode = node.LeaderId
				nodeMessages = append(nodeMessages, cloneMsg)
			}
		}
	}
	if len(nodeMessages) > 0 {
		reactor.Channel.AddMessagesToOutbound(fakeChannelId, channelType, nodeMessages)
	}
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
	service.TagManager.SetChannelTag(orgFakeChannelId, channelType, tag.Key)
	return tag, nil
}
