package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"github.com/valyala/bytebufferpool"
	"go.uber.org/zap"
)

func (p *User) processSend(msg *reactor.UserMessage) {

	sendPacket := msg.Frame.(*wkproto.SendPacket)
	channelId := sendPacket.ChannelID
	channelType := sendPacket.ChannelType
	fakeChannelId := channelId
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(channelId, msg.Conn.Uid)
	}

	// 解密消息
	newPayload, err := p.decryptPayload(sendPacket, msg.Conn)
	if err != nil {
		p.Error("Failed to decrypt payload！", zap.Error(err), zap.String("uid", msg.Conn.Uid), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		sendack := &wkproto.SendackPacket{
			Framer:      sendPacket.Framer,
			MessageID:   msg.MessageId,
			ClientSeq:   sendPacket.ClientSeq,
			ClientMsgNo: sendPacket.ClientMsgNo,
			ReasonCode:  wkproto.ReasonPayloadDecodeError,
		}
		reactor.User.ConnWrite(msg.Conn, sendack)
		return
	}
	sendPacket.Payload = newPayload

	// 根据需要唤醒频道
	reactor.Channel.WakeIfNeed(fakeChannelId, channelType)

	trace.GlobalTrace.Metrics.App().SendPacketCountAdd(1)
	trace.GlobalTrace.Metrics.App().SendPacketBytesAdd(sendPacket.GetFrameSize())
	// 添加消息到频道
	reactor.Channel.SendMessage(&reactor.ChannelMessage{
		FakeChannelId: fakeChannelId,
		ChannelType:   channelType,
		Conn:          msg.Conn,
		SendPacket:    sendPacket,
		MsgType:       reactor.ChannelMsgSend,
		MessageId:     msg.MessageId,
	})
}

// decode payload
func (p *User) decryptPayload(sendPacket *wkproto.SendPacket, conn *reactor.Conn) ([]byte, error) {

	aesKey, aesIV := conn.AesKey, conn.AesIV
	vail, err := p.sendPacketIsVail(sendPacket, conn)
	if err != nil {
		return nil, err
	}
	if !vail {
		return nil, errors.New("sendPacket is illegal！")
	}
	// decode payload
	decodePayload, err := wkutil.AesDecryptPkcs7Base64(sendPacket.Payload, aesKey, aesIV)
	if err != nil {
		p.Error("Failed to decode payload！", zap.Error(err))
		return nil, err
	}

	return decodePayload, nil
}

// send packet is vail
func (p *User) sendPacketIsVail(sendPacket *wkproto.SendPacket, conn *reactor.Conn) (bool, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV
	signStr := sendPacket.VerityString()

	signBuff := bytebufferpool.Get()
	_, _ = signBuff.WriteString(signStr)

	defer bytebufferpool.Put(signBuff)

	actMsgKey, err := wkutil.AesEncryptPkcs7Base64(signBuff.Bytes(), aesKey, aesIV)
	if err != nil {
		p.Error("msgKey is illegal！", zap.Error(err), zap.String("sign", signStr), zap.String("aesKey", string(aesKey)), zap.String("aesIV", string(aesIV)), zap.Any("conn", conn))
		return false, err
	}
	actMsgKeyStr := sendPacket.MsgKey
	exceptMsgKey := wkutil.MD5Bytes(actMsgKey)
	if actMsgKeyStr != exceptMsgKey {
		p.Error("msgKey is illegal！", zap.String("except", exceptMsgKey), zap.String("act", actMsgKeyStr), zap.String("sign", signStr), zap.String("aesKey", string(aesKey)), zap.String("aesIV", string(aesIV)), zap.Any("conn", conn))
		return false, errors.New("msgKey is illegal！")
	}
	return true, nil
}
