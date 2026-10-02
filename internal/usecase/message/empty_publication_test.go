package message

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestBeforeSendEmptyPublicationPreservesNilAndExplicitReplacement(t *testing.T) {
	for _, source := range []publication.Source{publication.SourceMQTT, publication.SourceWill} {
		md := publication.Metadata{Source: source, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "topic"}
		if source == publication.SourceMQTT {
			md.AcceptedAtMS = 1000
		} else {
			md.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("a", 64)
		}
		encoded, err := publication.Encode(md)
		require.NoError(t, err)
		for _, tc := range []struct {
			name                     string
			input, replacement, want []byte
		}{
			{"empty input", nil, nil, nil}, {"empty replacement", []byte("before"), []byte{}, nil}, {"nil preserves", []byte("before"), nil, []byte("before")},
		} {
			t.Run(string(rune('0'+source))+"/"+tc.name, func(t *testing.T) {
				calls := 0
				w := testBeforeSendWebhook(t, beforeSendCallerFunc(func(_ context.Context, q BeforeSendRequest) (BeforeSendDecision, error) {
					calls++
					require.Equal(t, tc.input, q.Payload)
					return BeforeSendDecision{Allow: true, Payload: tc.replacement}, nil
				}), nil)
				out, reason, err := w.check(context.Background(), SendCommand{FromUID: "alice", ChannelID: "room", ChannelType: 2, Payload: tc.input, PublicationMetadata: encoded}, channelid.CommandCodec{})
				require.NoError(t, err)
				require.Equal(t, ReasonSuccess, reason)
				require.Equal(t, 1, calls)
				require.Equal(t, string(tc.want), string(out.Payload))
				require.Equal(t, encoded, out.PublicationMetadata)
			})
		}
	}
}

func TestBeforeSendEmptyPublicationRejectsNativeCorruptionAndUnkeyedWill(t *testing.T) {
	template, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "topic"})
	require.NoError(t, err)
	for _, encoded := range [][]byte{nil, {1}, template} {
		calls := 0
		w := testBeforeSendWebhook(t, beforeSendCallerFunc(func(context.Context, BeforeSendRequest) (BeforeSendDecision, error) {
			calls++
			return BeforeSendDecision{Allow: true}, nil
		}), func(opts *BeforeSendOptions) { opts.OnError = "allow" })
		_, reason, err := w.check(context.Background(), SendCommand{FromUID: "alice", ChannelID: "room", ChannelType: 2, PublicationMetadata: encoded}, channelid.CommandCodec{})
		require.NoError(t, err)
		require.Equal(t, ReasonInvalidRequest, reason)
		require.Zero(t, calls)
	}
}
