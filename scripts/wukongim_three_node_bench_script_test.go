package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMThreeNode10kBenchScriptSetsEvidenceDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-10kch.sh"))

	for _, want := range []string{
		`CHANNELS="${WK_BENCH_ACTIVATE_CHANNELS:-10000}"`,
		`USERS="${WK_BENCH_ACTIVATE_USERS:-1000}"`,
		`CONNECT_RATE="${WK_BENCH_ACTIVATE_CONNECT_RATE:-500}"`,
		`ACTIVATION_WINDOW="${WK_BENCH_ACTIVATE_WINDOW:-120s}"`,
		`STABLE_P99="${WK_BENCH_ACTIVATE_STABLE_P99:-2s}"`,
		`WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("10k activate script missing default %q", want)
		}
	}
}

func TestWukongIMThreeNode1000ChannelBenchUsesReviewedRateLadder(t *testing.T) {
	t.Parallel()

	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-three-nodes-1000ch.sh"))
	for _, want := range []string{
		`QPS_LIST="${WK_BENCH_THREE_NODE_QPS:-250,500,750,1000}"`,
		`Default: 250,500,750,1000.`,
		`--qps 250,500,750,1000`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("three-node benchmark missing reviewed ladder contract %q", want)
		}
	}
}

func TestStorageMetricsSummaryReportsCommitAndPebbleEvidence(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	sample := filepath.Join(dir, "sample.prom")
	after := filepath.Join(dir, "after.prom")
	writeStorageMetricsFixture(t, before, storageMetricsFixture{
		queue: 2, batches: 10, requests: 40, records: 60, bytes: 6000,
		collectSum: .01, buildSum: .02, commitSum: .05, publishSum: .01, totalSum: .09,
		requestOKCount: 40, requestOKSum: .20, requestTimeoutCount: 1, requestTimeoutSum: .10,
		walIn: 1000, walWritten: 2000, flushBytes: 300, flushCount: 3,
		compactionRead: 400, compactionWritten: 500, compactionCount: 4,
		sstable: 10000, debt: 100, compactions: 1, readAmplification: 4, diskUsage: 100,
	})
	writeStorageMetricsFixture(t, sample, storageMetricsFixture{
		queue: 9, batches: 15, requests: 65, records: 110, bytes: 11000,
		collectSum: .02, buildSum: .035, commitSum: .09, publishSum: .02, totalSum: .165,
		requestOKCount: 65, requestOKSum: .35, requestTimeoutCount: 1, requestTimeoutSum: .10,
		walIn: 3000, walWritten: 6000, flushBytes: 600, flushCount: 5,
		compactionRead: 900, compactionWritten: 1200, compactionCount: 6,
		sstable: 12000, debt: 900, compactions: 3, readAmplification: 7, diskUsage: 130,
	})
	writeStorageMetricsFixture(t, after, storageMetricsFixture{
		queue: 0, batches: 20, requests: 90, records: 160, bytes: 16000,
		collectSum: .03, buildSum: .05, commitSum: .13, publishSum: .03, totalSum: .24,
		requestOKCount: 89, requestOKSum: .50, requestTimeoutCount: 2, requestTimeoutSum: .15,
		requestCanceledCount: 1, requestCanceledSum: .01, requestErrorCount: 1, requestErrorSum: .02,
		walIn: 5000, walWritten: 12000, flushBytes: 1000, flushCount: 7,
		compactionRead: 2000, compactionWritten: 3000, compactionCount: 9,
		sstable: 11000, debt: 500, compactions: 2, readAmplification: 6, diskUsage: 125,
	})

	command := exec.Command("awk", "-v", "tag=001000", "-v", "node=node-1", "-f",
		filepath.Join(root, "scripts", "storage-metrics-summary.awk"), before, sample, after)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("storage summary failed: %v\n%s", err, output)
	}
	want := "001000\tnode-1\tcomplete\t9\t10\t50\t100\t10000\t5.000000\t10.000000\t2.000000\t3.000000\t8.000000\t2.000000\t15.000000\t52\t7.307692\t49\t6.122449\t1\t50.000000\t1\t10.000000\t1\t20.000000\t52\t7.307692\t0\t0.000000\t0\t0.000000\t4000\t10000\t2.500000\t700\t4\t1600\t2500\t5\t12000\t900\t3\t7\t130\t1000.000000\t3.250000\t14.500000\t15.700000\t3.250000\t14.500000\t15.700000\t832.000000\t3712.000000\t4019.200000\n"
	if string(output) != want {
		t.Fatalf("storage summary:\n%s\nwant:\n%s", output, want)
	}
}

func TestStorageMetricsSummaryReportsBatchSizeDistributionForFixedNodes(t *testing.T) {
	root := repoRoot(t)
	headerOutput, err := exec.Command("awk", "-v", "header=1", "-f",
		filepath.Join(root, "scripts", "storage-metrics-summary.awk"), "/dev/null").CombinedOutput()
	if err != nil {
		t.Fatalf("storage summary header failed: %v\n%s", err, headerOutput)
	}
	header := strings.Split(strings.TrimSpace(string(headerOutput)), "\t")

	testdata := filepath.Join(root, "scripts", "testdata", "storage_metrics_summary")
	for _, test := range []struct {
		node string
		want map[string]string
	}{
		{
			node: "node-1",
			want: map[string]string{
				"evidence":                "complete",
				"commit_queue_depth_max":  "9",
				"physical_commits_delta":  "10",
				"logical_requests_delta":  "50",
				"records_delta":           "100",
				"commit_avg_ms":           "8.000000",
				"wal_bytes_in_delta":      "4000",
				"wal_bytes_written_delta": "10000",
				"flush_count_delta":       "4",
				"compaction_count_delta":  "5",
				"avg_bytes_per_commit":    "1000.000000",
				"requests_per_commit_p50": "3.333333",
				"requests_per_commit_p95": "12.000000",
				"requests_per_commit_p99": "15.200000",
				"records_per_commit_p50":  "10.000000",
				"records_per_commit_p95":  "52.000000",
				"records_per_commit_p99":  "61.600000",
				"bytes_per_commit_p50":    "3072.000000",
				"bytes_per_commit_p95":    "14336.000000",
				"bytes_per_commit_p99":    "15974.400000",
			},
		},
		{
			node: "node-2",
			want: map[string]string{
				"evidence":                "missing",
				"physical_commits_delta":  "4",
				"requests_per_commit_p50": "1.000000",
				"requests_per_commit_p95": "1.900000",
				"bytes_per_commit_p50":    "512.000000",
			},
		},
		{
			node: "node-3",
			want: map[string]string{
				"evidence":                "counter_reset",
				"physical_commits_delta":  "4",
				"requests_per_commit_p99": "1.980000",
				"records_per_commit_p99":  "15.760000",
			},
		},
	} {
		t.Run(test.node, func(t *testing.T) {
			arguments := []string{"-v", "tag=fixed", "-v", "node=" + test.node, "-f",
				filepath.Join(root, "scripts", "storage-metrics-summary.awk"),
				filepath.Join(testdata, test.node+"-before.prom"),
				filepath.Join(testdata, test.node+"-sample.prom"),
				filepath.Join(testdata, test.node+"-after.prom")}
			output, err := exec.Command("awk", arguments...).CombinedOutput()
			if err != nil {
				t.Fatalf("storage summary failed: %v\n%s", err, output)
			}
			fields := strings.Split(strings.TrimSpace(string(output)), "\t")
			if len(fields) != len(header) {
				t.Fatalf("storage summary fields = %d, header fields = %d\nheader=%s\nrow=%s", len(fields), len(header), headerOutput, output)
			}
			byName := make(map[string]string, len(header))
			for index, name := range header {
				byName[name] = fields[index]
			}
			if got := byName["node"]; got != test.node {
				t.Errorf("node = %q, want %q", got, test.node)
			}
			for name, want := range test.want {
				if got := byName[name]; got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestStorageMetricsSummaryFailsClosedOnMissingOrResetCounters(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "storage-metrics-summary.awk")
	base := storageMetricsFixture{
		queue: 1, batches: 10, requests: 20, records: 30, bytes: 3000,
		collectSum: .01, buildSum: .02, commitSum: .03, publishSum: .01, totalSum: .07,
		requestOKCount: 20, requestOKSum: .10,
		walIn: 1000, walWritten: 1500, flushBytes: 200, flushCount: 2,
		compactionRead: 300, compactionWritten: 400, compactionCount: 3,
		sstable: 5000, debt: 100, compactions: 1, readAmplification: 4, diskUsage: 6000,
	}
	after := base
	after.batches, after.requests, after.records, after.bytes = 20, 40, 60, 6000
	after.collectSum, after.buildSum, after.commitSum, after.publishSum, after.totalSum = .02, .04, .06, .02, .14
	after.requestOKCount, after.requestOKSum = 40, .20
	after.walIn, after.walWritten = 2000, 3000
	after.flushBytes, after.flushCount = 400, 4
	after.compactionRead, after.compactionWritten, after.compactionCount = 600, 800, 6

	t.Run("lazy commit series start at zero", func(t *testing.T) {
		dir := t.TempDir()
		beforePath, afterPath := filepath.Join(dir, "before.prom"), filepath.Join(dir, "after.prom")
		writeStorageMetricsFixture(t, beforePath, base)
		writeStorageMetricsFixture(t, afterPath, after)
		lines := strings.Split(readFile(t, beforePath), "\n")
		kept := lines[:0]
		for _, line := range lines {
			if strings.HasPrefix(line, "wukongim_storage_commit_") {
				continue
			}
			kept = append(kept, line)
		}
		if err := os.WriteFile(beforePath, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("awk", "-v", "tag=t", "-v", "node=n", "-f", script, beforePath, afterPath).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(strings.TrimSpace(string(output)), "\t")
		for index, want := range map[int]string{2: "complete", 4: "20", 5: "40", 6: "60", 7: "6000", 15: "40", 17: "40", 25: "40"} {
			if fields[index] != want {
				t.Fatalf("field[%d] = %q, want %q; output=%q", index, fields[index], want, output)
			}
		}
	})

	t.Run("optional failure series stay zero", func(t *testing.T) {
		dir := t.TempDir()
		beforePath, afterPath := filepath.Join(dir, "before.prom"), filepath.Join(dir, "after.prom")
		writeStorageMetricsFixture(t, beforePath, base)
		writeStorageMetricsFixture(t, afterPath, after)
		for _, path := range []string{beforePath, afterPath} {
			body := readFile(t, path)
			lines := strings.Split(body, "\n")
			kept := lines[:0]
			for _, line := range lines {
				if strings.Contains(line, `result="timeout"`) || strings.Contains(line, `result="canceled"`) || strings.Contains(line, `result="err"`) {
					continue
				}
				kept = append(kept, line)
			}
			if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		output, err := exec.Command("awk", "-v", "tag=t", "-v", "node=n", "-f", script, beforePath, afterPath).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(strings.TrimSpace(string(output)), "\t")
		if fields[2] != "complete" || fields[19] != "0" || fields[21] != "0" || fields[23] != "0" {
			t.Fatalf("optional failure evidence = %q", output)
		}
	})

	t.Run("required metric missing", func(t *testing.T) {
		dir := t.TempDir()
		beforePath, afterPath := filepath.Join(dir, "before.prom"), filepath.Join(dir, "after.prom")
		writeStorageMetricsFixture(t, beforePath, base)
		writeStorageMetricsFixture(t, afterPath, after)
		body := strings.ReplaceAll(readFile(t, afterPath), "wukongim_storage_pebble_wal_bytes_written{store=\"channel_log\"} 3000\n", "")
		if err := os.WriteFile(afterPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("awk", "-v", "tag=t", "-v", "node=n", "-f", script, beforePath, afterPath).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if fields := strings.Split(strings.TrimSpace(string(output)), "\t"); fields[2] != "missing" {
			t.Fatalf("missing evidence = %q", output)
		}
	})

	t.Run("wrong pebble store label", func(t *testing.T) {
		dir := t.TempDir()
		beforePath, afterPath := filepath.Join(dir, "before.prom"), filepath.Join(dir, "after.prom")
		writeStorageMetricsFixture(t, beforePath, base)
		writeStorageMetricsFixture(t, afterPath, after)
		for _, path := range []string{beforePath, afterPath} {
			body := strings.ReplaceAll(readFile(t, path), `store="channel_log"`, `store="message"`)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		output, err := exec.Command("awk", "-v", "tag=t", "-v", "node=n", "-f", script, beforePath, afterPath).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if fields := strings.Split(strings.TrimSpace(string(output)), "\t"); fields[2] != "missing" {
			t.Fatalf("wrong store evidence = %q", output)
		}
	})

	t.Run("unknown request lane", func(t *testing.T) {
		dir := t.TempDir()
		beforePath, afterPath := filepath.Join(dir, "before.prom"), filepath.Join(dir, "after.prom")
		writeStorageMetricsFixture(t, beforePath, base)
		writeStorageMetricsFixture(t, afterPath, after)
		for _, path := range []string{beforePath, afterPath} {
			body := strings.ReplaceAll(readFile(t, path), `lane="leader_append"`, `lane="unknown"`)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		output, err := exec.Command("awk", "-v", "tag=t", "-v", "node=n", "-f", script, beforePath, afterPath).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if fields := strings.Split(strings.TrimSpace(string(output)), "\t"); fields[2] != "missing" {
			t.Fatalf("unknown lane evidence = %q", output)
		}
	})

	t.Run("counter reset", func(t *testing.T) {
		dir := t.TempDir()
		beforePath, afterPath := filepath.Join(dir, "before.prom"), filepath.Join(dir, "after.prom")
		writeStorageMetricsFixture(t, beforePath, base)
		reset := after
		reset.walIn = 10
		writeStorageMetricsFixture(t, afterPath, reset)
		output, err := exec.Command("awk", "-v", "tag=t", "-v", "node=n", "-f", script, beforePath, afterPath).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if fields := strings.Split(strings.TrimSpace(string(output)), "\t"); fields[2] != "counter_reset" {
			t.Fatalf("reset evidence = %q", output)
		}
	})
}

func TestWukongIMBenchScriptsPublishStorageEvidence(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		"scripts/bench-wukongim-single-node-1000ch.sh",
		"scripts/bench-wukongim-three-nodes-1000ch.sh",
	} {
		t.Run(path, func(t *testing.T) {
			script := readFile(t, filepath.Join(root, path))
			for _, want := range []string{
				"storage_metrics_summary()",
				`storage-metrics-summary.awk`,
				`storage_metrics_summary.tsv`,
				`storage_metrics_summary "$tag"`,
				`-v header=1`,
				`evidence`,
			} {
				if !strings.Contains(script, want) {
					t.Fatalf("benchmark script missing storage evidence contract %q", want)
				}
			}
		})
	}
}

type storageMetricsFixture struct {
	queue, batches, requests, records, bytes                                     float64
	collectSum, buildSum, commitSum, publishSum, totalSum                        float64
	requestOKCount, requestOKSum, requestTimeoutCount, requestTimeoutSum         float64
	requestCanceledCount, requestCanceledSum, requestErrorCount, requestErrorSum float64
	walIn, walWritten, flushBytes, flushCount                                    float64
	compactionRead, compactionWritten, compactionCount                           float64
	sstable, debt, compactions, readAmplification, diskUsage                     float64
}

func writeStorageMetricsFixture(t *testing.T, path string, f storageMetricsFixture) {
	t.Helper()
	body := strings.TrimSpace(fmt.Sprintf(`
wukongim_storage_commit_queue_depth{store="message"} %g
wukongim_storage_commit_batch_requests_count{store="message"} %g
wukongim_storage_commit_batch_requests_sum{store="message"} %g
wukongim_storage_commit_batch_requests_bucket{store="message",le="1"} %g
wukongim_storage_commit_batch_requests_bucket{store="message",le="4"} %g
wukongim_storage_commit_batch_requests_bucket{store="message",le="16"} %g
wukongim_storage_commit_batch_requests_bucket{store="message",le="+Inf"} %g
wukongim_storage_commit_batch_records_sum{store="message"} %g
wukongim_storage_commit_batch_records_bucket{store="message",le="1"} %g
wukongim_storage_commit_batch_records_bucket{store="message",le="4"} %g
wukongim_storage_commit_batch_records_bucket{store="message",le="16"} %g
wukongim_storage_commit_batch_records_bucket{store="message",le="+Inf"} %g
wukongim_storage_commit_batch_bytes_sum{store="message"} %g
wukongim_storage_commit_batch_bytes_bucket{store="message",le="256"} %g
wukongim_storage_commit_batch_bytes_bucket{store="message",le="1024"} %g
wukongim_storage_commit_batch_bytes_bucket{store="message",le="4096"} %g
wukongim_storage_commit_batch_bytes_bucket{store="message",le="+Inf"} %g
wukongim_storage_commit_batch_duration_seconds_count{store="message",stage="collect",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_sum{store="message",stage="collect",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_count{store="message",stage="build",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_sum{store="message",stage="build",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_count{store="message",stage="commit",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_sum{store="message",stage="commit",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_count{store="message",stage="publish",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_sum{store="message",stage="publish",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_count{store="message",stage="total",result="ok"} %g
wukongim_storage_commit_batch_duration_seconds_sum{store="message",stage="total",result="ok"} %g
wukongim_storage_commit_request_duration_seconds_count{store="message",lane="leader_append",result="ok"} %g
wukongim_storage_commit_request_duration_seconds_sum{store="message",lane="leader_append",result="ok"} %g
wukongim_storage_commit_request_duration_seconds_count{store="message",lane="leader_append",result="timeout"} %g
wukongim_storage_commit_request_duration_seconds_sum{store="message",lane="leader_append",result="timeout"} %g
wukongim_storage_commit_request_duration_seconds_count{store="message",lane="leader_append",result="canceled"} %g
wukongim_storage_commit_request_duration_seconds_sum{store="message",lane="leader_append",result="canceled"} %g
wukongim_storage_commit_request_duration_seconds_count{store="message",lane="leader_append",result="err"} %g
wukongim_storage_commit_request_duration_seconds_sum{store="message",lane="leader_append",result="err"} %g
wukongim_storage_pebble_wal_bytes_in{store="channel_log"} %g
wukongim_storage_pebble_wal_bytes_written{store="channel_log"} %g
wukongim_storage_pebble_flush_bytes_written{store="channel_log"} %g
wukongim_storage_pebble_flush_count{store="channel_log"} %g
wukongim_storage_pebble_compaction_bytes_read{store="channel_log"} %g
wukongim_storage_pebble_compaction_bytes_written{store="channel_log"} %g
wukongim_storage_pebble_compaction_count{store="channel_log"} %g
wukongim_storage_pebble_sstable_size_bytes{store="channel_log"} %g
wukongim_storage_pebble_compaction_estimated_debt_bytes{store="channel_log"} %g
wukongim_storage_pebble_compactions_in_progress{store="channel_log"} %g
wukongim_storage_pebble_read_amplification{store="channel_log"} %g
wukongim_storage_pebble_disk_usage_bytes{store="channel_log"} %g`,
		f.queue, f.batches, f.requests,
		f.batches*.2, f.batches*.6, f.batches, f.batches,
		f.records, f.batches*.2, f.batches*.6, f.batches, f.batches,
		f.bytes, f.batches*.2, f.batches*.6, f.batches, f.batches,
		f.batches, f.collectSum, f.batches, f.buildSum, f.batches, f.commitSum,
		f.batches, f.publishSum, f.batches, f.totalSum,
		f.requestOKCount, f.requestOKSum, f.requestTimeoutCount, f.requestTimeoutSum,
		f.requestCanceledCount, f.requestCanceledSum, f.requestErrorCount, f.requestErrorSum,
		f.walIn, f.walWritten, f.flushBytes, f.flushCount,
		f.compactionRead, f.compactionWritten, f.compactionCount,
		f.sstable, f.debt, f.compactions, f.readAmplification, f.diskUsage)) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWukongIMThreeNodeBenchScriptKeepsSamplersAsChildProcesses(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-1000ch.sh"))
	for _, forbidden := range []string{
		`$(start_runtime_pool_sampler`,
		`$(start_run_pprof_sampler`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("sampler started through command substitution cannot be waited by the parent shell: %s", forbidden)
		}
	}
	for _, want := range []string{
		`RUNTIME_POOL_SAMPLER_PID="$!"`,
		`RUN_PPROF_PID="$!"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bench script missing parent-owned sampler pid %q", want)
		}
	}
}

func TestWukongIMBenchDefaultEvidenceAssertionsUsePrimaryChannelSummary(t *testing.T) {
	root := repoRoot(t)
	source := readFile(t, filepath.Join(root, "scripts", "wukongim_three_node_bench_script_test.go")) +
		readFile(t, filepath.Join(root, "scripts", "wukongim_three_node_bench_script_integration_test.go"))
	legacySummary := "channelv2" + "_metrics_summary.tsv"
	for _, testName := range []string{
		"TestWukongIMThreeNodeBenchScriptCollectsLocalEvidence",
		"TestWukongIMThreeNodeRealQPSScriptUses15KTunedDefaults",
	} {
		start := strings.Index(source, "func "+testName)
		if start < 0 {
			t.Fatalf("test %s not found", testName)
		}
		end := strings.Index(source[start+1:], "\nfunc ")
		body := source[start:]
		if end >= 0 {
			body = source[start : start+1+end]
		}
		if strings.Contains(body, legacySummary) {
			t.Fatalf("%s should assert channel_metrics_summary.tsv as the default; legacy alias belongs in dedicated compatibility tests", testName)
		}
	}
}

func TestWukongIMBenchScriptsKeepChannelSummaryLegacyAliasCompatibility(t *testing.T) {
	root := repoRoot(t)
	legacySummary := "channelv2" + "_metrics_summary.tsv"
	for _, scriptPath := range []string{
		"scripts/bench-wukongim-three-nodes-1000ch.sh",
		"scripts/bench-wukongim-single-node-1000ch.sh",
	} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readFile(t, filepath.Join(root, scriptPath))
			for _, want := range []string{
				`local out="$OUT_DIR/channel_metrics_summary.tsv"`,
				`local legacy_out="$OUT_DIR/` + legacySummary + `"`,
				`cp "$out" "$legacy_out"`,
				`cp "$OUT_DIR/channel_metrics_summary.tsv" "$OUT_DIR/` + legacySummary + `"`,
			} {
				if !strings.Contains(script, want) {
					t.Fatalf("bench script missing channel summary legacy alias compatibility %q", want)
				}
			}
		})
	}
}

func TestWukongIMBenchScriptsStopOnlyExactWorkerAssignment(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		path        string
		cleanupCall string
	}{
		{path: "scripts/bench-wukongim-three-nodes-1000ch.sh", cleanupCall: `stop_worker_exact_from_status "before qps=$qps"`},
		{path: "scripts/bench-wukongim-single-node-1000ch.sh", cleanupCall: `stop_worker_exact_from_status "before qps=$qps"`},
		{path: "scripts/bench-wukongim-delivery.sh", cleanupCall: `stop_worker_exact_from_status "script cleanup"`},
		{path: "scripts/bench-wukongim-three-nodes-presence.sh", cleanupCall: `stop_worker_exact_from_status "script cleanup"`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			script := readFile(t, filepath.Join(root, tc.path))
			for _, want := range []string{
				"stop_worker_exact_from_status()",
				`.assignment.assignment_id // ""`,
				`{run_id:$run_id,assignment_id:$assignment_id}`,
				`--data "$payload" "${WORKER_ADDR%/}/v1/stop"`,
				`.phase == "stopped"`,
				tc.cleanupCall,
			} {
				if !strings.Contains(script, want) {
					t.Fatalf("bench script missing exact worker cleanup contract %q", want)
				}
			}
			if strings.Contains(script, `curl -fsS -X POST "${WORKER_ADDR%/}/v1/stop"`) {
				t.Fatal("bench script must not fall back to an empty or run-only worker stop")
			}
		})
	}
}

func TestWukongIMThreeNodeRealQPSScriptUses15KTunedDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-real-qps.sh"))

	for _, want := range []string{
		`STABLE_P99="${WK_BENCH_STABLE_P99:-600ms}"`,
		`CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"`,
		`CLUSTER_CHANNEL_REACTOR_COUNT=${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}`,
		`ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"`,
		`RECV_ACK="${WK_BENCH_RECV_ACK:-true}"`,
		`HEARTBEAT_ENABLED="${WK_BENCH_HEARTBEAT_ENABLED:-true}"`,
		"--concurrency N            wkbench send concurrency. Default: 2800.",
		"--ack-timeout DURATION     Per-SEND sendack wait timeout. Default: 15s.",
		`--phase-poll-timeout "$PHASE_POLL_TIMEOUT"`,
		"--recv-ack BOOL            Whether group recv frames are acknowledged. Default: true.",
		"--heartbeat BOOL           Whether benchmark clients send heartbeat pings. Default: true.",
		`--recv-ack "$RECV_ACK"`,
		`--heartbeat "$HEARTBEAT_ENABLED"`,
		`TOP_API_ENABLE=${WK_TOP_API_ENABLE:-false}`,
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}`,
		`CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-4}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}`,
		`CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-160}`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-160}"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY="${WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY:-100}"`,
		`WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY`,
		`WK_TOP_API_ENABLE="${WK_TOP_API_ENABLE:-false}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SHARDS="${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-4}"`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}`,
		`GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}`,
		`GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}`,
		"runtime_pool_attempt_summary",
		"channel_metrics_summary.tsv",
		"runtime_pool_queue_fill_max",
		"runtime_pool_admission_busy_delta",
		"write_ants_pool_usage_summary",
		"# ants pool usage",
		"ants_pool_usage_summary.tsv",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("real-qps script missing high-concurrency default %q", want)
		}
	}
}

func TestSingleNodeBenchRuntimeSamplerRemainsParentOwned(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	if strings.Contains(script, `sampler_pid="$(start_runtime_pool_sampler "$tag")"`) {
		t.Fatal("runtime sampler must not start in command substitution because the parent cannot wait for the orphaned child")
	}
	for _, want := range []string{
		`RUNTIME_POOL_SAMPLER_PID="$!"`,
		`start_runtime_pool_sampler "$tag"`,
		`stop_runtime_pool_sampler`,
		`declare -F stop_runtime_pool_sampler`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node runtime sampler missing parent-owned lifecycle %q", want)
		}
	}
}

func TestSingleNodeBenchUsesReviewedLocalThroughputBaseline(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		`QPS_LIST="${WK_BENCH_SINGLE_NODE_QPS:-250,500,750,1000}"`,
		`USERS="${WK_BENCH_USERS:-2500}"`,
		`CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"`,
		`DURATION="${WK_BENCH_DURATION:-5m}"`,
		`WARMUP="${WK_BENCH_WARMUP:-60s}"`,
		`COOLDOWN="${WK_BENCH_COOLDOWN:-90s}"`,
		`env -i`,
		`WK_CLUSTER_INITIAL_SLOT_COUNT=12`,
		`WK_CLUSTER_HASH_SLOT_COUNT=256`,
		`WK_CLUSTER_SLOT_REPLICA_N=1`,
		`WK_CLUSTER_CHANNEL_REPLICA_N=1`,
		`WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=500us`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=1`,
		`MINIMUM_FREE_PERCENT="${WK_BENCH_MINIMUM_FREE_PERCENT:-10}"`,
		`storage_confounded`,
		`host_confounded`,
		`local-baseline.json`,
		`online_users=$USERS`,
		`send_concurrency=$CONCURRENCY`,
		`logical_slot_groups=12`,
		`hash_slots=256`,
		`objectives:`,
		`scale: small`,
		`ingress_qps: ${qps}/s`,
		`online_fanout_qps: ${fanout}/s`,
		`workload_scheduler_planned_total`,
		`scheduler_accounting`,
		`start_host_metrics`,
		`host-io-summary.awk`,
		`host_io_summary.tsv`,
		`write_local_artifact_checksums`,
		`authorizes_three_node_diagnostic`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node baseline missing reviewed contract %q", want)
		}
	}
	if strings.Contains(script, `scale: local-diagnostic`) {
		t.Fatal("single-node baseline must use a wkbench-supported objective scale")
	}
}

func TestWukongIMDeliveryBenchScriptIsLocalThreeNodeOnly(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-delivery.sh"))

	for _, want := range []string{
		"scripts/start-wukongim-three-nodes.sh",
		`SCENARIO="${WK_BENCH_DELIVERY_SCENARIO:-group}"`,
		`DELIVERY_ENABLE="${WK_DELIVERY_ENABLE:-true}"`,
		`DELIVERY_EVENT_QUEUE_SIZE="${WK_DELIVERY_EVENT_QUEUE_SIZE:-1024}"`,
		`DELIVERY_FANOUT_PAGE_SIZE="${WK_DELIVERY_FANOUT_PAGE_SIZE:-512}"`,
		`DELIVERY_PUSH_BATCH_SIZE="${WK_DELIVERY_PUSH_BATCH_SIZE:-512}"`,
		`DELIVERY_PENDING_ACK_MAX_PER_SESSION="${WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION:-1024}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_DELIVERY_PHASE_POLL_TIMEOUT:-120s}"`,
		"write_delivery_summary",
		"delivery-summary.tsv",
		"wukongim_delivery_event_queue_total",
		"wukongim_delivery_retry_total",
		"wukongim_delivery_ack_bindings",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("delivery bench script missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"bench-wukongim-three-nodes-real-qps.sh",
		"docker compose",
		"dev-sim",
		"--start-script",
		"--api LIST",
		"--gateway LIST",
		"--metrics LIST",
		"WK_BENCH_API_ADDRS",
		"WK_BENCH_GATEWAY_ADDRS",
		"WK_BENCH_METRICS_ADDRS",
		"WK_BENCH_THREE_NODE_START_SCRIPT",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("delivery bench script should not contain %q", forbidden)
		}
	}
}

func TestWukongIMDeliveryBenchScriptInjectsDeliveryEnvWhenStartingCluster(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-delivery.sh"))
	for _, want := range []string{
		`WK_DELIVERY_ENABLE="$DELIVERY_ENABLE"`,
		`WK_DELIVERY_EVENT_QUEUE_SIZE="$DELIVERY_EVENT_QUEUE_SIZE"`,
		`WK_DELIVERY_FANOUT_PAGE_SIZE="$DELIVERY_FANOUT_PAGE_SIZE"`,
		`WK_DELIVERY_PUSH_BATCH_SIZE="$DELIVERY_PUSH_BATCH_SIZE"`,
		`WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION="$DELIVERY_PENDING_ACK_MAX_PER_SESSION"`,
		`WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("delivery script missing start env %q", want)
		}
	}
}

