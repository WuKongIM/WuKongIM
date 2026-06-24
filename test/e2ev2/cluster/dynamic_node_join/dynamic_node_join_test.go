//go:build e2e

package dynamic_node_join

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestDynamicJoinFourthDataNode(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-join-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	client := cluster.ManagerClient(t, 1)
	before := client.MustSlots(t)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})

	client.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	client.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	client.MustActivateNode(t, 4)
	client.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	activeReadyCtx, cancelActiveReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelActiveReady()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())
	requireNode4GatewaySend(t, cluster, node4)

	after := client.MustSlots(t)
	if !suite.SameSlotAssignments(before, after) {
		t.Fatalf("slot assignments changed during join: before=%#v after=%#v", before, after)
	}
}

func requireNode4GatewaySend(t *testing.T, cluster *suite.StartedCluster, node4 *suite.StartedNode) {
	t.Helper()

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	require.NoError(t, sender.Connect(node4.GatewayAddr(), "dynamic-join-sender", "dynamic-join-sender-device"), cluster.DumpDiagnostics())
	const clientSeq uint64 = 1
	const clientMsgNo = "dynamic-join-node4-send-1"
	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   "dynamic-join-recipient",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     []byte("hello from dynamic node4"),
	}), cluster.DumpDiagnostics())

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, cluster.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)
}
