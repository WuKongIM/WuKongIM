//go:build e2e

package suite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSumMetricSamplesMatchesLabelSubset(t *testing.T) {
	samples := []MetricSample{
		{Name: "rows_total", Labels: map[string]string{"directory": "ordinary", "operation": "upsert"}, Value: 2},
		{Name: "rows_total", Labels: map[string]string{"directory": "ordinary", "operation": "activate"}, Value: 1},
		{Name: "rows_total", Labels: map[string]string{"directory": "cmd", "operation": "upsert"}, Value: 7},
		{Name: "other_total", Labels: map[string]string{"directory": "ordinary"}, Value: 9},
	}

	require.Equal(t, float64(3), SumMetricSamples(samples, "rows_total", map[string]string{"directory": "ordinary"}))
	require.Zero(t, SumMetricSamples(samples, "rows_total", map[string]string{"directory": "missing"}))
}

func TestHistogramSnapshotReturnsCountAndSum(t *testing.T) {
	samples := []MetricSample{
		{Name: "batch_calls_count", Labels: map[string]string{"result": "ok", "node_name": "n1"}, Value: 3},
		{Name: "batch_calls_sum", Labels: map[string]string{"result": "ok", "node_name": "n1"}, Value: 6},
		{Name: "batch_calls_count", Labels: map[string]string{"result": "error", "node_name": "n1"}, Value: 1},
		{Name: "batch_calls_sum", Labels: map[string]string{"result": "error", "node_name": "n1"}, Value: 2},
	}

	require.Equal(t, MetricHistogramSnapshot{Count: 3, Sum: 6}, HistogramSnapshot(samples, "batch_calls", map[string]string{"result": "ok"}))
}
