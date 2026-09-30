package gateway

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// DeferredMessageUsecase joins preparation and transfers serialized emissions
// followed by one completion. A returned error transfers no callbacks.
type DeferredMessageUsecase interface {
	SubmitBatchEach([]message.SendBatchItem, func(int, message.SendBatchItemResult) error, func(error)) error
}

// WithDeferredSends shares this handler's session lifecycle and terminal binding,
// exposing deferred SEND only when composition supplies its owned message port.
func (h *Handler) WithDeferredSends(messages DeferredMessageUsecase) coregateway.Handler {
	if messages == nil {
		return h
	}
	return &deferredHandler{Handler: h, messages: messages}
}

type deferredHandler struct {
	*Handler
	messages DeferredMessageUsecase
}

type preparedGatewayBatch struct {
	contexts                         []coregateway.Context
	prechecked                       []bool
	precheckResults                  []message.SendResult
	precheckSources, precheckClasses []string
	validIndexes                     []int
	validItems                       []message.SendBatchItem
	traceFields                      []sendTraceFields
}

func (h *deferredHandler) OnSendBatchDeferred(items []coregateway.SendBatchItem, publish func(int, func() error) error, done func(error)) error {
	if publish == nil || done == nil {
		return ErrSendBatchResultCountMismatch
	}
	batch, err := h.prepareSendBatch(items)
	if err != nil {
		return err
	}
	// This closure captures values only. Core may retain it after preparation and
	// result callbacks return, and remains the sole cross-batch publication owner.
	queue := func(index int, result gatewayBatchSendack) error {
		ctx, pkt := batch.contexts[index], items[index].Frame
		return publish(index, func() error {
			return h.writeSendack(&ctx, pkt, result.result, result.source, result.class, result.trace)
		})
	}
	for i := range items {
		if !batch.prechecked[i] {
			continue
		}
		result := gatewayBatchSendack{result: batch.precheckResults[i], source: batch.precheckSources[i], class: batch.precheckClasses[i]}
		if batch.traceFields != nil {
			result.trace = batch.traceFields[i]
		}
		if err := queue(i, result); err != nil {
			return err
		}
	}
	if len(batch.validItems) == 0 {
		done(nil)
		return nil
	}
	var started time.Time
	if batch.traceFields != nil {
		started = time.Now()
	}
	emitted := make([]bool, len(batch.validItems))
	emittedCount, emissionCount := 0, 0
	return h.messages.SubmitBatchEach(batch.validItems, func(index int, result message.SendBatchItemResult) error {
		emissionCount++
		if index < 0 || index >= len(emitted) || emitted[index] {
			return ErrSendBatchResultCountMismatch
		}
		emitted[index] = true
		emittedCount++
		return queue(batch.validIndexes[index], h.sendBatchCompletion(batch, index, result, started))
	}, func(err error) {
		if err == nil && emittedCount != len(emitted) {
			err = ErrSendBatchResultCountMismatch
		}
		if errors.Is(err, ErrSendBatchResultCountMismatch) {
			h.logSendBatchResultCountMismatch(len(items), len(emitted), emissionCount)
		}
		done(err)
	})
}

func (h *Handler) sendBatchCompletion(batch *preparedGatewayBatch, index int, result message.SendBatchItemResult, started time.Time) gatewayBatchSendack {
	completion := gatewayBatchSendack{result: result.Result, source: sendackSourceBatchResult, class: sendackErrorClassNone}
	if result.Err != nil {
		completion.result.Reason = reasonForError(result.Err)
		completion.source = sendackSourceBatchResultError
		completion.class = sendackErrorClassForError(result.Err)
		h.logSendFailure(batch.validItems[index].Command, completion.source, completion.class, result.Err)
	}
	if batch.traceFields != nil {
		recordGatewayMessagesSend(batch.validItems[index].Command, completion.result, completion.class, sendtraceElapsedSince(started))
		completion.trace = batch.traceFields[batch.validIndexes[index]]
	}
	return completion
}

// prepareSendBatch shares protocol mapping and prechecks between joined and deferred entries.
func (h *Handler) prepareSendBatch(items []coregateway.SendBatchItem) (*preparedGatewayBatch, error) {
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

	for i := range items {
		item := items[i]
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
			return nil, err
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
	return &preparedGatewayBatch{contexts: contexts, prechecked: prechecked, precheckResults: precheckResults, precheckSources: precheckSources, precheckClasses: precheckClasses, validIndexes: validIndexes, validItems: validItems, traceFields: traceFields}, nil
}
