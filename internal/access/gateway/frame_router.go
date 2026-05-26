package gateway

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics/tracectx"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func (h *Handler) OnFrame(ctx coregateway.Context, f frame.Frame) error {
	switch pkt := f.(type) {
	case *frame.SendPacket:
		return h.handleSend(&ctx, pkt)
	case *frame.RecvackPacket:
		return h.handleRecvAck(&ctx, pkt)
	case *frame.PingPacket:
		return h.handlePing(&ctx)
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

	traceEnabled := sendtrace.Enabled()
	channelKey := ""
	if traceEnabled {
		var traceCtx tracectx.Context
		reqCtx, traceCtx = tracectx.Ensure(reqCtx, h.now)
		cmd.TraceID = traceCtx.TraceID
		channelKey = string(channelhandler.KeyFromChannelID(channel.ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}))
	}

	sendStartedAt := h.now()
	result, err := h.messages.Send(reqCtx, cmd)
	if traceEnabled {
		sendtrace.Record(sendtrace.Event{
			TraceID:     cmd.TraceID,
			Stage:       sendtrace.StageGatewayMessagesSend,
			At:          sendStartedAt,
			Duration:    sendtrace.Elapsed(sendStartedAt, h.now()),
			NodeID:      h.localNodeID,
			ChannelKey:  channelKey,
			ClientMsgNo: cmd.ClientMsgNo,
			MessageSeq:  result.MessageSeq,
			FromUID:     cmd.FromUID,
		})
	}
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("access.gateway.frame.send_failed"),
			wklog.SourceModule("message.send"),
		}, gatewaySendFields(ctx, cmd.ChannelID, cmd.ChannelType)...)
		fields = append(fields, wklog.Error(err))
		h.frameLogger().Warn("send request failed", fields...)
		if reason, ok := mapSendErrorReason(err); ok {
			result.Reason = reason
			if !traceEnabled {
				return writeSendack(ctx, pkt, result)
			}
			return h.writeSendackWithTrace(ctx, pkt, cmd.ClientMsgNo, cmd.TraceID, channelKey, cmd.FromUID, result)
		}
		return err
	}

	if !traceEnabled {
		return writeSendack(ctx, pkt, result)
	}
	return h.writeSendackWithTrace(ctx, pkt, cmd.ClientMsgNo, cmd.TraceID, channelKey, cmd.FromUID, result)
}

