package channelwrite

import (
	"context"
	"errors"
	"time"
)

const (
	channelWriteResultOK                 = "ok"
	channelWriteResultMixed              = "mixed"
	channelWriteResultCanceled           = "canceled"
	channelWriteResultTimeout            = "timeout"
	channelWriteResultBackpressured      = "backpressured"
	channelWriteResultChannelBusy        = "channel_busy"
	channelWriteResultRouteNotReady      = "route_not_ready"
	channelWriteResultStaleRoute         = "stale_route"
	channelWriteResultNotAuthority       = "not_authority"
	channelWriteResultNotLeader          = "not_leader"
	channelWriteResultChannelNotFound    = "channel_not_found"
	channelWriteResultAppendMissing      = "append_result_missing"
	channelWriteResultAppendFailed       = "append_failed"
	channelWriteResultCommitFailed       = "commit_failed"
	channelWriteResultInvalidSubscribers = "invalid_subscribers"
	channelWriteResultInvalidCursor      = "invalid_cursor"
	channelWriteResultUnsupported        = "unsupported"
	channelWriteResultAuthFail           = "auth_fail"
	channelWriteResultInvalidRequest     = "invalid_request"
	channelWriteResultSystemError        = "system_error"
	channelWriteResultOther              = "other"
)

func observeRouterGroup(observer RouterObserver, event RouterObservation) {
	if observer == nil {
		return
	}
	if event.Path == "" {
		event.Path = "unknown"
	}
	if event.Result == "" {
		event.Result = channelWriteResultOther
	}
	observer.ObserveChannelWriteRouter(event)
}

func observeLocalAdmission(observer AppendObserver, event LocalAdmissionObservation) {
	admissionObserver, ok := observer.(LocalAdmissionObserver)
	if !ok || admissionObserver == nil {
		return
	}
	if event.Result == "" {
		event.Result = channelWriteResultOther
	}
	admissionObserver.ObserveChannelWriteLocalAdmission(event)
}

func observeReactorPressure(observer AppendObserver, event ReactorPressureObservation) {
	pressureObserver := reactorPressureObserver(observer)
	if pressureObserver == nil {
		return
	}
	pressureObserver.SetChannelWriteReactorPressure(event)
}

func reactorPressureObserver(observer AppendObserver) ReactorPressureObserver {
	pressureObserver, ok := observer.(ReactorPressureObserver)
	if !ok || pressureObserver == nil {
		return nil
	}
	return pressureObserver
}

func observeEffectWorkerPressure(observer AppendObserver, event EffectWorkerPressureObservation) {
	pressureObserver, ok := observer.(EffectWorkerPressureObserver)
	if !ok || pressureObserver == nil {
		return
	}
	if event.Stage == "" {
		event.Stage = "unknown"
	}
	pressureObserver.SetChannelWriteEffectWorkerPressure(event)
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
		event.Result = channelWriteResultOther
	}
	effectObserver.ObserveChannelWriteEffect(event)
}

func observePostCommitFailure(observer AppendObserver, event PostCommitFailureObservation) {
	failureObserver, ok := observer.(PostCommitFailureObserver)
	if !ok || failureObserver == nil {
		return
	}
	if event.Result == "" {
		event.Result = channelWriteResultOther
	}
	failureObserver.ObserveChannelWritePostCommitFailure(event)
}

func routerResultsClass(results []SendBatchItemResult) string {
	if len(results) == 0 {
		return channelWriteResultOK
	}
	class := ""
	for _, result := range results {
		next := resultClass(result)
		if class == "" {
			class = next
			continue
		}
		if class != next {
			return channelWriteResultMixed
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
		return channelWriteResultOK
	case ReasonInvalidRequest:
		return channelWriteResultInvalidRequest
	case ReasonAuthFail:
		return channelWriteResultAuthFail
	case ReasonChannelNotExist:
		return channelWriteResultChannelNotFound
	case ReasonNodeNotMatch:
		return channelWriteResultRouteNotReady
	case ReasonSystemError:
		return channelWriteResultSystemError
	case ReasonUnsupported:
		return channelWriteResultUnsupported
	default:
		return channelWriteResultOther
	}
}

func errorClass(err error) string {
	switch {
	case err == nil:
		return channelWriteResultOK
	case errors.Is(err, context.Canceled):
		return channelWriteResultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return channelWriteResultTimeout
	case errors.Is(err, ErrBackpressured):
		return channelWriteResultBackpressured
	case errors.Is(err, ErrChannelBusy):
		return channelWriteResultChannelBusy
	case errors.Is(err, ErrRouteNotReady):
		return channelWriteResultRouteNotReady
	case errors.Is(err, ErrStaleRoute):
		return channelWriteResultStaleRoute
	case errors.Is(err, ErrNotChannelAuthority):
		return channelWriteResultNotAuthority
	case errors.Is(err, ErrNotLeader):
		return channelWriteResultNotLeader
	case errors.Is(err, ErrChannelNotFound):
		return channelWriteResultChannelNotFound
	case errors.Is(err, ErrAppendResultMissing):
		return channelWriteResultAppendMissing
	case errors.Is(err, ErrAppendFailed), errors.Is(err, ErrAppenderRequired):
		return channelWriteResultAppendFailed
	case errors.Is(err, ErrCommitEffectFailed):
		return channelWriteResultCommitFailed
	case errors.Is(err, ErrInvalidSubscriberCursor):
		return channelWriteResultInvalidCursor
	case errors.Is(err, ErrRequestSubscribersRequired),
		errors.Is(err, ErrRequestSubscribersRequireSyncOnce),
		errors.Is(err, ErrRequestSubscribersConflictChannel):
		return channelWriteResultInvalidSubscribers
	default:
		return channelWriteResultOther
	}
}

func elapsedSince(started time.Time) time.Duration {
	if started.IsZero() {
		return 0
	}
	return time.Since(started)
}
