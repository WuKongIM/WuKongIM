package channelappend

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestWillIdentityRequiredBeforeSendAdmission(t *testing.T) {
	m := publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t"}
	template, err := publication.Encode(m)
	require.NoError(t, err)
	for _, transient := range []bool{false, true} {
		cmd := SendCommand{FromUID: "u", ClientMsgNo: "client", ChannelID: "room", ChannelType: 2, Payload: []byte("body"), PublicationMetadata: template, NoPersist: transient}
		_, r, done := preRouteChannel(cmd, channelid.CommandCodec{})
		require.True(t, done, "Will template reached route resolution before server identity binding")
		require.Equal(t, ReasonInvalidRequest, r.Result.Reason)
		prepared, done := prepareSend(context.Background(), cmd, preparePorts{}, true)
		require.True(t, done)
		require.Equal(t, ReasonInvalidRequest, prepared.result.Reason)
	}
	m.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("a", 64)
	published, err := publication.Encode(m)
	require.NoError(t, err)
	require.True(t, validSendPublication(published))
}
