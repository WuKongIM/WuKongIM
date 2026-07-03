package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
)

func (a *App) SubmitCommitted(ctx context.Context, env runtimedelivery.CommittedEnvelope) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.Submit(ctx, env)
}

// SubmitRealtime forwards a non-durable realtime message to the delivery runtime.
func (a *App) SubmitRealtime(ctx context.Context, event messageevents.MessageRealtime) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	clone := event.Clone()
	return a.runtime.Submit(ctx, runtimedelivery.CommittedEnvelope{
		Message:           clone.Message,
		SenderSessionID:   clone.SenderSessionID,
		MessageScopedUIDs: clone.MessageScopedUIDs,
	})
}

func (noopRuntime) Submit(context.Context, runtimedelivery.CommittedEnvelope) error {
	return nil
}
