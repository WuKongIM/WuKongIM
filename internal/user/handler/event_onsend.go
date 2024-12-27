package handler

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/valyala/bytebufferpool"
	"go.uber.org/zap"
)

// onSend 收到发送消息
func (h *Handler) onSend(ctx *eventbus.UserContext) {
	for _, event := range ctx.Events {
		frameType := event.Frame.GetFrameType()
		switch frameType {
		case wkproto.SEND:
			h.handleOnSend(event)
		case wkproto.RECVACK:
			h.recvack(event)
		case wkproto.PING:
			h.ping(event)
		}
	}
}

func (h *Handler) handleOnSend(event *eventbus.Event) {

	// 记录消息路径
	event.Track.Record(track.PositionUserOnSend)

	conn := event.Conn

	sendPacket := event.Frame.(*wkproto.SendPacket)
	channelId := sendPacket.ChannelID
	channelType := sendPacket.ChannelType
	fakeChannelId := channelId
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(channelId, conn.Uid)
	}

	// 解密消息
	newPayload, err := h.decryptPayload(sendPacket, conn)
	if err != nil {
		h.Error("handleOnSend: Failed to decrypt payload！", zap.Error(err), zap.String("uid", conn.Uid), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		sendack := &wkproto.SendackPacket{
			Framer:      sendPacket.Framer,
			MessageID:   event.MessageId,
			ClientSeq:   sendPacket.ClientSeq,
			ClientMsgNo: sendPacket.ClientMsgNo,
			ReasonCode:  wkproto.ReasonPayloadDecodeError,
		}
		eventbus.User.ConnWrite(conn, sendack)
		return
	}
	sendPacket.Payload = newPayload

	trace.GlobalTrace.Metrics.App().SendPacketCountAdd(1)
	trace.GlobalTrace.Metrics.App().SendPacketBytesAdd(sendPacket.GetFrameSize())
	// 添加消息到频道
	eventbus.Channel.SendMessage(fakeChannelId, channelType, &eventbus.Event{
		Type:      eventbus.EventChannelOnSend,
		Conn:      conn,
		Frame:     sendPacket,
		MessageId: event.MessageId,
		Track:     event.Track,
	})
	// 推进
	eventbus.Channel.Advance(fakeChannelId, channelType)

}

// decode payload
func (h *Handler) decryptPayload(sendPacket *wkproto.SendPacket, conn *eventbus.Conn) ([]byte, error) {

	aesKey, aesIV := conn.AesKey, conn.AesIV
	vail, err := h.sendPacketIsVail(sendPacket, conn)
	if err != nil {
		return nil, err
	}
	if !vail {
		return nil, errors.New("sendPacket is illegal！")
	}
	// decode payload
	decodePayload, err := wkutil.AesDecryptPkcs7Base64(sendPacket.Payload, aesKey, aesIV)
	if err != nil {
		h.Error("Failed to decode payload！", zap.Error(err))
		return nil, err
	}

	return decodePayload, nil
}

// send packet is vail
func (h *Handler) sendPacketIsVail(sendPacket *wkproto.SendPacket, conn *eventbus.Conn) (bool, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV
	signStr := sendPacket.VerityString()

	signBuff := bytebufferpool.Get()
	_, _ = signBuff.WriteString(signStr)

	defer bytebufferpool.Put(signBuff)

	actMsgKey, err := wkutil.AesEncryptPkcs7Base64(signBuff.Bytes(), aesKey, aesIV)
	if err != nil {
		h.Error("msgKey is illegal！", zap.Error(err), zap.String("sign", signStr), zap.String("aesKey", string(aesKey)), zap.String("aesIV", string(aesIV)), zap.Any("conn", conn))
		return false, err
	}
	actMsgKeyStr := sendPacket.MsgKey
	exceptMsgKey := wkutil.MD5Bytes(actMsgKey)
	if actMsgKeyStr != exceptMsgKey {
		h.Error("msgKey is illegal！", zap.String("except", exceptMsgKey), zap.String("act", actMsgKeyStr), zap.String("sign", signStr), zap.String("aesKey", string(aesKey)), zap.String("aesIV", string(aesIV)), zap.Any("conn", conn))
		return false, errors.New("msgKey is illegal！")
	}
	return true, nil
}
