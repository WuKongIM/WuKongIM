package app

import (
	"context"
	"errors"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
)

const (
	authorityResultOK            = "ok"
	authorityResultError         = "error"
	authorityResultLocal         = "local"
	authorityResultRemote        = "remote"
	authorityResultAccepted      = "accepted"
	authorityResultFull          = "full"
	authorityResultClosed        = "closed"
	authorityResultSyncOK        = "sync_ok"
	authorityResultSyncError     = "sync_error"
	authorityResultRouteNotReady = "route_not_ready"
	authorityResultStaleRoute    = "stale_route"
	authorityResultNotLeader     = "not_leader"
	authorityResultTimeout       = "timeout"
	authorityResultAuthFail      = "auth_fail"
	authorityResultInvalid       = "invalid"

	authorityRecipientPhaseWorker       = "worker"
	authorityRecipientPhaseConversation = "conversation"
	authorityRecipientPhaseDelivery     = "delivery"
)

type authorityObserver interface {
	ObserveAuthoritySenderRoute(authoritySenderRouteEvent)
	ObserveAuthorityRecipientQueue(authorityRecipientQueueEvent)
	ObserveAuthorityRecipientDispatch(authorityRecipientDispatchEvent)
}

type authoritySenderRouteEvent struct {
	// Result is a low-cardinality sender-authority route result.
	Result string
}

type authorityRecipientQueueEvent struct {
	// Result is a low-cardinality recipient committed-worker admission result.
	Result string
}

type authorityRecipientDispatchEvent struct {
	// Phase identifies the recipient-authority phase being observed.
	Phase string
	// Result is a low-cardinality dispatch outcome.
	Result string
	// Duration is the phase latency.
	Duration time.Duration
}

func (a *App) authorityObserver() authorityObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return authorityMetricsObserver{metrics: a.metrics}
}

func authorityResultFromError(err error) string {
	switch {
	case err == nil:
		return authorityResultOK
	case errors.Is(err, context.DeadlineExceeded):
		return authorityResultTimeout
	case errors.Is(err, context.Canceled):
		return authorityResultError
	case errors.Is(err, errRecipientCommittedWorkerFull):
		return authorityResultFull
	case errors.Is(err, errRecipientCommittedWorkerClosed):
		return authorityResultClosed
	case errors.Is(err, messageusecase.ErrRouteNotReady), errors.Is(err, recipientusecase.ErrRouteNotReady), errors.Is(err, conversationusecase.ErrRouteNotReady):
		return authorityResultRouteNotReady
	case errors.Is(err, messageusecase.ErrStaleRoute), errors.Is(err, recipientusecase.ErrStaleRoute), errors.Is(err, conversationusecase.ErrStaleRoute):
		return authorityResultStaleRoute
	case errors.Is(err, messageusecase.ErrNotLeader), errors.Is(err, recipientusecase.ErrNotLeader), errors.Is(err, conversationusecase.ErrNotLeader):
		return authorityResultNotLeader
	case errors.Is(err, conversationusecase.ErrCachePressure):
		return "cache_pressure"
	default:
		return authorityResultError
	}
}
