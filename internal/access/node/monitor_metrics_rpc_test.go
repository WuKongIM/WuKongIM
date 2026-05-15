package node

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/stretchr/testify/require"
)

func TestMonitorMetricsCodecRoundTrip(t *testing.T) {
	generatedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	resp := monitorMetricsResponse{
		Status: rpcStatusOK,
		Result: metrics.QueryResult{
			GeneratedAt:   generatedAt,
			WindowSeconds: 10,
			StepSeconds:   5,
			Points:        2,
			SendPerSec: metrics.MetricSeries{
				Latest: 4,
				Peak:   4,
				Avg:    3,
				Series: []float64{2, 4},
			},
			SendDelta: metrics.MetricSeries{
				Latest: 20,
				Peak:   20,
				Avg:    15,
				Series: []float64{10, 20},
			},
			SendFailDelta: metrics.MetricSeries{
				Latest: 2,
				Peak:   2,
				Avg:    1.5,
				Series: []float64{1, 2},
			},
		},
	}

	raw, err := encodeMonitorMetricsResponse(resp)
	require.NoError(t, err)
	got, err := decodeMonitorMetricsResponse(raw)

	require.NoError(t, err)
	require.Equal(t, resp.Status, got.Status)
	require.Equal(t, generatedAt, got.Result.GeneratedAt)
	require.Equal(t, []float64{2, 4}, got.Result.SendPerSec.Series)
	require.Equal(t, []float64{10, 20}, got.Result.SendDelta.Series)
	require.Equal(t, []float64{1, 2}, got.Result.SendFailDelta.Series)
}
