package capacity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestWriteActivateChannelsResultWritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	result := activateChannelsResultFixture(StatusPassed)
	result.ReportDir = dir

	require.NoError(t, WriteActivateChannelsResult(dir, result))

	reportPath := filepath.Join(dir, "activation_report.json")
	summaryPath := filepath.Join(dir, "summary.md")
	require.FileExists(t, reportPath)
	require.FileExists(t, summaryPath)

	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-token")
	var decoded ActivateChannelsResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, StatusPassed, decoded.Status)
	require.Equal(t, "activate-test", decoded.Config.RunID)
	require.Equal(t, 10000, decoded.Config.Channels)
	require.Equal(t, 1000, decoded.Config.Users)
	require.Equal(t, 10, decoded.Config.GroupMembers)
	require.Equal(t, 1000.0, decoded.Config.PrepareRatePerSecond)
	require.Equal(t, 500.0, decoded.Config.ConnectRatePerSecond)
	require.Equal(t, 512, decoded.Config.ActivationConcurrency)
	require.Equal(t, 120*time.Second, decoded.Config.ActivationWindow)
	require.Equal(t, 60*time.Second, decoded.Config.Hold)
	require.Equal(t, 1000, decoded.Config.ProbeBatchSize)
	require.False(t, decoded.Config.EvictAfter)
	require.Equal(t, uint64(10000), decoded.Evaluation.ActivationSuccess)

	summary, err := os.ReadFile(summaryPath)
	require.NoError(t, err)
	require.Contains(t, string(summary), "# wkbench activate-channels")
	require.Contains(t, string(summary), "- status: passed")
	require.Contains(t, string(summary), "- run_id: activate-test")
}

func TestActivateChannelsSummaryMarkdownIncludesFailureReasons(t *testing.T) {
	result := activateChannelsResultFixture(StatusFailed)
	result.Evaluation.Passed = false
	result.Evaluation.FailureReasons = []string{"activation_errors", "probe_missing_all_nodes"}
	result.Evaluation.ActivationErrors = 1
	result.Evaluation.ProbeMissingAllNodes = []string{"activate-groups-9"}

	got := ActivateChannelsSummaryMarkdown(result)

	require.Contains(t, got, "# wkbench activate-channels")
	require.Contains(t, got, "- status: failed")
	require.Contains(t, got, "- activation_success: 10000")
	require.Contains(t, got, "- activation_errors: 1")
	require.Contains(t, got, "- active_leader_total: 10000")
	require.Contains(t, got, "- probe_missing_all_nodes_count: 1")
	require.Contains(t, got, "- activation_errors")
	require.Contains(t, got, "- probe_missing_all_nodes")
	require.Contains(t, got, "activate-groups-9")
	require.Equal(t, ExitNoStableAttempt, result.ExitCode())
}

func TestActivateChannelsSummaryMarkdownIncludesActiveNodeDistribution(t *testing.T) {
	result := activateChannelsResultFixture(StatusFailed)
	result.Evaluation.Passed = false
	result.Evaluation.FailureReasons = []string{"active_leader_single_node"}
	result.Evaluation.ActiveLeaderNodeCount = 1
	result.Evaluation.ActiveLeaderMaxNodeID = 1
	result.Evaluation.ActiveLeaderMaxNodeShare = 1.0
	result.Evaluation.ActiveNodes = []ActivateChannelsNodeRuntime{
		{NodeID: 1, ActiveTotal: 7500, ActiveLeader: 7500},
		{NodeID: 2, ActiveTotal: 7500, ActiveFollower: 7500, FollowerParked: 7500},
		{NodeID: 3, ActiveTotal: 7500, ActiveFollower: 7500, FollowerParked: 7500},
	}

	got := ActivateChannelsSummaryMarkdown(result)

	require.Contains(t, got, "- active_leader_node_count: 1")
	require.Contains(t, got, "- active_leader_max_node: 1")
	require.Contains(t, got, "- active_leader_max_node_share: 1.000")
	require.Contains(t, got, "## Active Runtime Distribution")
	require.Contains(t, got, "| 1 | 7500 | 7500 | 0 | 0 |")
	require.Contains(t, got, "| 2 | 7500 | 0 | 7500 | 7500 |")
	require.Contains(t, got, "- active_leader_single_node")
}

