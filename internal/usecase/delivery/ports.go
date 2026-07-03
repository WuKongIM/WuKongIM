package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
)

// Runtime receives delivery orchestration commands.
type Runtime interface {
	SubmitCommitted(context.Context, messageevents.MessageCommitted) error
	Recvack(context.Context, RecvackCommand) error
	SessionClosed(context.Context, SessionClosedCommand) error
}
