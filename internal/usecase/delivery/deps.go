package delivery

import (
	"context"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

type Runtime interface {
	Submit(ctx context.Context, env runtimedelivery.CommittedEnvelope) error
	AckRoute(ctx context.Context, cmd runtimedelivery.RouteAck) error
	SessionClosed(ctx context.Context, cmd runtimedelivery.SessionClosed) error
}