func TestWukongIMThreeNodePresenceScriptSetsPresenceDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-presence.sh"))

	for _, want := range []string{
		`USERS="${WK_BENCH_PRESENCE_USERS:-1000}"`,
		`CONNECT_RATE="${WK_BENCH_PRESENCE_CONNECT_RATE:-500}"`,
		`HEARTBEAT_INTERVAL="${WK_BENCH_PRESENCE_HEARTBEAT_INTERVAL:-1s}"`,
		`SAMPLE_INTERVAL="${WK_BENCH_PRESENCE_SAMPLE_INTERVAL:-1}"`,
		`STABLE_SAMPLES="${WK_BENCH_PRESENCE_STABLE_SAMPLES:-2}"`,
		`CLEANUP_TIMEOUT="${WK_BENCH_PRESENCE_CLEANUP_TIMEOUT:-0}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_PRESENCE_PHASE_POLL_TIMEOUT:-30s}"`,
		`REQUIRE_TOUCH="${WK_BENCH_PRESENCE_REQUIRE_TOUCH:-1}"`,
		`SOURCE_IPS="${WK_BENCH_PRESENCE_SOURCE_IPS:-}"`,
		`SOURCE_PORT_MIN="${WK_BENCH_PRESENCE_SOURCE_PORT_MIN:-}"`,
		`SOURCE_PORT_MAX="${WK_BENCH_PRESENCE_SOURCE_PORT_MAX:-}"`,
		`--source-ips`,
		`--source-port-min`,
		`--source-port-max`,
		`validate_presence_report`,
		`wait_for_presence_cleanup`,
		`cleanup_zero_status`,
		`presence-samples.jsonl`,
		`presence-summary.tsv`,
		`METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"`,
		`RESOURCE_SAMPLE_INTERVAL="${WK_BENCH_RESOURCE_SAMPLE_INTERVAL:-1}"`,
		`collect_node_logs`,
		`capture_node_pprof`,
		`sample_server_resources`,
		`server-process-summary.tsv`,
		`"$ROOT_DIR/pkg/protocol"`,
		`WK_CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-96}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("presence bench script missing %q", want)
		}
	}
	if strings.Contains(script, "docker compose") {
		t.Fatalf("presence bench script should use local startup scripts, not docker compose")
	}
}

func TestChannelRuntimeMetricsSummaryAwkSummarizesBeforeAfterPrometheus(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `# HELP ignored ignored
wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="0",role="leader"} 5
wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="1",role="follower"} 7
wukongim_channelv2_follower_parked{node_id="1",node_name="node-1",reactor_id="0"} 2
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="0",priority="normal"} 3
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="1",priority="normal"} 6
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="store_append"} 2
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="rpc"} 4
wukongim_channelv2_activation_rejected_total{node_id="1",node_name="node-1",reason="max_channels"} 1
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="submitted"} 10
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="ok"} 4
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="err"} 1
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="false"} 20
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="true"} 8
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="err",empty="false"} 2
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="ok"} 6
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="err"} 1
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="hit"} 100
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="miss"} 3
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="invalidate"} 1
wukongim_channelv2_append_duration_seconds_count{node_id="1",node_name="node-1",commit_mode="local"} 20
wukongim_channelv2_append_duration_seconds_sum{node_id="1",node_name="node-1",commit_mode="local"} 0.100
wukongim_channelv2_append_batch_records_count{node_id="1",node_name="node-1"} 5
wukongim_channelv2_append_batch_records_sum{node_id="1",node_name="node-1"} 25
wukongim_channelv2_append_batch_bytes_count{node_id="1",node_name="node-1"} 5
wukongim_channelv2_append_batch_bytes_sum{node_id="1",node_name="node-1"} 500
wukongim_channelv2_append_batch_wait_duration_seconds_count{node_id="1",node_name="node-1"} 5
wukongim_channelv2_append_batch_wait_duration_seconds_sum{node_id="1",node_name="node-1"} 0.015
wukongim_channelv2_worker_task_duration_seconds_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 40
wukongim_channelv2_worker_task_duration_seconds_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 0.200
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 2
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 8
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 1
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 3
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 6
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 12
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 4
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 10
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 2
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="scheduler",queue="scheduler",priority="rpc",result="busy"} 1
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="dirty"} 4
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="requeued"} 0
`)
	writeFile(t, after, `wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="0",role="leader"} 8
wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="1",role="follower"} 9
wukongim_channelv2_follower_parked{node_id="1",node_name="node-1",reactor_id="0"} 4
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="0",priority="normal"} 12
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="1",priority="normal"} 6
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="store_append"} 6
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="rpc"} 5
wukongim_channelv2_activation_rejected_total{node_id="1",node_name="node-1",reason="max_channels"} 3
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="submitted"} 15
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="ok"} 9
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="err"} 2
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="false"} 35
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="true"} 10
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="err",empty="false"} 5
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="ok"} 16
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="err"} 2
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="hit"} 160
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="miss"} 13
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="invalidate"} 4
wukongim_channelv2_append_duration_seconds_count{node_id="1",node_name="node-1",commit_mode="local"} 30
wukongim_channelv2_append_duration_seconds_sum{node_id="1",node_name="node-1",commit_mode="local"} 0.160
wukongim_channelv2_append_batch_records_count{node_id="1",node_name="node-1"} 8
wukongim_channelv2_append_batch_records_sum{node_id="1",node_name="node-1"} 37
wukongim_channelv2_append_batch_bytes_count{node_id="1",node_name="node-1"} 8
wukongim_channelv2_append_batch_bytes_sum{node_id="1",node_name="node-1"} 1100
wukongim_channelv2_append_batch_wait_duration_seconds_count{node_id="1",node_name="node-1"} 8
wukongim_channelv2_append_batch_wait_duration_seconds_sum{node_id="1",node_name="node-1"} 0.027
wukongim_channelv2_worker_task_duration_seconds_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 55
wukongim_channelv2_worker_task_duration_seconds_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 0.320
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 6
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 26
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 3
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 11
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 16
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 58
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 9
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 31
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 7
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 10
wukongim_runtime_pool_queue_bytes{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 200
wukongim_runtime_pool_queue_bytes_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 400
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="channelv2",pool="reactor_0",queue="mailbox",priority="high"} 3
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="channelv2",pool="reactor_0",queue="mailbox",priority="high"} 6
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="gateway",pool="async_auth"} 8
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="gateway",pool="async_auth"} 16
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="transportv2",pool="service_9"} 3
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="transportv2",pool="service_9"} 4
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 5
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="scheduler",queue="scheduler",priority="rpc",result="busy"} 3
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="dirty"} 5
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="requeued"} 5
`)

	cmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=10",
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
		before,
		after,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("summary awk failed: %v\n%s", err, output)
	}

	want := "qps_1000\tnode1\t17\t8\t9\t4\t12\t6\t7\t0.700\t200\t0.500\t8\t0.750\t3\t2\t1\t5\t2\t5\t5\t1\t15\t2\t3\t10\t1\t1.100\t60\t10\t3\t10\t6.000\t3\t4.000\t200.000\t4.000\t15\t8.000\t4\t18\t4.500\t2\t8\t4.000\t10\t46\t4.600\t5\t21\t4.200\n"
	if string(output) != want {
		t.Fatalf("unexpected summary row:\nwant %q\n got %q", want, output)
	}
}

func TestChannelRuntimeMetricsSummaryAwkTestsUsePromotedEntrypointByDefault(t *testing.T) {
	root := repoRoot(t)
	source := readFile(t, filepath.Join(root, "scripts", "wukongim_three_node_bench_script_test.go"))
	legacyEntrypoint := "channelv2" + "-metrics-summary.awk"
	if strings.Contains(source, legacyEntrypoint) {
		t.Fatalf("channel runtime summary tests should use scripts/channel-metrics-summary.awk by default")
	}
}

func TestChannelRuntimeMetricsSummaryAwkAcceptsPromotedPrometheusNamesAndDedupesLegacyAliases(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	writeFile(t, before, `wukongim_channel_active_runtimes{role="leader"} 1
wukongim_channel_active_runtimes{role="follower"} 1
wukongim_channelv2_active_runtimes{role="leader"} 1
wukongim_channel_activation_rejected_total{reason="max_channels"} 1
wukongim_channel_recovery_probe_total{result="submitted"} 1
wukongim_channel_pull_total{result="ok",empty="false"} 1
wukongim_channel_rpc_pull_total{result="ok"} 1
wukongim_channel_meta_cache_total{result="hit"} 1
wukongim_channel_append_duration_seconds_count{commit_mode="local"} 10
wukongim_channelv2_append_duration_seconds_count{commit_mode="local"} 10
wukongim_channel_append_duration_seconds_sum{commit_mode="local"} 0.010
wukongim_channel_append_batch_records_count 2
wukongim_channel_append_batch_records_sum 8
wukongim_channel_append_batch_bytes_sum 200
wukongim_channel_append_batch_wait_duration_seconds_sum 0.002
wukongim_channel_worker_task_duration_seconds_count{kind="store_append",result="ok"} 4
wukongim_channel_worker_task_duration_seconds_sum{kind="store_append",result="ok"} 0.020
`)
	writeFile(t, after, `wukongim_channel_active_runtimes{role="leader"} 2
