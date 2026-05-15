package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerCommandRequiresControlToken(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "")
	var stderr bytes.Buffer

	code := runWithStderr([]string{"worker", "--listen", "127.0.0.1:0"}, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--control-token is required") {
		t.Fatalf("expected control token error, got %q", stderr.String())
	}
}

func TestWorkerCommandAllowsExplicitInsecureControl(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "")
	var stderr bytes.Buffer

	cfg, code := parseWorkerConfig([]string{"--listen", "127.0.0.1:0", "--insecure-control=true"}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d and stderr %q", code, stderr.String())
	}
	if !cfg.server.InsecureControl {
		t.Fatalf("expected insecure control to be enabled")
	}
}

func TestWorkerCommandInsecureControlIgnoresEnvToken(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "from-env")
	var stderr bytes.Buffer

	cfg, code := parseWorkerConfig([]string{"--listen", "127.0.0.1:0", "--insecure-control=true"}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d and stderr %q", code, stderr.String())
	}
	if !cfg.server.InsecureControl {
		t.Fatalf("expected insecure control to be enabled")
	}
	if cfg.server.ControlToken != "" {
		t.Fatalf("expected insecure control to clear effective token, got %q", cfg.server.ControlToken)
	}
}

func TestValidateCommandLoadsConfigsAndBuildsPlanWithoutNetwork(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
name: target
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected validate success, got code %d stderr %q", code, stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForInvalidConfig(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: false
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "bench_api.enabled") {
		t.Fatalf("expected bench_api.enabled error, got %q", stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForMissingWorkerAddr(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "workers[0].addr") {
		t.Fatalf("expected worker addr error, got %q", stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForMissingWorkerToken(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "control_token") {
		t.Fatalf("expected control token error, got %q", stderr.String())
	}
}

func TestDoctorCommandReturnsPreflightExitCodeForNetworkFailure(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"doctor", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 2 {
		t.Fatalf("expected preflight exit code 2, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "preflight failed") {
		t.Fatalf("expected preflight error, got %q", stderr.String())
	}
}

func TestDoctorCommandRunsWithoutScenario(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeWkbenchJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"users_tokens_batch":        true,
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer targetSrv.Close()
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requirePath(t, r, "/v1/info")
		requireHeader(t, r, "Authorization", "Bearer secret")
		writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
	}))
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"doctor", "--target", targetPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected doctor success, got code %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandCompletesWorkloadOrchestration(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench workload orchestration completed") {
		t.Fatalf("expected workload orchestration note, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "fake/no-op") {
		t.Fatalf("did not expect stale fake/no-op note, got %q", stderr.String())
	}
}

func TestRunCommandWritesReportDirectoryWhenConfigured(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	reportDir := t.TempDir()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAMLWithReportDir(reportDir))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
	for _, rel := range []string{"report.json", "summary.md", "coordinator.log", "metrics/worker-1s.jsonl", "errors/samples.jsonl"} {
		if _, err := os.Stat(filepath.Join(reportDir, rel)); err != nil {
			t.Fatalf("expected report artifact %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(reportDir, "workers", "w1.report.json")); err != nil {
		t.Fatalf("expected worker report artifact: %v", err)
	}
}

func TestRunCommandReturnsInternalExitCodeWhenReportWriteFails(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	reportDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(reportDir, []byte("file blocks report dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAMLWithReportDir(reportDir))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 6 {
		t.Fatalf("expected internal exit code 6, got %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandReturnsPreflightExitCodeForNetworkFailure(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 2 {
		t.Fatalf("expected preflight exit code 2, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "preflight failed") {
		t.Fatalf("expected preflight error, got %q", stderr.String())
	}
}

func TestRunCommandReturnsWorkerExitCodeForPhaseFailure(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/info", "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": "prepare", "assignment": map[string]string{"run_id": "bench-run", "worker_id": "w1"}})
		case "/v1/assign", "/v1/phase/prepare", "/v1/stop":
			writeWkbenchJSON(t, w, map[string]any{"phase": "prepare", "assignment": map[string]string{"run_id": "bench-run", "worker_id": "w1"}})
		case "/v1/phase/connect":
			http.Error(w, "connect failed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 4 {
		t.Fatalf("expected worker exit code 4, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "worker run failed") {
		t.Fatalf("expected worker failure error, got %q", stderr.String())
	}
}

func TestRunCommandReturnsHardLimitExitCodeWhenCollectionAlsoFails(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := hardLimitAndCollectionFailureWorkerServer(t, "secret")
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML()+`
limits:
  hard:
    max_sendack_error_rate: 0
`)
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 3 {
		t.Fatalf("expected hard-limit exit code 3, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "hard limit failed") {
		t.Fatalf("expected hard limit error, got %q", stderr.String())
	}
}

func TestValidateCommandRequiresConfigFlags(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", "target.yaml"}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--scenario is required") {
		t.Fatalf("expected missing scenario error, got %q", stderr.String())
	}
}

func TestDevSimCommandReturnsConfigExitCodeForMissingConfig(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"dev-sim", "--config", filepath.Join(t.TempDir(), "missing.yaml")}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "config validation failed") {
		t.Fatalf("expected config validation error, got %q", stderr.String())
	}
}

func TestDevSimCommandHelp(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"dev-sim", "--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench dev-sim") || !strings.Contains(stderr.String(), "--status-listen") {
		t.Fatalf("expected dev-sim help, got %q", stderr.String())
	}
}

func writeWkbenchTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validScenarioYAML() string {
	return `
version: wkbench/v1
run:
  id: bench-run
online:
  total_users: 10
channels:
  profiles:
    - name: group-hot
      channel_type: group
      count: 1
      members:
        count: 5
messages:
  traffic:
    - name: hot-group-send
      channel_ref: group-hot
      rate_per_channel: 1/s
`
}

func validScenarioYAMLWithReportDir(reportDir string) string {
	return `
version: wkbench/v1
run:
  id: bench-run
  report_dir: ` + reportDir + `
online:
  total_users: 10
channels:
  profiles:
    - name: group-hot
      channel_type: group
      count: 1
      members:
        count: 5
messages:
  traffic:
    - name: hot-group-send
      channel_ref: group-hot
      rate_per_channel: 1/s
`
}

func validTargetYAML(apiAddr string) string {
	return `
name: target
api:
  addrs: [` + apiAddr + `]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`
}

func goodWkbenchTargetServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeWkbenchJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"users_tokens_batch":        true,
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func goodWkbenchWorkerServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	phase := "assigned"
	assignment := map[string]string{"run_id": "bench-run", "worker_id": "w1"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			phase = "assigned"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/connect":
			phase = "connect"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			writeWkbenchJSON(t, w, map[string]any{"worker_id": "w1"})
		case "/v1/stop":
			phase = "stopped"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func hardLimitAndCollectionFailureWorkerServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	phase := "assigned"
	assignment := map[string]string{"run_id": "bench-run", "worker_id": "w1"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			phase = "assigned"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/connect":
			phase = "connect"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{"person_send_success_total": 9, "person_send_error_total": 1}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			http.Error(w, "report exploded", http.StatusInternalServerError)
		case "/v1/stop":
			phase = "stopped"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func writeWkbenchJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func requirePath(t *testing.T, r *http.Request, want string) {
	t.Helper()
	if r.URL.Path != want {
		t.Fatalf("path = %s, want %s", r.URL.Path, want)
	}
}

func requireHeader(t *testing.T, r *http.Request, key, want string) {
	t.Helper()
	if got := r.Header.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
