//go:build e2e

package dynamic_node_join

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestDynamicNodeJoin(t *testing.T) {
	const joinToken = "join-secret"

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
		suite.WithNodeConfigOverrides(2, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
		suite.WithNodeConfigOverrides(3, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	joined := s.StartDynamicJoinNode(cluster, 4, joinToken)
	require.NoError(t, suite.WaitNodeReady(ctx, *joined), cluster.DumpDiagnostics())

	staticObserver := cluster.MustNode(1)
	joinedView, body, err := suite.WaitForManagerNode(ctx, *staticObserver, joined.Spec.ID, func(node suite.ManagerNode) bool {
		return node.Status == "alive" && node.Addr == joined.Spec.ClusterAddr && node.Controller.Role == "none"
	})
	require.NoError(t, err, managerDiagnostics(cluster, body))
	require.Equal(t, joined.Spec.ClusterAddr, joinedView.Addr)
	require.Equal(t, "none", joinedView.Controller.Role)

	_, body, err = suite.WaitForManagerNode(ctx, *joined, staticObserver.Spec.ID, func(node suite.ManagerNode) bool {
		return node.Status == "alive" && node.Addr == staticObserver.Spec.ClusterAddr
	})
	require.NoError(t, err, managerDiagnostics(cluster, body))

	staticClient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = staticClient.Close() }()

	joinedClient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = joinedClient.Close() }()

	staticUID := "join-static-user"
	joinedUID := "join-node4-user"
	require.NoError(t, staticClient.Connect(staticObserver.GatewayAddr(), staticUID, "join-static-device"))
	require.NoError(t, joinedClient.Connect(joined.GatewayAddr(), joinedUID, "join-node4-device"))

	ok, err := suite.ConnectionsContainUID(ctx, *staticObserver, staticUID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	ok, err = suite.ConnectionsContainUID(ctx, *joined, joinedUID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	sendAndRequirePersonRecv(
		t,
		cluster,
		staticClient,
		joinedClient,
		staticUID,
		joinedUID,
		1,
		"dynamic-join-static-to-node4-01",
		[]byte("hello joined node from static node"),
	)
	sendAndRequirePersonRecv(
		t,
		cluster,
		joinedClient,
		staticClient,
		joinedUID,
		staticUID,
		1,
		"dynamic-join-node4-to-static-01",
		[]byte("hello static node from joined node"),
	)
}

func sendAndRequirePersonRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
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

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
}

func managerDiagnostics(cluster *suite.StartedCluster, body []byte) string {
	return fmt.Sprintf("%s\nmanager nodes body: %s", cluster.DumpDiagnostics(), string(body))
}
