//go:build e2e

package dynamic_node_join

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInDrainKeepsExistingSessionAndBlocksFinalRemove(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2e-live-session-drain-token"
	const senderPrefix = "scale-in-live-session"
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

	manager.EventuallyNodeJoinState(t, 4, "joining", 45*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	activeReadyCtx, cancelActiveReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelActiveReady()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())

	sender := requireGatewaySender(t, cluster, node4, senderPrefix)

	start := manager.MustStartScaleIn(t, 4)
	require.Equal(t, uint64(4), start.NodeID)
	require.Equal(t, "leaving", start.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)

	statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	status, err := manager.NodeScaleInStatus(statusCtx, 4)
	cancelStatus()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.False(t, status.SafeToRemove, "scale-in status should not be safe while the gateway session is open: %#v", status)

	drain := manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	require.False(t, drain.AcceptingNewSessions)

	drainingStatus := eventuallyScaleInBlockedByLiveSession(t, cluster, manager, 4, 20*time.Second)
	require.True(t, drainingStatus.GatewayDraining)
	require.False(t, drainingStatus.AcceptingNewSessions)
	require.False(t, drainingStatus.SafeToRemove)
	require.True(t, drainingStatus.BlockedByRuntimeDrain)
	require.True(t, scaleInHasLiveSessionBlocker(drainingStatus))

	require.NoError(t, sender.SendFrame(&frame.PingPacket{}), cluster.DumpDiagnostics())

	rejected, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = rejected.Close() }()
	rejectCtx, cancelReject := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = rejected.ConnectContext(rejectCtx, node4.GatewayAddr(), "scale-in-live-session-rejected", "scale-in-live-session-rejected-device")
	cancelReject()
	require.Error(t, err, "new WKProto sessions should be rejected while node 4 is draining")
	require.False(t, errors.Is(err, context.DeadlineExceeded), "new WKProto session rejection timed out instead of being actively rejected")

	removeCtx, cancelRemove := context.WithTimeout(context.Background(), 5*time.Second)
	removeStatus, removeBody, err := manager.RemoveScaleInStatus(removeCtx, 4)
	cancelRemove()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, http.StatusConflict, removeStatus, "remove before closing live session body=%s\n%s", string(removeBody), cluster.DumpDiagnostics())

	require.NoError(t, sender.SendFrame(&frame.PingPacket{}), cluster.DumpDiagnostics())

	require.NoError(t, sender.Close(), cluster.DumpDiagnostics())

	safe := manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove)
	require.False(t, safe.BlockedByRuntimeDrain)
	require.False(t, scaleInHasLiveSessionBlocker(safe))

	removed := manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, uint64(4), removed.NodeID)
	require.Equal(t, "removed", removed.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
}

func eventuallyScaleInBlockedByLiveSession(t *testing.T, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastErr    error
	)
	for {
		status, err := manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if status.GatewayDraining &&
				!status.AcceptingNewSessions &&
				!status.SafeToRemove &&
				status.BlockedByRuntimeDrain &&
				scaleInHasLiveSessionBlocker(status) {
				return status
			}
			lastErr = fmt.Errorf("gateway_draining=%t accepting_new_sessions=%t safe_to_remove=%t blocked_by_runtime_drain=%t gateway_sessions=%d active_online=%d total_online=%d",
				status.GatewayDraining,
				status.AcceptingNewSessions,
				status.SafeToRemove,
				status.BlockedByRuntimeDrain,
				status.GatewaySessions,
				status.ActiveOnline,
				status.TotalOnline,
			)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in did not report live-session drain block: last=%#v lastErr=%v\n%s", nodeID, lastStatus, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func scaleInHasLiveSessionBlocker(status suite.NodeScaleInStatusDTO) bool {
	return status.GatewaySessions > 0 || status.ActiveOnline > 0 || status.TotalOnline > 0
}
