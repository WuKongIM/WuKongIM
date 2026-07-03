//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInSlotDrainSurvivesDelayedLeaderTransfer(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2e-gofail-scale-in-slot-drain-token"
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
		enableGofail(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay, `return("5s")`)
		defer disableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	advance := f.manager.MustAdvanceScaleIn(t, 4, 1)
	require.Equal(t, uint32(1), advance.Created, "advance=%#v\n%s", advance, f.cluster.DumpDiagnostics())
	requireScaleInAdvanceMatchesCandidate(t, advance, candidate, f.cluster.DumpDiagnostics())
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

func TestLeavingNodeRestartDuringScaleInSlotDrainRecovers(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2e-gofail-scale-in-slot-drain-restart-token"
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
		enableGofail(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay, `return("30s")`)
		defer disableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	advance := f.manager.MustAdvanceScaleIn(t, 4, 1)
	require.Equal(t, uint32(1), advance.Created, "advance=%#v\n%s", advance, f.cluster.DumpDiagnostics())
	requireScaleInAdvanceMatchesCandidate(t, advance, candidate, f.cluster.DumpDiagnostics())
	active := waitScaleInTaskActive(t, f, 4, 10*time.Second)
	require.False(t, active.SafeToRemove, "status=%#v\n%s", active, f.cluster.DumpDiagnostics())
	waitGofailCountWhileScaleInBlocked(t, f, 4, slotReplicaMoveTransferLeaderDelay, 1, 20*time.Second)

	require.NoError(t, f.cluster.RestartNode(4), f.cluster.DumpDiagnostics())
	waitGofailListed(t, f.cluster, f.nodeFails[4], slotReplicaMoveTransferLeaderDelay)
	for _, endpoint := range f.staticFailpoints() {
		requireDisableDelayFailpoint(t, endpoint, slotReplicaMoveTransferLeaderDelay, false)
	}
	requireDisableDelayFailpoint(t, f.nodeFails[4], slotReplicaMoveTransferLeaderDelay, true)

	waitStableFaultableClusterReady(t, f, 30*time.Second)
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)
	eventuallyReapplyScaleInDrain(t, f, 4, 120*time.Second)

	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 90*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ActiveTaskCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.FailedTaskCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
	f.manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
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
	waitAnyManagerSlotPeerReady(t, f.cluster, candidate.SlotID, 4, 30*time.Second)
	ensureSlotLeaderForReplicaMoveSourceTB(t, f.cluster, candidate.SlotID, 4, 45*time.Second)
	return plan
}

func requireScaleInAdvanceMatchesCandidate(
	t testing.TB,
	advance suite.NodeScaleInAdvanceDTO,
	candidate suite.NodeScaleInCandidateDTO,
	diagnostics string,
) {
	t.Helper()

	require.Len(t, advance.Candidates, 1, "advance=%#v\n%s", advance, diagnostics)
	actual := advance.Candidates[0]
	require.Equal(t, candidate.SlotID, actual.SlotID, "advance=%#v candidate=%#v\n%s", advance, candidate, diagnostics)
	require.Equal(t, candidate.SourceNodeID, actual.SourceNodeID, "advance=%#v candidate=%#v\n%s", advance, candidate, diagnostics)
	require.Equal(t, candidate.TargetNodeID, actual.TargetNodeID, "advance=%#v candidate=%#v\n%s", advance, candidate, diagnostics)
	require.Equal(t, candidate.ConfigEpoch, actual.ConfigEpoch, "advance=%#v candidate=%#v\n%s", advance, candidate, diagnostics)
	require.Equal(t, candidate.TargetPeers, actual.TargetPeers, "advance=%#v candidate=%#v\n%s", advance, candidate, diagnostics)
}

func requireDisableDelayFailpoint(t testing.TB, endpoint suite.GofailEndpoint, failpoint string, tolerateDisabled bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := endpoint.Disable(ctx, failpoint)
	if err == nil {
		return
	}
	if tolerateDisabled && strings.Contains(err.Error(), "failpoint is disabled") {
		return
	}
	require.NoError(t, err)
}

