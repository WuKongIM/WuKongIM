package process

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 扩散消息
func (c *Channel) processDiffuse(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {

	// 在线cmd消息
	if options.G.IsOnlineCmdChannel(fakeChannelId) {
		c.processDiffuseForOnlineCmdChannel(fakeChannelId, channelType, messages)
		return
	}

	tagKey := messages[0].TagKey
	tag, err := c.commonService.GetOrRequestAndMakeTagWithLocal(fakeChannelId, channelType, tagKey)
	if err != nil {
		c.Error("processDiffuse: get tag failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType), zap.String("tagKey", tagKey))
		return
	}
	if tag == nil {
		c.Error("processDiffuse: tag not found", zap.String("tagKey", tagKey), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	// 处理tag消息
	c.processDiffuseByTag(fakeChannelId, channelType, messages, tag)

}

func (c *Channel) processDiffuseByTag(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage, tag *types.Tag) {
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
	var offlineUids []string // 需要推离线的用户
	for _, node := range tag.Nodes {
		if !options.G.IsLocalNode(node.LeaderId) {
			continue
		}

		for _, uid := range node.Uids {
			if options.G.IsSystemUid(uid) {
				continue
			}
			if !c.isOnline(uid) {
				continue
			}
			if !c.masterDeviceIsOnline(uid) {
				if offlineUids == nil {
					offlineUids = make([]string, 0, len(node.Uids))
				}
				offlineUids = append(offlineUids, uid)
			}

			for _, msg := range messages {

				if pubshMessages == nil {
					pubshMessages = make([]*reactor.ChannelMessage, 0, len(messages)*len(node.Uids))
				}
				cloneMsg := msg.Clone()
				cloneMsg.ToUid = uid
				cloneMsg.MsgType = reactor.ChannelMsgPushOnline
				pubshMessages = append(pubshMessages, cloneMsg)
			}
		}
	}
	if len(pubshMessages) > 0 {
		reactor.Push.PushMessages(pubshMessages)
	}

	if len(offlineUids) > 0 {
		offlineMessages := make([]*reactor.ChannelMessage, 0, len(messages))
		for _, msg := range messages {
			cloneMsg := msg.Clone()
			cloneMsg.OfflineUsers = offlineUids
			cloneMsg.MsgType = reactor.ChannelMsgPushOffline
			offlineMessages = append(offlineMessages, cloneMsg)
		}
		reactor.Push.PushMessages(offlineMessages)
	}
}

func (c *Channel) processDiffuseForOnlineCmdChannel(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	leaderId, err := service.Cluster.LeaderIdOfChannel(timeoutCtx, fakeChannelId, channelType)
	cancel()
	if err != nil {
		wklog.Error("processDiffuseForOnlineCmdChannel: getLeaderOfChannel failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	// // 按照tagKey分组消息
	tagKeyMessages := c.groupMessagesByTagKey(messages)
	for tagKey, msgs := range tagKeyMessages {
		if tagKey == "" {
			c.Warn("processDiffuseForOnlineCmdChannel: tagKey is nil", zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
			continue
		}
		var tag *types.Tag

		if options.G.IsLocalNode(leaderId) {
			tag = service.TagManager.Get(tagKey)
		} else {
			tag, err = c.requestTag(leaderId, tagKey)
			if err != nil {
				c.Error("processDiffuseForOnlineCmdChannel: request tag failed", zap.Error(err), zap.String("tagKey", tagKey), zap.String("fakeChannelId", fakeChannelId))
				return
			}
		}
		if tag == nil {
			c.Error("processDiffuseForOnlineCmdChannel: tag not found", zap.String("tagKey", tagKey), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
			return
		}
		// 处理tag消息
		c.processDiffuseByTag(fakeChannelId, channelType, msgs, tag)
	}
}

// 请求tag
func (c *Channel) requestTag(leaderId uint64, tagKey string) (*types.Tag, error) {
	// 去领导节点请求
	tagResp, err := c.client.RequestTag(leaderId, &ingress.TagReq{
		TagKey: tagKey,
		NodeId: options.G.Cluster.NodeId,
	})
	if err != nil {
		c.Error("processDiffuseForOnlineCmdChannel: get tag failed", zap.Error(err), zap.Uint64("leaderId", leaderId))
		return nil, err
	}
	tag, err := service.TagManager.MakeTagNotCacheWithTagKey(tagKey, tagResp.Uids)
	if err != nil {
		c.Error("processDiffuseForOnlineCmdChannel: MakeTagNotCacheWithTagKey failed", zap.Error(err))
		return nil, err
	}
	return tag, nil
}

func (c *Channel) isOnline(uid string) bool {
	toConns := reactor.User.AuthedConnsByUid(uid)
	return len(toConns) > 0
}

// 用户的主设备是否在线
func (c *Channel) masterDeviceIsOnline(uid string) bool {
	toConns := reactor.User.AuthedConnsByUid(uid)
	online := false
	for _, conn := range toConns {
		if conn.DeviceLevel == wkproto.DeviceLevelMaster {
			online = true
			break
		}
	}
	return online
}
