//go:build e2e && legacy_e2e

package wkbench_smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/test/legacy/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWkbenchSmokeCompletesThroughBenchAPIAndWKProto(t *testing.T) {
	s := suite.New(t)
	node := s.StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_BENCH_API_ENABLE": "true",
	}))
	requireHTTPReady(t, node)

	workerSrv := httptest.NewServer(worker.NewServer(worker.Config{InsecureControl: true}))
	t.Cleanup(workerSrv.Close)

	reportDir := filepath.Join(t.TempDir(), "report")
	var stderr bytes.Buffer
	code := runWkbench(t, []string{
		"run",
		"--target", writeFile(t, "target.yaml", targetYAML(node)),
		"--scenario", writeFile(t, "scenario.yaml", smokeScenarioYAML(reportDir)),
		"--workers", writeFile(t, "workers.yaml", workerYAML(workerSrv.URL)),
	}, &stderr)

	require.Equal(t, 0, code, "stderr: %s\nnode diagnostics:\n%s", stderr.String(), node.Process.DumpDiagnostics())
	var rep wkbenchReport
	readJSONFile(t, filepath.Join(reportDir, "report.json"), &rep)
	require.Equal(t, "passed", rep.Status)
	requireCounterGreater(t, rep, "person_send_success_total")
	requireCounterGreater(t, rep, "group_send_success_total")
	requireAnyCounterGreater(t, rep, []string{
		"person_recv_success_total",
		"group_recv_success_total",
		"recv_verify_success_total",
	})
	requireStandardArtifacts(t, reportDir)
}

func TestWkbenchDoctorFailsPreflightWhenServerBenchAPIIsDisabled(t *testing.T) {
	s := suite.New(t)
	node := s.StartSingleNodeCluster()
	requireHTTPReady(t, node)

	workerSrv := httptest.NewServer(worker.NewServer(worker.Config{InsecureControl: true}))
	t.Cleanup(workerSrv.Close)

	var stderr bytes.Buffer
	code := runWkbench(t, []string{
		"doctor",
		"--target", writeFile(t, "target.yaml", targetYAML(node)),
		"--scenario", writeFile(t, "scenario.yaml", smokeScenarioYAML(t.TempDir())),
		"--workers", writeFile(t, "workers.yaml", workerYAML(workerSrv.URL)),
	}, &stderr)

	require.Equal(t, 2, code, "stderr: %s\nnode diagnostics:\n%s", stderr.String(), node.Process.DumpDiagnostics())
	require.Contains(t, stderr.String(), "preflight failed")
	require.Contains(t, stderr.String(), "bench api capabilities")
}

func runWkbench(t *testing.T, args []string, stderr *bytes.Buffer) int {
	t.Helper()
	binary := buildWkbenchBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	cmd.Stderr = stderr
	cmd.Stdout = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			t.Fatalf("wkbench command timed out: %v (ctx_err=%v)", err, ctx.Err())
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		t.Fatalf("wkbench command failed before exit: %v (ctx_err=%v)", err, ctx.Err())
	}
	return 0
}

var wkbenchBinaryCache struct {
	once sync.Once
	path string
	err  error
}

func buildWkbenchBinary(t *testing.T) string {
	t.Helper()
	wkbenchBinaryCache.once.Do(func() {
		root, err := os.MkdirTemp("", "wkbench-e2e-bin-*")
		if err != nil {
			wkbenchBinaryCache.err = err
			return
		}
		wkbenchBinaryCache.path = filepath.Join(root, "wkbench-e2e")
		cmd := exec.Command("go", "build", "-o", wkbenchBinaryCache.path, "./cmd/wkbench")
		cmd.Dir = repoRoot()
		cmd.Env = append(os.Environ(), "GOWORK=off")
		output, err := cmd.CombinedOutput()
		if err != nil {
			wkbenchBinaryCache.err = fmt.Errorf("go build ./cmd/wkbench: %w\n%s", err, output)
		}
	})
	require.NoError(t, wkbenchBinaryCache.err)
	return wkbenchBinaryCache.path
}

