package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestPrometheusRuntimeRendersScrapeConfig(t *testing.T) {
	dataDir := t.TempDir()
	runtime := newPrometheusRuntime(prometheusRuntimeConfig{
		BinaryPath:     "/opt/prometheus/prometheus",
		ListenAddr:     "127.0.0.1:9091",
		DataDir:        dataDir,
		RetentionTime:  48 * time.Hour,
		RetentionSize:  "2GB",
		ScrapeInterval: 10 * time.Second,
		ScrapeTargets:  []string{"127.0.0.1:5001", "127.0.0.1:5002"},
	})

	rendered, err := runtime.renderConfig()
	if err != nil {
		t.Fatalf("renderConfig() error = %v", err)
	}
	content := string(rendered)
	for _, want := range []string{
		"scrape_interval: 10s",
		"job_name: wukongimv2",
		"metrics_path: /metrics",
		"- 127.0.0.1:5001",
		"- 127.0.0.1:5002",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, content)
		}
	}
}

func TestPrometheusRuntimeCommandArgs(t *testing.T) {
	dataDir := t.TempDir()
	runtime := newPrometheusRuntime(prometheusRuntimeConfig{
		BinaryPath:    "/opt/prometheus/prometheus",
		ListenAddr:    "127.0.0.1:9091",
		DataDir:       dataDir,
		RetentionTime: 48 * time.Hour,
		RetentionSize: "2GB",
	})

	args := strings.Join(runtime.commandArgs(), "\n")
	for _, want := range []string{
		"--config.file=" + filepath.Join(dataDir, "prometheus.yml"),
		"--storage.tsdb.path=" + dataDir,
		"--web.listen-address=127.0.0.1:9091",
		"--storage.tsdb.retention.time=48h",
		"--storage.tsdb.retention.size=2GB",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("command args missing %q:\n%s", want, args)
		}
	}
}

func TestExtractEmbeddedPrometheusBinary(t *testing.T) {
	dir := t.TempDir()
	fsys := fstest.MapFS{
		"prometheus_embedded/prometheus-testos-testarch": {
			Data: []byte("#!/usr/bin/env sh\necho prometheus\n"),
			Mode: 0o755,
		},
	}

	path, err := extractEmbeddedPrometheusBinary(fsys, "testos", "testarch", dir)
	if err != nil {
		t.Fatalf("extractEmbeddedPrometheusBinary() error = %v", err)
	}
	if path != filepath.Join(dir, "prometheus-testos-testarch") {
		t.Fatalf("path = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("extracted binary is not executable: mode=%s", info.Mode())
	}
}

func TestExtractEmbeddedPrometheusBinaryMissingAsset(t *testing.T) {
	_, err := extractEmbeddedPrometheusBinary(fstest.MapFS{}, "testos", "testarch", t.TempDir())
	if !errors.Is(err, errEmbeddedPrometheusMissing) {
		t.Fatalf("extractEmbeddedPrometheusBinary() error = %v, want %v", err, errEmbeddedPrometheusMissing)
	}
}

func TestPrometheusDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "hours", in: 48 * time.Hour, want: "48h"},
		{name: "minutes", in: 90 * time.Minute, want: "90m"},
		{name: "seconds", in: 15 * time.Second, want: "15s"},
		{name: "fractional", in: 1500 * time.Millisecond, want: "1500ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prometheusDuration(tt.in); got != tt.want {
				t.Fatalf("prometheusDuration(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
