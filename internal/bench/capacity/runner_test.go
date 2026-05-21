package capacity

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/stretchr/testify/require"
)

func TestEvaluateAttemptClassifiesPassAndFailureReasons(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Duration = time.Second
	attempt := Attempt{Index: 0, OfferedQPS: 100}
	rep := report.Report{
		Summary: report.Summary{},
		Metrics: metrics.SnapshotData{
			Counters: map[string]uint64{
				"person_send_success_total{channel_type=person,phase=run,profile=p,traffic=t}": 100,
			},
			Histograms: map[string]metrics.HistogramSummary{
				"person_send_latency_seconds{channel_type=person,phase=run,profile=p,traffic=t}": {P50Seconds: 0.010, P95Seconds: 0.020, P99Seconds: 0.030},
			},
		},
	}

	got := EvaluateAttempt(cfg, attempt, rep)

	require.True(t, got.Passed)
	require.Equal(t, 100.0, got.ActualQPS)
	require.Equal(t, 30*time.Millisecond, got.SendackP99)

	cfg.StableP99 = time.Millisecond
	got = EvaluateAttempt(cfg, attempt, rep)
	require.False(t, got.Passed)
	require.Equal(t, "sendack_p99_exceeded", got.FailureReason)
}
