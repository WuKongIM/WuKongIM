//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestStage10AControllerStateEventDropRecoversAfterHealthWakeup(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2e-stage10a-watch-drop-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	waitNodeSchedulable(t, f, 4, 30*time.Second)

	staticFailpoints := f.staticFailpoints()
	for _, endpoint := range staticFailpoints {
		waitGofailListed(t, f.cluster, endpoint, controllerStateEventDrop)
	}
	for _, endpoint := range staticFailpoints {
		enableGofail(t, f.cluster, endpoint, controllerStateEventDrop, `return("all")`)
		defer disableGofail(t, endpoint, controllerStateEventDrop)
	}

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState, "start=%#v\n%s", start, f.cluster.DumpDiagnostics())
	requiredRevision := start.Revision

	requireAnyGofailCountAtLeast(t, f.cluster, staticFailpoints, controllerStateEventDrop, 1)
	for _, endpoint := range staticFailpoints {
		requireDisableGofail(t, endpoint, controllerStateEventDrop)
	}

	staticNodes := waitStaticNodesObservedControlRevision(t, f, requiredRevision, 30*time.Second)
	require.Len(t, staticNodes, 3, "static_nodes=%#v\n%s", staticNodes, f.cluster.DumpDiagnostics())

	leaving := waitNodeJoinStateSnapshot(t, f, 4, "leaving", 30*time.Second)
	require.Equal(t, "leaving", leaving.Membership.JoinState, "node=%#v\n%s", leaving, f.cluster.DumpDiagnostics())

	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining, "drain=%#v\nstatic_nodes=%#v\nnode=%#v\n%s", drain, staticNodes, leaving, f.cluster.DumpDiagnostics())
}

func waitNodeJoinStateSnapshot(t testing.TB, f faultableDynamicCluster, nodeID uint64, state string, timeout time.Duration) suite.NodeDTO {
	t.Helper()

	return waitNodeHealthState(t, f, nodeID, timeout, func(node suite.NodeDTO) error {
		if node.Membership.JoinState == state {
			return nil
		}
		return fmt.Errorf("node %d join_state=%q, want %q", nodeID, node.Membership.JoinState, state)
	})
}

func waitStaticNodesObservedControlRevision(t testing.TB, f faultableDynamicCluster, requiredRevision uint64, timeout time.Duration) []suite.NodeDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	staticNodeIDs := []uint64{1, 2, 3}
	var (
		lastStatic []suite.NodeDTO
		lastErr    error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 3*time.Second)
		list, err := f.manager.ListNodes(reqCtx)
		cancelReq()
		if err == nil {
			byID := make(map[uint64]suite.NodeDTO, len(list.Items))
			for _, node := range list.Items {
				byID[node.NodeID] = node
			}

			static := make([]suite.NodeDTO, 0, len(staticNodeIDs))
			ok := true
			for _, nodeID := range staticNodeIDs {
				node, exists := byID[nodeID]
				if !exists {
					lastErr = fmt.Errorf("static node %d missing from manager list", nodeID)
					ok = false
					break
				}
				static = append(static, node)
				if !node.Health.Fresh {
					lastErr = fmt.Errorf("static node %d health not fresh: %#v", nodeID, node.Health)
					ok = false
					break
				}
				if node.Health.ObservedControlRevision < requiredRevision {
					lastErr = fmt.Errorf(
						"static node %d observed control revision=%d, want >= %d: health=%#v",
						nodeID,
						node.Health.ObservedControlRevision,
						requiredRevision,
						node.Health,
					)
					ok = false
					break
				}
				if !node.Health.RuntimeReady {
					lastErr = fmt.Errorf("static node %d runtime not ready: %#v", nodeID, node.Health)
					ok = false
					break
				}
			}
			lastStatic = static
			if ok {
				return static
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf(
				"static nodes did not observe control revision >= %d: last=%#v lastErr=%v\n%s",
				requiredRevision,
				lastStatic,
				lastErr,
				f.cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}
