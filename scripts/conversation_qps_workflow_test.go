package scripts_test

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversationQPSBlocksBothPublishers(t *testing.T) {
	for _, name := range []string{"docker-image-publish.yml", "binary-release-publish.yml"} {
		t.Run(name, func(t *testing.T) {
			var w struct {
				Jobs map[string]struct {
					Needs       string            `yaml:"needs"`
					Uses        string            `yaml:"uses"`
					If          string            `yaml:"if"`
					With        map[string]string `yaml:"with"`
					Permissions map[string]string `yaml:"permissions"`
					Secrets     any               `yaml:"secrets"`
					Continue    bool              `yaml:"continue-on-error"`
				} `yaml:"jobs"`
			}
			raw := readWorkflow(t, name)
			require.NoError(t, yaml.Unmarshal(raw, &w))
			gate := w.Jobs["conversation-qps"]
			require.Equal(t, "./.github/workflows/conversation-qps-gate.yml", gate.Uses)
			require.Equal(t, map[string]string{"contents": "read"}, gate.Permissions)
			require.Equal(t, "${{ inputs.version || github.ref_name }}", gate.With["source_ref"])
			require.Nil(t, gate.Secrets)
			require.Equal(t, "conversation-qps", w.Jobs["publish"].Needs)
			require.Equal(t, "github.repository == 'WuKongIM/WuKongIM'", w.Jobs["publish"].If)
			require.False(t, gate.Continue)
			require.False(t, w.Jobs["publish"].Continue)
			require.Contains(t, string(raw), "QPS_SOURCE_SHA: ${{ needs.conversation-qps.outputs.source_sha }}")
			require.Contains(t, string(raw), `[[ "$(git rev-parse HEAD)" == "$QPS_SOURCE_SHA" ]]`)
		})
	}
}

