package handler

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (h *Handler) pushOnline(ctx *eventbus.PushContext) {
	h.processChannelPush(ctx.Events)
}

// 以频道为单位推送消息
func (h *Handler) processChannelPush(events []*eventbus.Event) {
	for _, e := range events {
		if !h.shouldProcessEvent(e) {
			continue
		}

		fromUid := h.getDisplayFromUid(e.Conn.Uid)
		toConns := eventbus.User.AuthedConnsByUid(e.ToUid)
		if len(toConns) == 0 {
			continue
		}

		// 记录消息轨迹
		e.Track.Record(track.PositionPushOnline)

		switch frame := e.Frame.(type) {
		case *wkproto.SendPacket:
			// 推送消息
			h.pushMessageToConnections(e, frame, fromUid, toConns)
			eventbus.User.Advance(e.ToUid)
		case *wkproto.EventPacket:
			// 推送事件
			h.pushEventToConnections(e, frame, toConns)
		}

	}
}

// shouldProcessEvent 检查事件是否应该被处理
func (h *Handler) shouldProcessEvent(e *eventbus.Event) bool {
	return !options.G.IsSystemUid(e.ToUid)
}

// getDisplayFromUid 获取用于显示的发送者UID，系统账号返回空字符串
func (h *Handler) getDisplayFromUid(uid string) string {
	if options.G.IsSystemUid(uid) {
		return ""
	}
	return uid
}

// pushMessageToConnections 向所有目标连接推送消息
func (h *Handler) pushMessageToConnections(e *eventbus.Event, sendPacket *wkproto.SendPacket, fromUid string, toConns []*eventbus.Conn) {
	fakeChannelId := e.ChannelId
	channelType := e.ChannelType

	for _, toConn := range toConns {
		if h.shouldSkipConnection(e.Conn, toConn) {
			continue
		}

		h.logPushTrace(e, toConn)

		recvPacket := h.buildRecvPacket(e, sendPacket, fromUid, toConn)
		if recvPacket == nil {
			continue // 构建失败，跳过此连接
		}

		h.setupRetryIfNeeded(recvPacket, fakeChannelId, channelType, toConn, e.MessageId)
		eventbus.User.ConnWrite(e.ReqId, toConn, recvPacket)
	}
}

// pushEventToConnections 向所有目标连接推送事件包
func (h *Handler) pushEventToConnections(e *eventbus.Event, eventPacket *wkproto.EventPacket, toConns []*eventbus.Conn) {
	for _, toConn := range toConns {
		if h.shouldSkipConnection(e.Conn, toConn) {
			continue
		}
		eventbus.User.ConnWrite(e.ReqId, toConn, eventPacket)
	}
}

// shouldSkipConnection 检查是否应该跳过此连接（自己发的消息不推送给自己）
func (h *Handler) shouldSkipConnection(fromConn, toConn *eventbus.Conn) bool {
	return toConn.Uid == fromConn.Uid && toConn.NodeId == fromConn.NodeId && toConn.ConnId == fromConn.ConnId
}

// logPushTrace 记录推送跟踪日志（仅个人频道）
func (h *Handler) logPushTrace(e *eventbus.Event, toConn *eventbus.Conn) {
	if options.G.Logger.TraceOn && e.ChannelType == wkproto.ChannelTypePerson {
		h.Trace("推送在线消息...",
			"pushOnline",
			zap.Int64("messageId", e.MessageId),
			zap.Uint64("messageSeq", e.MessageSeq),
			zap.String("fromUid", e.Conn.Uid),
			zap.String("fromDeviceId", e.Conn.DeviceId),
			zap.String("fromDeviceFlag", e.Conn.DeviceFlag.String()),
			zap.Int64("fromConnId", e.Conn.ConnId),
			zap.String("toUid", toConn.Uid),
			zap.String("toDeviceId", toConn.DeviceId),
			zap.String("toDeviceFlag", toConn.DeviceFlag.String()),
			zap.Int64("toConnId", toConn.ConnId),
			zap.String("channelId", e.ChannelId),
			zap.Uint8("channelType", e.ChannelType),
		)
	}
}

