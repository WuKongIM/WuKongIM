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
