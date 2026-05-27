package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
)

func TestLoadConfigDefaultValues(t *testing.T) {
	unsetLoadConfigEnv(t)
	chdir(t, t.TempDir())

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 0 || cfg.Cluster.NodeID != 0 {
		t.Fatalf("NodeID = %d/%d, want zero values", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != "" || cfg.Cluster.DataDir != "" {
		t.Fatalf("DataDir = %q/%q, want empty", cfg.DataDir, cfg.Cluster.DataDir)
	}
	assertListeners(t, cfg.Gateway.Listeners, []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100"),
		binding.WSMux("ws-gateway", "0.0.0.0:5200"),
	})
}

func TestLoadConfigDefaultPathSearch(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	writeConf(t, filepath.Join(dir, "conf", "wukongim.conf"),
		"WK_NODE_ID=7",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7007",
	)

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 7 || cfg.Cluster.NodeID != 7 {
		t.Fatalf("NodeID = %d/%d, want 7", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.Cluster.ListenAddr != "127.0.0.1:7007" {
		t.Fatalf("ListenAddr = %q", cfg.Cluster.ListenAddr)
	}
}

func TestLoadConfigExplicitConfigFile(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"# single-node cluster skeleton",
		"",
		"WK_NODE_ID=42",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-42"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7042",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=3",
		"WK_CLUSTER_HASH_SLOT_COUNT=64",
		"WK_CLUSTER_SLOT_REPLICA_N=1",
		"WK_GATEWAY_SEND_TIMEOUT=5s",
	)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 42 || cfg.Cluster.NodeID != 42 {
		t.Fatalf("NodeID = %d/%d, want 42", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != filepath.Join(dir, "node-42") || cfg.Cluster.DataDir != filepath.Join(dir, "node-42") {
		t.Fatalf("DataDir = %q/%q", cfg.DataDir, cfg.Cluster.DataDir)
	}
	if cfg.Cluster.ListenAddr != "127.0.0.1:7042" {
		t.Fatalf("ListenAddr = %q", cfg.Cluster.ListenAddr)
	}
	if cfg.Cluster.Slots.InitialSlotCount != 3 {
		t.Fatalf("InitialSlotCount = %d", cfg.Cluster.Slots.InitialSlotCount)
	}
	if cfg.Cluster.Slots.HashSlotCount != 64 {
		t.Fatalf("HashSlotCount = %d", cfg.Cluster.Slots.HashSlotCount)
	}
	if cfg.Cluster.Slots.ReplicaCount != 1 {
		t.Fatalf("ReplicaCount = %d", cfg.Cluster.Slots.ReplicaCount)
	}
	if cfg.Gateway.SendTimeout != 5*time.Second {
		t.Fatalf("SendTimeout = %s", cfg.Gateway.SendTimeout)
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "file-node"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_GATEWAY_SEND_TIMEOUT=1s",
	)
	t.Setenv("WK_NODE_ID", "2")
	t.Setenv("WK_NODE_DATA_DIR", filepath.Join(dir, "env-node"))
	t.Setenv("WK_CLUSTER_LISTEN_ADDR", "127.0.0.1:7002")
	t.Setenv("WK_GATEWAY_SEND_TIMEOUT", "2s")

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 2 || cfg.Cluster.NodeID != 2 {
		t.Fatalf("NodeID = %d/%d, want 2", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != filepath.Join(dir, "env-node") || cfg.Cluster.DataDir != filepath.Join(dir, "env-node") {
		t.Fatalf("DataDir = %q/%q", cfg.DataDir, cfg.Cluster.DataDir)
	}
	if cfg.Cluster.ListenAddr != "127.0.0.1:7002" {
		t.Fatalf("ListenAddr = %q", cfg.Cluster.ListenAddr)
	}
	if cfg.Gateway.SendTimeout != 2*time.Second {
		t.Fatalf("SendTimeout = %s", cfg.Gateway.SendTimeout)
	}
}

func TestLoadConfigJSONListeners(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-test","network":"tcp","address":"127.0.0.1:5101","transport":"gnet","protocol":"wkproto"}]`,
	)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	assertListeners(t, cfg.Gateway.Listeners, []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-test", "127.0.0.1:5101"),
	})
}

func TestLoadConfigRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "node id", line: "WK_NODE_ID=bad"},
		{name: "slot count", line: "WK_CLUSTER_INITIAL_SLOT_COUNT=-1"},
		{name: "listener json", line: "WK_GATEWAY_LISTENERS=not-json"},
		{name: "send timeout", line: "WK_GATEWAY_SEND_TIMEOUT=soon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			writeConf(t, path, tc.line)

			if _, err := loadConfig([]string{"-config", path}); err == nil {
				t.Fatalf("loadConfig() error = nil, want error")
			}
		})
	}
}

func assertListeners(t *testing.T, got, want []gateway.ListenerOptions) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("listeners len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listener[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func writeConf(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(): %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func unsetLoadConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range supportedConfigKeys {
		old, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%s): %v", key, err)
		}
		t.Cleanup(func() {
			if ok {
				if err := os.Setenv(key, old); err != nil {
					t.Fatalf("restore env %s: %v", key, err)
				}
			} else if err := os.Unsetenv(key); err != nil {
				t.Fatalf("restore unset env %s: %v", key, err)
			}
		})
	}
}
