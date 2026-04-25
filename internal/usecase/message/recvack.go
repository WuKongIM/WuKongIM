package message

import "context"

func (a *App) RecvAck(cmd RecvAckCommand) error {
	if a == nil || a.deliveryAck == nil {
		return nil
	}
	return a.deliveryAck.AckRoute(context.Background(), RouteAckCommand{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  uint64(cmd.MessageID),
		MessageSeq: cmd.MessageSeq,
	})
}

func (a *App) SessionClosed(cmd SessionClosedCommand) error {
	if a == nil || a.deliveryOffline == nil {
		return nil
	}
	return a.deliveryOffline.SessionClosed(context.Background(), cmd)
}
