package workload

import "github.com/WuKongIM/WuKongIM/internal/bench/metrics"

const (
	schedulerDropPendingWindowExpired   = "pending_window_expired"
	schedulerDropUnstartedWindowExpired = "unstarted_window_expired"
)

func recordSchedulerStats(registry *metrics.Registry, labels metrics.Labels, stats *scheduledMessageStats) {
	if registry == nil || stats == nil {
		return
	}
	registry.AddCounter("workload_scheduler_planned_total", labels, stats.Planned)
	registry.AddCounter("workload_scheduler_enqueued_total", labels, stats.Enqueued)
	registry.AddCounter("workload_scheduler_dispatched_total", labels, stats.Dispatched)
	registry.AddCounter("workload_scheduler_busy_key_stall_total", labels, stats.BusyKeyStalls)
	if stats.DroppedPendingWindowExpired > 0 {
		registry.AddCounter("workload_scheduler_dropped_total", schedulerDropLabels(labels, schedulerDropPendingWindowExpired), stats.DroppedPendingWindowExpired)
	}
	if stats.DroppedUnstartedWindowExpired > 0 {
		registry.AddCounter("workload_scheduler_dropped_total", schedulerDropLabels(labels, schedulerDropUnstartedWindowExpired), stats.DroppedUnstartedWindowExpired)
	}
	registry.SetGauge("workload_scheduler_max_pending", labels, float64(stats.MaxPendingDepth))
	registry.SetGauge("workload_scheduler_max_active", labels, float64(stats.MaxActive))
	registry.SetGauge("workload_scheduler_max_busy_keys", labels, float64(stats.MaxBusyKeys))
}

func schedulerDropLabels(labels metrics.Labels, reason string) metrics.Labels {
	out := make(metrics.Labels, len(labels)+1)
	for key, value := range labels {
		out[key] = value
	}
	out["reason"] = reason
	return out
}
