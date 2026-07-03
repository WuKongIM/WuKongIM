//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInRemovePostCommitResponseLossIsIdempotent(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2e-gofail-remove-post-commit-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)
	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, markNodeRemovedPostCommitFault)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, markNodeRemovedPostCommitFault, `return("temporary mark removed post commit fault")`)
		defer disableGofail(t, endpoint, markNodeRemovedPostCommitFault)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	status, body, err := f.manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	if err == nil && status/100 == 2 {
		t.Fatalf("remove unexpectedly succeeded on post-commit response-loss request: status=%d body=%s\n%s", status, string(body), f.cluster.DumpDiagnostics())
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	require.True(t,
		strings.Contains(errText, "temporary mark removed post commit fault") ||
			strings.Contains(string(body), "temporary mark removed post commit fault"),
		"status=%d err=%v body=%s\n%s",
		status,
		err,
		string(body),
		f.cluster.DumpDiagnostics(),
	)
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), markNodeRemovedPostCommitFault, 1)

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, markNodeRemovedPostCommitFault)
	}

	f.manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
	second := f.manager.MustRemoveScaleInNode(t, 4)
	require.False(t, second.Changed, "second remove should be idempotent: %#v\n%s", second, f.cluster.DumpDiagnostics())
	require.Equal(t, "removed", second.JoinState)
	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if status.FailedTaskCount != 0 || status.ActiveTaskCount != 0 {
			return fmt.Errorf("unexpected task counters after post-commit response loss: %#v", status)
		}
		return nil
	})
}
