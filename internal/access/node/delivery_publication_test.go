package node

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func deliveryPublicationFixture(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	return b
}

func TestPublicationDeliveryRPCPreservesEveryExtension(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*channelappend.CommittedEnvelope)
	}{
		{"setting", func(e *channelappend.CommittedEnvelope) { e.Setting = 0xff }},
		{"topic", func(e *channelappend.CommittedEnvelope) { e.Topic = "topic" }},
		{"expire", func(e *channelappend.CommittedEnvelope) { e.Expire = ^uint32(0) }},
		{"timestamp", func(e *channelappend.CommittedEnvelope) { e.ServerTimestampMS = 1000 }},
		{"command", func(e *channelappend.CommittedEnvelope) { e.SyncOnce = true }},
		{"publication", func(e *channelappend.CommittedEnvelope) { e.PublicationMetadata = deliveryPublicationFixture(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			push := onlineDeliveryPushFromLegacy(testDeliveryPushCommand())
			tc.edit(&push.Event)
			want := push.Clone()
			cmd := legacyDeliveryPushFromOnline(push)
			require.Equal(t, want, onlineDeliveryPushFromLegacy(cmd), "compatibility projection lost content")
			raw, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: cmd})
			require.NoError(t, err)
			require.Equal(t, byte(2), raw[4])
			decoded, err := decodeDeliveryPushRequest(raw)
			require.NoError(t, err)
			require.Equal(t, want, onlineDeliveryPushFromLegacy(decoded.Command))
			for n := 0; n < len(raw); n++ {
				_, err := decodeDeliveryPushRequest(raw[:n])
				require.Error(t, err, "accepted prefix %d", n)
			}
			for _, version := range []byte{1, 3} {
				bad := bytes.Clone(raw)
				bad[4] = version
				_, err := decodeDeliveryPushRequest(bad)
				require.Error(t, err)
			}
			clear(raw)
			clear(push.Event.Payload)
			clear(push.Event.PublicationMetadata)
			require.Equal(t, want, onlineDeliveryPushFromLegacy(cmd), "encode projection borrowed source content")
			require.Equal(t, want, onlineDeliveryPushFromLegacy(decoded.Command), "decode borrowed wire content")
			projected := onlineDeliveryPushFromLegacy(decoded.Command)
			clear(projected.Event.Payload)
			clear(projected.Event.PublicationMetadata)
			require.Equal(t, want, onlineDeliveryPushFromLegacy(decoded.Command), "decode projection borrowed legacy content")
		})
	}
}

func TestPublicationDeliveryRPCRejectsInvalidMetadata(t *testing.T) {
	push := onlineDeliveryPushFromLegacy(testDeliveryPushCommand())
	for _, bad := range [][]byte{{1}, {2}, make([]byte, publication.MaxEncodedBytes+1)} {
		push.Event.PublicationMetadata = bad
		_, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: legacyDeliveryPushFromOnline(push)})
		require.Error(t, err)
	}
	metadata := deliveryPublicationFixture(t)
	push.Event.PublicationMetadata = metadata
	raw, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: legacyDeliveryPushFromOnline(push)})
	require.NoError(t, err)
	at := bytes.Index(raw, metadata)
	require.Greater(t, at, 0)
	raw[at] = 2
	_, err = decodeDeliveryPushRequest(raw)
	require.Error(t, err)
}

func TestPublicationDeliveryRPCNativeV1Fixture(t *testing.T) {
	req := deliveryPushRequest{Command: testDeliveryPushCommand()}
	raw, err := encodeDeliveryPushRequest(req)
	require.NoError(t, err)
	require.Equal(t, "574b5644010de90707096368616e6e656c2d31020673656e646572032108636c69656e742d3101040102030402027531027532020275310d17cd0865096465766963652d753101020275320d17b209ca01096465766963652d75320102", hex.EncodeToString(raw))
	decoded, err := decodeDeliveryPushRequest(raw)
	require.NoError(t, err)
	require.Equal(t, req, decoded)
}

func TestPublicationDeliveryClientAndHandlerRoundTrip(t *testing.T) {
	push := onlineDeliveryPushFromLegacy(testDeliveryPushCommand())
	push.Event.Setting, push.Event.Topic, push.Event.Expire = 0x12, "legacy-topic", 3600
	push.Event.ServerTimestampMS, push.Event.SyncOnce = 1000, true
	push.Event.PublicationMetadata = deliveryPublicationFixture(t)
	push.Event.MessageSeq = 0 // Transient delivery must carry the same content.
	want := push.Clone()
	result := onlinedelivery.OwnerPushResult{Accepted: push.Routes[:1], Retryable: push.Routes[1:]}
	owner := &fakeOnlineDeliveryOwnerPush{result: result}
	rpc := &publicationDeliveryLoopback{adapter: New(Options{Delivery: AdaptOnlineDeliveryOwnerPush(owner)})}
	got, err := NewClient(rpc).PushOwner(context.Background(), push)
	require.NoError(t, err)
	require.Equal(t, result, got)
	require.Equal(t, push.OwnerNodeID, rpc.nodeID)
	require.Equal(t, DeliveryPushRPCServiceID, rpc.serviceID)
	require.Equal(t, byte(2), rpc.request[4])
	require.Equal(t, []onlinedelivery.OwnerPush{want}, owner.pushes)
	clear(push.Event.Payload)
	clear(push.Event.PublicationMetadata)
	clear(rpc.request)
	require.Equal(t, []onlinedelivery.OwnerPush{want}, owner.pushes)
}

type publicationDeliveryLoopback struct {
	adapter   *Adapter
	nodeID    uint64
	serviceID uint8
	request   []byte
}

func (n *publicationDeliveryLoopback) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, request []byte) ([]byte, error) {
	n.nodeID, n.serviceID, n.request = nodeID, serviceID, request
	return n.adapter.HandleDeliveryPushRPC(ctx, request)
}
