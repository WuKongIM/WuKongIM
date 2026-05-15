//go:build e2e

package wkbench_smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
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
	require.Greater(t, sendackSuccessTotal(rep), uint64(0), "report metrics counters: %#v", rep.Metrics.Counters)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	binary := buildWkbenchBinary(t)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	cmd.Stderr = stderr
	cmd.Stdout = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		t.Fatalf("wkbench command failed before exit: %v", err)
	}
	return 0
}

func buildWkbenchBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wkbench-e2e")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", path, "./cmd/wkbench")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	return path
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

type wkbenchReport struct {
	Status  string `json:"status"`
	Metrics struct {
		Counters map[string]uint64 `json:"counters"`
	} `json:"metrics"`
}

func sendackSuccessTotal(rep wkbenchReport) uint64 {
	var total uint64
	for key, value := range rep.Metrics.Counters {
		switch key {
		case "person_send_success_total", "group_send_success_total", "sendack_success_total":
			total += value
		}
	}
	return total
}
