package app

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type willMessageSender func(context.Context, message.WillSendCommand) (message.SendResult, error)

func (f willMessageSender) SendPreparedWill(ctx context.Context, q message.WillSendCommand) (message.SendResult, error) {
	return f(ctx, q)
}

func (f willMessageSender) PrepareWill(_ context.Context, q message.WillSendCommand) ([]byte, message.Reason, error) {
	return append([]byte(nil), q.Payload...), message.ReasonSuccess, nil
}

func TestMQTTWillPublisherReusesMessageUsecaseWithoutInventingOwner(t *testing.T) {
	md, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: "mqtt-will-v1:" + strings.Repeat("a", 64), Properties: []publication.Property{{Kind: publication.MessageExpiry, Number: 60}}})
	require.NoError(t, err)
	q := mqttsession.WillPublication{UID: "alice", ClientMsgNo: "client", Target: mqttsession.WillTarget{Topic: "t", TargetID: "bob", TargetType: 1}, Payload: []byte("bye"), PublicationMetadata: md}
	for _, bad := range []bool{false, true} {
		p := mqttWillPublications{messages: willMessageSender(func(_ context.Context, c message.WillSendCommand) (message.SendResult, error) {
			require.Equal(t, message.WillSendCommand{FromUID: q.UID, ClientMsgNo: q.ClientMsgNo, TargetID: "bob", TargetType: 1, Payload: q.Payload, PublicationMetadata: md}, c)
			if bad {
				return message.SendResult{Reason: message.ReasonNotAllowSend}, nil
			}
			return message.SendResult{MessageID: 44, MessageSeq: 7, Reason: message.ReasonSuccess}, nil
		})}
		err := p.PublishWill(context.Background(), q)
		if bad {
			require.ErrorIs(t, err, mqttsession.ErrWillPending)
		} else {
			require.NoError(t, err)
		}
	}
}
