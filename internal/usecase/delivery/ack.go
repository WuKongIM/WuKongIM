package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

func (a *App) AckRoute(ctx context.Context, cmd deliveryevents.RouteAck) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.AckRoute(ctx, routeAckFromEvent(cmd))
}

func (noopRuntime) AckRoute(context.Context, runtimedelivery.RouteAck) error {
	return nil
}
