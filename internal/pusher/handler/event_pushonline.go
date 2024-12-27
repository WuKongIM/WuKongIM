package handler

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types"
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
		toConns := eventbus.User.AuthedConnsByUid(e.ToUid)
		if len(toConns) == 0 {
			continue
		}
		// 记录消息轨迹
		e.Track.Record(track.PositionPushOnline)

		sendPacket := e.Frame.(*wkproto.SendPacket)

		fakeChannelId := sendPacket.ChannelID
		channelType := sendPacket.ChannelType
		if channelType == wkproto.ChannelTypePerson {
			fakeChannelId = options.GetFakeChannelIDWith(e.Conn.Uid, e.ToUid)
		}

		fromUid := e.Conn.Uid
		// 如果发送者是系统账号，则不显示发送者
		if options.G.IsSystemUid(fromUid) {
			fromUid = ""
		}

		for _, toConn := range toConns {
			if toConn.Uid == e.Conn.Uid && toConn.NodeId == e.Conn.NodeId && toConn.ConnId == e.Conn.ConnId { // 自己发的不处理
				continue
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
			recvPacket.StreamFlag = wkproto.StreamFlagIng
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
			if len(toConn.AesIV) == 0 || len(toConn.AesKey) == 0 {
				h.Error("aesIV or aesKey is empty",
					zap.String("uid", toConn.Uid),
					zap.String("deviceId", toConn.DeviceId),
					zap.String("channelId", recvPacket.ChannelID),
					zap.Uint8("channelType", recvPacket.ChannelType),
				)
				continue
			}
			encryptPayload, err := encryptMessagePayload(sendPacket.Payload, toConn)
			if err != nil {
				h.Error("加密payload失败！",
					zap.Error(err),
					zap.String("uid", toConn.Uid),
					zap.String("channelId", recvPacket.ChannelID),
					zap.Uint8("channelType", recvPacket.ChannelType),
				)
				continue
			}
			recvPacket.Payload = encryptPayload
			signStr := recvPacket.VerityString()
			msgKey, err := makeMsgKey(signStr, toConn)
			if err != nil {
				h.Error("生成MsgKey失败！", zap.Error(err))
				continue
			}
			recvPacket.MsgKey = msgKey

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

			eventbus.User.ConnWrite(toConn, recvPacket)
		}
		eventbus.User.Advance(e.ToUid)
	}

	if options.G.Logger.TraceOn {
		if len(events) < options.G.Logger.TraceMaxMsgCount { // 消息数小于指定数量才打印，要不然日志太多了
			for _, e := range events {
				sendPacket := e.Frame.(*wkproto.SendPacket)
				// 记录消息轨迹
				e.Track.Record(track.PositionPushOnlineEnd)
				connCount := eventbus.User.ConnCountByUid(e.ToUid)
				h.Trace(e.Track.String(),
					"pushOnline",
					zap.Int64("messageId", e.MessageId),
					zap.Uint64("messageSeq", e.MessageSeq),
					zap.Uint64("clientSeq", sendPacket.ClientSeq),
					zap.String("clientMsgNo", sendPacket.ClientMsgNo),
					zap.String("toUid", e.ToUid),
					zap.Int("toConnCount", connCount),
					zap.String("conn.uid", e.Conn.Uid),
					zap.String("conn.deviceId", e.Conn.DeviceId),
					zap.Uint64("conn.nodeId", e.Conn.NodeId),
					zap.Int64("conn.connId", e.Conn.ConnId),
				)
			}
		}
	}

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
