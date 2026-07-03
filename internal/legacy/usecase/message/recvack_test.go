package message

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecvAckForwardsSessionIDAndMessageIdentity(t *testing.T) {
	acks := &recordingDeliveryAck{}
	app := New(Options{DeliveryAck: acks})

	require.NoError(t, app.RecvAck(RecvAckCommand{
		UID:        "u1",
		SessionID:  42,
		MessageID:  88,
		MessageSeq: 9,
	}))

	require.Equal(t, []RouteAckCommand{{
		UID:        "u1",
		SessionID:  42,
		MessageID:  88,
		MessageSeq: 9,
	}}, acks.calls)
}

func TestSessionClosedForwardsSessionIdentity(t *testing.T) {
	offline := &recordingDeliveryOffline{}
	app := New(Options{DeliveryOffline: offline})

	require.NoError(t, app.SessionClosed(SessionClosedCommand{
		UID:       "u1",
		SessionID: 42,
	}))

	require.Equal(t, []SessionClosedCommand{{
		UID:       "u1",
		SessionID: 42,
	}}, offline.calls)
}

type recordingDeliveryAck struct {
	calls []RouteAckCommand
}

func (r *recordingDeliveryAck) AckRoute(_ context.Context, cmd RouteAckCommand) error {
	r.calls = append(r.calls, cmd)
	return nil
}

type recordingDeliveryOffline struct {
	calls []SessionClosedCommand
}

func (r *recordingDeliveryOffline) SessionClosed(_ context.Context, cmd SessionClosedCommand) error {
	r.calls = append(r.calls, cmd)
	return nil
}
