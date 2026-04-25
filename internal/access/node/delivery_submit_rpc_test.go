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
		SenderSessionID: 42,
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
		SenderSessionID: 42,
	}}, recorder.calls)
}

type recordingDeliverySubmit struct {
	calls []deliveryruntime.CommittedEnvelope
}

func (r *recordingDeliverySubmit) SubmitCommitted(_ context.Context, env deliveryruntime.CommittedEnvelope) error {
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	r.calls = append(r.calls, copied)
	return nil
}