wukongim_channel_active_runtimes{role="follower"} 3
wukongim_channelv2_active_runtimes{role="leader"} 2
wukongim_channel_follower_parked{reactor_id="0"} 1
wukongim_channel_reactor_mailbox_depth{reactor_id="0",priority="normal"} 4
wukongim_channel_worker_queue_depth{pool="store_append"} 6
wukongim_channel_activation_rejected_total{reason="max_channels"} 2
wukongim_channel_recovery_probe_total{result="submitted"} 3
wukongim_channel_recovery_probe_total{result="ok"} 1
wukongim_channel_recovery_probe_total{result="err"} 1
wukongim_channel_pull_total{result="ok",empty="false"} 4
wukongim_channel_pull_total{result="ok",empty="true"} 2
wukongim_channel_pull_total{result="err",empty="false"} 1
wukongim_channel_rpc_pull_total{result="ok"} 5
wukongim_channel_rpc_pull_total{result="err"} 1
wukongim_channel_meta_cache_total{result="hit"} 4
wukongim_channel_meta_cache_total{result="miss"} 2
wukongim_channel_meta_cache_total{result="invalidate"} 1
wukongim_channel_append_duration_seconds_count{commit_mode="local"} 15
wukongim_channelv2_append_duration_seconds_count{commit_mode="local"} 15
wukongim_channel_append_duration_seconds_sum{commit_mode="local"} 0.035
wukongim_channel_append_batch_records_count 4
wukongim_channel_append_batch_records_sum 20
wukongim_channel_append_batch_bytes_sum 600
wukongim_channel_append_batch_wait_duration_seconds_sum 0.008
wukongim_channel_worker_task_duration_seconds_count{kind="store_append",result="ok"} 9
wukongim_channel_worker_task_duration_seconds_sum{kind="store_append",result="ok"} 0.070
`)

	cmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
		before,
		after,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("summary awk failed: %v\n%s", err, output)
	}

	fields := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(fields) < 39 {
		t.Fatalf("summary fields = %d, want at least 39: %q", len(fields), output)
	}
	for index, want := range map[int]string{
		2:  "5",
		3:  "2",
		4:  "3",
		5:  "1",
		6:  "4",
		7:  "6",
		18: "1",
		19: "2",
		22: "3",
		25: "4",
		27: "1.000",
		31: "5",
		32: "5.000",
		33: "2",
		34: "6.000",
		35: "200.000",
		36: "3.000",
		37: "5",
		38: "10.000",
	} {
		if fields[index] != want {
			t.Fatalf("field[%d] = %q, want %q; row=%q", index, fields[index], want, output)
		}
	}
}

func TestChannelRuntimeMetricsSummaryAwkPromotedEntrypoint(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	writeFile(t, before, "")
	writeFile(t, after, `wukongim_channel_active_runtimes{role="leader"} 1
