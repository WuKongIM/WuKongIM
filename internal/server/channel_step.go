package server

import (
	"errors"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (c *channel) step(a *ChannelAction) error {

	if a.UniqueNo != c.uniqueNo {
		c.Error("uniqueNo not match", zap.String("channelId", c.channelId), zap.String("expectUniqueNo", c.uniqueNo), zap.String("uniqueNo", a.UniqueNo))
		return errors.New("uniqueNo not match")
	}
	// c.Info("channel step", zap.String("actionType", a.ActionType.String()), zap.Uint64("leaderId", c.leaderId), zap.Uint8("channelType", c.channelType), zap.String("queue", c.msgQueue.String()))

	switch a.ActionType {
	case ChannelActionInitResp: // 初始化返回
		if a.Reason == ReasonSuccess {
			c.initTick = c.opts.Reactor.ChannelProcessIntervalTick // 立即处理下个逻辑
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

	case ChannelActionLeaderChange: // leader变更
		c.leaderId = a.LeaderId
		if c.role == channelRoleLeader { // 当前节点是leader
			if a.LeaderId != c.r.opts.Cluster.NodeId {
				c.becomeProxy(a.LeaderId)
			}
		} else if c.role == channelRoleProxy {
			if a.LeaderId == c.r.opts.Cluster.NodeId {
				c.becomeLeader()
			}
		}

	case ChannelActionSend: // 发送
		for _, message := range a.Messages {
			message.Index = c.msgQueue.lastIndex + 1
			message.ReasonCode = wkproto.ReasonSuccess // 默认设置为成功
			c.msgQueue.appendMessage(message)
		}
		// c.Debug("channel send", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
	case ChannelActionPayloadDecryptResp: // payload解密
		c.payloadDecrypting = false
		if len(a.Messages) == 0 {
			return nil
		}
		if a.Reason == ReasonSuccess {
			c.payloadDecryptingTick = c.opts.Reactor.ChannelProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求
		}

		lastMsg := a.Messages[len(a.Messages)-1]

		startIndex := c.msgQueue.getArrayIndex(c.msgQueue.payloadDecryptingIndex)

		if lastMsg.Index > c.msgQueue.payloadDecryptingIndex {
			c.msgQueue.payloadDecryptingIndex = lastMsg.Index
		}

		endIndex := c.msgQueue.getArrayIndex(c.msgQueue.payloadDecryptingIndex)

		if startIndex >= endIndex {
			return nil
		}

		msgLen := len(a.Messages)
		for i := startIndex; i < endIndex; i++ {
			msg := c.msgQueue.messages[i]
			for j := 0; j < msgLen; j++ {
				decryptMsg := a.Messages[j]
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

		if a.Reason == ReasonSuccess {
			c.permissionCheckingTick = c.opts.Reactor.ChannelProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求
		} else {
			// 权限校验失败，需要将消息的ReasonCode设置为失败
			startIndex := c.msgQueue.getArrayIndex(c.msgQueue.permissionCheckingIndex)
			endIndex := c.msgQueue.getArrayIndex(a.Index)
			if startIndex < endIndex {
				for i := startIndex; i < endIndex; i++ {
					c.msgQueue.messages[i].ReasonCode = a.ReasonCode
				}
			}
		}

		if a.Index > c.msgQueue.permissionCheckingIndex {
			c.msgQueue.permissionCheckingIndex = a.Index
		}
		// c.Info("channel permission check resp", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))

	case ChannelActionStorageResp: // 存储完成
		c.storaging = false
		if a.Reason == ReasonSuccess {
			c.storageTick = c.opts.Reactor.ChannelProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求
		}

		startIndex := c.msgQueue.getArrayIndex(c.msgQueue.storagingIndex)
		if a.Index > c.msgQueue.storagingIndex && a.Reason == ReasonSuccess {
			c.msgQueue.storagingIndex = a.Index
		}
		endIndex := c.msgQueue.getArrayIndex(c.msgQueue.storagingIndex)

		if startIndex >= endIndex {
			return nil
		}
		msgLen := len(a.Messages)
		for i := startIndex; i < endIndex; i++ {
			msg := c.msgQueue.messages[i]
			for j := 0; j < msgLen; j++ {
				storedMsg := a.Messages[j]
				if msg.MessageId == storedMsg.MessageId {
					msg.MessageSeq = storedMsg.MessageSeq
					c.msgQueue.messages[i] = msg
					break
				}
			}
		}
	// 消息存储完毕后，需要通知发送者
	// c.exec(&ChannelAction{ActionType: ChannelActionSendack, Messages: a.Messages, Reason: a.Reason, ReasonCode: a.ReasonCode})

	// c.Info("channel storage resp", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))

	case ChannelActionSendackResp: // 发送ack返回
		c.sendacking = false
		if a.Reason == ReasonSuccess {
			c.sendackingTick = c.opts.Reactor.ChannelProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求
		}
		if a.Index > c.msgQueue.sendackingIndex {
			c.msgQueue.sendackingIndex = a.Index
		}

	case ChannelActionDeliverResp: // 消息投递返回
		c.delivering = false
		if a.Reason == ReasonSuccess {
			c.deliveringTick = c.opts.Reactor.ChannelProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求
		}
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
		if a.Reason == ReasonSuccess {
			c.forwardTick = c.opts.Reactor.ChannelProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求
			lastMsg := a.Messages[len(a.Messages)-1]
			if lastMsg.Index > c.msgQueue.forwardingIndex {
				c.msgQueue.forwardingIndex = lastMsg.Index
				c.msgQueue.truncateTo(lastMsg.Index)
			}
		}

	}
	return nil
}

func (c *channel) exec(a *ChannelAction) {
	c.actions = append(c.actions, a)
}
