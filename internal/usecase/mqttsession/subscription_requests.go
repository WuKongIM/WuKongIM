package mqttsession

import (
	"context"
	"errors"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type SubscriptionRequestOptions struct {
	Subscriptions *Subscriptions
	// Attempts caps explicit pending turns per request; zero defaults to 64,
	// maximum 256. Unknown outcomes and conflicts are never retried here.
	Attempts int
	// Retry spaces pending turns, default 25ms, between 1ms and one second.
	Retry time.Duration
	// Timeout bounds the whole request, default five seconds, at most one minute.
	Timeout time.Duration
}

// SubscriptionRequests completes short pending preparations within one bounded
// caller-owned request. It creates no worker and does not replace maintenance;
// every attempt rereads durable intent and current ownership through Subscriptions.
type SubscriptionRequests struct{ options SubscriptionRequestOptions }

func NewSubscriptionRequests(o SubscriptionRequestOptions) (*SubscriptionRequests, error) {
	if o.Attempts == 0 {
		o.Attempts = 64
	}
	if o.Retry == 0 {
		o.Retry = 25 * time.Millisecond
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Subscriptions == nil || o.Subscriptions.options.Store == nil || o.Attempts < 1 || o.Attempts > 256 || o.Retry < time.Millisecond || o.Retry > time.Second || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &SubscriptionRequests{options: o}, nil
}

// Subscribe returns Active only after the existing replicated projection proof.
func (s *SubscriptionRequests) Subscribe(ctx context.Context, o contract.Owner, r SubscriptionRequest) (out meta.MQTTSubscription, err error) {
	var preparation subscriptionPreparation
	err = s.await(ctx, ErrReplayPending, func(ctx context.Context) error {
		var e error
		out, e = s.options.Subscriptions.subscribe(ctx, o, r, &preparation)
		return e
	})
	if err != nil {
		out = meta.MQTTSubscription{}
	}
	return out, err
}

// Unsubscribe retains begun exchanges while waiting for bounded source draining.
func (s *SubscriptionRequests) Unsubscribe(ctx context.Context, o contract.Owner, topic string) (existed bool, err error) {
	err = s.await(ctx, ErrSourceDrainPending, func(ctx context.Context) error {
		var e error
		existed, e = s.options.Subscriptions.Unsubscribe(ctx, o, topic)
		return e
	})
	return existed && err == nil, err
}

func (s *SubscriptionRequests) await(parent context.Context, pending error, call func(context.Context) error) error {
	if s == nil || parent == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, s.options.Timeout)
	defer cancel()
	for attempt := 0; attempt < s.options.Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := call(ctx)
		if stopped := ctx.Err(); stopped != nil {
			return stopped
		}
		if !errors.Is(err, pending) || attempt+1 == s.options.Attempts {
			return err
		}
		timer := time.NewTimer(s.options.Retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ErrInvalid
}
