//go:build e2e

package cross_node_closure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestCrossNodeClosure(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	topology, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Len(t, topology.FollowerNodeIDs, 2)

	senderNode := cluster.MustNode(topology.FollowerNodeIDs[0])
	recipientNode := cluster.MustNode(topology.FollowerNodeIDs[1])

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	recipient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = recipient.Close() }()

	require.NoError(t, sender.Connect(senderNode.GatewayAddr(), "u1", "u1-device"))
	require.NoError(t, recipient.Connect(recipientNode.GatewayAddr(), "u2", "u2-device"))

	ok, err := suite.ConnectionsContainUID(ctx, *senderNode, "u1")
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	ok, err = suite.ConnectionsContainUID(ctx, *recipientNode, "u2")
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	for i := 1; i <= 10; i++ {
		sendAndRequireCrossNodeRecv(
			t,
			cluster,
			sender,
			recipient,
			"u1",
			"u2",
			fmt.Sprintf("e2e-msg-3n-u1-to-u2-%02d", i),
			uint64(i),
			[]byte(fmt.Sprintf("hello u2 from u1 #%02d", i)),
		)
	}
	for i := 1; i <= 10; i++ {
		sendAndRequireCrossNodeRecv(
			t,
			cluster,
			recipient,
			sender,
			"u2",
			"u1",
			fmt.Sprintf("e2e-msg-3n-u2-to-u1-%02d", i),
			uint64(i),
			[]byte(fmt.Sprintf("hello u1 from u2 #%02d", i)),
		)
	}
}

func sendAndRequireCrossNodeRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID, clientMsgNo string,
	clientSeq uint64,
	payload []byte,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}))

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	recv, err := recipient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, senderUID, recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, payload, recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq))
}
