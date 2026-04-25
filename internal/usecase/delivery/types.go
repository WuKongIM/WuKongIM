package delivery

import (
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

func routeAckFromMessage(cmd message.RouteAckCommand) runtimedelivery.RouteAck {
	return runtimedelivery.RouteAck{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  cmd.MessageID,
		MessageSeq: cmd.MessageSeq,
	}
}

func sessionClosedFromMessage(cmd message.SessionClosedCommand) runtimedelivery.SessionClosed {
	return runtimedelivery.SessionClosed{
		UID:       cmd.UID,
		SessionID: cmd.SessionID,
	}
}