func TestConversationQPSGateRequiresCompleteBoundEvidence(t *testing.T) {
	raw := readWorkflow(t, "conversation-qps-gate.yml")
	var w struct {
		On          map[string]any    `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			RunsOn      string            `yaml:"runs-on"`
			Timeout     int               `yaml:"timeout-minutes"`
			Environment string            `yaml:"environment"`
			Env         map[string]string `yaml:"env"`
			Steps       []struct {
				If   string         `yaml:"if"`
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &w))
	require.Len(t, w.On, 1)
	require.Contains(t, w.On, "workflow_call")
	require.Equal(t, map[string]string{"contents": "read"}, w.Permissions)
	require.Len(t, w.Jobs, 1)
	job := w.Jobs["verify"]
	require.Equal(t, "ubuntu-24.04", job.RunsOn)
	require.Equal(t, 24, job.Timeout)
	require.Empty(t, job.Environment)
	require.Equal(t, "1", job.Env["WK_E2E_CONVERSATION_QPS"])
	require.Equal(t, false, job.Steps[0].With["persist-credentials"])
	last := job.Steps[len(job.Steps)-1]
	require.Equal(t, "always()", last.If)
	require.Equal(t, "error", last.With["if-no-files-found"])
	for _, fragment := range []string{"unset WK_E2E_BINARY", "-run '^TestConversationQPSReleaseGate$' -count=1 -timeout=18m -p=1", "-f scripts/validate-conversation-qps-report.jq", "git status --porcelain", "set -euo pipefail"} {
		require.Contains(t, string(raw), fragment)
	}
	filter, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/validate-conversation-qps-report.jq"))
	require.NoError(t, err)
	for _, fragment := range []string{".source_sha == $sha", ".source_dirty == false", ".profile_sha256 == $profile_sha", ".passed == true", ".phases | length == 12", "unique | length == 12", ".errors == 0", ".dropped == 0", ".runtime_loads == 0", ".membership_writes == 0", ".actual_qps >=", ".p99_ms <=", ".stress_config == $profile[0].stress_gate", "expected_windows"} {
		require.Contains(t, string(filter), fragment)
	}

	for _, fragment := range []string{"secrets.", "continue-on-error", "workflow_dispatch:", "packages: write", "id-token: write"} {
		require.NotContains(t, string(raw), fragment)
	}
}

// Exercise the actual release receipt filter, including forged pass flags.
func TestConversationQPSReportFilterRejectsInvalidEvidence(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq required for Workflow expression contracts")
	}
	filterRaw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/validate-conversation-qps-report.jq"))
	require.NoError(t, err)
	filter := string(filterRaw)

	profilePath := filepath.Join(repoRoot(t), "test/e2e/message/conversation_qps/profile.json")
	data, err := os.ReadFile(profilePath)
	require.NoError(t, err)
	var p struct {
		Duration int              `json:"duration_seconds"`
		Workers  int              `json:"workers"`
		MaxP99MS float64          `json:"max_p99_ms"`
		Cases    []map[string]any `json:"cases"`
		Stress   map[string]any   `json:"stress_gate"`
	}
	require.NoError(t, json.Unmarshal(data, &p))
	phases := []map[string]any{}
	for _, nodes := range []int{1, 3} {
		for _, c := range p.Cases {
			qps := c["offered_qps"].(float64)
			count := qps * float64(p.Duration)
			phases = append(phases, map[string]any{"driver_workers": p.Workers, "queue_capacity": max(p.Workers, int(math.Ceil(qps*p.MaxP99MS/1000))), "nodes": nodes, "case": c, "verdict": "pass", "errors": 0, "unexpected_errors": 0, "dropped": 0, "runtime_loads": 0, "active_runtimes_before": 0, "active_runtimes_after": 0, "membership_writes": 0, "cpu_seconds": 1, "heap_bytes": 1024, "allocated_bytes": 4096, "p50_ms": 1, "p95_ms": 2, "duration_seconds": p.Duration, "scheduled": count, "completed": count, "completed_in_window": count, "actual_qps": qps, "p99_ms": 10})
		}
	}
	stress := syntheticStressWindows(t, p.Stress, p.MaxP99MS)
	base := map[string]any{"schema": "wukongim/conversation-qps-report/v2", "source_sha": "source", "source_dirty": false, "profile_sha256": "profile", "binary_sha256": strings.Repeat("a", 64), "os": "linux", "arch": "amd64", "node_gomaxprocs": 2, "passed": true, "phases": phases, "stress_config": p.Stress, "stress": stress}
	for _, name := range []string{"good", "missing_case", "duplicate_case", "wrong_sha", "dirty", "missing_cpu", "missing_heap", "warm_fixture", "allocation_regression", "missing_allocations", "unexpected_error", "oversized_queue", "wrong_workers", "slow", "errors", "empty", "missing_stress", "duplicate_hidden", "missing_mixed_endpoint", "wrong_stress_rate", "short_stress_window", "stress_error", "stress_allocation_regression", "duplicate_stress_cpu", "missing_stress_cpu", "missing_stress_activation_counter", "wrong_hidden_page"} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(base)
			require.NoError(t, err)
			var r map[string]any
			require.NoError(t, json.Unmarshal(encoded, &r))
			cases := r["phases"].([]any)
			first := cases[0].(map[string]any)
			switch name {
			case "missing_case":
				r["phases"] = cases[:11]
			case "duplicate_case":
				cases[11] = cases[0]
			case "wrong_sha":
				r["source_sha"] = "wrong"
			case "dirty":
				r["source_dirty"] = true
			case "missing_cpu":
				delete(first, "cpu_seconds")
			case "missing_heap":
				delete(first, "heap_bytes")
			case "warm_fixture":
				first["active_runtimes_before"] = 1
			case "allocation_regression":
				first["allocated_bytes"] = 1e15
			case "missing_allocations":
				delete(first, "allocated_bytes")
			case "unexpected_error":
				first["unexpected_errors"] = 1
			case "oversized_queue":
				first["queue_capacity"] = first["queue_capacity"].(float64) + 1
			case "wrong_workers":
				first["driver_workers"] = 99
			case "slow":
				first["p99_ms"] = 501
			case "errors":
				first["errors"] = 1
			case "empty":
				r = map[string]any{}
			case "missing_stress":
				delete(r, "stress")
			case "duplicate_hidden":
				r["stress"].([]any)[6] = r["stress"].([]any)[5]
			case "missing_mixed_endpoint":
				r["stress"].([]any)[0].(map[string]any)["phases"] = []any{}
			case "wrong_stress_rate":
				r["stress"].([]any)[0].(map[string]any)["phases"].([]any)[0].(map[string]any)["case"].(map[string]any)["offered_qps"] = 1
			case "short_stress_window":
				r["stress"].([]any)[0].(map[string]any)["end"] = "2026-09-13T00:00:01Z"
			case "stress_error":
				r["stress"].([]any)[0].(map[string]any)["phases"].([]any)[0].(map[string]any)["errors"] = 1
			case "stress_allocation_regression":
				r["stress"].([]any)[0].(map[string]any)["allocated_bytes"] = 1e20
			case "duplicate_stress_cpu":
				r["stress"].([]any)[0].(map[string]any)["phases"].([]any)[0].(map[string]any)["cpu_seconds"] = 1
			case "missing_stress_cpu":
				delete(r["stress"].([]any)[0].(map[string]any), "cpu_seconds")
			case "missing_stress_activation_counter":
				delete(r["stress"].([]any)[0].(map[string]any), "runtime_loads")
			case "wrong_hidden_page":
				r["stress"].([]any)[1].(map[string]any)["page"] = 2
			}
			encoded, err = json.Marshal(r)
			require.NoError(t, err)
			cmd := exec.Command(jq, "-e", "--arg", "sha", "source", "--arg", "profile_sha", "profile", "--slurpfile", "profile", profilePath, filter)
			cmd.Stdin = strings.NewReader(string(encoded))
			out, err := cmd.CombinedOutput()
			if name == "good" {
				require.NoError(t, err, string(out))
			} else {
				require.Error(t, err, string(out))
			}
		})
	}
}

// syntheticStressWindows exercises the actual release filter without processes.
func syntheticStressWindows(t *testing.T, cfg map[string]any, maxP99 float64) []map[string]any {
	t.Helper()
	require.NotEmpty(t, cfg)
	number := func(key string) int { value, ok := cfg[key].(float64); require.True(t, ok, key); return int(value) }
	phase := func(endpoint string, size, qps, seconds, workers int) map[string]any {
		count := qps * seconds
		return map[string]any{"driver_workers": workers, "queue_capacity": max(workers, int(math.Ceil(float64(qps)*maxP99/1000))), "nodes": 3, "case": map[string]any{"endpoint": endpoint, "page_size": size, "offered_qps": qps}, "scheduled": count, "completed": count, "completed_in_window": count, "actual_qps": qps, "duration_seconds": seconds, "verdict": "pass", "errors": 0, "unexpected_errors": 0, "dropped": 0, "runtime_loads": 0, "membership_writes": 0, "active_runtimes_before": 0, "active_runtimes_after": 0, "cpu_seconds": nil, "allocated_bytes": 0, "heap_bytes": 0, "p50_ms": 1, "p95_ms": 2, "p99_ms": 10}
	}
	start := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	window := func(name string, page, cohort, seconds int, phases []map[string]any) map[string]any {
		return map[string]any{"name": name, "page": page, "cohort": cohort, "start": start.Format(time.RFC3339), "end": start.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339), "cpu_seconds": 1, "allocated_bytes": 1024, "heap_bytes": 1024, "runtime_loads": 0, "membership_writes": 0, "active_runtimes_before": 0, "active_runtimes_after": 0, "phases": phases}
	}
	seconds, workers := number("mixed_seconds"), number("mixed_workers_per_endpoint")
	result := []map[string]any{window("mixed", 0, 0, seconds, []map[string]any{phase("/conversation/list", 100, number("mixed_list_qps"), seconds, workers), phase("/conversation/sync", 100, number("mixed_sync_qps"), seconds, workers)})}
	seconds, workers = number("hidden_seconds"), number("hidden_workers")
	for cohort, name := range []string{"hidden_50_percent", "hidden_90_percent", "hidden_after_99_visible"} {
		for _, page := range []int{1, 2} {
			size := 100
			if page == 2 {
				size = 50
			}
			result = append(result, window(name, page, cohort, seconds, []map[string]any{phase("/conversation/sync", size, number("hidden_qps"), seconds, workers)}))
		}
	}
	return result
}
