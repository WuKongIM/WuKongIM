//go:build e2e && legacy_e2e

package channel_leader_transfer

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/legacy/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestChannelLeaderTransferPreservesCrossNodeDelivery(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	topology, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Len(t, topology.FollowerNodeIDs, 2)

	senderNode := cluster.MustNode(topology.FollowerNodeIDs[0])
	recipientNode := cluster.MustNode(topology.FollowerNodeIDs[1])
	managerNode := cluster.MustNode(topology.LeaderNodeID)

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	recipient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = recipient.Close() }()

	const senderUID = "clt-u1"
	const recipientUID = "clt-u2"
	require.NoError(t, sender.Connect(senderNode.GatewayAddr(), senderUID, senderUID+"-device"))
	require.NoError(t, recipient.Connect(recipientNode.GatewayAddr(), recipientUID, recipientUID+"-device"))

	ok, err := suite.ConnectionsContainUID(ctx, *senderNode, senderUID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	ok, err = suite.ConnectionsContainUID(ctx, *recipientNode, recipientUID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	sendAndRequireRecv(t, cluster, sender, recipient, senderUID, recipientUID, "channel-transfer-bootstrap-1", 1, []byte("before channel leader transfer"))

	channelID := suite.PersonChannelID(senderUID, recipientUID)
	before, beforeBody, err := suite.WaitForChannelClusterReplicas(ctx, *managerNode, frame.ChannelTypePerson, channelID, func(detail suite.ManagerChannelClusterReplicaDetail) bool {
		if detail.Channel.Status != "active" || detail.Channel.Leader == 0 {
			return false
		}
		return selectTransferTarget(detail) != 0
	})
	require.NoError(t, err, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(beforeBody))
	t.Logf("channel replicas before transfer: %s", beforeBody)

	targetNodeID := selectTransferTarget(before)
	require.True(t, targetIsTransferable(before, targetNodeID), cluster.DumpDiagnostics()+"\nchannel replicas: "+string(beforeBody))
	require.NotZero(t, targetNodeID, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(beforeBody))
	require.NotEqual(t, before.Channel.Leader, targetNodeID)

	result, transferBody, err := suite.TransferChannelClusterLeader(ctx, *managerNode, frame.ChannelTypePerson, channelID, targetNodeID)
	require.NoError(t, err, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(beforeBody))
	require.True(t, result.Changed, cluster.DumpDiagnostics()+"\ntransfer response: "+string(transferBody))
	require.Equal(t, targetNodeID, result.Channel.Leader)
	require.Greater(t, result.Channel.LeaderEpoch, before.Channel.LeaderEpoch)
	require.Equal(t, before.Channel.ChannelEpoch, result.Channel.ChannelEpoch)
	require.Equal(t, before.Channel.Replicas, result.Channel.Replicas)
	require.Equal(t, before.Channel.ISR, result.Channel.ISR)
	require.Equal(t, before.Channel.MinISR, result.Channel.MinISR)
	require.Equal(t, before.Channel.Status, result.Channel.Status)
	require.Equal(t, before.Channel.Features, result.Channel.Features)

	after, afterBody, err := suite.WaitForChannelClusterReplicas(ctx, *managerNode, frame.ChannelTypePerson, channelID, func(detail suite.ManagerChannelClusterReplicaDetail) bool {
		return detail.Channel.Leader == targetNodeID &&
			detail.Channel.LeaderEpoch >= result.Channel.LeaderEpoch &&
			reportedLeader(detail, targetNodeID)
	})
	require.NoError(t, err, cluster.DumpDiagnostics()+"\ntransfer response: "+string(transferBody))
	t.Logf("channel replicas after transfer: %s", afterBody)
	require.Equal(t, targetNodeID, after.Channel.Leader, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(afterBody))

	sendAndRequireRecv(t, cluster, sender, recipient, senderUID, recipientUID, "channel-transfer-after-2", 2, []byte("after channel leader transfer"))
	sendAndRequireRecv(t, cluster, recipient, sender, recipientUID, senderUID, "channel-transfer-reverse-1", 1, []byte("reverse after channel leader transfer"))
}

func selectTransferTarget(detail suite.ManagerChannelClusterReplicaDetail) uint64 {
	for _, replica := range detail.Replicas {
		if !replica.IsLeader && replica.InISR {
			return replica.NodeID
		}
	}
	return 0
}

func targetIsTransferable(detail suite.ManagerChannelClusterReplicaDetail, nodeID uint64) bool {
	for _, replica := range detail.Replicas {
		if replica.NodeID == nodeID {
			return !replica.IsLeader && replica.InISR
		}
	}
	return false
}

func reportedLeader(detail suite.ManagerChannelClusterReplicaDetail, nodeID uint64) bool {
	if !detail.RuntimeReported {
		return false
	}
	for _, replica := range detail.Replicas {
		if replica.NodeID == nodeID {
			return replica.IsLeader && replica.Reported
		}
	}
	return false
}

func sendAndRequireRecv(
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
