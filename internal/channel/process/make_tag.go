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

	// 在线cmd消息
	if options.G.IsOnlineCmdChannel(fakeChannelId) {
		c.processMakeTagForOnlineCmdChannel(fakeChannelId, channelType, messages)
		return
	}

	// 获取或创建tag
	tag, err := c.getOrMakeTag(fakeChannelId, channelType)
	if err != nil {
		c.Error("processMakeTag: get or make tag failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	if tag == nil {
		c.Error("processMakeTag: get or make tag failed, tag is nil", zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	// 处理tag消息
	c.processMakeTagByTag(fakeChannelId, channelType, messages, tag)
}

func (c *Channel) processMakeTagByTag(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage, tag *types.Tag) {
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

// 处理cmd消息
func (c *Channel) processMakeTagForOnlineCmdChannel(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {
	// // 按照tagKey分组消息
	tagKeyMessages := c.groupMessagesByTagKey(messages)
	for tagKey, msgs := range tagKeyMessages {
		if tagKey == "" {
			c.Warn("processMakeTagForOnlineCmdChannel: tagKey is nil", zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
			continue
		}
		// 获取tag
		tag := service.TagManager.Get(tagKey)
		if tag == nil {
			c.Error("processMakeTagForOnlineCmdChannel: tag not found", zap.String("tagKey", tagKey), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
			continue
		}
		// 处理tag消息
		c.processMakeTagByTag(fakeChannelId, channelType, msgs, tag)
	}
}

// 按照tagKey分组消息
func (c *Channel) groupMessagesByTagKey(messages []*reactor.ChannelMessage) map[string][]*reactor.ChannelMessage {
	tagKeyMessages := make(map[string][]*reactor.ChannelMessage)
	for _, m := range messages {
		tagKeyMessages[m.TagKey] = append(tagKeyMessages[m.TagKey], m)
	}
	return tagKeyMessages
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
		// 如果是命令频道，不能创建tag，只能去原频道获取
		if options.G.IsCmdChannel(fakeChannelId) {
			tag, err = c.commonService.GetOrRequestAndMakeTag(orgFakeChannelId, channelType, tagKey, 0)
			if err != nil {
				c.Error("processMakeTag: getOrRequestAndMakeTag failed", zap.Error(err), zap.String("tagKey", tagKey), zap.String("orgFakeChannelId", orgFakeChannelId))
				return nil, err
			}
		} else {
			tag, err = c.makeChannelTag(orgFakeChannelId, channelType)
			if err != nil {
				c.Error("processMakeTag: makeTag failed", zap.Error(err), zap.String("tagKey", tagKey))
				return nil, err
			}
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
			c.Error("processMakeTag: getSubscribers failed", zap.Error(err), zap.String("orgFakeChannelId", orgFakeChannelId), zap.Uint8("channelType", channelType))
			return nil, err
		}
		for _, member := range members {
			subscribers = append(subscribers, member.Uid)
		}
	}
	tag, err := service.TagManager.MakeTag(subscribers)
	if err != nil {
		c.Error("processMakeTag: makeTag failed", zap.Error(err), zap.String("orgFakeChannelId", orgFakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}
	service.TagManager.SetChannelTag(orgFakeChannelId, channelType, tag.Key)
	return tag, nil
}