func requireHTTPReady(t *testing.T, node *suite.StartedNode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.WaitHTTPReady(ctx, node.Spec.APIAddr, "/readyz"), node.Process.DumpDiagnostics())
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func targetYAML(node *suite.StartedNode) string {
	return fmt.Sprintf(`name: e2e-single-node-cluster
api:
  addrs: [http://%s]
gateway:
  tcp:
    addrs: [%s]
bench_api:
  enabled: true
`, node.Spec.APIAddr, node.Spec.GatewayAddr)
}

func workerYAML(addr string) string {
	return fmt.Sprintf(`workers:
  - id: e2e-worker-1
    addr: %s
    weight: 1
    insecure_control: true
`, addr)
}

func smokeScenarioYAML(reportDir string) string {
	return fmt.Sprintf(`version: wkbench/v1
run:
  id: wkbench-smoke
  duration: 1s
  warmup: 0s
  cooldown: 0s
  report_dir: %s
identity:
  uid_prefix: wkbench-smoke-u
  device_prefix: wkbench-smoke-d
  client_msg_prefix: wkbench-smoke-m
  token:
    mode: bench_api
online:
  total_users: 7
  gateway_balance: round_robin
channels:
  profiles:
    - name: person-pairs
      channel_type: person
      count: 2
    - name: group-small
      channel_type: group
      count: 1
      members:
        count: 3
      online:
        member_ratio: 1
messages:
  payload:
    size_bytes: 64
    mode: fixed
  traffic:
    - name: person-send
      channel_ref: person-pairs
      rate_per_channel: 1/s
      recv_ack: true
      verify:
        recv:
          mode: full
    - name: group-send
      channel_ref: group-small
      rate_per_channel: 1/s
      recv_ack: true
      verify:
        recv:
          mode: sampled
          sample_size_per_message: 1
`, reportDir)
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func readJSONFile(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, out))
}

func requireCounterGreater(t *testing.T, rep wkbenchReport, name string) {
	t.Helper()
	require.Greater(t, rep.Metrics.Counters[name], uint64(0), "report metrics counters: %#v", rep.Metrics.Counters)
}

func requireAnyCounterGreater(t *testing.T, rep wkbenchReport, names []string) {
	t.Helper()
	for _, name := range names {
		if rep.Metrics.Counters[name] > 0 {
			return
		}
	}
	require.Failf(t, "expected at least one receive success counter to be greater than zero", "names=%v counters=%#v", names, rep.Metrics.Counters)
}

func requireStandardArtifacts(t *testing.T, reportDir string) {
	t.Helper()
	for _, rel := range []string{
		"errors/samples.jsonl",
	} {
		requireFileExists(t, filepath.Join(reportDir, rel))
	}
	requireNonEmptyFile(t, filepath.Join(reportDir, "summary.md"))
	requireNonEmptyFile(t, filepath.Join(reportDir, "coordinator.log"))
	requireNonEmptyJSONFile(t, filepath.Join(reportDir, "workers", "e2e-worker-1.report.json"))
	requireNonEmptyJSONLines(t, filepath.Join(reportDir, "metrics", "worker-1s.jsonl"))
	requireNonEmptyJSONLines(t, filepath.Join(reportDir, "metrics", "target-snapshots.jsonl"))
	requireJSONLines(t, filepath.Join(reportDir, "errors", "samples.jsonl"))
}

func requireFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.False(t, info.IsDir(), "%s should be a file", path)
}

func requireNonEmptyJSONFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, bytes.TrimSpace(data), "%s should not be empty", path)
	var decoded any
	require.NoError(t, json.Unmarshal(data, &decoded), "%s should contain JSON", path)
}

func requireNonEmptyJSONLines(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, bytes.TrimSpace(data), "%s should not be empty", path)
	requireJSONLinesData(t, path, data)
}

func requireJSONLines(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	requireJSONLinesData(t, path, data)
}

func requireJSONLinesData(t *testing.T, path string, data []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	var lines int
	for {
		var decoded any
		err := dec.Decode(&decoded)
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "%s should contain JSON lines", path)
		lines++
	}
	if len(bytes.TrimSpace(data)) > 0 {
		require.Greater(t, lines, 0, "%s should contain at least one JSON line", path)
	}
}

func requireNonEmptyFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, bytes.TrimSpace(data), "%s should not be empty", path)
}

type wkbenchReport struct {
	Status  string `json:"status"`
	Metrics struct {
		Counters map[string]uint64 `json:"counters"`
	} `json:"metrics"`
}
