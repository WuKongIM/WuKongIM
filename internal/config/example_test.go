package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootTOMLExampleLoads(t *testing.T) {
	cfg, err := Load(Options{Args: []string{"-config", filepath.Join("..", "..", "wukongim.toml.example")}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(root example) error = %v", err)
	}
	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("HashSlotCount = %d, want 256", cfg.Cluster.Slots.HashSlotCount)
	}
}

func TestCommandTOMLExampleLoads(t *testing.T) {
	cfg, err := Load(Options{Args: []string{"-config", filepath.Join("..", "..", "cmd", "wukongim", "wukongim.toml.example")}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(cmd example) error = %v", err)
	}
	if cfg.Cluster.Control.ClusterID != "wukongim-single" {
		t.Fatalf("ClusterID = %q, want wukongim-single", cfg.Cluster.Control.ClusterID)
	}
}

func TestSingleNodeClusterPrometheusExamplesUseDedicatedDefaultPort(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "wukongim.toml.example"),
		filepath.Join("..", "..", "cmd", "wukongim", "wukongim.toml.example"),
		filepath.Join("..", "..", "scripts", "wukongim", "wukongim.toml"),
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", file, err)
			}
			if !strings.Contains(string(content), `listen_addr = "127.0.0.1:9099"`) {
				t.Fatalf("%s must use the dedicated app-managed Prometheus port 9099", file)
			}
		})
	}
}

func TestPresenceExamplesDocumentTouchMaxRoutesPerFlush(t *testing.T) {
	files := []string{filepath.Join("..", "..", "wukongim.toml.example")}
	for _, pattern := range []string{
		filepath.Join("..", "..", "cmd", "wukongim", "*.toml.example"),
		filepath.Join("..", "..", "scripts", "wukongim", "*.toml"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("Glob(%s) error = %v", pattern, err)
		}
		files = append(files, matches...)
	}

	want := "# Maximum owner-local dirty routes processed across all touch chunks in one flush.\n" +
		"# Must be positive and greater than or equal to touch_batch_size.\n" +
		"touch_max_routes_per_flush = 65536"
	foundPresence := 0
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		if !strings.Contains(string(content), "[presence]") {
			continue
		}
		foundPresence++
		if !strings.Contains(string(content), want) {
			t.Errorf("%s must document touch_max_routes_per_flush with the required adjacent English comments", file)
		}
	}
	if foundPresence == 0 {
		t.Fatal("no shipped [presence] examples found")
	}
}

func TestDeliveryExamplesDocumentRecipientWorkerConcurrency(t *testing.T) {
	files := []string{filepath.Join("..", "..", "wukongim.toml.example")}
	for _, pattern := range []string{
		filepath.Join("..", "..", "cmd", "wukongim", "*.toml.example"),
		filepath.Join("..", "..", "scripts", "wukongim", "*.toml"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("Glob(%s) error = %v", pattern, err)
		}
		files = append(files, matches...)
	}

	want := "# Maximum recipient-authority delivery batches processed concurrently by this node.\n" +
		"# This is independent from channel_append.recipient_authority_dispatch_concurrency.\n" +
		"recipient_worker_concurrency = 100"
	foundDelivery := 0
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		if !strings.Contains(string(content), "[delivery]") {
			continue
		}
		foundDelivery++
		if !strings.Contains(string(content), want) {
			t.Errorf("%s must document recipient_worker_concurrency with the required adjacent English comments", file)
		}
	}
	if foundDelivery == 0 {
		t.Fatal("no shipped [delivery] examples found")
	}
}
