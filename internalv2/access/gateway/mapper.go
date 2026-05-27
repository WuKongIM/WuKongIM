package gateway

import (
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func mapSendCommand(ctx *coregateway.Context, pkt *frame.SendPacket) (message.SendCommand, error) {
	if ctx == nil || ctx.Session == nil {
		return message.SendCommand{}, ErrUnauthenticatedSession
	}
	fromUID, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if fromUID == "" {
		return message.SendCommand{}, ErrUnauthenticatedSession
	}

	protocolVersion := uint8(frame.LatestVersion)
	if value, ok := ctx.Session.Value(coregateway.SessionValueProtocolVersion).(uint8); ok && value != 0 {
		protocolVersion = value
	}

	cmd := message.SendCommand{
		FromUID:         fromUID,
		SenderSessionID: ctx.Session.ID(),
		ProtocolVersion: protocolVersion,
	}
	if pkt == nil {
		return cmd, nil
	}
	cmd.ClientSeq = pkt.ClientSeq
	cmd.ClientMsgNo = pkt.ClientMsgNo
	cmd.ChannelID = pkt.ChannelID
	cmd.ChannelType = pkt.ChannelType
	cmd.Payload = cloneBytes(pkt.Payload)
	cmd.NoPersist = pkt.Framer.NoPersist
	cmd.SyncOnce = pkt.Framer.SyncOnce
	cmd.RedDot = pkt.Framer.RedDot
	cmd.MessageID = 0
	return cmd, nil
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
		MessageID:   int64(result.MessageID),
		MessageSeq:  result.MessageSeq,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ReasonCode:  mapReason(result.Reason),
	})
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
