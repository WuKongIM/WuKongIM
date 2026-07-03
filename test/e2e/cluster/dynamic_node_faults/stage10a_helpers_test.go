//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

const (
	reportNodeHealthFault        = "wkReportNodeHealthFault"
	controllerV2StateEventDrop   = "wkControllerV2StateEventDrop"
	stage10AHealthReportInterval = "200ms"
	stage10AHealthReportTTL      = "2s"
)

func stage10AOverrides() map[string]string {
	return map[string]string{
		"WK_BENCH_API_ENABLE":                    "true",
		"WK_METRICS_ENABLE":                      "true",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": stage10AHealthReportInterval,
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":      stage10AHealthReportTTL,
		"WK_DELIVERY_ENABLE":                     "true",
	}
}

func startStage10AFaultableCluster(t testing.TB, joinToken string) faultableDynamicCluster {
	t.Helper()

	nodeFails := map[uint64]suite.GofailEndpoint{
		1: suite.ReserveGofailEndpoint(t),
		2: suite.ReserveGofailEndpoint(t),
		3: suite.ReserveGofailEndpoint(t),
		4: suite.ReserveGofailEndpoint(t),
	}
	overrides := stage10AOverrides()
	s := suite.New(testingT(t))
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
		suite.WithNodeConfigOverrides(4, overrides),
		suite.WithNodeEnv(1, nodeFails[1].Env()),
		suite.WithNodeEnv(2, nodeFails[2].Env()),
		suite.WithNodeEnv(3, nodeFails[3].Env()),
		suite.WithNodeEnv(4, nodeFails[4].Env()),
	)
	readyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cluster.WaitClusterReady(readyCtx); err != nil {
		t.Fatalf("cluster not ready: %v\n%s", err, cluster.DumpDiagnostics())
	}
	return faultableDynamicCluster{cluster: cluster, manager: cluster.ManagerClient(t, 1), nodeFails: nodeFails}
}

func waitNodeHealthState(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration, check func(suite.NodeDTO) error) suite.NodeDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last suite.NodeDTO
	var lastErr error
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 3*time.Second)
		list, err := f.manager.ListNodes(reqCtx)
		cancelReq()
		if err == nil {
			for _, item := range list.Items {
				if item.NodeID != nodeID {
					continue
				}
				last = item
				if checkErr := check(item); checkErr == nil {
					return item
				} else {
					lastErr = checkErr
				}
			}
			if last.NodeID == 0 {
				lastErr = fmt.Errorf("node %d missing from manager list", nodeID)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("node %d health state did not match: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, f.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func waitNodeSchedulable(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()

	return waitNodeHealthState(t, f, nodeID, timeout, func(node suite.NodeDTO) error {
		if node.Membership.Schedulable && node.Health.Fresh && node.Health.Status == "alive" && node.Health.RuntimeReady {
			return nil
		}
		return fmt.Errorf("node not schedulable: membership=%#v health=%#v", node.Membership, node.Health)
	})
}

func waitNodeUnschedulableBecauseHealthStale(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()

	return waitNodeHealthState(t, f, nodeID, timeout, func(node suite.NodeDTO) error {
		if !node.Membership.Schedulable && !node.Health.Fresh && node.Health.Freshness == "stale" {
			return nil
		}
		return fmt.Errorf("node did not become stale/unschedulable: membership=%#v health=%#v", node.Membership, node.Health)
	})
}
