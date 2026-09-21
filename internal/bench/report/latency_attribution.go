package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
)

// writeLatencyAttribution exposes offered-load shortfalls separately from
// target failures. Diagnostic quantiles never participate in existing limits.
func writeLatencyAttribution(b *strings.Builder, snapshot metrics.SnapshotData) {
	planned := counterSumWithPhase(snapshot, "run", "workload_scheduler_planned_total")
	dispatched := counterSumWithPhase(snapshot, "run", "workload_scheduler_dispatched_total")
	dropped := counterSumWithPhase(snapshot, "run", "workload_scheduler_dropped_total")
	if planned > 0 {
		fmt.Fprintf(b, "\n## Measured scheduler\n\n- planned: %d\n- dispatched: %d\n- dropped_at_window_end: %d\n", planned, dispatched, dropped)
		if dispatched < planned {
			fmt.Fprintln(b, "\nOffered load was not fully dispatched. A passing configured-limit verdict does not certify the planned send rate; scheduler drops are not target message loss.")
		}
	}
	names := []string{"workload_dispatch_lag_seconds", "workload_send_submit_seconds", "workload_sendack_wait_seconds", "workload_operation_seconds"}
	var rows strings.Builder
	for _, name := range names {
		var count uint64
		var sum, p99 float64
		for key, h := range snapshot.Histograms {
			if metricName(key) != name || seriesLabels(key)["phase"] != "run" {
				continue
			}
			count += h.Count
			sum += h.SumSeconds
			p99 = max(p99, h.P99Seconds)
		}
		if count == 0 {
			continue
		}
		fmt.Fprintf(&rows, "| %s | %d | %s | %s |\n", name, count, time.Duration(sum/float64(count)*float64(time.Second)), time.Duration(p99*float64(time.Second)))
	}
	if rows.Len() > 0 {
		fmt.Fprintln(b, "\n## Client latency attribution\n\nFixed-bucket P99 upper bounds, maximum across worker/traffic series. Means use exact counts and sums. Missing stages are unobserved, not zero. Failed attempts are included; stage percentiles must not be added or subtracted.")
		fmt.Fprintln(b, "\nDispatch lag measures scheduled arrival to admission. SEND submission is the client API call, not a wire timestamp. SENDACK wait includes target/network and client matching. Operation time covers SEND through configured receive verification, including retries.")
		fmt.Fprintln(b, "\n| Stage | Observations | Mean | P99 upper bound |\n|---|---:|---:|---:|")
		b.WriteString(rows.String())
	}
}
