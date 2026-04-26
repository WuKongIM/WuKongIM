package app

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
)

type committedSubscriber interface {
	SubmitCommitted(context.Context, messageevents.MessageCommitted) error
}

type committedFanout struct {
	subscribers []committedSubscriber
}

// SubmitCommitted returns subscriber errors for logging by the caller; message.Send must not convert them into send failures after durable append.
func (f committedFanout) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	var joined error
	for _, sub := range f.subscribers {
		if sub == nil {
			continue
		}
		joined = errors.Join(joined, sub.SubmitCommitted(ctx, event.Clone()))
	}
	return joined
}
