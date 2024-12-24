package process

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (p *Push) processPushOnline(messages []*reactor.ChannelMessage) {
	// 按照频道分组
	channelMessages := p.groupByChannel(messages)
	for channelKey, messages := range channelMessages {
		p.processChannelPush(channelKey, messages)
	}
}

// 以频道为单位推送消息
func (p *Push) processChannelPush(channelKey string, messages []*reactor.ChannelMessage) {
	fakeChannelId, _ := wkutil.ChannelFromlKey(channelKey)
	for _, message := range messages {
		if options.G.IsSystemUid(message.ToUid) {
			continue
		}
		toConns := reactor.User.AuthedConnsByUid(message.ToUid)
		if len(toConns) == 0 {
			continue
		}
		// 记录消息轨迹
		message.Track.Record(track.PositionPushOnline)

		sendPacket := message.SendPacket
		fromUid := message.Conn.Uid
		// 如果发送者是系统账号，则不显示发送者
		if options.G.IsSystemUid(fromUid) {
			fromUid = ""
		}

		recvPacket := &wkproto.RecvPacket{}

		recvPacket.Framer = wkproto.Framer{
			RedDot:    sendPacket.GetRedDot(),
			SyncOnce:  sendPacket.GetsyncOnce(),
			NoPersist: sendPacket.GetNoPersist(),
		}
		recvPacket.Setting = sendPacket.Setting
		recvPacket.MessageID = message.MessageId
		recvPacket.MessageSeq = uint32(message.MessageSeq)
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

		for _, toConn := range toConns {
			if toConn.Uid == message.Conn.Uid && toConn.DeviceId == message.Conn.DeviceId { // 自己发的不处理
				continue
			}

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
				p.Error("aesIV or aesKey is empty",
					zap.String("uid", toConn.Uid),
					zap.String("deviceId", toConn.DeviceId),
					zap.String("channelId", recvPacket.ChannelID),
					zap.Uint8("channelType", recvPacket.ChannelType),
				)
				continue
			}
			encryptPayload, err := encryptMessagePayload(sendPacket.Payload, toConn)
			if err != nil {
				p.Error("加密payload失败！",
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
				p.Error("生成MsgKey失败！", zap.Error(err))
				continue
			}
			recvPacket.MsgKey = msgKey
			recvPacketData, err := reactor.Proto.EncodeFrame(recvPacket, toConn.ProtoVersion)
			if err != nil {
				p.Error("encode recvPacket failed", zap.String("uid", toConn.Uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType), zap.Error(err))
				continue
			}

			if !recvPacket.NoPersist { // 只有存储的消息才重试
				service.RetryManager.AddRetry(&types.RetryMessage{
					Uid:            toConn.Uid,
					ChannelId:      fakeChannelId,
					ChannelType:    recvPacket.ChannelType,
					ConnId:         toConn.ConnId,
					FromNode:       toConn.FromNode,
					MessageId:      message.MessageId,
					RecvPacketData: recvPacketData,
				})
			}

			// 统计
			trace.GlobalTrace.Metrics.App().RecvPacketCountAdd(1)
			trace.GlobalTrace.Metrics.App().RecvPacketBytesAdd(int64(len(recvPacketData)))

			reactor.User.ConnWriteBytesNoAdvance(toConn, recvPacketData)
		}

		reactor.User.Advance(message.ToUid)

	}

	if options.G.Logger.TraceOn {
		if len(messages) < options.G.Logger.TraceMaxMsgCount { // 消息数小于指定数量才打印，要不然日志太多了
			for _, m := range messages {
				// 记录消息轨迹
				m.Track.Record(track.PositionPushOnlineEnd)
				connCount := reactor.User.ConnCountByUid(m.ToUid)
				p.Trace(m.Track.String(),
					"pushOnline",
					zap.Int64("messageId", m.MessageId),
					zap.Uint64("messageSeq", m.MessageSeq),
					zap.Uint64("clientSeq", m.SendPacket.ClientSeq),
					zap.String("clientMsgNo", m.SendPacket.ClientMsgNo),
					zap.String("channelId", m.FakeChannelId),
					zap.Uint8("channelType", m.ChannelType),
					zap.String("toUid", m.ToUid),
					zap.Int("toConnCount", connCount),
					zap.String("conn.uid", m.Conn.Uid),
					zap.String("conn.deviceId", m.Conn.DeviceId),
					zap.Uint64("conn.fromNode", m.Conn.FromNode),
					zap.Int64("conn.connId", m.Conn.ConnId),
				)
			}
		}
	}

}

// 消息按照频道分组
func (p *Push) groupByChannel(messages []*reactor.ChannelMessage) map[string][]*reactor.ChannelMessage {
	channelMessages := make(map[string][]*reactor.ChannelMessage)
	for _, m := range messages {
		channelKey := wkutil.ChannelToKey(m.FakeChannelId, m.ChannelType)
		if _, ok := channelMessages[channelKey]; !ok {
			channelMessages[channelKey] = make([]*reactor.ChannelMessage, 0)
		}
		channelMessages[channelKey] = append(channelMessages[channelKey], m)
	}
	return channelMessages
}

// 加密消息
func encryptMessagePayload(payload []byte, conn *reactor.Conn) ([]byte, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV
	// 加密payload
	payloadEnc, err := wkutil.AesEncryptPkcs7Base64(payload, aesKey, aesIV)
	if err != nil {
		return nil, err
	}
	return payloadEnc, nil
}

func makeMsgKey(signStr string, conn *reactor.Conn) (string, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV
	// 生成MsgKey
	msgKeyBytes, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		wklog.Error("生成MsgKey失败！", zap.Error(err))
		return "", err
	}
	return wkutil.MD5(string(msgKeyBytes)), nil
}
