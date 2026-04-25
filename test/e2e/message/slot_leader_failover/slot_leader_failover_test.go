//go:build e2e

package slot_leader_failover

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestSlotLeaderFailover(t *testing.T) {
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
	leaderNode := cluster.MustNode(topology.LeaderNodeID)

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

	require.NoError(t, leaderNode.Stop())

	failoverCtx, failoverCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer failoverCancel()

	newTopology, err := cluster.WaitForSlotLeaderChange(failoverCtx, 1, topology.LeaderNodeID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.NotEqual(t, topology.LeaderNodeID, newTopology.LeaderNodeID)

	ok, err = suite.ConnectionsContainUID(failoverCtx, *senderNode, "u1")
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	ok, err = suite.ConnectionsContainUID(failoverCtx, *recipientNode, "u2")
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	sendAndRequireCrossNodeRecv(t, cluster, sender, recipient, "e2e-msg-3n-failover-1", 1, []byte("after failover"))
}

func sendAndRequireCrossNodeRecv(t *testing.T, cluster *suite.StartedCluster, sender, recipient *suite.WKProtoClient, clientMsgNo string, clientSeq uint64, payload []byte) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   "u2",
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
	require.Equal(t, "u1", recv.FromUID)
	require.Equal(t, "u1", recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, payload, recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq))
}
