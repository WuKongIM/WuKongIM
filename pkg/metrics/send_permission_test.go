package metrics

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Fixed metric dimensions must not turn malformed identities into labels;
// byte/RPC counts and barrier time must stay separable from total SEND latency.
func TestSendPermissionMetricsHaveBoundedDimensions(t *testing.T) {
	reg := New(1, "n1")
	reg.Message.ObserveSendPermissionCount("node_envelopes", 2)
	reg.Message.ObserveSendPermissionCount("request_bytes", 120)
	reg.Message.ObserveSendPermissionCount("user-secret", 1)
	reg.Message.ObserveSendPermissionStage("barrier", "ok", time.Millisecond)
	reg.Message.ObserveSendPermissionStage("secret-channel", "secret-error", 0)
	reg.Message.ObserveSendPermissionInflight(1)
	reg.Message.ObserveSendPermissionInflight(-1)
	reg.Message.ObserveSendBanRejection("user", 3)
	families, err := reg.Gather()
	require.NoError(t, err)
	counts := requireMetricFamily(t, families, "wukongim_message_permission_counts_total")
	require.Equal(t, float64(2), findMetricByLabels(t, counts, map[string]string{"kind": "node_envelopes"}).GetCounter().GetValue())
	require.Len(t, counts.Metric, 3)
	durations := requireMetricFamily(t, families, "wukongim_message_permission_duration_seconds")
	require.EqualValues(t, 1, findMetricByLabels(t, durations, map[string]string{"stage": "barrier", "result": "ok"}).GetHistogram().GetSampleCount())
	require.NotNil(t, findMetricByLabels(t, durations, map[string]string{"stage": "unknown", "result": "unknown"}))
	require.EqualValues(t, 3, findMetricByLabels(t, requireMetricFamily(t, families, "wukongim_message_send_ban_rejections_total"), map[string]string{"scope": "user"}).GetCounter().GetValue())
}

// Ownership gauges must balance conservative memory credits and calls without
// admitting entity IDs as label values. Unknown dimensions are folded.
func TestSendPermissionCohortOwnershipMetrics(t *testing.T) {
	reg := New(1, "n1")
	reg.Message.ObserveSendPermissionCohortOwned("budget_bytes", 2048)
	reg.Message.ObserveSendPermissionCohortOwned("calls", 2)
	reg.Message.ObserveSendPermissionCohortOwned("cohorts", 1)
	reg.Message.ObserveSendPermissionCohortOwned("user-secret", 1)
	families, err := reg.Gather()
	require.NoError(t, err)
	owned := requireMetricFamily(t, families, "wukongim_message_permission_cohort_owned")
	require.Len(t, owned.Metric, 4)
	require.EqualValues(t, 2048, findMetricByLabels(t, owned, map[string]string{"kind": "budget_bytes"}).GetGauge().GetValue())
	reg.Message.ObserveSendPermissionCohortOwned("budget_bytes", -2048)
	reg.Message.ObserveSendPermissionCohortOwned("calls", -2)
	reg.Message.ObserveSendPermissionCohortOwned("cohorts", -1)
	families, err = reg.Gather()
	require.NoError(t, err)
	owned = requireMetricFamily(t, families, "wukongim_message_permission_cohort_owned")
	for _, kind := range []string{"budget_bytes", "calls", "cohorts"} {
		require.Zero(t, findMetricByLabels(t, owned, map[string]string{"kind": kind}).GetGauge().GetValue())
	}
}
