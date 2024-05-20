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

	default:
		return u.stepFnc(a)
	}

	return nil

}

func (u *userHandler) stepLeader(a *UserAction) error {
	switch a.ActionType {
	case UserActionSend: // 发送消息
		for _, msg := range a.Messages {
			switch msg.InPacket.GetFrameType() {
			case wkproto.PING:
				u.pingQueue.appendMessage(msg)
			case wkproto.RECVACK:
				u.recvackQueue.appendMessage(msg)
			default:
				u.Error("unknown frame type", zap.String("frameType", msg.InPacket.GetFrameType().String()))
				return fmt.Errorf("unknown packet type: %v", msg.InPacket.GetFrameType())
			}
		}
	case UserActionRecv: // 收消息
		for _, msg := range a.Messages {
			msg.Index = u.recvMsgQueue.lastIndex + 1
			u.recvMsgQueue.appendMessage(msg)
		}
	case UserActionPingResp: // ping处理返回
		u.sendPing = false
		if a.Reason == ReasonSuccess && u.pingQueue.processingIndex < a.Index {
			u.pingQueue.processingIndex = a.Index
			u.pingQueue.truncateTo(a.Index)
		}
	case UserActionRecvackResp: // recvack处理返回
		u.sendRecvacking = false
		if a.Reason == ReasonSuccess && u.recvackQueue.processingIndex < a.Index {
			u.recvackQueue.processingIndex = a.Index
			u.recvackQueue.truncateTo(a.Index)
		}

	case UserActionRecvResp: // 收消息返回
		if a.Reason == ReasonSuccess && u.recvMsgQueue.processingIndex < a.Index {
			u.recvMsgQueue.processingIndex = a.Index
			u.recvMsgQueue.truncateTo(a.Index)
		}

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
				u.recvMsgQueue.appendMessage(&ReactorUserMessage{
					ConnId:   msg.ConnId,
					Uid:      msg.Uid,
					DeviceId: msg.DeviceId,
					OutBytes: pongData,
				})
			case wkproto.RECVACK:
				u.recvackQueue.appendMessage(msg)
			default:
				u.Error("unknown frame type", zap.String("frameType", msg.InPacket.GetFrameType().String()))
				return fmt.Errorf("unknown packet type: %v", msg.InPacket.GetFrameType())
			}
		}
	case UserActionForwardRecvackResp: // 转发recvack处理返回
		if a.Reason == ReasonSuccess && u.recvackQueue.processingIndex < a.Index {
			u.recvackQueue.processingIndex = a.Index
			u.recvackQueue.truncateTo(a.Index)
		}
	}
	return nil
}
