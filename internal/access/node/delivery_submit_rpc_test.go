package node

import (
	"context"
	"testing"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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
		SenderSessionID:   42,
		MessageScopedUIDs: []string{"u1", "u2"},
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
		SenderSessionID:   42,
		MessageScopedUIDs: []string{"u1", "u2"},
	}}, recorder.calls)
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
		SenderSessionID:   42,
		MessageScopedUIDs: []string{"u1", "u2"},
	}}

	body, err := encodeDeliverySubmitRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isDeliverySubmitRequestBinary(body))

	got, err := decodeDeliverySubmitRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
}

func TestDeliverySubmitBinaryCodecRejectsTooManyMessageScopedUIDs(t *testing.T) {
	body := append([]byte{}, deliverySubmitRequestMagic[:]...)
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

func (r *recordingDeliverySubmit) SubmitCommitted(_ context.Context, env deliveryruntime.CommittedEnvelope) error {
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	copied.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	r.calls = append(r.calls, copied)
	return nil
}
