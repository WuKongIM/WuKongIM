package main

import (
	"bytes"
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
