package channelwrite

import (
	"context"
	"time"
)

// RecipientProcessorOptions configures recipient-authority post-commit processing.
type RecipientProcessorOptions struct {
	// ConversationProjector updates recipient conversations before delivery is resolved.
	ConversationProjector ConversationProjector
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

// RecipientProcessor applies recipient-authority conversation and delivery effects.
type RecipientProcessor struct {
	ports recipientPorts
}

type recipientPorts struct {
	conversations               ConversationProjector
	presence                    PresenceResolver
	pusher                      OwnerPusher
	deliveryRetryMaxAttempts    int
	deliveryRetryInitialBackoff time.Duration
	deliveryRetryMaxBackoff     time.Duration
}

// NewRecipientProcessor creates a recipient-authority post-commit processor.
func NewRecipientProcessor(opts RecipientProcessorOptions) *RecipientProcessor {
	return &RecipientProcessor{ports: recipientPorts{
		conversations:               opts.ConversationProjector,
		presence:                    opts.PresenceResolver,
		pusher:                      opts.OwnerPusher,
		deliveryRetryMaxAttempts:    opts.DeliveryRetryMaxAttempts,
		deliveryRetryInitialBackoff: opts.DeliveryRetryInitialBackoff,
		deliveryRetryMaxBackoff:     opts.DeliveryRetryMaxBackoff,
	}}
}

// ProcessRecipientBatch applies conversation patches and pushes online delivery routes.
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
		return err
	}
	if ports.conversations != nil {
		if err := ports.conversations.AdmitRecipientPatches(ctx, conversationPatchesForRecipients(batch)); err != nil {
			return err
		}
	}
	if ports.presence == nil || ports.pusher == nil {
		return nil
	}
	routes, err := ports.presence.EndpointsByUIDs(ctx, recipientUIDs(batch.Recipients))
	if err != nil {
		return err
	}
	routes = filterSenderEchoRoute(batch.Event, routes)
	grouped, ownerOrder := routesByOwner(routes)
	for _, ownerNodeID := range ownerOrder {
		ownerRoutes := grouped[ownerNodeID]
		if err := pushOwnerRoutesWithRetry(ctx, ports, PushCommand{
			OwnerNodeID: ownerNodeID,
			Envelope:    batch.Event.Clone(),
			Routes:      ownerRoutes,
		}); err != nil {
			return err
		}
	}
	return nil
}

func conversationPatchesForRecipients(batch RecipientBatch) []ConversationPatch {
	event := batch.Event
	patches := make([]ConversationPatch, 0, len(batch.Recipients))
	for _, recipient := range batch.Recipients {
		patches = append(patches, ConversationPatch{
			UID:          recipient.UID,
			ChannelID:    event.ChannelID,
			ChannelType:  int64(event.ChannelType),
			ReadSeq:      recipient.JoinSeq,
			DeletedToSeq: recipient.JoinSeq,
			ActiveAt:     event.ServerTimestampMS,
			UpdatedAt:    event.ServerTimestampMS,
			MessageSeq:   event.MessageSeq,
		})
	}
	return patches
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
		result, err := ports.pusher.Push(ctx, cmd.Clone())
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
				return nil
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