`)

	cmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
		before,
		after,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("summary awk failed: %v\n%s", err, output)
	}

	fields := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(fields) < 5 {
		t.Fatalf("summary fields = %d, want at least 5: %q", len(fields), output)
	}
	for index, want := range map[int]string{
		2: "1",
		3: "1",
		4: "0",
	} {
		if fields[index] != want {
			t.Fatalf("field[%d] = %q, want %q; row=%q", index, fields[index], want, output)
		}
	}
}

func TestChannelRuntimeMetricsSummaryAwkLegacyEntrypointMatchesPromotedEntrypoint(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	writeFile(t, before, "")
	writeFile(t, after, `wukongim_channel_active_runtimes{role="leader"} 1
wukongim_channel_worker_queue_depth{pool="store_append"} 3
`)

	promotedCmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
		before,
		after,
	)
	promotedOutput, err := promotedCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("promoted summary awk failed: %v\n%s", err, promotedOutput)
	}

	legacyEntrypoint := "channelv2" + "-metrics-summary.awk"
	legacyCmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", legacyEntrypoint),
		before,
		after,
	)
	legacyOutput, err := legacyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("legacy summary awk failed: %v\n%s", err, legacyOutput)
	}
	if string(legacyOutput) != string(promotedOutput) {
		t.Fatalf("legacy entrypoint output differs from promoted entrypoint:\npromoted=%q\nlegacy=%q", promotedOutput, legacyOutput)
	}
}

func TestRuntimePoolPressureSummaryAwkReportsTimeoutAdmissions(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="db",pool="message_commit",queue="commit",priority="none",result="timeout"} 2
`)
	writeFile(t, after, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="db",pool="message_commit",queue="commit",priority="none",result="timeout"} 5
