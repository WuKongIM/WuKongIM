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

func TestSlotReplicaMoveKeepsSendAvailable(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-slot-onboarding-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
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

	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())
	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())

	requireGatewaySendDuringOnboardingActive(t, cluster, manager, node4, 4, "slot-onboarding-move", 5, 20*time.Second)
	manager.EventuallyOnboardingSafe(t, 4, 45*time.Second)
	requireGatewaySendLoop(t, cluster, node4, "slot-onboarding-move-safe", 100)
}

func requireGatewaySendDuringOnboardingActive(t *testing.T, cluster *suite.StartedCluster, manager *suite.ManagerClient, node *suite.StartedNode, nodeID uint64, prefix string, count int, timeout time.Duration) {
	t.Helper()

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	require.NoError(t, sender.Connect(node.GatewayAddr(), prefix+"-sender", prefix+"-sender-device"), cluster.DumpDiagnostics())

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeOnboardingStatusDTO
		lastErr    error
	)
	for sent := 0; sent < count; {
		status, err := manager.NodeOnboardingStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			switch {
			case status.Summary.Failed > 0:
				t.Fatalf("node %d onboarding failed while sending through active window: status=%#v\n%s", nodeID, status, cluster.DumpDiagnostics())
			case status.Summary.TotalActive > 0:
				sendGatewayMessage(t, cluster, sender, prefix, sent)
				sent++
				continue
			default:
				lastErr = fmt.Errorf("onboarding not active yet")
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d did not produce %d SENDACKs while onboarding was active: got=%d last=%#v lastErr=%v\n%s", nodeID, count, sent, lastStatus, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireGatewaySendLoop(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, prefix string, count int) {
	t.Helper()

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	require.NoError(t, sender.Connect(node.GatewayAddr(), prefix+"-sender", prefix+"-sender-device"), cluster.DumpDiagnostics())
	for i := 0; i < count; i++ {
		sendGatewayMessage(t, cluster, sender, prefix, i)
	}
}

func sendGatewayMessage(t *testing.T, cluster *suite.StartedCluster, sender *suite.WKProtoClient, prefix string, index int) {
	t.Helper()

	clientSeq := uint64(index + 1)
	clientMsgNo := fmt.Sprintf("%s-msg-%03d", prefix, index)
	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   fmt.Sprintf("%s-recipient-%03d", prefix, index),
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     []byte(clientMsgNo),
	}), cluster.DumpDiagnostics())

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, cluster.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)
}