func TestActivateChannelsSummaryMarkdownCapsProbeMissingSamples(t *testing.T) {
	result := activateChannelsResultFixture(StatusFailed)
	result.Evaluation.Passed = false
	result.Evaluation.FailureReasons = []string{"probe_missing_all_nodes"}
	result.Evaluation.ProbeMissingAllNodes = make([]string, 40)
	for i := range result.Evaluation.ProbeMissingAllNodes {
		result.Evaluation.ProbeMissingAllNodes[i] = "activate-groups-" + strconv.Itoa(i)
	}

	got := ActivateChannelsSummaryMarkdown(result)

	require.Contains(t, got, "- probe_missing_all_nodes_count: 40")
	require.Contains(t, got, "Showing first 32 of 40")
	require.Contains(t, got, "activate-groups-31")
	require.NotContains(t, got, "activate-groups-32")
}

func TestActivateChannelsConsoleSummary(t *testing.T) {
	passed := activateChannelsResultFixture(StatusPassed)
	passed.ReportDir = "/tmp/wkbench-activate"
	require.Equal(t, ExitSuccess, passed.ExitCode())

	got := ActivateChannelsConsoleSummary(passed)

	require.Contains(t, got, "wkbench activate-channels")
	require.Contains(t, got, "status: passed")
	require.Contains(t, got, "run_id: activate-test")
	require.Contains(t, got, "channels: 10000")
	require.Contains(t, got, "success=10000")
	require.Contains(t, got, "leader_nodes=1")
	require.Contains(t, got, "p99=30ms")
	require.Contains(t, got, "report: /tmp/wkbench-activate")

	require.Equal(t, ExitWorkerFailed, ActivateChannelsResult{Status: StatusFailed}.ExitCode())
}

func activateChannelsResultFixture(status Status) ActivateChannelsResult {
	cfg := DefaultActivateChannelsConfig()
	cfg.RunID = "activate-test"
	cfg.Channels = 10000
	cfg.Users = 1000
	cfg.GroupMembers = 10
	cfg.PrepareRatePerSecond = 1000
	cfg.ConnectRatePerSecond = 500
	cfg.ActivationConcurrency = 512
	cfg.ActivationWindow = 120 * time.Second
	cfg.Hold = 60 * time.Second
	cfg.ProbeBatchSize = 1000
	cfg.BenchToken = "secret-token"
	return ActivateChannelsResult{
		Status: status,
		Config: activateChannelsReportConfigFromConfig(cfg),
		Evaluation: ActivateChannelsEvaluation{
			Passed:                   status == StatusPassed,
			ActivationSuccess:        10000,
			ActivationErrors:         0,
			ActivationBacklog:        0,
			SendackP50:               10 * time.Millisecond,
			SendackP95:               20 * time.Millisecond,
			SendackP99:               30 * time.Millisecond,
			ActiveLeaderTotal:        10000,
			ActiveLeaderNodeCount:    1,
			ActiveLeaderMaxNodeID:    1,
			ActiveLeaderMaxNodeShare: 1.0,
			ActiveNodes: []ActivateChannelsNodeRuntime{{
				NodeID:       1,
				ActiveTotal:  10000,
				ActiveLeader: 10000,
			}},
			ActivationRejectedDelta: 0,
			ProbeMissingAllNodes:    nil,
		},
		Cold: []model.ChannelRuntimeSnapshot{{
			Version: "bench/v1",
			NodeID:  1,
		}},
		Active: []model.ChannelRuntimeSnapshot{{
			Version:      "bench/v1",
			NodeID:       1,
			ActiveLeader: 10000,
		}},
		ProbeBatches: [][]model.ChannelRuntimeProbeResult{{
			{Version: "bench/v1", NodeID: 1, Checked: 10000, LoadedLeader: 10000},
		}},
	}
}
