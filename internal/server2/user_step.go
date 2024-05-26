package server

import (
	"fmt"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (u *userHandler) step(a *UserAction) error {
	switch a.ActionType {
	case UserActionInitResp: // 初始化返回
		if a.Reason == ReasonSuccess {
			u.status = userStatusInitialized
			u.leaderId = a.LeaderId
			if a.LeaderId == u.sub.r.s.opts.Cluster.NodeId {
				u.becomeLeader()
			} else {
				u.becomeProxy(a.LeaderId)
			}
		} else {
			u.status = userStatusUninitialized
		}
		// u.Info("init finished")
	case UserActionConnect: // 连接
		for _, msg := range a.Messages {
			msg.Index = u.authQueue.lastIndex + 1
			u.authQueue.appendMessage(msg)
		}
		// u.Info("connecting...")
	case UserActionRecv: // 收消息
		for _, msg := range a.Messages {
			msg.Index = u.recvMsgQueue.lastIndex + 1
			u.recvMsgQueue.appendMessage(msg)
			// if msg.InPacket != nil {
			// 	u.Info("recv...", zap.String("frameType", msg.InPacket.GetFrameType().String()))
			// } else {
			// 	u.Info("recv...", zap.Int("size", len(msg.OutBytes)))
			// }

		}

	case UserActionRecvResp: // 收消息返回
		u.recvMsging = false
		if a.Index == 0 {
			panic("0")
		}
		if a.Reason == ReasonSuccess && u.recvMsgQueue.processingIndex < a.Index {
			u.recvMsgQueue.processingIndex = a.Index
			u.recvMsgQueue.truncateTo(a.Index)
		}
		// u.Info("recv resp...")

	default:
		return u.stepFnc(a)
	}

	return nil

}

func (u *userHandler) stepLeader(a *UserAction) error {
	switch a.ActionType {

	case UserActionAuthResp:
		u.authing = false
		if a.Reason == ReasonSuccess && u.authQueue.processingIndex < a.Index {
			u.authQueue.processingIndex = a.Index
			u.authQueue.truncateTo(a.Index)
		}
		// u.Info("auth resp...")
	case UserActionSend: // 发送消息
		for _, msg := range a.Messages {
			switch msg.InPacket.GetFrameType() {
			case wkproto.PING:
				msg.Index = u.pingQueue.lastIndex + 1
				u.pingQueue.appendMessage(msg)
			case wkproto.RECVACK:
				msg.Index = u.recvackQueue.lastIndex + 1
				u.recvackQueue.appendMessage(msg)
			default:
				u.Error("unknown frame type", zap.String("frameType", msg.InPacket.GetFrameType().String()))
				return fmt.Errorf("unknown packet type: %v", msg.InPacket.GetFrameType())
			}
			u.Info("leader: sending...", zap.String("frameType", msg.InPacket.GetFrameType().String()))
		}

	case UserActionPingResp: // ping处理返回
		u.sendPing = false
		if a.Reason == ReasonSuccess && u.pingQueue.processingIndex < a.Index {
			u.pingQueue.processingIndex = a.Index
			u.pingQueue.truncateTo(a.Index)
		}
		// u.Info("ping resp...")
	case UserActionRecvackResp: // recvack处理返回
		u.sendRecvacking = false
		if a.Reason == ReasonSuccess && u.recvackQueue.processingIndex < a.Index {
			u.recvackQueue.processingIndex = a.Index
			u.recvackQueue.truncateTo(a.Index)
		}
		// u.Info("recvack resp...")

	}
	return nil
}

func (u *userHandler) stepProxy(a *UserAction) error {
	switch a.ActionType {
	case UserActionSend: // 发送消息
		for _, msg := range a.Messages {
			switch msg.InPacket.GetFrameType() {
			case wkproto.PING:
				// 如果是代理角色，那么连接也只可能是真实连接，则直接回应连接的pong（TODO: 这里协议版本理论上应该使用用户的协议版本，但是考虑到ping/pong协议一般不会变动，所以这里用任意协议版本也问题不大）
				pongData, _ := defaultWkproto.EncodeFrame(&wkproto.PongPacket{}, defaultProtoVersion)

				m := &ReactorUserMessage{
					ConnId:   msg.ConnId,
					DeviceId: msg.DeviceId,
					OutBytes: pongData,
				}
				m.Index = u.recvMsgQueue.lastIndex + 1
				u.recvMsgQueue.appendMessage(m)
			case wkproto.RECVACK:
				msg.Index = u.recvackQueue.lastIndex + 1
				u.recvackQueue.appendMessage(msg)
			default:
				u.Error("unknown frame type", zap.String("frameType", msg.InPacket.GetFrameType().String()))
				return fmt.Errorf("unknown packet type: %v", msg.InPacket.GetFrameType())
			}
			// u.Info("proxy: sending...", zap.String("frameType", msg.InPacket.GetFrameType().String()))
		}
	case UserActionForwardResp: // 转发recvack处理返回
		if a.Reason == ReasonSuccess {
			var maxRecvackIndex uint64
			var maxAuthIndex uint64
			for _, msg := range a.Messages {
				if msg.InPacket != nil && msg.InPacket.GetFrameType() == wkproto.RECVACK {
					if msg.Index > maxRecvackIndex {
						maxRecvackIndex = msg.Index
					}
				}
				if msg.InPacket != nil && msg.InPacket.GetFrameType() == wkproto.CONNECT {
					if msg.Index > maxAuthIndex {
						maxAuthIndex = msg.Index
					}
				}
			}
			if u.recvackQueue.processingIndex < maxRecvackIndex {
				u.recvackQueue.processingIndex = maxRecvackIndex
				u.recvackQueue.truncateTo(maxRecvackIndex)
			}
			if u.authQueue.processingIndex < maxAuthIndex {
				u.authQueue.processingIndex = maxAuthIndex
				u.authQueue.truncateTo(maxAuthIndex)
			}
		}
		// u.Info("forward resp...")
	}
	return nil
}
