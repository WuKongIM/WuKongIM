//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInSlotDrainSurvivesDelayedLeaderTransfer(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-scale-in-slot-drain-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	onboardOneSlotToNode4ForDelayedScaleIn(t, f)
	plan := startScaleInDrainWithOneSlotMove(t, f)
	candidate := plan.Candidates[0]
	require.Equal(t, uint64(4), candidate.SourceNodeID, "candidate=%#v\n%s", candidate, f.cluster.DumpDiagnostics())

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay, `return("2s")`)
		defer disableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	advance := f.manager.MustAdvanceScaleIn(t, 4, 1)
	require.Equal(t, uint32(1), advance.Created, "advance=%#v\n%s", advance, f.cluster.DumpDiagnostics())
	active := waitScaleInTaskActive(t, f, 4, 10*time.Second)
	require.False(t, active.SafeToRemove, "status=%#v\n%s", active, f.cluster.DumpDiagnostics())
	waitGofailCountWhileScaleInBlocked(t, f, 4, slotReplicaMoveTransferLeaderDelay, 1, 10*time.Second)
	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if status.SafeToRemove {
			return fmt.Errorf("safe_to_remove=true while delayed Slot drain is still active")
		}
		if !status.BlockedBySlots && !status.BlockedByTasks {
			return fmt.Errorf("expected Slot or task blocker while delayed; status=%#v", status)
		}
		return nil
	})

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 90*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.False(t, safe.BlockedBySlots, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.FailedTaskCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
}

func onboardOneSlotToNode4ForDelayedScaleIn(t testing.TB, f faultableDynamicCluster) suite.NodeOnboardingPlanDTO {
	t.Helper()

	managerNodeID := controlWriteForwardingNodeID(t, f.cluster)
	plan := requireStableFaultableOnboardingPlan(t, f.cluster, managerNodeID, 4, 30*time.Second)
	candidate := plan.Candidates[0]
	ensureSlotLeaderForReplicaMoveSourceTB(t, f.cluster, candidate.SlotID, candidate.SourceNodeID, 45*time.Second)

	manager := f.cluster.ManagerClient(t, managerNodeID)
	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, f.cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)

	statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer statusCancel()
	status, err := manager.NodeOnboardingStatus(statusCtx, 4)
	require.NoError(t, err, f.cluster.DumpDiagnostics())
	require.Zero(t, status.Summary.Failed, "onboarding status=%#v\n%s", status, f.cluster.DumpDiagnostics())
	waitManagerSlotPeerReady(t, f.cluster, candidate.SlotID, 4, 30*time.Second)
	ensureSlotLeaderForReplicaMoveSourceTB(t, f.cluster, candidate.SlotID, 4, 45*time.Second)
	return plan
}

func waitManagerSlotPeerReady(t testing.TB, cluster *suite.StartedCluster, slotID uint32, nodeID uint64, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastSlots []suite.SlotDTO
		lastErr   error
	)
	for {
		for _, node := range cluster.Nodes {
			var resp struct {
				Items []suite.SlotDTO `json:"items"`
			}
			_, err := suite.GetJSON(ctx, fmt.Sprintf("http://%s/manager/slots", node.ManagerAddr()), &resp)
			if err != nil {
				lastErr = err
				continue
			}
			lastSlots = resp.Items
			if managerSlotPeerReady(resp.Items, slotID, nodeID) {
				return
			}
			lastErr = fmt.Errorf("manager node %d Slot %d is not ready on peer %d: %#v", node.Spec.ID, slotID, nodeID, resp.Items)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("manager Slot inventory never showed Slot %d ready on peer %d: lastSlots=%#v lastErr=%v\n%s",
				slotID,
				nodeID,
				lastSlots,
				lastErr,
				cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func waitGofailCountWhileScaleInBlocked(
	t testing.TB,
	f faultableDynamicCluster,
	nodeID uint64,
	failpoint string,
	min int,
	timeout time.Duration,
) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastCounts []string
		lastErr    error
	)
	for {
		total := 0
		counts := make([]string, 0, len(f.allFailpoints()))
		for _, endpoint := range f.allFailpoints() {
			countCtx, countCancel := context.WithTimeout(ctx, 2*time.Second)
			count, err := endpoint.Count(countCtx, failpoint)
			countCancel()
			if err != nil {
				lastErr = err
				continue
			}
			total += count
			counts = append(counts, fmt.Sprintf("%s=%d", endpoint.Addr, count))
		}
		lastCounts = counts
		if total >= min {
			return total
		}

		statusCtx, statusCancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := f.manager.NodeScaleInStatus(statusCtx, nodeID)
		statusCancel()
		if err != nil {
			lastErr = err
		} else {
			lastStatus = status
			if status.SafeToRemove {
				t.Fatalf("node %d became safe_to_remove before %s was observed: counts=%v status=%#v\n%s",
					nodeID,
					failpoint,
					counts,
					status,
					f.cluster.DumpDiagnostics(),
				)
			}
			if status.FailedTaskCount > 0 {
				t.Fatalf("node %d scale-in task failed before %s was observed: counts=%v status=%#v\n%s",
					nodeID,
					failpoint,
					counts,
					status,
					f.cluster.DumpDiagnostics(),
				)
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("failpoint %s was not observed while scale-in was blocked: counts=%v lastStatus=%#v lastErr=%v\n%s",
				failpoint,
				lastCounts,
				lastStatus,
				lastErr,
				f.cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func managerSlotPeerReady(slots []suite.SlotDTO, slotID uint32, nodeID uint64) bool {
	for _, slot := range slots {
		if slot.SlotID != slotID {
			continue
		}
		return slot.Task == nil &&
			slices.Contains(slot.Assignment.DesiredPeers, nodeID) &&
			slices.Contains(slot.Runtime.CurrentVoters, nodeID) &&
			slot.Runtime.HasQuorum
	}
	return false
}
