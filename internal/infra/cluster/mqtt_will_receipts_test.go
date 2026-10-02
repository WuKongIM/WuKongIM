package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type willReceiptMeta func(context.Context, ch.ChannelID) (ch.Meta, error)

func (f willReceiptMeta) ResolveChannelMetaFresh(c context.Context, id ch.ChannelID) (ch.Meta, error) {
	return f(c, id)
}

type willReceiptNode func(context.Context, ch.WillReceiptRequest) (ch.WillReceiptResult, error)

func (f willReceiptNode) ReadChannelWillReceipt(c context.Context, q ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
	return f(c, q)
}

func TestMQTTWillReceiptAdapterVerifiesIdentityAndFreshAuthority(t *testing.T) {
	md, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: "mqtt-will-v1:" + strings.Repeat("a", 64)})
	require.NoError(t, err)
	for _, mode := range []string{"group", "person", "absent", "metadata unavailable", "read unavailable", "wrong channel", "hash mismatch", "invalid proof", "unkeyed"} {
		t.Run(mode, func(t *testing.T) {
			q := mqttsession.WillPublication{UID: "alice", ClientMsgNo: "no", Target: mqttsession.WillTarget{Topic: "t", TargetID: "room", TargetType: 2}, Payload: []byte("body"), PublicationMetadata: md}
			id := ch.ChannelID{ID: "room", Type: 2}
			if mode == "person" {
				q.Target.TargetID, q.Target.TargetType = "bob", 1
				id = ch.ChannelID{ID: channelid.EncodePersonChannel("alice", "bob"), Type: 1}
			}
			if mode == "unkeyed" {
				q.PublicationMetadata = nil
			}
			unavailable := errors.New("authority unavailable")
			reads := 0
			m := willReceiptMeta(func(_ context.Context, got ch.ChannelID) (ch.Meta, error) {
				require.Equal(t, id, got)
				if mode == "metadata unavailable" {
					return ch.Meta{}, unavailable
				}
				if mode == "wrong channel" {
					got.ID = "other"
				}
				return ch.Meta{ID: got, Epoch: 5, LeaderEpoch: 7, RouteGeneration: 11, Leader: 2}, nil
			})
			n := willReceiptNode(func(_ context.Context, got ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
				reads++
				require.Equal(t, id, got.ChannelID)
				require.Equal(t, uint64(5), got.ExpectedChannelEpoch)
				require.Equal(t, uint64(7), got.ExpectedLeaderEpoch)
				require.Equal(t, uint64(11), got.ExpectedRouteGeneration)
				require.Equal(t, "alice", got.FromUID)
				require.Equal(t, "mqtt-will-v1:"+strings.Repeat("a", 64), got.ServerWillKey)
				if mode == "read unavailable" {
					return ch.WillReceiptResult{}, unavailable
				}
				if mode == "absent" {
					return ch.WillReceiptResult{CommittedThrough: 88}, nil
				}
				hash, err := messagedb.WillPublicationHash(q.UID, q.ClientMsgNo, q.Payload, q.PublicationMetadata)
				require.NoError(t, err)
				if mode == "hash mismatch" {
					hash[0] ^= 1
				}
				r := ch.WillReceiptResult{CommittedThrough: 88, Found: true, Receipt: ch.WillReceipt{MessageID: 33, MessageSeq: 44, ServerTimestampMS: 5500, ContentHash: hash}}
				if mode == "invalid proof" {
					r.CommittedThrough = 43
				}
				return r, nil
			})
			a, err := NewMQTTWillReceipts(m, n)
			require.NoError(t, err)
			r, found, err := a.LookupWillPublication(context.Background(), q)
			switch mode {
			case "group", "person":
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, mqttsession.WillPublicationReceipt{MessageID: 33, MessageSeq: 44, PublishedAtMS: 5500}, r)
			case "absent":
				require.NoError(t, err)
				require.False(t, found)
				require.Zero(t, r)
			default:
				require.Error(t, err)
				require.False(t, found)
				require.Zero(t, r)
			}
			if mode == "metadata unavailable" || mode == "wrong channel" || mode == "unkeyed" {
				require.Zero(t, reads)
			}
			if mode == "metadata unavailable" || mode == "read unavailable" {
				require.ErrorIs(t, err, unavailable)
			}
		})
	}
}
