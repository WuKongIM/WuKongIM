package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func (h *Handler) OnSendBatch(items []coregateway.SendBatchItem) error {
	if len(items) == 0 {
		return nil
	}

	contexts := make([]coregateway.Context, len(items))
	results := make([]message.SendResult, len(items))
	sources := make([]string, len(items))
	classes := make([]string, len(items))
	validIndexes := make([]int, 0, len(items))
	validItems := make([]message.SendBatchItem, 0, len(items))
	deadline := time.Now().Add(h.sendTimeout)

	for i, item := range items {
		contexts[i] = item.Context
		if item.ReplyToken != "" {
			contexts[i].ReplyToken = item.ReplyToken
		}
		ctx := &contexts[i]
		cmd, err := mapSendCommandWithPayload(ctx, item.Frame, h.ownerNodeID, false)
		if err != nil {
			if errors.Is(err, ErrUnauthenticatedSession) {
				results[i].Reason = message.ReasonAuthFail
				sources[i] = sendackSourceBatchPrecheck
				classes[i] = sendackErrorClassUnauthenticated
				continue
			}
			h.logSendMappingFailure(ctx, item.Frame, err)
			return err
		}
		if ctx.RequestContext == nil {
			results[i].Reason = message.ReasonSystemError
			sources[i] = sendackSourceBatchMissingRequestContext
			classes[i] = sendackErrorClassMissingRequestContext
			h.logMissingRequestContext(ctx, item.Frame, sendackSourceBatchMissingRequestContext)
			continue
		}
		validIndexes = append(validIndexes, i)
		validItems = append(validItems, message.SendBatchItem{Context: ctx.RequestContext, Deadline: deadline, Command: cmd})
	}

	if batcher, ok := h.messages.(MessageBatchUsecase); ok {
		batchResults := batcher.SendBatch(validItems)
		if len(batchResults) != len(validItems) {
			h.frameLogger().Error("gateway send batch result count mismatch",
				wklog.Event("internalv2.access.gateway.send_batch_result_count_mismatch"),
				wklog.Int("items", len(items)),
				wklog.Int("validItems", len(validItems)),
				wklog.Int("results", len(batchResults)),
				wklog.Error(ErrSendBatchResultCountMismatch),
			)
			return ErrSendBatchResultCountMismatch
		}
		for j, index := range validIndexes {
			result := batchResults[j].Result
			if batchResults[j].Err != nil {
				result.Reason = reasonForError(batchResults[j].Err)
				sources[index] = sendackSourceBatchResultError
				classes[index] = sendackErrorClassForError(batchResults[j].Err)
				h.logSendFailure(validItems[j].Command, sendackSourceBatchResultError, classes[index], batchResults[j].Err)
			} else {
				sources[index] = sendackSourceBatchResult
				classes[index] = sendackErrorClassNone
			}
			results[index] = result
		}
	} else {
		for j, index := range validIndexes {
			item := validItems[j]
			reqCtx, cancel := context.WithDeadline(item.Context, item.Deadline)
			result, source, class := h.sendOne(reqCtx, item.Command)
			if source == sendackSourceSingleError {
				source = sendackSourceBatchFallbackError
			} else {
				source = sendackSourceBatchFallbackResult
			}
			results[index] = result
			sources[index] = source
			classes[index] = class
			cancel()
		}
	}

	for i, item := range items {
		if err := h.writeSendack(&contexts[i], item.Frame, results[i], sources[i], classes[i]); err != nil {
			return err
		}
	}
	return nil
}
