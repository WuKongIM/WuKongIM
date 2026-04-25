package delivery

import (
	"context"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

func (a *App) SubmitCommitted(ctx context.Context, env runtimedelivery.CommittedEnvelope) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.Submit(ctx, env)
}

func (noopRuntime) Submit(context.Context, runtimedelivery.CommittedEnvelope) error {
	return nil
}
