//go:build e2e

package dynamic_node_faults

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestStage10AHealthReportFaultMakesActiveNodeUnschedulableThenRecovers(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-stage10a-health-fault-token"
	f := startStage10AHealthFaultCluster(t, joinToken)
	waitNodesSchedulable(t, f, []uint64{1, 2, 3}, 30*time.Second)

	startActiveNode4(t, f, joinToken)
	waitNodeSchedulable(t, f, 4, 30*time.Second)

	staticFailpoints := f.staticFailpoints()
	for _, endpoint := range staticFailpoints {
		waitGofailListed(t, f.cluster, endpoint, reportNodeHealthFault)
	}
	for _, endpoint := range staticFailpoints {
		enableGofail(t, f.cluster, endpoint, reportNodeHealthFault, `return("4:stage10a health report paused")`)
		defer disableGofail(t, endpoint, reportNodeHealthFault)
	}

	stale := waitNodeUnschedulableBecauseHealthStale(t, f, 4, 10*time.Second)
	require.False(t, stale.Membership.Schedulable, "stale=%#v\n%s", stale, f.cluster.DumpDiagnostics())
	require.False(t, stale.Health.Fresh, "stale=%#v\n%s", stale, f.cluster.DumpDiagnostics())
	require.Equal(t, "stale", stale.Health.Freshness, "stale=%#v\n%s", stale, f.cluster.DumpDiagnostics())

	status, body, err := onboardingPlanStatus(t, f, 4, 1)
	require.NoError(t, err, "body=%s\n%s", string(body), f.cluster.DumpDiagnostics())
	require.Equal(t, http.StatusConflict, status, "status=%d body=%s\nstale=%#v\n%s", status, string(body), stale, f.cluster.DumpDiagnostics())

	requireAnyGofailCountAtLeast(t, f.cluster, staticFailpoints, reportNodeHealthFault, 1)
	for _, endpoint := range staticFailpoints {
		requireDisableGofail(t, endpoint, reportNodeHealthFault)
	}

	fresh := waitNodeSchedulable(t, f, 4, 30*time.Second)
	require.True(t, fresh.Membership.Schedulable, "fresh=%#v\n%s", fresh, f.cluster.DumpDiagnostics())
}

func startStage10AHealthFaultCluster(t testing.TB, joinToken string) faultableDynamicCluster {
	t.Helper()

	nodeFails := map[uint64]suite.GofailEndpoint{
		1: suite.ReserveGofailEndpoint(t),
		2: suite.ReserveGofailEndpoint(t),
		3: suite.ReserveGofailEndpoint(t),
		4: suite.ReserveGofailEndpoint(t),
	}
	staticOverrides := map[string]string{
		"WK_BENCH_API_ENABLE":                    "true",
		"WK_METRICS_ENABLE":                      "true",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": "1s",
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":      "2s",
		"WK_DELIVERY_ENABLE":                     "true",
	}
	node4Overrides := stage10AOverrides()

	s := suite.New(testingT(t))
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, staticOverrides),
		suite.WithNodeConfigOverrides(2, staticOverrides),
		suite.WithNodeConfigOverrides(3, staticOverrides),
		suite.WithNodeConfigOverrides(4, node4Overrides),
		suite.WithNodeEnv(1, nodeFails[1].Env()),
		suite.WithNodeEnv(2, nodeFails[2].Env()),
		suite.WithNodeEnv(3, nodeFails[3].Env()),
		suite.WithNodeEnv(4, nodeFails[4].Env()),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	return faultableDynamicCluster{
		cluster:   cluster,
		manager:   cluster.ManagerClient(t, 1),
		nodeFails: nodeFails,
	}
}

func waitNodesSchedulable(t testing.TB, f faultableDynamicCluster, nodeIDs []uint64, timeout time.Duration) {
	t.Helper()

	for _, nodeID := range nodeIDs {
		waitNodeSchedulable(t, f, nodeID, timeout)
	}
}

func onboardingPlanStatus(t testing.TB, f faultableDynamicCluster, nodeID uint64, maxSlotMoves uint32) (int, []byte, error) {
	t.Helper()

	managerNode := f.cluster.MustNode(1)
	body, err := json.Marshal(map[string]uint32{
		"max_slot_moves": maxSlotMoves,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("marshal onboarding plan request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("http://%s/manager/nodes/%d/onboarding/plan", managerNode.ManagerAddr(), nodeID),
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, nil, fmt.Errorf("build onboarding plan request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read onboarding plan response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}
