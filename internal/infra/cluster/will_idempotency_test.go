package cluster

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type willIdentityNode struct {
	*recordingIdempotencyNode
	serverKey string
	willCalls int
}

func (n *willIdentityNode) LookupChannelWillIdempotency(_ context.Context, id ch.ChannelID, uid, key string) (channelstore.IdempotencyHit, bool, error) {
	n.id, n.fromUID, n.serverKey = id, uid, key
	n.willCalls++
	return n.hit, n.ok, n.err
}

func TestWillIdempotencyUsesServerPortAndRequiresCommittedProof(t *testing.T) {
	key := "mqtt-will-v1:" + strings.Repeat("a", 64)
	metadata, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: key})
	require.NoError(t, err)
	msg := ch.Message{MessageID: 11, MessageSeq: 7, FromUID: "u", ClientMsgNo: "client", Payload: []byte("body"), PublicationMetadata: metadata}
	query := channelappend.IdempotencyQuery{ChannelID: "room", ChannelType: 2, FromUID: "u", ClientMsgNo: "client", Payload: msg.Payload, PublicationMetadata: metadata}
	for _, uncommitted := range []bool{false, true} {
		n := &willIdentityNode{recordingIdempotencyNode: &recordingIdempotencyNode{ok: true, hit: channelstore.IdempotencyHit{Message: msg}, uncommitted: uncommitted, committed: &msg}}
		result, found, err := NewChannelIdempotencyStore(n).LookupSend(context.Background(), query)
		require.NoError(t, err)
		require.Equal(t, !uncommitted, found)
		require.Equal(t, 1, n.willCalls)
		require.Equal(t, key, n.serverKey)
		require.Empty(t, n.clientMsgNo, "native lookup must never be selected")
		if found {
			require.Equal(t, msg.MessageID, result.MessageID)
		}
	}
	// Missing server capability cannot fall back to a client-domain hit.
	n := &recordingIdempotencyNode{ok: true, hit: channelstore.IdempotencyHit{Message: msg}}
	_, found, err := NewChannelIdempotencyStore(n).LookupSend(context.Background(), query)
	require.Error(t, err)
	require.False(t, found)
	require.Empty(t, n.clientMsgNo)
}
