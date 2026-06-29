//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInChannelInventoryFaultFailsClosed(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-channel-inventory-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)
	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, scaleInChannelInventoryFault)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, scaleInChannelInventoryFault, `return("temporary channel inventory fault")`)
		defer disableGofail(t, endpoint, scaleInChannelInventoryFault)
	}

	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if !status.UnknownChannelInventory || !status.BlockedByChannels {
			return fmt.Errorf("channel inventory did not fail closed: %#v", status)
		}
		if status.SafeToRemove {
			return fmt.Errorf("safe_to_remove=true while channel inventory is unknown")
		}
		return nil
	})
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), scaleInChannelInventoryFault, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	status, body, err := f.manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	requireScaleInRemoveConflict(t, status, body, err, f.cluster.DumpDiagnostics())
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 5*time.Second)

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, scaleInChannelInventoryFault)
	}
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.False(t, safe.UnknownChannelInventory, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ChannelLeaderCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ChannelReplicaCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ChannelISRCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
}

func TestScaleInRuntimeSummaryFaultFailsClosed(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-runtime-summary-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, clusterNetCallShardFault)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, clusterNetCallShardFault, `return("manager_connections:`+scaleInRuntimeSummaryFaultMarker+`")`)
		requireGofailContains(t, f.cluster, endpoint, clusterNetCallShardFault, scaleInRuntimeSummaryFaultMarker)
		defer disableGofail(t, endpoint, clusterNetCallShardFault)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	drainStatus, drainBody, err := f.manager.SetScaleInDrainStatus(ctx, 4, true)
	cancel()
	requireScaleInInjectedRuntimeFault(t, drainStatus, drainBody, err, f.cluster.DumpDiagnostics())
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), clusterNetCallShardFault, 1)

	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if !status.UnknownRuntime && !status.RuntimeUnknown {
			return fmt.Errorf("runtime summary did not become unknown: %#v", status)
		}
		if !status.BlockedByRuntimeDrain {
			return fmt.Errorf("runtime drain blocker missing: %#v", status)
		}
		if status.SafeToRemove {
			return fmt.Errorf("safe_to_remove=true while runtime summary is unknown")
		}
		return nil
	})

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	removeStatus, removeBody, err := f.manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	requireScaleInRemoveConflict(t, removeStatus, removeBody, err, f.cluster.DumpDiagnostics())
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 5*time.Second)

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, clusterNetCallShardFault)
	}
	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.False(t, safe.UnknownRuntime || safe.RuntimeUnknown, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
}

func requireScaleInInjectedRuntimeFault(t testing.TB, status int, body []byte, err error, diagnostics string) {
	t.Helper()

	text := string(body)
	require.NoError(t, err, "status=%d body=%s\n%s", status, text, diagnostics)
	require.Equal(t, http.StatusInternalServerError, status, "body=%s\n%s", text, diagnostics)
	require.Contains(t, text, `"internal_error"`, "body=%s\n%s", text, diagnostics)
	require.Contains(t, text, scaleInRuntimeSummaryFaultMarker, "body=%s\n%s", text, diagnostics)
}
