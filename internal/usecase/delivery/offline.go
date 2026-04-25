package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

func (a *App) SessionClosed(ctx context.Context, cmd deliveryevents.SessionClosed) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.SessionClosed(ctx, sessionClosedFromEvent(cmd))
}

func (noopRuntime) SessionClosed(context.Context, runtimedelivery.SessionClosed) error {
	return nil
}
