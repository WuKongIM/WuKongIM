package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadScenarioRequiresBenchAPIByTarget(t *testing.T) {
	target := Target{BenchAPI: BenchAPIConfig{Enabled: false}}
	scenario := Scenario{Version: "wkbench/v1", Run: RunConfig{ID: "bench-run"}}
	err := ValidateTargetScenario(target, scenario)
	require.ErrorContains(t, err, "bench_api.enabled")
}
