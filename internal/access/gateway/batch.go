package gateway

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
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
	var traceIDGenerator TraceIDGenerator
	var traceFields []sendTraceFields
	if sendtrace.Enabled() {
		traceIDGenerator = h.traceIDGenerator
		traceFields = make([]sendTraceFields, len(items))
	}

	for i, item := range items {
		contexts[i] = item.Context
		if item.ReplyToken != "" {
			contexts[i].ReplyToken = item.ReplyToken
		}
		ctx := &contexts[i]
		cmd, err := mapSendCommandWithPayload(ctx, item.Frame, h.ownerNodeID, traceIDGenerator)
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
		if traceFields != nil {
			traceFields[i] = sendTraceFieldsFromCommand(cmd)
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

	if h.messages == nil {
		h.logMessageUsecaseMissing()
		for j, index := range validIndexes {
			result := message.SendResult{Reason: message.ReasonSystemError}
			results[index] = result
			sources[index] = sendackSourceBatchResult
			classes[index] = sendackErrorClassOther
			if traceFields != nil {
				recordGatewayMessagesSend(validItems[j].Command, result, classes[index], 0)
			}
		}
	} else {
		var startedAt time.Time
		if traceFields != nil {
			startedAt = time.Now()
		}
		batchResults := h.messages.SendBatch(validItems)
		duration := time.Duration(0)
		if traceFields != nil {
			duration = sendtraceElapsedSince(startedAt)
		}
		if len(batchResults) != len(validItems) {
			h.logSendBatchResultCountMismatch(len(items), len(validItems), len(batchResults))
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
			if traceFields != nil {
				recordGatewayMessagesSend(validItems[j].Command, result, classes[index], duration)
			}
		}
	}

	for i, item := range items {
		var trace sendTraceFields
		if traceFields != nil {
			trace = traceFields[i]
		}
		if err := h.writeSendack(&contexts[i], item.Frame, results[i], sources[i], classes[i], trace); err != nil {
			return err
		}
	}
	return nil
}
