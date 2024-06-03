package server

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
		// c.Info("channel init resp", zap.Int("status", int(c.status)), zap.Uint64("leaderId", c.leaderId))
	case ChannelActionSend: // 发送
		c.appendMessage(a.Messages...) // 消息是按照发送者分组的，所以取第一个即可
		// c.Info("channel send", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
	case ChannelActionPayloadDecryptResp: // paylaod解密
		c.payloadDecrypting = false
		if len(a.Messages) == 0 {
			return nil
		}
		lastMsg := a.Messages[len(a.Messages)-1]
		if lastMsg.Index > c.msgQueue.payloadDecryptingIndex {
			c.msgQueue.payloadDecryptingIndex = lastMsg.Index
		}

		for _, decryptMsg := range a.Messages {
			for i, msg := range c.msgQueue.messages {
				if msg.MessageId == decryptMsg.MessageId {
					msg.SendPacket.Payload = decryptMsg.SendPacket.Payload
					msg.IsEncrypt = decryptMsg.IsEncrypt
					c.msgQueue.messages[i] = msg
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
		if a.Index > c.msgQueue.permissionCheckingIndex {
			c.msgQueue.permissionCheckingIndex = a.Index
		}
		// c.Info("channel permission check resp", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))

	case ChannelActionStorageResp: // 存储完成
		c.storaging = false
		if a.Index > c.msgQueue.storagingIndex {
			c.msgQueue.storagingIndex = a.Index
		}
	// 消息存储完毕后，需要通知发送者
	// c.exec(&ChannelAction{ActionType: ChannelActionSendack, Messages: a.Messages, Reason: a.Reason, ReasonCode: a.ReasonCode})

	// c.Info("channel storage resp", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))

	case ChannelActionSendackResp: // 发送ack返回
		c.sendacking = false
		if a.Index > c.msgQueue.sendackingIndex {
			c.msgQueue.sendackingIndex = a.Index
		}

	case ChannelActionDeliverResp: // 消息投递返回
		c.delivering = false
		if a.Index > c.msgQueue.deliveringIndex {
			c.msgQueue.deliveringIndex = a.Index
			c.msgQueue.truncateTo(a.Index)

		}
		// c.Info("channel deliver resp", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
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
		if lastMsg.Index > c.msgQueue.forwardingIndex {
			c.msgQueue.forwardingIndex = lastMsg.Index
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

func (c *channel) appendMessage(messages ...ReactorChannelMessage) {

	for _, message := range messages {
		message.Index = c.msgQueue.lastIndex + 1
		c.msgQueue.appendMessage(message)
	}

}
