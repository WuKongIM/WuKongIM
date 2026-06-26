//go:build e2e

package dynamic_node_join

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInRejectsUnsafeTargetsAndRemoveIsIdempotent(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-scale-in-safety-token"
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
	require.NotNil(t, node4)

	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	activeReadyCtx, cancelActiveReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelActiveReady()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	drainStatus, drainBody, err := manager.SetScaleInDrainStatus(ctx, 4, true)
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
	requireScaleInConflict(t, drainStatus, drainBody, cluster.DumpDiagnostics())

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	removeStatus, removeBody, err := manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
	requireScaleInConflict(t, removeStatus, removeBody, cluster.DumpDiagnostics())

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	controllerStartStatus, controllerStartBody, err := manager.StartScaleInStatus(ctx, 1)
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
	requireScaleInConflict(t, controllerStartStatus, controllerStartBody, cluster.DumpDiagnostics())

	start := manager.MustStartScaleIn(t, 4)
	require.Equal(t, uint64(4), start.NodeID)
	require.Equal(t, "leaving", start.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	removeBeforeDrainStatus, removeBeforeDrainBody, err := manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
	requireScaleInConflict(t, removeBeforeDrainStatus, removeBeforeDrainBody, cluster.DumpDiagnostics())

	drain := manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	require.False(t, drain.AcceptingNewSessions)

	safe := manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove)
	require.False(t, safe.BlockedByRuntimeDrain)
	require.True(t, safe.GatewayDraining)
	require.False(t, safe.AcceptingNewSessions)

	removed := manager.MustRemoveScaleInNode(t, 4)
	require.True(t, removed.Changed)
	require.Equal(t, uint64(4), removed.NodeID)
	require.Equal(t, "removed", removed.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)

	second := manager.MustRemoveScaleInNode(t, 4)
	require.False(t, second.Changed)
	require.Equal(t, uint64(4), second.NodeID)
	require.Equal(t, "removed", second.JoinState)
}

func requireScaleInConflict(t testing.TB, status int, body []byte, diagnostics string) {
	t.Helper()

	require.Equal(t, http.StatusConflict, status, "body=%s\n%s", string(body), diagnostics)
	var payload map[string]string
	require.NoError(t, json.Unmarshal(body, &payload), "body=%s\n%s", string(body), diagnostics)
	code := payload["error"]
	if code == "" {
		code = payload["code"]
	}
	require.Equal(t, "conflict", code, "body=%s\n%s", string(body), diagnostics)
}
