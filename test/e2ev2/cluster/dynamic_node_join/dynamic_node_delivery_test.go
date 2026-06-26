//go:build e2e

package dynamic_node_join

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestActivatedDynamicNodeDeliversCrossNodeTraffic(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-dynamic-delivery-token"
	overrides := dynamicDeliveryOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
		suite.WithNodeConfigOverrides(4, overrides),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})

	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	activeReadyCtx, cancelActiveReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelActiveReady()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())

	userA := connectedDynamicDeliveryClient(t, node4, "e2ev2-dynamic-delivery-a")
	defer func() { _ = userA.Close() }()
	node2 := cluster.MustNode(2)
	userB := connectedDynamicDeliveryClient(t, node2, "e2ev2-dynamic-delivery-b")
	defer func() { _ = userB.Close() }()

	sendAndRequireDynamicDelivery(t, cluster, node2, userA, userB, "e2ev2-dynamic-delivery-a", "e2ev2-dynamic-delivery-b", 1, "e2ev2-dynamic-a-to-b-1", []byte("hello b from dynamic node"))
	eventuallySendAndRequireDynamicDelivery(t, cluster, node4, userB, userA, "e2ev2-dynamic-delivery-b", "e2ev2-dynamic-delivery-a", "e2ev2-dynamic-b-to-a", []byte("hello dynamic node from b"), 30*time.Second)
}

func dynamicDeliveryOverrides() map[string]string {
	return map[string]string{
		"WK_DELIVERY_ENABLE":      "true",
		"WK_TOP_API_ENABLE":       "true",
		"WK_TOP_COLLECT_INTERVAL": "100ms",
		"WK_TOP_HISTORY_WINDOW":   "2s",
	}
}

func connectedDynamicDeliveryClient(t *testing.T, node *suite.StartedNode, uid string) *suite.WKProtoClient {
	t.Helper()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), node.DumpDiagnostics())
	return client
}

func sendAndRequireDynamicDelivery(
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
	t.Logf("dynamic recipient received message: %+v", recv)

	suite.RequireTopDeliveryAckBindingsAtLeastEventually(t, *recipientOwner, 1)
	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
	suite.RequireTopDeliveryAckBindingsEventually(t, *recipientOwner, 0)
}

func eventuallySendAndRequireDynamicDelivery(
	t *testing.T,
	cluster *suite.StartedCluster,
	recipientOwner *suite.StartedNode,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID string,
	clientMsgNoPrefix string,
	payloadPrefix []byte,
	timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	pending := make(map[int64]dynamicDeliveryPending)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		clientSeq := uint64(attempt + 1)
		clientMsgNo := fmt.Sprintf("%s-%02d", clientMsgNoPrefix, attempt)
		payload := []byte(fmt.Sprintf("%s %02d", string(payloadPrefix), attempt))
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
		pending[sendack.MessageID] = dynamicDeliveryPending{
			clientSeq:   clientSeq,
			clientMsgNo: clientMsgNo,
			payload:     payload,
			sendack:     sendack,
		}

		recv, err := recipient.ReadRecv()
		if err != nil {
			continue
		}
		expected, ok := pending[recv.MessageID]
		if !ok {
			continue
		}
		require.Equal(t, senderUID, recv.FromUID)
		require.Equal(t, senderUID, recv.ChannelID)
		require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
		require.Equal(t, expected.payload, recv.Payload)
		require.Equal(t, expected.sendack.MessageSeq, recv.MessageSeq)
		require.Equal(t, expected.clientSeq, expected.sendack.ClientSeq)
		require.Equal(t, expected.clientMsgNo, expected.sendack.ClientMsgNo)
		t.Logf("dynamic recipient eventually received message: %+v", recv)

		suite.RequireTopDeliveryAckBindingsAtLeastEventually(t, *recipientOwner, 1)
		require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
		suite.RequireTopDeliveryAckBindingsEventually(t, *recipientOwner, 0)
		return
	}
	t.Fatalf("dynamic delivery from %s to %s did not reach recipient before timeout; pending=%d\n%s", senderUID, recipientUID, len(pending), cluster.DumpDiagnostics())
}

type dynamicDeliveryPending struct {
	clientSeq   uint64
	clientMsgNo string
	payload     []byte
	sendack     *frame.SendackPacket
}
