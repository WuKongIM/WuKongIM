//go:build e2e

package controller_voter_promotion

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestPromoteDynamicDataNodeToControllerVoter(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-controller-voter-token"
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

	beforeNode := eventuallyPromotableDataNode(t, cluster, manager, 4, 60*time.Second)
	require.False(t, beforeNode.Controller.Voter, "node 4 should start as a non-Controller voter")
	require.True(t, beforeNode.Actions.CanPromoteControllerVoter, "node 4 should expose the promotion action")

	beforeStatus := manager.EventuallyControllerRaftVoters(t, 1, []uint64{1, 2, 3}, 30*time.Second)
	require.NotContains(t, beforeStatus.Voters, uint64(4))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	promoted, status, body, err := manager.PromoteControllerVoter(ctx, 4, 0)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equalf(t, http.StatusAccepted, status, "body=%s\n%s", strings.TrimSpace(string(body)), cluster.DumpDiagnostics())
	require.True(t, promoted.Changed)
	require.Equal(t, uint64(4), promoted.NodeID)
	require.NotContains(t, promoted.PreviousVoters, uint64(4))
	require.Contains(t, promoted.NextVoters, uint64(4))
	require.Greater(t, promoted.StateRevision, beforeNode.Health.ObservedControlRevision)

	afterNode := eventuallyControllerVoterNode(t, cluster, manager, 4, 60*time.Second)
	require.True(t, afterNode.Controller.Voter)
	require.False(t, afterNode.Actions.CanPromoteControllerVoter)

	node4Status := manager.EventuallyControllerRaftVoters(t, 4, promoted.NextVoters, 60*time.Second)
	require.Equal(t, uint64(4), node4Status.NodeID)
	require.NotZero(t, node4Status.AppliedIndex)
	require.NotEqual(t, "unknown", node4Status.Role)

	manager.EventuallyControllerRaftVoters(t, 1, promoted.NextVoters, 30*time.Second)
	requireNode4GatewaySend(t, cluster, node4)

	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRetry()
	retry, retryStatus, retryBody, retryErr := manager.PromoteControllerVoter(retryCtx, 4, 0)
	require.NoError(t, retryErr, cluster.DumpDiagnostics())
	require.Equalf(t, http.StatusOK, retryStatus, "body=%s\n%s", strings.TrimSpace(string(retryBody)), cluster.DumpDiagnostics())
	require.False(t, retry.Changed)
	require.ElementsMatch(t, promoted.NextVoters, retry.NextVoters)
}

func eventuallyPromotableDataNode(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	return eventuallyManagerNode(t, cluster, manager, nodeID, timeout, func(node suite.NodeDTO) error {
		switch {
		case node.Membership.JoinState != "active":
			return fmt.Errorf("join_state=%q", node.Membership.JoinState)
		case node.Controller.Voter:
			return fmt.Errorf("node is already a Controller voter")
		case !node.Actions.CanPromoteControllerVoter:
			return fmt.Errorf("promotion action is not available: node=%#v", node)
		case !node.Health.Fresh || !node.Health.RuntimeReady:
			return fmt.Errorf("health fresh=%v runtime_ready=%v", node.Health.Fresh, node.Health.RuntimeReady)
		default:
			return nil
		}
	})
}

func eventuallyControllerVoterNode(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	return eventuallyManagerNode(t, cluster, manager, nodeID, timeout, func(node suite.NodeDTO) error {
		switch {
		case node.Membership.JoinState != "active":
			return fmt.Errorf("join_state=%q", node.Membership.JoinState)
		case !node.Controller.Voter:
			return fmt.Errorf("node is not a Controller voter: node=%#v", node)
		case node.Actions.CanPromoteControllerVoter:
			return fmt.Errorf("promotion action is still available: node=%#v", node)
		default:
			return nil
		}
	})
}

func eventuallyManagerNode(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration, accept func(suite.NodeDTO) error) suite.NodeDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastNode suite.NodeDTO
		lastErr  error
	)
	for {
		reqCtx, reqCancel := context.WithTimeout(ctx, 2*time.Second)
		nodes, err := manager.ListNodes(reqCtx)
		reqCancel()
		if err == nil {
			for _, node := range nodes.Items {
				if node.NodeID != nodeID {
					continue
				}
				lastNode = node
				if checkErr := accept(node); checkErr == nil {
					return node
				} else {
					lastErr = checkErr
				}
				break
			}
			if lastNode.NodeID == 0 {
				lastErr = fmt.Errorf("node %d missing from manager inventory", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d manager inventory did not converge: last=%#v lastErr=%v\n%s", nodeID, lastNode, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireNode4GatewaySend(t *testing.T, cluster *suite.StartedCluster, node4 *suite.StartedNode) {
	t.Helper()

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	require.NoError(t, sender.Connect(node4.GatewayAddr(), "controller-voter-promotion-sender", "controller-voter-promotion-device"), cluster.DumpDiagnostics())
	const clientSeq uint64 = 1
	const clientMsgNo = "controller-voter-promotion-node4-send-1"
	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   "controller-voter-promotion-recipient",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     []byte("hello after controller voter promotion"),
	}), cluster.DumpDiagnostics())

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, cluster.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)
}
