package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 扩散消息
func (c *Channel) processDiffuse(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {
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

func (c *Channel) isOnline(uid string) bool {
	toConns := reactor.User.ConnsByUid(uid)
	return len(toConns) > 0
}

// 用户的主设备是否在线
func (c *Channel) masterDeviceIsOnline(uid string) bool {
	toConns := reactor.User.ConnsByUid(uid)
	online := false
	for _, conn := range toConns {
		if conn.DeviceLevel == wkproto.DeviceLevelMaster {
			online = true
			break
		}
	}
	return online
}
