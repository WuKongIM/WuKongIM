package gateway

import (
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func mapSendCommand(ctx *coregateway.Context, pkt *frame.SendPacket) (message.SendCommand, error) {
	return mapSendCommandWithPayload(ctx, pkt, 0, nil)
}

func mapSendCommandForBatch(ctx *coregateway.Context, pkt *frame.SendPacket) (message.SendCommand, error) {
	return mapSendCommandWithPayload(ctx, pkt, 0, nil)
}

func mapSendCommandWithPayload(ctx *coregateway.Context, pkt *frame.SendPacket, ownerNodeID uint64, traceIDGenerator TraceIDGenerator) (message.SendCommand, error) {
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
		DeviceID:        deviceIDFromValue(ctx.Session.Value(coregateway.SessionValueDeviceID)),
		DeviceFlag:      deviceFlagFromValue(ctx.Session.Value(coregateway.SessionValueDeviceFlag)),
		SenderNodeID:    ownerNodeID,
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
	cmd.NormalizePersonChannel = pkt.ChannelType == frame.ChannelTypePerson
	cmd.Payload = pkt.Payload
	cmd.NoPersist = pkt.Framer.NoPersist
	cmd.SyncOnce = pkt.Framer.SyncOnce
	cmd.RedDot = pkt.Framer.RedDot
	cmd.MessageID = 0
	applySendTraceMetadata(&cmd, pkt, traceIDGenerator)
	return cmd, nil
}

func applySendTraceMetadata(cmd *message.SendCommand, pkt *frame.SendPacket, traceIDGenerator TraceIDGenerator) {
	if cmd == nil || pkt == nil || traceIDGenerator == nil || pkt.ChannelID == "" || pkt.ChannelType == 0 || !sendtrace.Enabled() {
		return
	}
	traceID := traceIDGenerator()
	if traceID == "" {
		return
	}
	cmd.TraceID = traceID
	cmd.ChannelKey = sendtrace.ChannelKeyFromID(pkt.ChannelID, pkt.ChannelType)
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
