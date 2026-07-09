package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigPathsUseTOML(t *testing.T) {
	got := DefaultPaths()
	want := []string{"./wukongim.toml", "./conf/wukongim.toml", "/etc/wukongim/wukongim.toml"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("DefaultPaths() = %v, want %v", got, want)
	}
}

func TestLoadExplicitMissingFileReportsPath(t *testing.T) {
	_, err := Load(Options{Args: []string{"-config", filepath.Join(t.TempDir(), "missing.toml")}})
	if err == nil {
		t.Fatal("Load() error = nil, want missing explicit config error")
	}
	if !strings.Contains(err.Error(), "missing.toml") {
		t.Fatalf("Load() error = %v, want explicit path", err)
	}
}

func TestLoadMinimalTOMLConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"
hash_slot_count = 256
`)

	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != dir+"/node1" || cfg.Cluster.ListenAddr != "127.0.0.1:7001" {
		t.Fatalf("loaded config = %#v", cfg)
	}
}

func TestLoadRejectsUnknownTOMLKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"
hash_slots_count = 256
`)

	_, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err == nil {
		t.Fatal("Load() error = nil, want unknown key")
	}
	if !strings.Contains(err.Error(), "cluster.hash_slots_count") {
		t.Fatalf("Load() error = %v, want unknown TOML path", err)
	}
}

func TestLoadAllowsEnvOnlyStartup(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=9",
		"WK_NODE_DATA_DIR=" + dir + "/node9",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7009",
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NodeID != 9 || cfg.Cluster.ListenAddr != "127.0.0.1:7009" {
		t.Fatalf("env-only config = %#v", cfg)
	}
}

func TestLoadEnvOverridesTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"
`)
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=2",
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NodeID != 2 || cfg.Cluster.NodeID != 2 {
		t.Fatalf("NodeID = %d/%d, want env override 2", cfg.NodeID, cfg.Cluster.NodeID)
	}
}

func TestLoadRejectsUnknownWKEnv(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_HASH_SLOTS_COUNT=256",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want unknown env")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_HASH_SLOTS_COUNT") {
		t.Fatalf("Load() error = %v, want unknown env name", err)
	}
}

func TestLoadRejectsRemovedWKEnvWithReplacement(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_GROUP_COUNT=16",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want removed env")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_GROUP_COUNT") || !strings.Contains(err.Error(), "WK_CLUSTER_INITIAL_SLOT_COUNT") {
		t.Fatalf("Load() error = %v, want removed key replacement", err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func cleanEnv() []string {
	return []string{"PATH=" + os.Getenv("PATH")}
}
