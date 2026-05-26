package gateway

import (
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func mapSendCommand(ctx *coregateway.Context, pkt *frame.SendPacket) (message.SendCommand, error) {
	if ctx == nil || ctx.Session == nil {
		return message.SendCommand{}, ErrUnauthenticatedSession
	}

	senderUID, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if senderUID == "" {
		return message.SendCommand{}, ErrUnauthenticatedSession
	}
	deviceID, _ := ctx.Session.Value(coregateway.SessionValueDeviceID).(string)
	deviceFlag := deviceFlagFromValue(ctx.Session.Value(coregateway.SessionValueDeviceFlag))

	protocolVersion := uint8(frame.LatestVersion)
	if sessionVersion, ok := ctx.Session.Value(coregateway.SessionValueProtocolVersion).(uint8); ok && sessionVersion != 0 {
		protocolVersion = sessionVersion
	}

	if pkt == nil {
		return message.SendCommand{
			FromUID:         senderUID,
			SenderSessionID: ctx.Session.ID(),
			DeviceID:        deviceID,
			DeviceFlag:      deviceFlag,
			ProtocolVersion: protocolVersion,
		}, nil
	}

	return message.SendCommand{
		Framer:          pkt.Framer,
		Setting:         pkt.Setting,
		MsgKey:          pkt.MsgKey,
		Expire:          pkt.Expire,
		FromUID:         senderUID,
		SenderSessionID: ctx.Session.ID(),
		DeviceID:        deviceID,
		DeviceFlag:      deviceFlag,
		ClientSeq:       pkt.ClientSeq,
		ClientMsgNo:     pkt.ClientMsgNo,
		StreamNo:        pkt.StreamNo,
		ChannelID:       pkt.ChannelID,
		ChannelType:     pkt.ChannelType,
		Topic:           pkt.Topic,
		Payload:         pkt.Payload,
		ProtocolVersion: protocolVersion,
	}, nil
}

func mapRecvAckCommand(ctx *coregateway.Context, pkt *frame.RecvackPacket) (message.RecvAckCommand, error) {
	if ctx == nil || ctx.Session == nil {
		return message.RecvAckCommand{}, ErrUnauthenticatedSession
	}

	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if uid == "" {
		return message.RecvAckCommand{}, ErrUnauthenticatedSession
	}

	if pkt == nil {
		return message.RecvAckCommand{UID: uid, SessionID: ctx.Session.ID()}, nil
	}

	return message.RecvAckCommand{
		UID:        uid,
		SessionID:  ctx.Session.ID(),
		Framer:     pkt.Framer,
		MessageID:  pkt.MessageID,
		MessageSeq: pkt.MessageSeq,
	}, nil
}

func writeSendack(ctx *coregateway.Context, pkt *frame.SendPacket, result message.SendResult) error {
	if ctx == nil || ctx.Session == nil {
		return ErrUnauthenticatedSession
	}

	var clientSeq uint64
	var clientMsgNo string
	if pkt != nil {
		clientSeq = pkt.ClientSeq
		clientMsgNo = pkt.ClientMsgNo
	}

	return ctx.WriteFrame(&frame.SendackPacket{
		MessageID:   result.MessageID,
		MessageSeq:  result.MessageSeq,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ReasonCode:  result.Reason,
	})
}

func writePong(ctx *coregateway.Context) error {
	if ctx == nil || ctx.Session == nil {
		return ErrUnauthenticatedSession
	}
	return ctx.WriteFrame(&frame.PongPacket{})
}
