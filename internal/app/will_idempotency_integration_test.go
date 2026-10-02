//go:build integration

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

// This starts the real 256-hash-slot single-node cluster and exercises Send's
// composition through committed proof. It does not open the MQTT listener.
func TestWillIdempotencySingleNodeClusterIndependentOfClientNumber(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	id := ch.ChannelID{ID: "mqtt-will-identity", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, id.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	seedGroupSendPermission(t, node, id, "u1")
	key := "mqtt-will-v1:" + strings.Repeat("a", 64)
	cmd := message.SendCommand{FromUID: "u1", DeviceID: "d1", ChannelID: id.ID, ChannelType: id.Type, ClientMsgNo: key, Payload: []byte("original")}
	native, err := a.Messages().Send(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, native.Reason)
	m := publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: key}
	will := cmd.Clone()
	will.PublicationMetadata, err = publication.Encode(m)
	require.NoError(t, err)
	first, err := a.Messages().Send(ctx, will)
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, first.Reason)
	require.Equal(t, uint64(2), first.MessageSeq)
	second, err := a.Messages().Send(ctx, will)
	require.NoError(t, err)
	require.Equal(t, first, second, "Will retry must find server identity despite native collision")
	m.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("b", 64)
	other := cmd.Clone()
	other.PublicationMetadata, err = publication.Encode(m)
	require.NoError(t, err)
	third, err := a.Messages().Send(ctx, other)
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, third.Reason)
	require.Equal(t, uint64(3), third.MessageSeq)
	for _, change := range []string{"client", "body"} {
		bad := will.Clone()
		if change == "client" {
			bad.ClientMsgNo = "changed"
		} else {
			bad.Payload = []byte("different")
		}
		result, err := a.Messages().Send(ctx, bad)
		require.True(t, err != nil || result.Reason != message.ReasonSuccess, "changed Will content accepted")
	}
	nativeRetry, err := a.Messages().Send(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, native, nativeRetry)
	rows, err := node.ReadChannelOriginalCommittedBatch(ctx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 4096}}})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NoError(t, rows[0].Err)
	require.Len(t, rows[0].Read.Messages, 3)
	require.Equal(t, will.PublicationMetadata, rows[0].Read.Messages[1].PublicationMetadata)
}