func (h *Handler) OnSendBatch(items []coregateway.SendBatchItem) error {
	if len(items) == 0 {
		return nil
	}

	results := make([]message.SendResult, len(items))
	contexts := make([]coregateway.Context, len(items))
	channelKeys := make([]string, len(items))
	fromUIDs := make([]string, len(items))
	clientMsgNos := make([]string, len(items))
	traceIDs := make([]string, len(items))
	traceEnabled := sendtrace.Enabled()
	validIndexes := make([]int, 0, len(items))
	validItems := make([]message.SendBatchItem, 0, len(items))

	for i, item := range items {
		contexts[i] = sendBatchGatewayContext(item)
		ctx := &contexts[i]
		pkt := item.Frame
		if reason, err := decryptSendPacketIfNeeded(ctx, pkt); err != nil {
			fields := append([]wklog.Field{
				wklog.Event("access.gateway.frame.send_decrypt_failed"),
			}, gatewaySendFields(ctx, pkt.ChannelID, pkt.ChannelType)...)
			fields = append(fields, wklog.Error(err))
			h.frameLogger().Warn("reject encrypted send request", fields...)
			results[i].Reason = reason
			continue
		}

		cmd, err := mapSendCommand(ctx, pkt)
		if err != nil {
			fields := append([]wklog.Field{
				wklog.Event("access.gateway.frame.send_rejected"),
			}, gatewaySendFields(ctx, pkt.ChannelID, pkt.ChannelType)...)
			fields = append(fields, wklog.Error(err))
			h.frameLogger().Warn("reject send request", fields...)
			if errors.Is(err, ErrUnauthenticatedSession) {
				results[i].Reason = frame.ReasonAuthFail
				continue
			}
			if reason, ok := mapSendErrorReason(err); ok {
				results[i].Reason = reason
				continue
			}
			return err
		}
		if ctx.RequestContext == nil {
			results[i].Reason = frame.ReasonSystemError
			continue
		}

		reqCtx, cancel := context.WithTimeout(ctx.RequestContext, h.sendTimeout)
		defer cancel()
		if traceEnabled {
			var traceCtx tracectx.Context
			reqCtx, traceCtx = tracectx.Ensure(reqCtx, h.now)
			cmd.TraceID = traceCtx.TraceID
			traceIDs[i] = traceCtx.TraceID
			channelKeys[i] = string(channelhandler.KeyFromChannelID(channel.ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}))
		}
		fromUIDs[i] = cmd.FromUID
		clientMsgNos[i] = cmd.ClientMsgNo
		validIndexes = append(validIndexes, i)
		validItems = append(validItems, message.SendBatchItem{Context: reqCtx, Command: cmd})
	}

	sendStartedAt := h.now()
	batchResults := h.sendMessageBatch(validItems)
	sendDuration := sendtrace.Elapsed(sendStartedAt, h.now())
	for j, itemIndex := range validIndexes {
		var itemResult message.SendBatchItemResult
		if j < len(batchResults) {
			itemResult = batchResults[j]
		} else {
			itemResult.Err = errors.New("access/gateway: send batch result count mismatch")
		}
		result := itemResult.Result
		if traceEnabled {
			sendtrace.Record(sendtrace.Event{
				TraceID:     traceIDs[itemIndex],
				Stage:       sendtrace.StageGatewayMessagesSend,
				At:          sendStartedAt,
				Duration:    sendDuration,
				NodeID:      h.localNodeID,
				ChannelKey:  channelKeys[itemIndex],
				ClientMsgNo: clientMsgNos[itemIndex],
				MessageSeq:  result.MessageSeq,
				FromUID:     fromUIDs[itemIndex],
			})
		}
		if itemResult.Err != nil {
			fields := append([]wklog.Field{
				wklog.Event("access.gateway.frame.send_failed"),
				wklog.SourceModule("message.send"),
			}, gatewaySendFields(&contexts[itemIndex], validItems[j].Command.ChannelID, validItems[j].Command.ChannelType)...)
			fields = append(fields, wklog.Error(itemResult.Err))
			h.frameLogger().Warn("send request failed", fields...)
			if reason, ok := mapSendErrorReason(itemResult.Err); ok {
				result.Reason = reason
			} else {
				return itemResult.Err
			}
		}
		results[itemIndex] = result
	}

	for i, item := range items {
		ctx := &contexts[i]
		pkt := item.Frame
		if traceEnabled {
			if err := h.writeSendackWithTrace(ctx, pkt, clientMsgNos[i], traceIDs[i], channelKeys[i], fromUIDs[i], results[i]); err != nil {
				return err
			}
			continue
		}
		if err := writeSendack(ctx, pkt, results[i]); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) sendMessageBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	if len(items) == 0 {
		return nil
	}
	if batcher, ok := h.messages.(MessageBatchUsecase); ok {
		return batcher.SendBatch(items)
	} else {
		panic("message usecase does not implement MessageBatchUsecase")
	}
}

func sendBatchGatewayContext(item coregateway.SendBatchItem) coregateway.Context {
	ctx := item.Context
	if item.ReplyToken != "" {
		ctx.ReplyToken = item.ReplyToken
	}
	return ctx
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

func (h *Handler) writeSendackWithTrace(ctx *coregateway.Context, pkt *frame.SendPacket, clientMsgNo, traceID, channelKey, fromUID string, result message.SendResult) error {
	startedAt := h.now()
	err := writeSendack(ctx, pkt, result)
	sendtrace.Record(sendtrace.Event{
		TraceID:     traceID,
		Stage:       sendtrace.StageGatewayWriteSendack,
		At:          startedAt,
		Duration:    sendtrace.Elapsed(startedAt, h.now()),
		NodeID:      h.localNodeID,
		ChannelKey:  channelKey,
		ClientMsgNo: clientMsgNo,
		MessageSeq:  result.MessageSeq,
		FromUID:     fromUID,
	})
	return err
}
