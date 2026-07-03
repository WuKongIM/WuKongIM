package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
)

// SubmitCommitted forwards a cloned committed-message event to the runtime.
func (a *App) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.SubmitCommitted(ctx, event.Clone())
}

// Recvack forwards receive-ack feedback to the runtime.
func (a *App) Recvack(ctx context.Context, cmd RecvackCommand) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.Recvack(ctx, cmd)
}

// SessionClosed forwards session-close feedback to the runtime.
func (a *App) SessionClosed(ctx context.Context, cmd SessionClosedCommand) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.SessionClosed(ctx, cmd)
}