// setupRetryIfNeeded 为需要持久化的消息设置重试机制
func (h *Handler) setupRetryIfNeeded(recvPacket *wkproto.RecvPacket, fakeChannelId string, channelType uint8, toConn *eventbus.Conn, messageId int64) {
	if !recvPacket.NoPersist { // 只有存储的消息才重试
		service.RetryManager.AddRetry(&types.RetryMessage{
			ChannelId:   fakeChannelId,
			ChannelType: channelType,
			Uid:         toConn.Uid,
			ConnId:      toConn.ConnId,
			FromNode:    toConn.NodeId,
			MessageId:   messageId,
			RecvPacket:  recvPacket,
		})
	}
}

// buildRecvPacket 构建接收数据包
func (h *Handler) buildRecvPacket(e *eventbus.Event, sendPacket *wkproto.SendPacket, fromUid string, toConn *eventbus.Conn) *wkproto.RecvPacket {
	recvPacket := &wkproto.RecvPacket{}

	// 设置基本字段
	h.setRecvPacketBasicFields(recvPacket, e, sendPacket, fromUid)

	// 调整个人频道的ChannelID
	h.adjustPersonChannelID(recvPacket, toConn)

	// 设置红点显示
	h.setRedDotDisplay(recvPacket, sendPacket, toConn)

	// 处理消息负载加密
	finalPayload, err := h.processPayloadEncryption(sendPacket.Payload, toConn, recvPacket)
	if err != nil {
		return nil // 加密失败，返回nil
	}
	recvPacket.Payload = finalPayload

	// 生成MsgKey
	if err := h.generateMsgKey(recvPacket, toConn); err != nil {
		return nil // MsgKey生成失败，返回nil
	}

	return recvPacket
}

// setRecvPacketBasicFields 设置接收数据包的基本字段
func (h *Handler) setRecvPacketBasicFields(recvPacket *wkproto.RecvPacket, e *eventbus.Event, sendPacket *wkproto.SendPacket, fromUid string) {
	recvPacket.Framer = wkproto.Framer{
		RedDot:    sendPacket.GetRedDot(),
		SyncOnce:  sendPacket.GetsyncOnce(),
		NoPersist: sendPacket.GetNoPersist(),
	}
	recvPacket.Setting = sendPacket.Setting
	recvPacket.MessageID = e.MessageId
	recvPacket.MessageSeq = uint32(e.MessageSeq)
	recvPacket.ClientMsgNo = sendPacket.ClientMsgNo
	recvPacket.StreamNo = sendPacket.StreamNo
	recvPacket.StreamId = uint64(e.MessageId)
	recvPacket.StreamFlag = e.StreamFlag
	recvPacket.FromUID = fromUid
	recvPacket.Expire = sendPacket.Expire
	recvPacket.ChannelID = sendPacket.ChannelID
	recvPacket.ChannelType = sendPacket.ChannelType
	recvPacket.Topic = sendPacket.Topic
	recvPacket.Timestamp = int32(time.Now().Unix())
	recvPacket.ClientSeq = sendPacket.ClientSeq
}

// adjustPersonChannelID 调整个人频道的ChannelID
// A给B发消息，B收到的消息channelID应该是A，A收到的消息channelID应该是B
func (h *Handler) adjustPersonChannelID(recvPacket *wkproto.RecvPacket, toConn *eventbus.Conn) {
	if recvPacket.ChannelType == wkproto.ChannelTypePerson &&
		recvPacket.ChannelID == toConn.Uid {
		recvPacket.ChannelID = recvPacket.FromUID
	}
}

// setRedDotDisplay 设置红点显示逻辑
func (h *Handler) setRedDotDisplay(recvPacket *wkproto.RecvPacket, sendPacket *wkproto.SendPacket, toConn *eventbus.Conn) {
	recvPacket.RedDot = sendPacket.RedDot
	if toConn.Uid == recvPacket.FromUID { // 如果是自己则不显示红点
		recvPacket.RedDot = false
	}
}