`)

	cmd := exec.Command("awk",
		"-v", "tag=000100",
		"-v", "node=node1",
		"-f", filepath.Join(root, "scripts", "runtime-pool-pressure-summary.awk"),
		before,
		after,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime pool pressure awk failed: %v\n%s", err, output)
	}
	summary := string(output)
	for _, want := range []string{
		"000100\tnode1\tdb\tmessage_commit\tcommit\tnone",
		"admission_timeout",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("runtime pool pressure awk missing %q:\n%s", want, summary)
		}
	}
}

func TestRuntimePoolPressureSummaryAwkPromotesLegacyRuntimePoolLabels(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal",result="full"} 1
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc",result="busy"} 1
`)
	writeFile(t, after, `wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal"} 9
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal"} 10
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 8
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 8
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal",result="full"} 3
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 2
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 8
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc",result="busy"} 4
`)

	cmd := exec.Command("awk",
		"-v", "tag=000100",
		"-v", "node=node1",
		"-f", filepath.Join(root, "scripts", "runtime-pool-pressure-summary.awk"),
		before,
		after,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime pool pressure awk failed: %v\n%s", err, output)
	}
	summary := string(output)
	for _, want := range []string{
		"000100\tnode1\tchannel\tstore_apply\tapply\tnormal",
		"000100\tnode1\ttransport\tservice\tservice_9\trpc",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("runtime pool pressure awk missing promoted label %q:\n%s", want, summary)
		}
	}
	for _, unwanted := range []string{
		"channelv2",
		"channelv2-store-apply",
		"transportv2",
	} {
		if strings.Contains(summary, unwanted) {
			t.Fatalf("runtime pool pressure awk should not expose legacy label %q:\n%s", unwanted, summary)
		}
	}
}

