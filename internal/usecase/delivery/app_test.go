package delivery

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestSubmitCommittedDelegatesToRuntimeWithDurableMessage(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})

	err := app.SubmitCommitted(context.Background(), runtimedelivery.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u1@u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   101,
			MessageSeq:  7,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
			ClientSeq:   9,
		},
		SenderSessionID: 33,
	})

	require.NoError(t, err)
	require.Equal(t, []runtimedelivery.CommittedEnvelope{{
		Message: channel.Message{
			ChannelID:   "u1@u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   101,
			MessageSeq:  7,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
			ClientSeq:   9,
		},
		SenderSessionID: 33,
	}}, runtime.submits)
}

func TestSubmitRealtimeScopedEnvelopeDelegatesToRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})

	err := app.SubmitRealtime(context.Background(), messageevents.MessageRealtime{
		Message: channel.Message{
			ChannelID:   "tmp____cmd",
			ChannelType: frame.ChannelTypeTemp,
			MessageID:   202,
			MessageSeq:  0,
			Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
			FromUID:     "system",
			ClientMsgNo: "rt1",
			Payload:     []byte("cmd"),
			ClientSeq:   10,
		},
		SenderSessionID:   44,
		MessageScopedUIDs: []string{"u1", "u2"},
	})

	require.NoError(t, err)
	require.Equal(t, []runtimedelivery.CommittedEnvelope{{
		Message: channel.Message{
			ChannelID:   "tmp____cmd",
			ChannelType: frame.ChannelTypeTemp,
			MessageID:   202,
			MessageSeq:  0,
			Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
			FromUID:     "system",
			ClientMsgNo: "rt1",
			Payload:     []byte("cmd"),
			ClientSeq:   10,
		},
		SenderSessionID:   44,
		MessageScopedUIDs: []string{"u1", "u2"},
	}}, runtime.submits)
}

func TestAckRouteDelegatesToRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})

	err := app.AckRoute(context.Background(), deliveryevents.RouteAck{
		UID:        "u2",
		SessionID:  22,
		MessageID:  101,
		MessageSeq: 7,
	})

	require.NoError(t, err)
	require.Equal(t, []runtimedelivery.RouteAck{{
		UID:        "u2",
		SessionID:  22,
		MessageID:  101,
		MessageSeq: 7,
	}}, runtime.acks)
}

func TestSessionClosedDelegatesToRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})

	err := app.SessionClosed(context.Background(), deliveryevents.SessionClosed{
		UID:       "u2",
		SessionID: 22,
	})

	require.NoError(t, err)
	require.Equal(t, []runtimedelivery.SessionClosed{{
		UID:       "u2",
		SessionID: 22,
	}}, runtime.closed)
}

type recordingRuntime struct {
	submits []runtimedelivery.CommittedEnvelope
	acks    []runtimedelivery.RouteAck
	closed  []runtimedelivery.SessionClosed
}

func (r *recordingRuntime) Submit(_ context.Context, env runtimedelivery.CommittedEnvelope) error {
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	copied.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	r.submits = append(r.submits, copied)
	return nil
}

func (r *recordingRuntime) AckRoute(_ context.Context, cmd runtimedelivery.RouteAck) error {
	r.acks = append(r.acks, cmd)
	return nil
}

func (r *recordingRuntime) SessionClosed(_ context.Context, cmd runtimedelivery.SessionClosed) error {
	r.closed = append(r.closed, cmd)
	return nil
}
