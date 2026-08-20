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
	prechecked := make([]bool, len(items))
	precheckResults := make([]message.SendResult, len(items))
	precheckSources := make([]string, len(items))
	precheckClasses := make([]string, len(items))
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
				prechecked[i] = true
				precheckResults[i].Reason = message.ReasonAuthFail
				precheckSources[i] = sendackSourceBatchPrecheck
				precheckClasses[i] = sendackErrorClassUnauthenticated
				continue
			}
			h.logSendMappingFailure(ctx, item.Frame, err)
			return err
		}
		if traceFields != nil {
			traceFields[i] = sendTraceFieldsFromCommand(cmd)
		}
		if ctx.RequestContext == nil {
			prechecked[i] = true
			precheckResults[i].Reason = message.ReasonSystemError
			precheckSources[i] = sendackSourceBatchMissingRequestContext
			precheckClasses[i] = sendackErrorClassMissingRequestContext
			h.logMissingRequestContext(ctx, item.Frame, sendackSourceBatchMissingRequestContext)
			continue
		}
		validIndexes = append(validIndexes, i)
		validItems = append(validItems, message.SendBatchItem{Context: ctx.RequestContext, Deadline: deadline, Command: cmd})
	}
	for i, item := range items {
		if !prechecked[i] {
			continue
		}
		var trace sendTraceFields
		if traceFields != nil {
			trace = traceFields[i]
		}
		if err := h.writeSendack(&contexts[i], item.Frame, precheckResults[i], precheckSources[i], precheckClasses[i], trace); err != nil {
			return err
		}
	}

	if h.messages == nil {
		h.logMessageUsecaseMissing()
		for j, index := range validIndexes {
			result := message.SendResult{Reason: message.ReasonSystemError}
			if traceFields != nil {
				recordGatewayMessagesSend(validItems[j].Command, result, sendackErrorClassOther, 0)
			}
			var trace sendTraceFields
			if traceFields != nil {
				trace = traceFields[index]
			}
			if err := h.writeSendack(&contexts[index], items[index].Frame, result, sendackSourceBatchResult, sendackErrorClassOther, trace); err != nil {
				return err
			}
		}
	} else {
		var startedAt time.Time
		if traceFields != nil {
			startedAt = time.Now()
		}
		emitted := make([]bool, len(validItems))
		emittedCount := 0
		emissionCount := 0
		batchErr := h.messages.SendBatchEach(validItems, func(j int, batchResult message.SendBatchItemResult) error {
			emissionCount++
			if j < 0 || j >= len(validItems) || emitted[j] {
				return ErrSendBatchResultCountMismatch
			}
			emitted[j] = true
			emittedCount++
			index := validIndexes[j]
			result := batchResult.Result
			source := sendackSourceBatchResult
			class := sendackErrorClassNone
			if batchResult.Err != nil {
				result.Reason = reasonForError(batchResult.Err)
				source = sendackSourceBatchResultError
				class = sendackErrorClassForError(batchResult.Err)
				h.logSendFailure(validItems[j].Command, source, class, batchResult.Err)
			}
			if traceFields != nil {
				recordGatewayMessagesSend(validItems[j].Command, result, class, sendtraceElapsedSince(startedAt))
			}
			var trace sendTraceFields
			if traceFields != nil {
				trace = traceFields[index]
			}
			return h.writeSendack(&contexts[index], items[index].Frame, result, source, class, trace)
		})
		if batchErr != nil {
			if errors.Is(batchErr, ErrSendBatchResultCountMismatch) {
				h.logSendBatchResultCountMismatch(len(items), len(validItems), emissionCount)
			}
			return batchErr
		}
		if emittedCount != len(validItems) {
			h.logSendBatchResultCountMismatch(len(items), len(validItems), emittedCount)
			return ErrSendBatchResultCountMismatch
		}
	}
	return nil
}