func TestAntsPoolUsageSummaryAwkReportsDedicatedAntsPoolsOnly(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 1
`)
	writeFile(t, after, `wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 7
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 10
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_channelappend_writer_pool_running{node_id="1",node_name="node-1"} 6
wukongim_channelappend_writer_pool_capacity{node_id="1",node_name="node-1"} 12
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 3
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 4
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 2
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 0.750
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 5
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 64
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 0
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 0.078
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 6
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 10
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 1
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 0.600
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 1
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 2
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 0
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 4
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 8
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 1
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 4
 wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc",result="full"} 2
`)
	sample := filepath.Join(t.TempDir(), "sample.prom")
	writeFile(t, sample, `wukongim_ants_pool_running{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 10
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 20
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 9
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 0.500
`)

	cmd := exec.Command("awk",
		"-v", "tag=000100",
		"-v", "node=node1",
		"-f", filepath.Join(root, "scripts", "ants-pool-usage-summary.awk"),
		before,
		after,
		sample,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ants pool usage awk failed: %v\n%s", err, output)
	}
	summary := string(output)
	for _, want := range []string{
		"000100\tnode1\ttransport\tservice_executor\t3\t4\t2\t0.750",
		"000100\tnode1\tchannel\tstore_append\t5\t64\t0\t0.078",
		"000100\tnode1\tchannel\tstore_apply\t6\t10\t1\t0.600",
		"000100\tnode1\tchannelappend\tadvance\t1\t2\t0\t0.500",
		"000100\tnode1\tchannelappend\teffect\t4\t8\t1\t0.500",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("ants pool usage awk missing %q:\n%s", want, summary)
		}
	}
	for _, unwanted := range []string{
		"gateway\tasync_send",
		"gateway\tasync_auth",
		"channelv2",
		"channelappend\twriter",
		"effect_prepare",
	} {
		if strings.Contains(summary, unwanted) {
			t.Fatalf("ants pool usage awk should exclude non-ants runtime pool %q:\n%s", unwanted, summary)
		}
	}
}

func TestWukongIMThreeNodeBenchScriptCollectsLocalEvidence(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-1000ch.sh"))

	for _, want := range []string{
		"scripts/start-wukongim-three-nodes.sh",
		"collect_node_logs",
		"capture_node_pprof",
		"channel_metrics_summary",
		"scripts/channel-metrics-summary.awk",
		"channel_metrics_summary.tsv",
		"runtime_pool_queue_depth_max",
		"runtime_pool_admission_full_delta",
		"- ants_pool_usage: ants_pool_usage_summary.tsv",
		"ants_pool_usage_summary",
		"runtime_pool_pressure_summary",
		"runtime_pool_pressure_summary.tsv",
		`RUNTIME_POOL_SAMPLE_INTERVAL="${WK_BENCH_RUNTIME_POOL_SAMPLE_INTERVAL:-1}"`,
		`newer_source="$(find "$ROOT_DIR/cmd/wkbench" "$ROOT_DIR/internal/bench" -type f -newer "$WK_BENCH_BIN" -print -quit)"`,
		"write_evidence_summary",
		`ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"`,
		`RECV_ACK="${WK_BENCH_RECV_ACK:-true}"`,
		`HEARTBEAT_ENABLED="${WK_BENCH_HEARTBEAT_ENABLED:-true}"`,
		`CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"`,
		`SENDER_PICK="${WK_BENCH_SENDER_PICK:-round_robin}"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY="${WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY:-100}"`,
		`THREE_NODE_DATA_ROOT="${WK_WUKONGIM_THREE_NODES_DATA_ROOT:-$ROOT_DIR/data}"`,
		`THREE_NODE_DATA_ROOT_SOURCE="default"`,
		`if [[ -n "${WK_WUKONGIM_THREE_NODES_DATA_ROOT:-}" ]]; then`,
		`STORAGE_FREE_WARN_PERCENT="${WK_BENCH_STORAGE_FREE_WARN_PERCENT:-5}"`,
		`WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY`,
		`THREE_NODE_DATA_ROOT=$THREE_NODE_DATA_ROOT`,
		"write_storage_preflight",
		"storage-preflight.tsv",
		"external_cluster_data_root_unverified",
		`df -Pk "$probe_path" | awk 'NR == 2 { print; exit }'`,
		`ACTUAL_QPS_MIN_RATIO="${WK_BENCH_ACTUAL_QPS_MIN_RATIO:-0.90}"`,
		"actual/offered gate: >= %.2f",
		"--concurrency N        wkbench send concurrency. Default: 2800.",
		"--sender-pick MODE     Group sender selection: round_robin or first_online. Default: round_robin.",
		"ack_timeout: $ACK_TIMEOUT",
		"recv_ack: $RECV_ACK",
		"enabled: $HEARTBEAT_ENABLED",
		`--phase-poll-timeout "$PHASE_POLL_TIMEOUT"`,
		"--recv-ack BOOL",
		"--heartbeat BOOL",
		`WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-160}"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY="${WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY:-100}"`,
		`WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY`,
		`WK_TOP_API_ENABLE="${WK_TOP_API_ENABLE:-false}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SHARDS="${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-4}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SYNC="${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}"`,
		`WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT="${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}"`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}`,
		`CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-160}`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}`,
		`TOP_API_ENABLE=${WK_TOP_API_ENABLE:-false}`,
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}`,
		`CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-4}`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}`,
		`GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}`,
		`GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}`,
		"gateway_ready()",
		`>/dev/tcp/"$host"/"$port"`,
		`for gateway in "${GATEWAY_VALUES[@]}"`,
		"/debug/pprof/goroutine?debug=2",
		"/debug/pprof/heap",
		"start_run_pprof_sampler",
		"wait_run_pprof_sampler",
		"capture_node_pprof run",
		"summary.md",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bench script missing local evidence hook %q", want)
		}
	}
	if strings.Contains(script, "docker compose") {
		t.Fatalf("three-node bench script should use local startup scripts, not docker compose")
	}
}
