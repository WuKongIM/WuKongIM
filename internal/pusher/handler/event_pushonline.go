package handler

import (
	"context"
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
	// fakeChannelId, channelType := wkutil.ChannelFromlKey(channelKey)
	for _, e := range events {
		if options.G.IsSystemUid(e.ToUid) {
			continue
		}

		fromUid := e.Conn.Uid
		// 如果发送者是系统账号，则不显示发送者
		if options.G.IsSystemUid(fromUid) {
			fromUid = ""
		}
		// 是否是AI
		// if fromUid != e.ToUid && h.isAI(e.ToUid) {
		// 	// 处理AI推送
		// 	h.processAIPush(e.ToUid, e)
		// }

		toConns := eventbus.User.AuthedConnsByUid(e.ToUid)
		if len(toConns) == 0 {
			continue
		}
		// 记录消息轨迹
		e.Track.Record(track.PositionPushOnline)

		sendPacket := e.Frame.(*wkproto.SendPacket)

		fakeChannelId := e.ChannelId
		channelType := e.ChannelType

		for _, toConn := range toConns {
			if toConn.Uid == e.Conn.Uid && toConn.NodeId == e.Conn.NodeId && toConn.ConnId == e.Conn.ConnId { // 自己发的不处理
				continue
			}

			if options.G.Logger.TraceOn && e.ChannelType == wkproto.ChannelTypePerson { // 暂时只打印个人频道的推送，因为群的话日志会太多
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

			recvPacket := &wkproto.RecvPacket{}

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

			// 这里需要把channelID改成fromUID 比如A给B发消息，B收到的消息channelID应该是A A收到的消息channelID应该是B
			recvPacket.ChannelID = sendPacket.ChannelID
			if recvPacket.ChannelType == wkproto.ChannelTypePerson &&
				recvPacket.ChannelID == toConn.Uid {
				recvPacket.ChannelID = recvPacket.FromUID
			}
			// 红点设置
			recvPacket.RedDot = sendPacket.RedDot
			if toConn.Uid == recvPacket.FromUID { // 如果是自己则不显示红点
				recvPacket.RedDot = false
			}

			var finalPayload []byte
			var err error

			// 根据配置决定是否加密消息负载
			if !options.G.DisableEncryption && !toConn.IsJsonRpc {
				if len(toConn.AesIV) == 0 || len(toConn.AesKey) == 0 {
					h.Error("aesIV or aesKey is empty, cannot encrypt payload",
						zap.String("uid", toConn.Uid),
						zap.String("deviceId", toConn.DeviceId),
						zap.String("channelId", recvPacket.ChannelID),
						zap.Uint8("channelType", recvPacket.ChannelType),
					)
					continue // 跳过此连接的推送
				}
				finalPayload, err = encryptMessagePayload(sendPacket.Payload, toConn)
				if err != nil {
					h.Error("加密payload失败！",
						zap.Error(err),
						zap.String("uid", toConn.Uid),
						zap.String("channelId", recvPacket.ChannelID),
						zap.Uint8("channelType", recvPacket.ChannelType),
					)
					continue // 跳过此连接的推送
				}
			} else {
				// 如果禁用了加密，则直接使用原始 Payload
				finalPayload = sendPacket.Payload
			}

			recvPacket.Payload = finalPayload // 设置最终的 Payload (可能加密也可能未加密)

			// ---- MsgKey 的生成逻辑也需要考虑加密是否禁用 ----
			if !options.G.DisableEncryption && !toConn.IsJsonRpc {
				// 只有启用了加密才生成 MsgKey
				signStr := recvPacket.VerityString()       // VerityString 可能依赖 Payload
				msgKey, err := makeMsgKey(signStr, toConn) // makeMsgKey 内部会使用 AES 加密
				if err != nil {
					h.Error("生成MsgKey失败！", zap.Error(err))
					continue
				}
				recvPacket.MsgKey = msgKey
			} else {
				// 如果禁用了加密，则 MsgKey 为空
				recvPacket.MsgKey = ""
			}

			if !recvPacket.NoPersist { // 只有存储的消息才重试
				service.RetryManager.AddRetry(&types.RetryMessage{
					ChannelId:   fakeChannelId,
					ChannelType: channelType,
					Uid:         toConn.Uid,
					ConnId:      toConn.ConnId,
					FromNode:    toConn.NodeId,
					MessageId:   e.MessageId,
					RecvPacket:  recvPacket,
				})
			}

			eventbus.User.ConnWrite(e.ReqId, toConn, recvPacket)
		}
		eventbus.User.Advance(e.ToUid)
	}

	// if options.G.Logger.TraceOn {
	// 	if len(events) < options.G.Logger.TraceMaxMsgCount { // 消息数小于指定数量才打印，要不然日志太多了
	// 		for _, e := range events {
	// 			sendPacket := e.Frame.(*wkproto.SendPacket)
	// 			// 记录消息轨迹
	// 			e.Track.Record(track.PositionPushOnlineEnd)
	// 			connCount := eventbus.User.ConnCountByUid(e.ToUid)
	// 			h.Trace(e.Track.String(),
	// 				"pushOnline",
	// 				zap.Int64("messageId", e.MessageId),
	// 				zap.Uint64("messageSeq", e.MessageSeq),
	// 				zap.Uint64("clientSeq", sendPacket.ClientSeq),
	// 				zap.String("clientMsgNo", sendPacket.ClientMsgNo),
	// 				zap.String("toUid", e.ToUid),
	// 				zap.Int("toConnCount", connCount),
	// 				zap.String("conn.uid", e.Conn.Uid),
	// 				zap.String("conn.deviceId", e.Conn.DeviceId),
	// 				zap.Uint64("conn.nodeId", e.Conn.NodeId),
	// 				zap.Int64("conn.connId", e.Conn.ConnId),
	// 			)
	// 		}
	// 	}
	// }

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
