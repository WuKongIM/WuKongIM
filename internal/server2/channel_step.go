package server

import (
	"fmt"
)

func (c *channel) step(a *ChannelAction) error {

	switch a.ActionType {
	case ChannelActionInitResp: // 初始化返回
		if a.Reason == ReasonSuccess {
			c.status = channelStatusInitialized
			if a.LeaderId == c.r.opts.Cluster.NodeId {
				c.becomeLeader()
			} else {
				c.becomeProxy(a.LeaderId)
			}

		} else {
			c.status = channelStatusUninitialized
		}
	case ChannelActionSend: // 发送
		c.appendMessage(a.Messages...) // 消息是按照发送者分组的，所以取第一个即可
	case ChannelActionPayloadDecryptResp: // paylaod解密
		c.payloadDecrypting = false
		if len(a.Messages) == 0 {
			return nil
		}
		lastMsg := a.Messages[len(a.Messages)-1]
		if lastMsg.Index > c.msgQueue.payloadDecryptedIndex {
			c.msgQueue.payloadDecryptedIndex = lastMsg.Index
		}
		for _, decryptMsg := range a.Messages {
			for _, msg := range c.msgQueue.messages {
				if msg.MessageId == decryptMsg.MessageId {
					msg.SendPacket.Payload = decryptMsg.SendPacket.Payload
					msg.IsEncrypt = decryptMsg.IsEncrypt
					break
				}
			}
		}
	default:
		if c.stepFnc != nil {
			return c.stepFnc(a)
		}
	}

	return nil
}

func (c *channel) stepLeader(a *ChannelAction) error {
	switch a.ActionType {
	case ChannelActionPermissionCheckResp: // 权限校验返回
		c.permissionChecking = false
		if len(a.Messages) == 0 {
			return nil
		}
		lastMsg := a.Messages[len(a.Messages)-1]
		if lastMsg.Index > c.msgQueue.permissionCheckedIndex {
			c.msgQueue.permissionCheckedIndex = lastMsg.Index
		}

	case ChannelActionStorageResp: // 存储完成
		c.storaging = false
		if len(a.Messages) == 0 {
			return nil
		}
		lastMsg := a.Messages[len(a.Messages)-1]
		if lastMsg.Index > c.msgQueue.storagedIndex {
			c.msgQueue.storagedIndex = lastMsg.Index
		}
		// 消息存储完毕后，需要通知发送者
		c.exec(&ChannelAction{ActionType: ChannelActionSendack, Messages: a.Messages, Reason: a.Reason, ReasonCode: a.ReasonCode})

	case ChannelActionDeliverResp: // 消息投递返回
		c.delivering = false
		if len(a.Messages) == 0 {
			fmt.Println("messages is empty")
			return nil
		}
		lastMsg := a.Messages[len(a.Messages)-1]
		if lastMsg.Index > c.msgQueue.deliveredIndex {
			c.msgQueue.deliveredIndex = lastMsg.Index
			c.msgQueue.truncateTo(lastMsg.Index)
		}
	}

	return nil
}

func (c *channel) stepProxy(a *ChannelAction) error {

	switch a.ActionType {
	case ChannelActionForwardResp: // 转发
		c.forwarding = false
		if len(a.Messages) == 0 {
			return nil
		}
		lastMsg := a.Messages[len(a.Messages)-1]
		if lastMsg.Index > c.msgQueue.forwardedIndex {
			c.msgQueue.forwardedIndex = lastMsg.Index
			c.msgQueue.truncateTo(lastMsg.Index)
		}
	case ChannelActionLeaderChange: // leader变更
		a.LeaderId = c.leaderId
		if c.role == channelRoleLeader { // 当前节点是leader
			if a.LeaderId != c.r.opts.Cluster.NodeId {
				c.becomeProxy(a.LeaderId)
			}
		} else if c.role == channelRoleProxy {
			if a.LeaderId == c.r.opts.Cluster.NodeId {
				c.becomeLeader()
			}
		}
	}
	return nil
}

func (c *channel) exec(a *ChannelAction) {
	c.actions = append(c.actions, a)
}

func (c *channel) appendMessage(messages ...*ReactorChannelMessage) {

	for _, message := range messages {
		message.Index = c.msgQueue.lastIndex + 1
		c.msgQueue.appendMessage(message)
	}

}
