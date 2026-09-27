package channelappend

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestEmptyPublicationRequiresSendableMetadataBeforeRoutingAndAllocation(t *testing.T) {
	will := publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "topic"}
	template, err := publication.Encode(will)
	require.NoError(t, err)
	will.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("a", 64)
	keyed, err := publication.Encode(will)
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		metadata []byte
		allowed  bool
	}{{"native", nil, false}, {"mqtt", publicationFixture(t), true}, {"will", keyed, true}, {"template", template, false}, {"corrupt", []byte{1}, false}} {
		for _, mode := range []string{"ordinary", "transient", "scoped"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				cmd := SendCommand{FromUID: "u", ChannelID: "room", ChannelType: 2, ClientMsgNo: "empty", PublicationMetadata: tc.metadata}
				if mode == "transient" {
					cmd.NoPersist = true
				}
				if mode == "scoped" {
					cmd.ChannelID = ""
					cmd.RequestScoped = true
					cmd.SyncOnce = true
					cmd.MessageScopedUIDs = []string{"v"}
				}
				_, routeResult, rejected := preRouteChannel(cmd, channelid.CommandCodec{})
				require.Equal(t, !tc.allowed, rejected)
				ids := newSequenceIDsForPrepare(100)
				r, done := prepareSend(context.Background(), cmd, preparePorts{messageID: ids}, false)
				require.Equal(t, !tc.allowed, done)
				require.NoError(t, r.err)
				if tc.allowed {
					require.Equal(t, 1, ids.allocatedCount())
					require.Empty(t, r.item.Command.Payload)
					require.Equal(t, tc.metadata, r.item.Command.PublicationMetadata)
				} else {
					require.Zero(t, ids.allocatedCount())
					require.Equal(t, ReasonInvalidRequest, routeResult.Result.Reason)
					require.Equal(t, ReasonInvalidRequest, r.result.Reason)
				}
			})
		}
	}
}
