package app

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestCommittedFanoutCallsSubscribersInOrder(t *testing.T) {
	calls := make([]string, 0, 2)
	fanout := committedFanout{subscribers: []committedSubscriber{
		committedSubscriberFunc(func(context.Context, messageevents.MessageCommitted) error {
			calls = append(calls, "first")
			return nil
		}),
		committedSubscriberFunc(func(context.Context, messageevents.MessageCommitted) error {
			calls = append(calls, "second")
			return nil
		}),
	}}

	require.NoError(t, fanout.SubmitCommitted(context.Background(), messageevents.MessageCommitted{}))
	require.Equal(t, []string{"first", "second"}, calls)
}

func TestCommittedFanoutAggregatesSubscriberErrorsForLogging(t *testing.T) {
	errFirst := errors.New("first failed")
	errSecond := errors.New("second failed")
	fanout := committedFanout{subscribers: []committedSubscriber{
		committedSubscriberFunc(func(context.Context, messageevents.MessageCommitted) error { return errFirst }),
		committedSubscriberFunc(func(context.Context, messageevents.MessageCommitted) error { return errSecond }),
	}}

	err := fanout.SubmitCommitted(context.Background(), messageevents.MessageCommitted{})

	require.ErrorIs(t, err, errFirst)
	require.ErrorIs(t, err, errSecond)
}

func TestCommittedFanoutClonesEventPerSubscriber(t *testing.T) {
	seen := make([][]byte, 0, 2)
	fanout := committedFanout{subscribers: []committedSubscriber{
		committedSubscriberFunc(func(_ context.Context, event messageevents.MessageCommitted) error {
			event.Message.Payload[0] = 'X'
			seen = append(seen, append([]byte(nil), event.Message.Payload...))
			return nil
		}),
		committedSubscriberFunc(func(_ context.Context, event messageevents.MessageCommitted) error {
			seen = append(seen, append([]byte(nil), event.Message.Payload...))
			return nil
		}),
	}}
	event := messageevents.MessageCommitted{Message: channel.Message{Payload: []byte("abc")}}

	require.NoError(t, fanout.SubmitCommitted(context.Background(), event))

	require.Equal(t, [][]byte{[]byte("Xbc"), []byte("abc")}, seen)
	require.Equal(t, []byte("abc"), event.Message.Payload)
}

func TestSendReturnsSuccessWhenCommittedSubscriberFailsAfterDurableAppend(t *testing.T) {
	fanout := committedFanout{subscribers: []committedSubscriber{
		committedSubscriberFunc(func(context.Context, messageevents.MessageCommitted) error {
			return errors.New("subscriber failed")
		}),
	}}
	cluster := &fanoutMessageCluster{result: channel.AppendResult{
		MessageID:  202,
		MessageSeq: 8,
		Message: channel.Message{
			MessageID:   202,
			MessageSeq:  8,
			ChannelID:   "u2@u1",
			ChannelType: frame.ChannelTypePerson,
			FromUID:     "u1",
		},
	}}
	messages := messageusecase.New(messageusecase.Options{
		Cluster:             cluster,
		MetaRefresher:       fanoutMetaRefresher{},
		CommittedDispatcher: fanout,
	})

	result, err := messages.Send(context.Background(), messageusecase.SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(202), result.MessageID)
	require.Equal(t, uint64(8), result.MessageSeq)
	require.Len(t, cluster.requests, 1)
}

type committedSubscriberFunc func(context.Context, messageevents.MessageCommitted) error

func (f committedSubscriberFunc) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	return f(ctx, event)
}

type fanoutMessageCluster struct {
	result   channel.AppendResult
	requests []channel.AppendRequest
}

func (c *fanoutMessageCluster) ApplyMeta(channel.Meta) error { return nil }

func (c *fanoutMessageCluster) Append(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	c.requests = append(c.requests, req)
	return c.result, nil
}

type fanoutMetaRefresher struct{}

func (fanoutMetaRefresher) RefreshChannelMeta(context.Context, channel.ChannelID) (channel.Meta, error) {
	return channel.Meta{}, nil
}
