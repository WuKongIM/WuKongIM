package server

import (
	"errors"
	"strings"

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
			c.initTick = c.opts.Reactor.Channel.ProcessIntervalTick // 立即处理下个逻辑
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
		c.idleTick = 0
		for _, message := range a.Messages {

			if message.SendPacket == nil {
				c.Error("sendPacket is nil, cannot send stream message, skip", zap.Int64("messageId", message.MessageId))
				continue
			}

			// 如果是流消息，则加入到流消息的队列里
			streamNo := message.SendPacket.StreamNo
			if strings.TrimSpace(streamNo) != "" {
				streamNo := message.SendPacket.StreamNo
				if strings.TrimSpace(streamNo) == "" {
					c.Error("streamNo is nil, cannot send stream message, skip", zap.Int64("messageId", message.MessageId))
					continue
				}
				sm := c.streams.get(streamNo)
				if sm == nil {
					sm = newStream(streamNo, c.sub.r.s)
					c.streams.add(sm)
				}
				message.Index = sm.msgQueue.lastIndex + 1
				message.ReasonCode = wkproto.ReasonSuccess // 默认设置为成功
				sm.appendMsg(message)
				continue
			}

			message.Index = c.msgQueue.lastIndex + 1
			message.ReasonCode = wkproto.ReasonSuccess // 默认设置为成功
			c.msgQueue.appendMessage(message)
		}
		// c.Debug("channel send", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
	case ChannelActionPayloadDecryptResp: // payload解密
		if a.Reason == ReasonSuccess {
			c.payloadDecryptState.processing = false
			if len(a.Messages) == 0 {
				return nil
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
						msg.ReasonCode = decryptMsg.ReasonCode
						c.msgQueue.messages[i] = msg
						break
					}
				}
			}
		} else {
			c.payloadDecryptState.willRetry = true
		}
	case ChannelActionStreamPayloadDecryptResp: // stream payload解密
		if len(a.Messages) == 1 {
			msg := a.Messages[0]
			stream := c.streams.get(msg.SendPacket.StreamNo)
			if stream == nil {
				c.Error("stream not found", zap.String("streamNo", msg.SendPacket.StreamNo), zap.String("clientMsgNo", msg.SendPacket.ClientMsgNo), zap.Int64("messageId", msg.MessageId))
				return nil
			}
			stream.payloadDecryptFinish(a.Messages)
			return nil
		} else if len(a.Messages) > 1 {
			streamMessageMap := make(map[string][]ReactorChannelMessage)
			for _, msg := range a.Messages {
				streamMessageMap[msg.SendPacket.StreamNo] = append(streamMessageMap[msg.SendPacket.StreamNo], msg)
			}
			for streamNo, messages := range streamMessageMap {
				stream := c.streams.get(streamNo)
				if stream == nil {
					c.Error("stream not found", zap.String("streamNo", streamNo), zap.Int("messages", len(messages)))
					continue
				}
				stream.payloadDecryptFinish(messages)
			}
			return nil
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
		if a.Reason == ReasonSuccess {
			c.permissionCheckState.processing = false
			startIndex := c.msgQueue.getArrayIndex(c.msgQueue.permissionCheckingIndex)
			if a.Index > c.msgQueue.permissionCheckingIndex {
				c.msgQueue.permissionCheckingIndex = a.Index
			}
			endIndex := c.msgQueue.getArrayIndex(a.Index)
			if startIndex >= endIndex {
				return nil
			}
			msgLen := len(a.Messages)
			for i := startIndex; i < endIndex; i++ {
				msg := c.msgQueue.messages[i]
				for j := 0; j < msgLen; j++ {
					permMsg := a.Messages[j]
					if msg.MessageId == permMsg.MessageId {
						msg.ReasonCode = permMsg.ReasonCode
						c.msgQueue.messages[i] = msg
						break
					}
				}
			}
		} else {
			c.permissionCheckState.willRetry = true
		}
	case ChannelActionStorageResp: // 存储完成
		if a.Reason == ReasonSuccess {
			c.storageState.processing = false
			startIndex := c.msgQueue.getArrayIndex(c.msgQueue.storagingIndex)
			if a.Index > c.msgQueue.storagingIndex {
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
						msg.ReasonCode = storedMsg.ReasonCode
						c.msgQueue.messages[i] = msg
						break
					}
				}
			}
		} else {
			c.storageState.willRetry = true
		}

	// 消息存储完毕后，需要通知发送者
	// c.exec(&ChannelAction{ActionType: ChannelActionSendack, Messages: a.Messages, Reason: a.Reason, ReasonCode: a.ReasonCode})

	// c.Info("channel storage resp", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))

	case ChannelActionSendackResp: // 发送ack返回
		if a.Reason == ReasonSuccess {
			c.sendackState.processing = false // 设置为false，则不需要等待可以继续处理下一批请求
			if a.Index > c.msgQueue.sendackingIndex {
				c.msgQueue.sendackingIndex = a.Index
			}
		} else {
			c.sendackState.willRetry = true
		}

	case ChannelActionDeliverResp: // 消息投递返回
		if a.Reason == ReasonSuccess {
			c.deliveryState.processing = false
			if a.Index > c.msgQueue.deliveringIndex {
				c.msgQueue.deliveringIndex = a.Index
				c.msgQueue.truncateTo(a.Index)

			}
		} else {
			c.deliveryState.willRetry = true
		}
	// c.Info("channel deliver resp", zap.Int("messageCount", len(a.Messages)), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
	case ChannelActionStreamDeliverResp: // stream消息投递返回
		if len(a.Messages) == 1 {
			msg := a.Messages[0]
			stream := c.streams.get(msg.SendPacket.StreamNo)
			if stream == nil {
				c.Error("deliver: stream not found", zap.String("streamNo", msg.SendPacket.StreamNo), zap.String("clientMsgNo", msg.SendPacket.ClientMsgNo), zap.Int64("messageId", msg.MessageId))
				return nil
			}
			stream.deliverFinish(a.Messages)
			return nil
		} else if len(a.Messages) > 1 {
			streamMessageMap := make(map[string][]ReactorChannelMessage)
			for _, msg := range a.Messages {
				streamMessageMap[msg.SendPacket.StreamNo] = append(streamMessageMap[msg.SendPacket.StreamNo], msg)
			}
			for streamNo, messages := range streamMessageMap {
				stream := c.streams.get(streamNo)
				if stream == nil {
					c.Error("deliver: stream not found", zap.String("streamNo", streamNo), zap.Int("messages", len(messages)))
					continue
				}
				stream.deliverFinish(messages)
			}
			return nil
		}
	}

	return nil
}

func (c *channel) stepProxy(a *ChannelAction) error {

	switch a.ActionType {
	case ChannelActionForwardResp: // 转发
		if a.Reason == ReasonSuccess {
			c.forwardState.processing = false
			if len(a.Messages) == 0 {
				return nil
			}
			if a.Reason == ReasonSuccess {
				lastMsg := a.Messages[len(a.Messages)-1]
				if lastMsg.Index > c.msgQueue.forwardingIndex {
					c.msgQueue.forwardingIndex = lastMsg.Index
					c.msgQueue.truncateTo(lastMsg.Index)
				}
			}
		} else {
			c.forwardState.willRetry = true
		}

	}
	return nil
}

func (c *channel) exec(a *ChannelAction) {
	c.actions = append(c.actions, a)
}
