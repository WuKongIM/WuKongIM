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
