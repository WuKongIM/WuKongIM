package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
)

func (h *Handler) OnSendBatch(items []coregateway.SendBatchItem) error {
	if len(items) == 0 {
		return nil
	}

	contexts := make([]coregateway.Context, len(items))
	results := make([]message.SendResult, len(items))
	validIndexes := make([]int, 0, len(items))
	validItems := make([]message.SendBatchItem, 0, len(items))
	deadline := time.Now().Add(h.sendTimeout)

	for i, item := range items {
		contexts[i] = item.Context
		if item.ReplyToken != "" {
			contexts[i].ReplyToken = item.ReplyToken
		}
		ctx := &contexts[i]
		cmd, err := mapSendCommandForBatch(ctx, item.Frame)
		if err != nil {
			if errors.Is(err, ErrUnauthenticatedSession) {
				results[i].Reason = message.ReasonAuthFail
				continue
			}
			return err
		}
		if ctx.RequestContext == nil {
			results[i].Reason = message.ReasonSystemError
			continue
		}
		validIndexes = append(validIndexes, i)
		validItems = append(validItems, message.SendBatchItem{Context: ctx.RequestContext, Deadline: deadline, Command: cmd})
	}

	if batcher, ok := h.messages.(MessageBatchUsecase); ok {
		batchResults := batcher.SendBatch(validItems)
		if len(batchResults) != len(validItems) {
			return ErrSendBatchResultCountMismatch
		}
		for j, index := range validIndexes {
			result := batchResults[j].Result
			if batchResults[j].Err != nil {
				result.Reason = reasonForError(batchResults[j].Err)
			}
			results[index] = result
		}
	} else {
		for j, index := range validIndexes {
			item := validItems[j]
			reqCtx, cancel := context.WithDeadline(item.Context, item.Deadline)
			results[index] = h.sendOne(reqCtx, item.Command)
			cancel()
		}
	}

	for i, item := range items {
		if err := writeSendack(&contexts[i], item.Frame, results[i]); err != nil {
			return err
		}
	}
	return nil
}
