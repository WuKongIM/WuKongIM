package node

import (
	"context"
	"errors"
	"testing"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestSubmitCommittedMessageRPCRoutesToOwnerRuntime(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	recorder := &recordingDeliverySubmit{}
	New(Options{
		Cluster:        node2,
		Presence:       presence.New(presence.Options{}),
		Online:         online.NewRegistry(),
		GatewayBootID:  22,
		DeliverySubmit: recorder,
	})

	client := NewClient(node1)
	err := client.SubmitCommitted(context.Background(), 2, deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
			ClientSeq:   7,
		},
		SenderSessionID:                42,
		MessageScopedUIDs:              []string{"u1", "u2"},
		CMDConversationIntentSubmitted: true,
	})
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.CommittedEnvelope{{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
			ClientSeq:   7,
		},
		SenderSessionID:                42,
		MessageScopedUIDs:              []string{"u1", "u2"},
		CMDConversationIntentSubmitted: true,
	}}, recorder.calls)
}

func TestSubmitCommittedMessageScopedFailsWhenPeerDoesNotSupportProbe(t *testing.T) {
	cluster := &legacyDeliverySubmitCapabilityCluster{}
	client := NewClient(cluster)

	err := client.SubmitCommitted(context.Background(), 2, deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
		},
		MessageScopedUIDs: []string{"u1"},
	})

	require.ErrorIs(t, err, ErrMessageScopedDeliverySubmitUnsupported)
	require.Equal(t, 1, cluster.calls)
	require.Equal(t, deliverySubmitRPCServiceID, cluster.serviceID)
}

func TestSubmitCommittedIntentSubmittedFailsWhenPeerDoesNotSupportV3Probe(t *testing.T) {
	cluster := &v2OnlyDeliverySubmitCapabilityCluster{}
	client := NewClient(cluster)

	err := client.SubmitCommitted(context.Background(), 2, deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
		},
		CMDConversationIntentSubmitted: true,
	})

	require.ErrorIs(t, err, ErrMessageScopedDeliverySubmitUnsupported)
	require.Equal(t, 0, cluster.v2ProbeCalls)
	require.Equal(t, 1, cluster.v3ProbeCalls)
	require.Equal(t, 0, cluster.payloadCalls)
}

func TestDeliverySubmitCapabilityProbeReturnsOKWithoutSubmitting(t *testing.T) {
	recorder := &recordingDeliverySubmit{}
	adapter := New(Options{DeliverySubmit: recorder})

	respBody, err := adapter.handleDeliverySubmitRPC(context.Background(), encodeDeliverySubmitCapabilityProbe())
	require.NoError(t, err)
	resp, err := decodeDeliveryResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Empty(t, recorder.calls)
}

func TestDeliverySubmitV3CapabilityProbeReturnsOKWithoutSubmitting(t *testing.T) {
	recorder := &recordingDeliverySubmit{}
	adapter := New(Options{DeliverySubmit: recorder})

	respBody, err := adapter.handleDeliverySubmitRPC(context.Background(), encodeDeliverySubmitV3CapabilityProbe())
	require.NoError(t, err)
	resp, err := decodeDeliveryResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Empty(t, recorder.calls)
}

func TestDeliverySubmitBinaryCodecRoundTrip(t *testing.T) {
	req := deliverySubmitRequest{Envelope: deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
			ClientSeq:   7,
		},
		SenderSessionID:                42,
		MessageScopedUIDs:              []string{"u1", "u2"},
		CMDConversationIntentSubmitted: true,
	}}

	body, err := encodeDeliverySubmitRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isDeliverySubmitRequestBinary(body))

	got, err := decodeDeliverySubmitRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
}

func TestDeliverySubmitBinaryCodecEncodesScopedFalseAsV2(t *testing.T) {
	req := deliverySubmitRequest{Envelope: deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
		},
		MessageScopedUIDs: []string{"u1", "u2"},
	}}

	body, err := encodeDeliverySubmitRequestBinary(req)
	require.NoError(t, err)
	require.True(t, hasMagic(body, deliverySubmitRequestMagicV2[:]))
	require.False(t, hasMagic(body, deliverySubmitRequestMagicV3[:]))

	got, err := decodeDeliverySubmitRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
	require.False(t, got.Envelope.CMDConversationIntentSubmitted)
}

func TestDeliverySubmitBinaryCodecDecodesLegacyV1WithoutMessageScopedUIDs(t *testing.T) {
	envelope := deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
			ClientSeq:   7,
		},
		SenderSessionID: 42,
	}
	body := append([]byte{}, deliverySubmitRequestMagicV1[:]...)
	body = appendChannelMessage(body, envelope.Message)
	body = appendUvarint(body, envelope.SenderSessionID)

	got, err := decodeDeliverySubmitRequest(body)
	require.NoError(t, err)
	require.Equal(t, envelope, got.Envelope)
	require.Empty(t, got.Envelope.MessageScopedUIDs)
}

