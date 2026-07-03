//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestStage10AScaleInConcurrentAdvanceStaysBoundedWhileSlotDrainDelayed(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2e-stage10a-scale-in-retry-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	onboardOneSlotToNode4ForDelayedScaleIn(t, f)
	plan := startScaleInDrainWithOneSlotMove(t, f)
	candidate := plan.Candidates[0]

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay, `return("5s")`)
		defer disableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	results := runConcurrentScaleInRetryAdvances(t, f.manager, 4, 1, 4)
	var (
		created   int
		conflicts int
		skipped   int
	)
	for _, result := range results {
		require.NoError(t, result.err, "result=%s\n%s", result, f.cluster.DumpDiagnostics())
		switch result.status {
		case http.StatusOK, http.StatusAccepted:
			if result.response.Created > 0 {
				created += int(result.response.Created)
				requireScaleInAdvanceMatchesCandidate(t, result.response, candidate, f.cluster.DumpDiagnostics())
				continue
			}
			skipped++
		case http.StatusConflict:
			requireScaleInRemoveConflict(t, result.status, result.body, result.err, f.cluster.DumpDiagnostics())
			conflicts++
		case http.StatusInternalServerError:
			t.Fatalf("scale-in advance returned 500: result=%s\n%s", result, f.cluster.DumpDiagnostics())
		default:
			t.Fatalf("unexpected scale-in advance status=%d result=%s\n%s", result.status, result, f.cluster.DumpDiagnostics())
		}
	}
	require.LessOrEqual(t, created, 1, "results=%#v\n%s", results, f.cluster.DumpDiagnostics())
	require.True(t, conflicts >= 1 || skipped >= 1, "expected bounded non-create result: conflicts=%d skipped=%d results=%#v\n%s", conflicts, skipped, results, f.cluster.DumpDiagnostics())

	active := waitScaleInTaskActive(t, f, 4, 10*time.Second)
	require.False(t, active.SafeToRemove, "status=%#v\n%s", active, f.cluster.DumpDiagnostics())
	waitGofailCountWhileScaleInBlocked(t, f, 4, slotReplicaMoveTransferLeaderDelay, 1, 10*time.Second)

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 90*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.FailedTaskCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
}

type scaleInRetryAdvanceResult struct {
	response suite.NodeScaleInAdvanceDTO
	status   int
	body     []byte
	err      error
}

func runConcurrentScaleInRetryAdvances(t testing.TB, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32, count int) []scaleInRetryAdvanceResult {
	t.Helper()

	results := make(chan scaleInRetryAdvanceResult, count)
	for i := 0; i < count; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, status, body, err := manager.AdvanceScaleIn(ctx, nodeID, maxSlotMoves)
			results <- scaleInRetryAdvanceResult{response: response, status: status, body: body, err: err}
		}()
	}
	return collectScaleInRetryAdvanceResults(results, count)
}

func collectScaleInRetryAdvanceResults(results <-chan scaleInRetryAdvanceResult, count int) []scaleInRetryAdvanceResult {
	out := make([]scaleInRetryAdvanceResult, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, <-results)
	}
	return out
}

func (r scaleInRetryAdvanceResult) String() string {
	return fmt.Sprintf("{status:%d created:%d skipped:%d err:%v body:%s}", r.status, r.response.Created, r.response.Skipped, r.err, string(r.body))
}
