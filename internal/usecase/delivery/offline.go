package delivery

import (
	"context"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

func (a *App) SessionClosed(ctx context.Context, cmd message.SessionClosedCommand) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.SessionClosed(ctx, sessionClosedFromMessage(cmd))
}

func (noopRuntime) SessionClosed(context.Context, runtimedelivery.SessionClosed) error {
	return nil
}