func waitStableFaultableClusterReady(t testing.TB, f faultableDynamicCluster, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, nodeID := range []uint64{1, 2, 3} {
		node := f.cluster.MustNode(nodeID)
		if err := suite.WaitHTTPReady(ctx, node.APIAddr(), "/readyz"); err != nil {
			t.Fatalf("stable node %d http not ready: %v\n%s", nodeID, err, f.cluster.DumpDiagnostics())
		}
		if err := suite.WaitWKProtoReady(ctx, node.GatewayAddr()); err != nil {
			t.Fatalf("stable node %d wkproto not ready: %v\n%s", nodeID, err, f.cluster.DumpDiagnostics())
		}
	}
}

func eventuallyReapplyScaleInDrain(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastBody   []byte
		lastErr    error
		lastCode   int
	)
	for {
		callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
		statusCode, body, err := f.manager.SetScaleInDrainStatus(callCtx, nodeID, true)
		callCancel()
		lastCode = statusCode
		lastBody = body
		if err != nil {
			lastErr = err
		} else if statusCode/100 != 2 {
			lastErr = fmt.Errorf("set scale-in drain returned %d: %s", statusCode, strings.TrimSpace(string(body)))
		} else {
			statusCtx, statusCancel := context.WithTimeout(ctx, 2*time.Second)
			status, statusErr := f.manager.NodeScaleInStatus(statusCtx, nodeID)
			statusCancel()
			if statusErr != nil {
				lastErr = statusErr
			} else {
				lastStatus = status
				if status.GatewayDraining && !status.AcceptingNewSessions {
					return status
				}
				lastErr = fmt.Errorf("drain not reflected in scale-in status: %#v", status)
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in drain was not restored after restart: code=%d body=%s lastStatus=%#v lastErr=%v\n%s",
				nodeID,
				lastCode,
				strings.TrimSpace(string(lastBody)),
				lastStatus,
				lastErr,
				f.cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func waitAnyManagerSlotPeerReady(t testing.TB, cluster *suite.StartedCluster, slotID uint32, nodeID uint64, timeout time.Duration) {
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

func scaleInBlockedProofStatusError(status suite.NodeScaleInStatusDTO) error {
	if status.SafeToRemove {
		return fmt.Errorf("safe_to_remove=true before delayed Slot drain blocker was observed")
	}
	if status.FailedTaskCount > 0 {
		return fmt.Errorf("scale-in task failed before delayed Slot drain blocker was observed: failed=%d status=%#v", status.FailedTaskCount, status)
	}
	if !status.BlockedBySlots && !status.BlockedByTasks {
		return fmt.Errorf("expected Slot or task blocker while delayed Slot drain is active: status=%#v", status)
	}
	return nil
}

func TestScaleInBlockedProofStatusRequiresBlockedNotSafe(t *testing.T) {
	require.NoError(t, scaleInBlockedProofStatusError(suite.NodeScaleInStatusDTO{
		BlockedBySlots: true,
	}))
	require.NoError(t, scaleInBlockedProofStatusError(suite.NodeScaleInStatusDTO{
		BlockedByTasks: true,
	}))

	require.Error(t, scaleInBlockedProofStatusError(suite.NodeScaleInStatusDTO{
		SafeToRemove: true,
	}))
	require.Error(t, scaleInBlockedProofStatusError(suite.NodeScaleInStatusDTO{
		SafeToRemove:   true,
		BlockedBySlots: true,
	}))
	require.Error(t, scaleInBlockedProofStatusError(suite.NodeScaleInStatusDTO{
		FailedTaskCount: 1,
		BlockedByTasks:  true,
	}))
	require.Error(t, scaleInBlockedProofStatusError(suite.NodeScaleInStatusDTO{}))
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

		statusCtx, statusCancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := f.manager.NodeScaleInStatus(statusCtx, nodeID)
		statusCancel()
		if err != nil {
			lastErr = err
		} else {
			lastStatus = status
			if statusErr := scaleInBlockedProofStatusError(status); statusErr != nil {
				if status.SafeToRemove || status.FailedTaskCount > 0 {
					t.Fatalf("node %d reached terminal scale-in state before %s blocked proof was observed: counts=%v err=%v status=%#v\n%s",
						nodeID,
						failpoint,
						counts,
						statusErr,
						status,
						f.cluster.DumpDiagnostics(),
					)
				}
				lastErr = statusErr
			} else if total >= min {
				return total
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
