//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const slotReplicaMovePromoteLearnerDelay = "wkSlotReplicaMovePromoteLearnerDelay"

func TestJoiningNodeRestartDuringOnboardingRecovers(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2e-gofail-restart-onboarding-token"
	node1Fail := suite.ReserveGofailEndpoint(t)
	node2Fail := suite.ReserveGofailEndpoint(t)
	node3Fail := suite.ReserveGofailEndpoint(t)
	node4Fail := suite.ReserveGofailEndpoint(t)
	stablePromoteDelayEndpoints := []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail}
	promoteDelayEndpoints := append(append([]suite.GofailEndpoint(nil), stablePromoteDelayEndpoints...), node4Fail)

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, node1Fail.Env()),
		suite.WithNodeEnv(2, node2Fail.Env()),
		suite.WithNodeEnv(3, node3Fail.Env()),
		suite.WithNodeEnv(4, node4Fail.Env()),
	)
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
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

	activeReadyCtx, activeReadyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer activeReadyCancel()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())

	waitPromoteDelayListed(t, cluster, promoteDelayEndpoints)
	enablePromoteDelay(t, cluster, promoteDelayEndpoints, `return("30s")`)
	defer disablePromoteDelayBestEffort(t, promoteDelayEndpoints)

	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())

	activeStatus := waitOnboardingTaskActive(t, cluster, manager, 4, 30*time.Second)
	t.Logf("restarting node 4 during onboarding: status=%s", formatOnboardingStatus(activeStatus))
	require.NoError(t, cluster.RestartNode(4), cluster.DumpDiagnostics())

	waitGofailListed(t, cluster, node4Fail, slotReplicaMovePromoteLearnerDelay)
	requireDisablePromoteDelay(t, cluster, stablePromoteDelayEndpoints, node4Fail)

	restartedReadyCtx, restartedReadyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer restartedReadyCancel()
	require.NoError(t, cluster.WaitClusterReady(restartedReadyCtx), cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)

	statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer statusCancel()
	finalStatus, err := manager.NodeOnboardingStatus(statusCtx, 4)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, 0, finalStatus.Summary.Failed, "status=%#v\n%s", finalStatus, cluster.DumpDiagnostics())
	require.Empty(t, finalStatus.Tasks, "status=%#v\n%s", finalStatus, cluster.DumpDiagnostics())

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	require.NoError(t, client.Connect(node4.GatewayAddr(), "gofail-restart-onboarding-user", "gofail-restart-onboarding-device"), cluster.DumpDiagnostics())
}

func TestDisablePromoteDelayEndpointOnlyToleratesDisabledAfterRestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/"+slotReplicaMovePromoteLearnerDelay, r.URL.Path)
		http.Error(w, "failed to delete failpoint failpoint: failpoint is disabled", http.StatusBadRequest)
	}))
	defer server.Close()

	endpoint := suite.GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.Error(t, disablePromoteDelayEndpoint(ctx, endpoint, false))
	require.NoError(t, disablePromoteDelayEndpoint(ctx, endpoint, true))
}

func waitPromoteDelayListed(t testing.TB, cluster *suite.StartedCluster, endpoints []suite.GofailEndpoint) {
	t.Helper()

	for _, endpoint := range endpoints {
		waitGofailListed(t, cluster, endpoint, slotReplicaMovePromoteLearnerDelay)
	}
}

func enablePromoteDelay(t testing.TB, cluster *suite.StartedCluster, endpoints []suite.GofailEndpoint, expression string) {
	t.Helper()

	for _, endpoint := range endpoints {
		enableGofail(t, cluster, endpoint, slotReplicaMovePromoteLearnerDelay, expression)
	}
}

func disablePromoteDelayBestEffort(t testing.TB, endpoints []suite.GofailEndpoint) {
	t.Helper()

	for _, endpoint := range endpoints {
		disableGofail(t, endpoint, slotReplicaMovePromoteLearnerDelay)
	}
}

func requireDisablePromoteDelay(t testing.TB, cluster *suite.StartedCluster, stableEndpoints []suite.GofailEndpoint, restartedEndpoint suite.GofailEndpoint) {
	t.Helper()

	for _, endpoint := range stableEndpoints {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := disablePromoteDelayEndpoint(ctx, endpoint, false)
		cancel()
		require.NoError(t, err, cluster.DumpDiagnostics())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := disablePromoteDelayEndpoint(ctx, restartedEndpoint, true)
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
}

func disablePromoteDelayEndpoint(ctx context.Context, endpoint suite.GofailEndpoint, tolerateDisabled bool) error {
	err := endpoint.Disable(ctx, slotReplicaMovePromoteLearnerDelay)
	if err == nil {
		return nil
	}
	if tolerateDisabled && strings.Contains(err.Error(), "failpoint is disabled") {
		return nil
	}
	return err
}

func waitOnboardingTaskActive(
	t testing.TB,
	cluster *suite.StartedCluster,
	manager *suite.ManagerClient,
	nodeID uint64,
	timeout time.Duration,
) suite.NodeOnboardingStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeOnboardingStatusDTO
		lastErr    error
	)
	for {
		status, err := manager.NodeOnboardingStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if status.Summary.TotalActive > 0 &&
				(status.Summary.Pending > 0 || status.Summary.Running > 0 || len(status.Tasks) > 0) {
				return status
			}
			lastErr = fmt.Errorf("onboarding inactive: %s", formatOnboardingStatus(status))
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d onboarding task did not become active: last=%s lastErr=%v\n%s",
				nodeID,
				formatOnboardingStatus(lastStatus),
				lastErr,
				cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func formatOnboardingStatus(status suite.NodeOnboardingStatusDTO) string {
	return fmt.Sprintf("active=%d pending=%d running=%d failed=%d tasks=%#v",
		status.Summary.TotalActive,
		status.Summary.Pending,
		status.Summary.Running,
		status.Summary.Failed,
		status.Tasks,
	)
}
