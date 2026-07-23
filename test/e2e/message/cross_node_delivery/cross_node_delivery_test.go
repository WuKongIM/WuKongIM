//go:build e2e

package cross_node_delivery

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeClusterCrossNodeUsersExchangeMessages(t *testing.T) {
	s := suite.New(t)
	overrides := deliveryTopOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	convergence, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	t.Logf(
		"actual Slot leaders stable for %s after %s: %v",
		convergence.StableDuration,
		convergence.WaitDuration,
		convergence.Leaders,
	)

	userA := newConnectedClient(t, cluster.MustNode(1), "e2e-cross-a")
	defer func() { _ = userA.Close() }()
	userB := newConnectedClient(t, cluster.MustNode(2), "e2e-cross-b")
	defer func() { _ = userB.Close() }()

	sendAndRequireRecv(t, cluster, cluster.MustNode(2), userA, userB, "e2e-cross-a", "e2e-cross-b", 1, "e2e-cross-a-to-b-1", []byte("hello b from a"))
	sendAndRequireRecv(t, cluster, cluster.MustNode(1), userB, userA, "e2e-cross-b", "e2e-cross-a", 1, "e2e-cross-b-to-a-1", []byte("hello a from b"))
}

func deliveryTopOverrides() map[string]string {
	return map[string]string{
		"WK_DELIVERY_ENABLE":      "true",
		"WK_TOP_API_ENABLE":       "true",
		"WK_TOP_COLLECT_INTERVAL": "100ms",
		"WK_TOP_HISTORY_WINDOW":   "2s",
	}
}

func newConnectedClient(t *testing.T, node *suite.StartedNode, uid string) *suite.WKProtoClient {
	t.Helper()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), node.DumpDiagnostics())
	return client
}

func sendAndRequireRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
	recipientOwner *suite.StartedNode,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID string,
	clientSeq uint64,
	clientMsgNo string,
	payload []byte,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}), cluster.DumpDiagnostics())

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, cluster.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
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
	fmt.Printf("recipient received message: %+v\n", recv)

	suite.RequireTopDeliveryAckBindingsAtLeastEventually(t, *recipientOwner, 1)
	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
	suite.RequireTopDeliveryAckBindingsEventually(t, *recipientOwner, 0)
}
