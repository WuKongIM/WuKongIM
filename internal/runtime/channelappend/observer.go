package channelappend

import (
	"context"
	"errors"
	"time"
)

const (
	channelAppendResultOK                 = "ok"
	channelAppendResultMixed              = "mixed"
	channelAppendResultCanceled           = "canceled"
	channelAppendResultTimeout            = "timeout"
	channelAppendResultBackpressured      = "backpressured"
	channelAppendResultChannelBusy        = "channel_busy"
	channelAppendResultRouteNotReady      = "route_not_ready"
	channelAppendResultStaleRoute         = "stale_route"
	channelAppendResultNotAuthority       = "not_authority"
	channelAppendResultNotLeader          = "not_leader"
	channelAppendResultChannelNotFound    = "channel_not_found"
	channelAppendResultAppendMissing      = "append_result_missing"
	channelAppendResultAppendFailed       = "append_failed"
	channelAppendResultCommitFailed       = "commit_failed"
	channelAppendResultInvalidSubscribers = "invalid_subscribers"
	channelAppendResultInvalidCursor      = "invalid_cursor"
	channelAppendResultUnsupported        = "unsupported"
	channelAppendResultAuthFail           = "auth_fail"
	channelAppendResultInvalidRequest     = "invalid_request"
	channelAppendResultSystemError        = "system_error"
	channelAppendResultOther              = "other"
)

const (
	recipientDeliveryResultAccepted = "accepted"
	recipientDeliveryResultClosed   = "closed"
	recipientDeliveryResultCanceled = "canceled"
	recipientDeliveryResultTimeout  = "timeout"
	recipientDeliveryResultError    = "error"
	recipientDeliveryResultOK       = "ok"
	recipientDeliveryResultPanic    = "panic"
)

func observeRouterGroup(observer RouterObserver, event RouterObservation) {
	if observer == nil {
		return
	}
	if event.Path == "" {
		event.Path = "unknown"
	}
	if event.Result == "" {
		event.Result = channelAppendResultOther
	}
	observer.ObserveChannelAppendRouter(event)
}

func observeLocalAdmission(observer AppendObserver, event LocalAdmissionObservation) {
	admissionObserver, ok := observer.(LocalAdmissionObserver)
	if !ok || admissionObserver == nil {
		return
	}
	if event.Result == "" {
		event.Result = channelAppendResultOther
	}
	admissionObserver.ObserveChannelAppendLocalAdmission(event)
}

func observeWriterPressure(observer AppendObserver, event WriterPressureObservation) {
	pressureObserver := writerPressureObserver(observer)
	if pressureObserver == nil {
		return
	}
	pressureObserver.SetChannelAppendWriterPressure(event)
}

func writerPressureObserver(observer AppendObserver) WriterPressureObserver {
	pressureObserver, ok := observer.(WriterPressureObserver)
	if !ok || pressureObserver == nil {
		return nil
	}
	return pressureObserver
}

func observeEffectPool(observer AppendObserver, event EffectPoolObservation) {
	poolObserver, ok := observer.(EffectPoolObserver)
	if !ok || poolObserver == nil {
		return
	}
	if event.Stage == "" {
		event.Stage = "unknown"
	}
	if event.Result == "" {
		event.Result = "unknown"
	}
	poolObserver.ObserveChannelAppendEffectPool(event)
}

func observeEffect(observer AppendObserver, event EffectObservation) {
	effectObserver, ok := observer.(EffectObserver)
	if !ok || effectObserver == nil {
		return
	}
	if event.Stage == "" {
		event.Stage = "unknown"
	}
	if event.Result == "" {
		event.Result = channelAppendResultOther
	}
	effectObserver.ObserveChannelAppendEffect(event)
}

func observePostCommitFailure(observer AppendObserver, event PostCommitFailureObservation) {
	failureObserver, ok := observer.(PostCommitFailureObserver)
	if !ok || failureObserver == nil {
		return
	}
	if event.Result == "" {
		event.Result = channelAppendResultOther
	}
	failureObserver.ObserveChannelAppendPostCommitFailure(event)
}

func observeRecipientDeliveryQueue(observer AppendObserver, event RecipientDeliveryQueueObservation) {
	queueObserver, ok := observer.(RecipientDeliveryQueueObserver)
	if !ok || queueObserver == nil {
		return
	}
	if event.QueueDepth < 0 {
		event.QueueDepth = 0
	}
	if event.QueueCapacity < 0 {
		event.QueueCapacity = 0
	}
	queueObserver.SetChannelAppendRecipientDeliveryQueue(event)
}

