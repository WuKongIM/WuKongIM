package delivery

import (
	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

func routeAckFromEvent(cmd deliveryevents.RouteAck) runtimedelivery.RouteAck {
	return runtimedelivery.RouteAck{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  cmd.MessageID,
		MessageSeq: cmd.MessageSeq,
	}
}

func sessionClosedFromEvent(cmd deliveryevents.SessionClosed) runtimedelivery.SessionClosed {
	return runtimedelivery.SessionClosed{
		UID:       cmd.UID,
		SessionID: cmd.SessionID,
	}
}
