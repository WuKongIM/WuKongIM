package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestOpsMCPMetricsUseBoundedToolAndResultLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newOpsMCPMetrics(registry, prometheus.Labels{"node_id": "1", "node_name": "node-1"})

	metrics.CallStarted("logs_search")
	metrics.CallFinished("logs_search", "ok", 25*time.Millisecond)
	metrics.CallRejected("attacker-controlled-tool", "attacker-controlled-result")
	metrics.AuthenticationFailed()

	if got := testutil.ToFloat64(metrics.requests.WithLabelValues("logs_search", "ok")); got != 1 {
		t.Fatalf("logs_search requests = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.requests.WithLabelValues("unknown", "error")); got != 1 {
		t.Fatalf("normalized requests = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.active.WithLabelValues("logs_search")); got != 0 {
		t.Fatalf("active logs_search = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.authFailures); got != 1 {
		t.Fatalf("auth failures = %v, want 1", got)
	}
}
