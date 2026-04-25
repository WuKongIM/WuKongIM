package delivery

import (
	"context"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

func (a *App) AckRoute(ctx context.Context, cmd message.RouteAckCommand) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.AckRoute(ctx, routeAckFromMessage(cmd))
}

func (noopRuntime) AckRoute(context.Context, runtimedelivery.RouteAck) error {
	return nil
}