func observeRecipientDeliveryWorkerPressure(observer AppendObserver, event RecipientDeliveryWorkerPressureObservation) {
	pressureObserver, ok := observer.(RecipientDeliveryWorkerPressureObserver)
	if !ok || pressureObserver == nil {
		return
	}
	if event.Inflight < 0 {
		event.Inflight = 0
	}
	if event.Capacity < 0 {
		event.Capacity = 0
	}
	pressureObserver.SetChannelAppendRecipientDeliveryWorkerPressure(event)
}

func observeRecipientDeliveryAdmission(observer AppendObserver, event RecipientDeliveryAdmissionObservation) {
	admissionObserver, ok := observer.(RecipientDeliveryAdmissionObserver)
	if !ok || admissionObserver == nil {
		return
	}
	event.Result = recipientDeliveryResult(event.Result)
	if event.QueueDepth < 0 {
		event.QueueDepth = 0
	}
	if event.QueueCapacity < 0 {
		event.QueueCapacity = 0
	}
	if event.Duration < 0 {
		event.Duration = 0
	}
	admissionObserver.ObserveChannelAppendRecipientDeliveryAdmission(event)
}

func observeRecipientDeliveryProcess(observer AppendObserver, event RecipientDeliveryProcessObservation) {
	processObserver, ok := observer.(RecipientDeliveryProcessObserver)
	if !ok || processObserver == nil {
		return
	}
	event.Result = recipientDeliveryResult(event.Result)
	if event.Recipients < 0 {
		event.Recipients = 0
	}
	if event.Duration < 0 {
		event.Duration = 0
	}
	processObserver.ObserveChannelAppendRecipientDeliveryProcess(event)
}

func recipientDeliveryResult(result string) string {
	switch result {
	case recipientDeliveryResultAccepted,
		recipientDeliveryResultClosed,
		recipientDeliveryResultCanceled,
		recipientDeliveryResultTimeout,
		recipientDeliveryResultError,
		recipientDeliveryResultOK,
		recipientDeliveryResultPanic:
		return result
	default:
		return recipientDeliveryResultError
	}
}

func routerResultsClass(results []SendBatchItemResult) string {
	if len(results) == 0 {
		return channelAppendResultOK
	}
	class := ""
	for _, result := range results {
		next := resultClass(result)
		if class == "" {
			class = next
			continue
		}
		if class != next {
			return channelAppendResultMixed
		}
	}
	return class
}

func resultClass(result SendBatchItemResult) string {
	if result.Err != nil {
		return errorClass(result.Err)
	}
	switch result.Result.Reason {
	case ReasonSuccess:
		return channelAppendResultOK
	case ReasonInvalidRequest:
		return channelAppendResultInvalidRequest
	case ReasonAuthFail:
		return channelAppendResultAuthFail
	case ReasonChannelNotExist:
		return channelAppendResultChannelNotFound
	case ReasonNodeNotMatch:
		return channelAppendResultRouteNotReady
	case ReasonSystemError:
		return channelAppendResultSystemError
	case ReasonUnsupported:
		return channelAppendResultUnsupported
	default:
		return channelAppendResultOther
	}
}

func errorClass(err error) string {
	switch {
	case err == nil:
		return channelAppendResultOK
	case errors.Is(err, context.Canceled):
		return channelAppendResultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return channelAppendResultTimeout
	case errors.Is(err, ErrBackpressured):
		return channelAppendResultBackpressured
	case errors.Is(err, ErrChannelBusy):
		return channelAppendResultChannelBusy
	case errors.Is(err, ErrRouteNotReady):
		return channelAppendResultRouteNotReady
	case errors.Is(err, ErrStaleRoute):
		return channelAppendResultStaleRoute
	case errors.Is(err, ErrNotChannelAuthority):
		return channelAppendResultNotAuthority
	case errors.Is(err, ErrNotLeader):
		return channelAppendResultNotLeader
	case errors.Is(err, ErrChannelNotFound):
		return channelAppendResultChannelNotFound
	case errors.Is(err, ErrAppendResultMissing):
		return channelAppendResultAppendMissing
	case errors.Is(err, ErrAppendFailed), errors.Is(err, ErrAppenderRequired):
		return channelAppendResultAppendFailed
	case errors.Is(err, ErrCommitEffectFailed):
		return channelAppendResultCommitFailed
	case errors.Is(err, ErrInvalidSubscriberCursor):
		return channelAppendResultInvalidCursor
	case errors.Is(err, ErrRequestSubscribersRequired),
		errors.Is(err, ErrRequestSubscribersRequireSyncOnce),
		errors.Is(err, ErrRequestSubscribersConflictChannel):
		return channelAppendResultInvalidSubscribers
	default:
		return channelAppendResultOther
	}
}

func elapsedSince(started time.Time) time.Duration {
	if started.IsZero() {
		return 0
	}
	return time.Since(started)
}
