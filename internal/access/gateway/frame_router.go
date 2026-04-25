package gateway

import (
	"context"

	coregateway "github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func (h *Handler) OnFrame(ctx *coregateway.Context, f frame.Frame) error {
	switch pkt := f.(type) {
	case *frame.SendPacket:
		return h.handleSend(ctx, pkt)
	case *frame.RecvackPacket:
		return h.handleRecvAck(ctx, pkt)
	case *frame.PingPacket:
		return h.handlePing(ctx)
	default:
		return ErrUnsupportedFrame
	}
}

func (h *Handler) handleSend(ctx *coregateway.Context, pkt *frame.SendPacket) error {
	if reason, err := decryptSendPacketIfNeeded(ctx, pkt); err != nil {
		fields := append([]wklog.Field{
			wklog.Event("access.gateway.frame.send_decrypt_failed"),
		}, gatewaySendFields(ctx, pkt.ChannelID, pkt.ChannelType)...)
		fields = append(fields, wklog.Error(err))
		h.frameLogger().Warn("reject encrypted send request", fields...)
		return writeSendack(ctx, pkt, message.SendResult{Reason: reason})
	}

	cmd, err := mapSendCommand(ctx, pkt)
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("access.gateway.frame.send_rejected"),
		}, gatewaySendFields(ctx, pkt.ChannelID, pkt.ChannelType)...)
		fields = append(fields, wklog.Error(err))
		h.frameLogger().Warn("reject send request", fields...)
		if reason, ok := mapSendErrorReason(err); ok {
			return writeSendack(ctx, pkt, message.SendResult{Reason: reason})
		}
		return err
	}

	if ctx == nil || ctx.RequestContext == nil {
		return ErrMissingRequestContext
	}
	reqCtx, cancel := context.WithTimeout(ctx.RequestContext, h.sendTimeout)
	defer cancel()

	sendStartedAt := h.now()
	result, err := h.messages.Send(reqCtx, cmd)
	sendtrace.Record(sendtrace.Event{
		Stage:       sendtrace.StageGatewayMessagesSend,
		At:          sendStartedAt,
		Duration:    sendtrace.Elapsed(sendStartedAt, h.now()),
		NodeID:      h.localNodeID,
		ClientMsgNo: cmd.ClientMsgNo,
		MessageSeq:  result.MessageSeq,
	})
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("access.gateway.frame.send_failed"),
			wklog.SourceModule("message.send"),
		}, gatewaySendFields(ctx, cmd.ChannelID, cmd.ChannelType)...)
		fields = append(fields, wklog.Error(err))
		h.frameLogger().Warn("send request failed", fields...)
		if reason, ok := mapSendErrorReason(err); ok {
			result.Reason = reason
			return h.writeSendackWithTrace(ctx, pkt, cmd.ClientMsgNo, result)
		}
		return err
	}

	return h.writeSendackWithTrace(ctx, pkt, cmd.ClientMsgNo, result)
}

func (h *Handler) handleRecvAck(ctx *coregateway.Context, pkt *frame.RecvackPacket) error {
	cmd, err := mapRecvAckCommand(ctx, pkt)
	if err != nil {
		return err
	}
	return h.messages.RecvAck(cmd)
}

func (h *Handler) handlePing(ctx *coregateway.Context) error {
	return writePong(ctx)
}

func (h *Handler) writeSendackWithTrace(ctx *coregateway.Context, pkt *frame.SendPacket, clientMsgNo string, result message.SendResult) error {
	startedAt := h.now()
	err := writeSendack(ctx, pkt, result)
	sendtrace.Record(sendtrace.Event{
		Stage:       sendtrace.StageGatewayWriteSendack,
		At:          startedAt,
		Duration:    sendtrace.Elapsed(startedAt, h.now()),
		NodeID:      h.localNodeID,
		ClientMsgNo: clientMsgNo,
		MessageSeq:  result.MessageSeq,
	})
	return err
}
