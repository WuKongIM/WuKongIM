package main

import (
	"bytes"
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
bench_api:
  enabled: false
`)
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
	if !strings.Contains(stderr.String(), "bench_api.enabled") {
		t.Fatalf("expected bench_api.enabled error, got %q", stderr.String())
	}
}

func TestDoctorCommandReturnsPreflightExitCodeForNetworkFailure(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
api:
  addrs: [http://127.0.0.1:1]
bench_api:
  enabled: true
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
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