// processPayloadEncryption 处理消息负载加密
func (h *Handler) processPayloadEncryption(payload []byte, toConn *eventbus.Conn, recvPacket *wkproto.RecvPacket) ([]byte, error) {
	// 根据配置决定是否加密消息负载
	if !options.G.DisableEncryption && !toConn.IsJsonRpc {
		if len(toConn.AesIV) == 0 || len(toConn.AesKey) == 0 {
			h.Error("aesIV or aesKey is empty, cannot encrypt payload",
				zap.String("uid", toConn.Uid),
				zap.String("deviceId", toConn.DeviceId),
				zap.String("channelId", recvPacket.ChannelID),
				zap.Uint8("channelType", recvPacket.ChannelType),
			)
			return nil, errors.New("encryption keys missing")
		}

		finalPayload, err := encryptMessagePayload(payload, toConn)
		if err != nil {
			h.Error("加密payload失败！",
				zap.Error(err),
				zap.String("uid", toConn.Uid),
				zap.String("channelId", recvPacket.ChannelID),
				zap.Uint8("channelType", recvPacket.ChannelType),
			)
			return nil, err
		}
		return finalPayload, nil
	}

	// 如果禁用了加密，则直接使用原始 Payload
	return payload, nil
}

// generateMsgKey 生成消息密钥
func (h *Handler) generateMsgKey(recvPacket *wkproto.RecvPacket, toConn *eventbus.Conn) error {
	if !options.G.DisableEncryption && !toConn.IsJsonRpc {
		// 只有启用了加密才生成 MsgKey
		signStr := recvPacket.VerityString()       // VerityString 可能依赖 Payload
		msgKey, err := makeMsgKey(signStr, toConn) // makeMsgKey 内部会使用 AES 加密
		if err != nil {
			h.Error("生成MsgKey失败！", zap.Error(err))
			return err
		}
		recvPacket.MsgKey = msgKey
	} else {
		// 如果禁用了加密，则 MsgKey 为空
		recvPacket.MsgKey = ""
	}
	return nil
}

// 处理AI推送
func (h *Handler) processAIPush(uid string, e *eventbus.Event) {

	pluginNo, err := h.getAIPluginNo(uid)
	if err != nil {
		h.Error("获取AI插件编号失败！", zap.Error(err), zap.String("uid", uid))
		return
	}
	if len(pluginNo) == 0 {
		h.Debug("AI插件编号为空！", zap.String("uid", uid))
		return
	}
	pluginObj := service.PluginManager.Plugin(pluginNo)
	if pluginObj == nil {
		h.Debug("AI插件不存在！", zap.String("pluginNo", pluginNo), zap.String("uid", uid))
		return
	}

	sendPacket := e.Frame.(*wkproto.SendPacket)

	err = pluginObj.Receive(context.TODO(), &pluginproto.RecvPacket{
		FromUid:     e.Conn.Uid,
		ToUid:       uid,
		ChannelId:   sendPacket.ChannelID,
		ChannelType: uint32(sendPacket.ChannelType),
		Payload:     sendPacket.Payload,
	})
	if err != nil {
		h.Error("AI插件回复失败！", zap.Error(err), zap.String("pluginNo", pluginNo), zap.String("uid", uid))
	}
}

// 是否是AI
func (h *Handler) isAI(uid string) bool {

	return service.PluginManager.UserIsAI(uid)
}

// 获取用户AI插件编号
func (h *Handler) getAIPluginNo(uid string) (string, error) {

	return service.PluginManager.GetUserPluginNo(uid)
}

// 加密消息
func encryptMessagePayload(payload []byte, conn *eventbus.Conn) ([]byte, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV

	// 加密payload
	payloadEnc, err := wkutil.AesEncryptPkcs7Base64(payload, aesKey, aesIV)
	if err != nil {
		return nil, err
	}
	return payloadEnc, nil
}

func makeMsgKey(signStr string, conn *eventbus.Conn) (string, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV
	// 生成MsgKey
	msgKeyBytes, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		wklog.Error("生成MsgKey失败！", zap.Error(err))
		return "", err
	}
	return wkutil.MD5(string(msgKeyBytes)), nil
}
