package report

import (
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/stretchr/testify/require"
)

func TestReportShowsUnderloadAndStagesWithoutChangingLegacyLimits(t *testing.T) {
	r := metrics.NewRegistry()
	run := metrics.Labels{"phase": "run"}
	r.AddCounter("workload_scheduler_planned_total", run, 100)
	r.AddCounter("workload_scheduler_dispatched_total", run, 97)
	r.AddCounter("workload_scheduler_dropped_total", metrics.Labels{"phase": "run", "reason": "pending_window_expired"}, 3)
	r.AddCounter("workload_scheduler_planned_total", metrics.Labels{"phase": "warmup"}, 1000)
	r.ObserveLatency("workload_send_submit_seconds", run, 3*time.Millisecond)
	r.ObserveLatency("workload_send_submit_seconds", run, 7*time.Millisecond)
	r.ObserveLatency("workload_send_submit_seconds", metrics.Labels{"phase": "warmup"}, time.Hour)
	r.ObserveLatency("group_send_latency_seconds", run, 20*time.Millisecond)
	snap := r.Collect()
	summary := SummaryFromMetrics(snap, 0)
	require.Equal(t, 20*time.Millisecond, summary.SendackMaxWorkerP99)
	text := summaryMarkdown(Report{Metrics: snap, Summary: summary})
	require.Contains(t, text, "- planned: 100\n")
	require.Contains(t, text, "- dispatched: 97\n")
	require.Contains(t, text, "- dropped_at_window_end: 3\n")
	require.Contains(t, text, "does not certify the planned send rate")
	require.Contains(t, text, "| workload_send_submit_seconds | 2 | 5ms | 7ms |")
	require.NotContains(t, text, "| workload_sendack_wait_seconds |")
	require.False(t, strings.Contains(summaryMarkdown(Report{}), "Client latency attribution"))
}
