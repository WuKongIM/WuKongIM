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