func TestDeliverySubmitBinaryCodecEncodesUnscopedAsLegacyV1(t *testing.T) {
	req := deliverySubmitRequest{Envelope: deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
			ClientSeq:   7,
		},
		SenderSessionID: 42,
	}}

	body, err := encodeDeliverySubmitRequestBinary(req)
	require.NoError(t, err)
	require.True(t, hasMagic(body, deliverySubmitRequestMagicV1[:]))
	require.False(t, hasMagic(body, deliverySubmitRequestMagicV2[:]))

	envelope, next, err := readLegacyCommittedEnvelope(body, len(deliverySubmitRequestMagicV1))
	require.NoError(t, err)
	require.Equal(t, len(body), next)
	require.Equal(t, req.Envelope, envelope)
}

func TestDeliverySubmitBinaryCodecEncodesIntentSubmittedAsV3(t *testing.T) {
	req := deliverySubmitRequest{Envelope: deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   88,
			MessageSeq:  9,
		},
		MessageScopedUIDs:              []string{},
		CMDConversationIntentSubmitted: true,
	}}

	body, err := encodeDeliverySubmitRequestBinary(req)
	require.NoError(t, err)
	require.True(t, hasMagic(body, deliverySubmitRequestMagicV3[:]))

	got, err := decodeDeliverySubmitRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
}

func TestDeliverySubmitBinaryCodecRejectsTooManyMessageScopedUIDs(t *testing.T) {
	body := append([]byte{}, deliverySubmitRequestMagicV2[:]...)
	body = appendChannelMessage(body, channel.Message{})
	body = appendUvarint(body, 0)
	body = appendUvarint(body, uint64(maxDeliverySubmitMessageScopedUIDs+1))

	_, err := decodeDeliverySubmitRequest(body)
	require.ErrorContains(t, err, "message scoped uids exceeds limit")
}

func TestDeliverySubmitRPCRejectsJSONPayload(t *testing.T) {
	recorder := &recordingDeliverySubmit{}
	adapter := New(Options{DeliverySubmit: recorder})

	_, err := adapter.handleDeliverySubmitRPC(context.Background(), []byte(`{"envelope":{"ChannelID":"u2","ChannelType":1,"MessageID":88}}`))
	require.Error(t, err)
	require.Empty(t, recorder.calls)
}

type recordingDeliverySubmit struct {
	calls []deliveryruntime.CommittedEnvelope
}

type legacyDeliverySubmitCapabilityCluster struct {
	calls     int
	serviceID uint8
}

type v2OnlyDeliverySubmitCapabilityCluster struct {
	v2ProbeCalls int
	v3ProbeCalls int
	payloadCalls int
}

func (c *legacyDeliverySubmitCapabilityCluster) RPCMux() *transport.RPCMux { return nil }

func (c *legacyDeliverySubmitCapabilityCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}

func (c *legacyDeliverySubmitCapabilityCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c *legacyDeliverySubmitCapabilityCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c *legacyDeliverySubmitCapabilityCluster) RPCService(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, _ []byte) ([]byte, error) {
	c.calls++
	c.serviceID = serviceID
	return nil, errors.New("access/node: invalid delivery submit request codec")
}

func (c *legacyDeliverySubmitCapabilityCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

func (c *v2OnlyDeliverySubmitCapabilityCluster) RPCMux() *transport.RPCMux { return nil }

func (c *v2OnlyDeliverySubmitCapabilityCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}

func (c *v2OnlyDeliverySubmitCapabilityCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c *v2OnlyDeliverySubmitCapabilityCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c *v2OnlyDeliverySubmitCapabilityCluster) RPCService(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, body []byte) ([]byte, error) {
	if serviceID != deliverySubmitRPCServiceID {
		return nil, errors.New("unexpected service")
	}
	switch {
	case isDeliverySubmitCapabilityProbe(body):
		c.v2ProbeCalls++
		return encodeDeliveryResponse(deliveryResponse{Status: rpcStatusOK})
	case isDeliverySubmitV3CapabilityProbe(body):
		c.v3ProbeCalls++
		return nil, errors.New("access/node: invalid delivery submit request codec")
	default:
		c.payloadCalls++
		return encodeDeliveryResponse(deliveryResponse{Status: rpcStatusOK})
	}
}

func (c *v2OnlyDeliverySubmitCapabilityCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

func (r *recordingDeliverySubmit) SubmitCommitted(_ context.Context, env deliveryruntime.CommittedEnvelope) error {
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	copied.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	r.calls = append(r.calls, copied)
	return nil
}
