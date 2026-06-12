package channelappend

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidSubscriberCursor reports a non-terminal subscriber page without a usable next cursor.
	ErrInvalidSubscriberCursor = errors.New("internalv2/channelappend: invalid subscriber cursor")
	// ErrCommitEffectFailed reports a post-commit effect failure that will be logged and dropped.
	ErrCommitEffectFailed = errors.New("internalv2/channelappend: commit effect failed")
	// ErrDeliveryRetryExhausted reports retryable owner push routes after the final delivery attempt.
	ErrDeliveryRetryExhausted = errors.New("internalv2/channelappend: delivery retry exhausted")
	// ErrEffectPanic reports a recovered panic from an asynchronous channel append effect.
	ErrEffectPanic = errors.New("internalv2/channelappend: effect panic")
)

// RecipientProcessorOptions configures recipient-authority post-commit processing.
type RecipientProcessorOptions struct {
	// PresenceResolver resolves online recipient endpoints for delivery pushes.
	PresenceResolver PresenceResolver
	// OwnerPusher pushes online delivery commands to owner nodes.
	OwnerPusher OwnerPusher
	// DeliveryRetryMaxAttempts bounds retryable owner push attempts. Values <= 0 use a bounded default.
	DeliveryRetryMaxAttempts int
	// DeliveryRetryInitialBackoff is the first retry sleep for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryInitialBackoff time.Duration
	// DeliveryRetryMaxBackoff caps retry sleeps for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryMaxBackoff time.Duration
}

// RecipientProcessor applies recipient-authority delivery effects.
type RecipientProcessor struct {
	ports recipientPorts
}

type recipientPorts struct {
	presence                    PresenceResolver
	pusher                      OwnerPusher
	deliveryRetryMaxAttempts    int
	deliveryRetryInitialBackoff time.Duration
	deliveryRetryMaxBackoff     time.Duration
}

// NewRecipientProcessor creates a recipient-authority post-commit processor.
func NewRecipientProcessor(opts RecipientProcessorOptions) *RecipientProcessor {
	return &RecipientProcessor{ports: recipientPorts{
		presence:                    opts.PresenceResolver,
		pusher:                      opts.OwnerPusher,
		deliveryRetryMaxAttempts:    opts.DeliveryRetryMaxAttempts,
		deliveryRetryInitialBackoff: opts.DeliveryRetryInitialBackoff,
		deliveryRetryMaxBackoff:     opts.DeliveryRetryMaxBackoff,
	}}
}

// ProcessRecipientBatch pushes online delivery routes for a recipient-authority batch.
func (p *RecipientProcessor) ProcessRecipientBatch(ctx context.Context, batch RecipientBatch) error {
	if p == nil {
		return nil
	}
	return processRecipientBatch(ctx, batch, p.ports)
}

func processRecipientBatch(ctx context.Context, batch RecipientBatch, ports recipientPorts) error {
	if len(batch.Recipients) == 0 {
		return nil
	}
	if err := contextErr(ctx); err != nil {
		return withPostCommitFailureDetail(err, postCommitBatchDetail("context", batch))
	}
	if ports.presence == nil || ports.pusher == nil {
		return nil
	}
	uids := recipientUIDs(batch.Recipients)
	routes, err := ports.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		detail := postCommitBatchDetail("presence_resolve", batch)
		detail.UIDCount = len(uids)
		return withPostCommitFailureDetail(err, detail)
	}
	routes = filterSenderEchoRoute(batch.Event, routes)
	grouped, ownerOrder := routesByOwner(routes)
	for _, ownerNodeID := range ownerOrder {
		ownerRoutes := grouped[ownerNodeID]
		if err := pushOwnerRoutesWithRetry(ctx, ports, PushCommand{
			OwnerNodeID: ownerNodeID,
			Envelope:    batch.Event,
			Routes:      ownerRoutes,
		}); err != nil {
			detail := postCommitBatchDetail("owner_push", batch)
			detail.UID = firstRouteUID(ownerRoutes)
			detail.UIDCount = len(uids)
			detail.DispatchOwnerNodeID = ownerNodeID
			detail.DispatchOwnerRouteNum = len(ownerRoutes)
			return withPostCommitFailureDetail(err, detail)
		}
	}
	return nil
}

func postCommitBatchDetail(phase string, batch RecipientBatch) PostCommitFailureDetail {
	uids := recipientUIDs(batch.Recipients)
	return PostCommitFailureDetail{
		Phase:          phase,
		UID:            firstString(uids),
		UIDCount:       len(uids),
		RecipientCount: len(batch.Recipients),
	}
}

func recipientUIDs(recipients []Recipient) []string {
	uids := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		uids = append(uids, recipient.UID)
	}
	return uids
}

func filterSenderEchoRoute(event CommittedEnvelope, routes []Route) []Route {
	out := routes[:0]
	for _, route := range routes {
		if route.UID == event.FromUID &&
			route.OwnerNodeID == event.SenderNodeID &&
			route.SessionID == event.SenderSessionID {
			continue
		}
		out = append(out, route)
	}
	return out
}

func routesByOwner(routes []Route) (map[uint64][]Route, []uint64) {
	out := make(map[uint64][]Route)
	order := make([]uint64, 0)
	for _, route := range routes {
		if _, ok := out[route.OwnerNodeID]; !ok {
			order = append(order, route.OwnerNodeID)
		}
		out[route.OwnerNodeID] = append(out[route.OwnerNodeID], route)
	}
	return out, order
}

func firstRouteUID(routes []Route) string {
	if len(routes) == 0 {
		return ""
	}
	return routes[0].UID
}

func pushOwnerRoutesWithRetry(ctx context.Context, ports recipientPorts, cmd PushCommand) error {
	attempts := ports.deliveryRetryMaxAttempts
	if attempts <= 0 {
		attempts = defaultDeliveryRetryMaxAttempts
	}
	backoff := ports.deliveryRetryInitialBackoff
	if backoff <= 0 {
		backoff = defaultDeliveryRetryInitialBackoff
	}
	maxBackoff := ports.deliveryRetryMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultDeliveryRetryMaxBackoff
	}
	if len(cmd.Routes) == 0 {
		return nil
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := contextErr(ctx); err != nil {
			return err
		}
		result, err := ports.pusher.Push(ctx, cmd)
		if err != nil {
			if attempt == attempts {
				return err
			}
		} else {
			if len(result.Retryable) == 0 {
				return nil
			}
			cmd.Routes = append([]Route(nil), result.Retryable...)
			if attempt == attempts {
				return ErrDeliveryRetryExhausted
			}
		}
		if err := sleepDeliveryRetry(ctx, backoff); err != nil {
			return err
		}
		backoff = nextDeliveryRetryBackoff(backoff, maxBackoff)
	}
	return nil
}

func sleepDeliveryRetry(ctx context.Context, backoff time.Duration) error {
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nextDeliveryRetryBackoff(backoff, maxBackoff time.Duration) time.Duration {
	next := backoff * 2
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}
