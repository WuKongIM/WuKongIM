package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
)

func TestLoadConfigDefaultValues(t *testing.T) {
	unsetLoadConfigEnv(t)
	chdir(t, t.TempDir())
	dir := t.TempDir()
	t.Setenv("WK_NODE_ID", "1")
	t.Setenv("WK_NODE_DATA_DIR", filepath.Join(dir, "node-1"))
	t.Setenv("WK_CLUSTER_LISTEN_ADDR", "127.0.0.1:7001")

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != filepath.Join(dir, "node-1") || cfg.Cluster.DataDir != filepath.Join(dir, "node-1") {
		t.Fatalf("DataDir = %q/%q", cfg.DataDir, cfg.Cluster.DataDir)
	}
	assertListeners(t, cfg.Gateway.Listeners, []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100"),
		binding.WSMux("ws-gateway", "0.0.0.0:5200"),
	})
	wantLoops := adaptiveGatewayGnetEventLoops(runtime.GOMAXPROCS(0))
	if cfg.Gateway.Transport.Gnet.NumEventLoop != wantLoops {
		t.Fatalf("Gnet.NumEventLoop = %d, want adaptive %d", cfg.Gateway.Transport.Gnet.NumEventLoop, wantLoops)
	}
	if wantLoops > 1 && !cfg.Gateway.Transport.Gnet.Multicore {
		t.Fatalf("Gnet.Multicore = false, want true for %d event loops", wantLoops)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxWait != time.Millisecond {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want 1ms", cfg.Gateway.Session.AsyncSendBatchMaxWait)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxRecords != 512 {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want 512", cfg.Gateway.Session.AsyncSendBatchMaxRecords)
	}
}

func TestAdaptiveGatewayGnetEventLoops(t *testing.T) {
	tests := []struct {
		name       string
		gomaxprocs int
		want       int
	}{
		{name: "invalid clamps to one", gomaxprocs: 0, want: 1},
		{name: "small server keeps one", gomaxprocs: 2, want: 1},
		{name: "medium server uses half", gomaxprocs: 6, want: 3},
		{name: "larger server caps at four", gomaxprocs: 16, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveGatewayGnetEventLoops(tt.gomaxprocs); got != tt.want {
				t.Fatalf("adaptiveGatewayGnetEventLoops(%d) = %d, want %d", tt.gomaxprocs, got, tt.want)
			}
		})
	}
}

func TestLoadConfigDefaultPathSearch(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	writeConf(t, filepath.Join(dir, "conf", "wukongim.conf"),
		"WK_NODE_ID=7",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-7"),
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
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT=12",
		"WK_API_LISTEN_ADDR=127.0.0.1:5042",
		"WK_BENCH_API_ENABLE=true",
		"WK_BENCH_API_MAX_BATCH_SIZE=123",
		"WK_BENCH_API_MAX_PAYLOAD_BYTES=456789",
		"WK_METRICS_ENABLE=true",
		"WK_PPROF_ENABLE=true",
		"WK_EXTERNAL_TCPADDR=127.0.0.1:5142",
		"WK_EXTERNAL_WSADDR=ws://127.0.0.1:5242",
		"WK_EXTERNAL_WSSADDR=wss://127.0.0.1:5342",
		"WK_GATEWAY_GNET_MULTICORE=true",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP=4",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS=128",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT=750us",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=64",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES=262144",
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
	if cfg.Cluster.Channel.ReactorCount != 12 {
		t.Fatalf("Channel.ReactorCount = %d, want 12", cfg.Cluster.Channel.ReactorCount)
	}
	if cfg.Gateway.SendTimeout != 5*time.Second {
		t.Fatalf("SendTimeout = %s", cfg.Gateway.SendTimeout)
	}
	if cfg.API.ListenAddr != "127.0.0.1:5042" {
		t.Fatalf("API.ListenAddr = %q", cfg.API.ListenAddr)
	}
	if !cfg.Bench.APIEnabled {
		t.Fatalf("Bench.APIEnabled = false, want true")
	}
	if cfg.Bench.APIMaxBatchSize != 123 {
		t.Fatalf("Bench.APIMaxBatchSize = %d, want 123", cfg.Bench.APIMaxBatchSize)
	}
	if cfg.Bench.APIMaxPayloadBytes != 456789 {
		t.Fatalf("Bench.APIMaxPayloadBytes = %d, want 456789", cfg.Bench.APIMaxPayloadBytes)
	}
	if !cfg.Observability.MetricsEnabled {
		t.Fatalf("Observability.MetricsEnabled = false, want true")
	}
	if !cfg.Observability.PProfEnabled {
		t.Fatalf("Observability.PProfEnabled = false, want true")
	}
	if cfg.API.ExternalTCPAddr != "127.0.0.1:5142" || cfg.API.ExternalWSAddr != "ws://127.0.0.1:5242" || cfg.API.ExternalWSSAddr != "wss://127.0.0.1:5342" {
		t.Fatalf("external gateway addrs = %#v", cfg.API)
	}
	if !cfg.Gateway.Transport.Gnet.Multicore {
		t.Fatalf("Gnet.Multicore = false, want true")
	}
	if cfg.Gateway.Transport.Gnet.NumEventLoop != 4 {
		t.Fatalf("Gnet.NumEventLoop = %d, want 4", cfg.Gateway.Transport.Gnet.NumEventLoop)
	}
	if cfg.Gateway.Session.AsyncSendDispatchWorkers != 128 {
		t.Fatalf("AsyncSendDispatchWorkers = %d, want 128", cfg.Gateway.Session.AsyncSendDispatchWorkers)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxWait != 750*time.Microsecond {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want 750us", cfg.Gateway.Session.AsyncSendBatchMaxWait)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxRecords != 64 {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want 64", cfg.Gateway.Session.AsyncSendBatchMaxRecords)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxBytes != 262144 {
		t.Fatalf("AsyncSendBatchMaxBytes = %d, want 262144", cfg.Gateway.Session.AsyncSendBatchMaxBytes)
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
	t.Setenv("WK_CLUSTER_CHANNEL_REACTOR_COUNT", "6")
	t.Setenv("WK_GATEWAY_GNET_NUM_EVENT_LOOP", "5")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS", "256")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT", "1ms")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS", "96")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES", "131072")
	t.Setenv("WK_GATEWAY_SEND_TIMEOUT", "2s")
	t.Setenv("WK_API_LISTEN_ADDR", "127.0.0.1:5002")
	t.Setenv("WK_BENCH_API_ENABLE", "true")
	t.Setenv("WK_METRICS_ENABLE", "true")
	t.Setenv("WK_PPROF_ENABLE", "true")

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
	if cfg.Cluster.Channel.ReactorCount != 6 {
		t.Fatalf("Channel.ReactorCount = %d, want 6", cfg.Cluster.Channel.ReactorCount)
	}
	if cfg.Gateway.SendTimeout != 2*time.Second {
		t.Fatalf("SendTimeout = %s", cfg.Gateway.SendTimeout)
	}
	if cfg.Gateway.Transport.Gnet.NumEventLoop != 5 {
		t.Fatalf("Gnet.NumEventLoop = %d, want 5", cfg.Gateway.Transport.Gnet.NumEventLoop)
	}
	if cfg.Gateway.Session.AsyncSendDispatchWorkers != 256 {
		t.Fatalf("AsyncSendDispatchWorkers = %d, want 256", cfg.Gateway.Session.AsyncSendDispatchWorkers)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxWait != time.Millisecond {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want 1ms", cfg.Gateway.Session.AsyncSendBatchMaxWait)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxRecords != 96 {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want 96", cfg.Gateway.Session.AsyncSendBatchMaxRecords)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxBytes != 131072 {
		t.Fatalf("AsyncSendBatchMaxBytes = %d, want 131072", cfg.Gateway.Session.AsyncSendBatchMaxBytes)
	}
	if cfg.API.ListenAddr != "127.0.0.1:5002" {
		t.Fatalf("API.ListenAddr = %q", cfg.API.ListenAddr)
	}
	if !cfg.Bench.APIEnabled {
		t.Fatalf("Bench.APIEnabled = false, want true")
	}
	if !cfg.Observability.MetricsEnabled {
		t.Fatalf("Observability.MetricsEnabled = false, want true")
	}
	if !cfg.Observability.PProfEnabled {
		t.Fatalf("Observability.PProfEnabled = false, want true")
	}
}

func TestLoadConfigJSONListeners(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
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
		name    string
		line    string
		wantKey string
	}{
		{name: "node id", line: "WK_NODE_ID=bad", wantKey: "WK_NODE_ID"},
		{name: "slot count", line: "WK_CLUSTER_INITIAL_SLOT_COUNT=-1", wantKey: "WK_CLUSTER_INITIAL_SLOT_COUNT"},
		{name: "channel reactor count", line: "WK_CLUSTER_CHANNEL_REACTOR_COUNT=many", wantKey: "WK_CLUSTER_CHANNEL_REACTOR_COUNT"},
		{name: "listener json", line: "WK_GATEWAY_LISTENERS=not-json", wantKey: "WK_GATEWAY_LISTENERS"},
		{name: "gnet multicore", line: "WK_GATEWAY_GNET_MULTICORE=maybe", wantKey: "WK_GATEWAY_GNET_MULTICORE"},
		{name: "gnet event loop", line: "WK_GATEWAY_GNET_NUM_EVENT_LOOP=many", wantKey: "WK_GATEWAY_GNET_NUM_EVENT_LOOP"},
		{name: "async dispatch workers", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS=many", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS"},
		{name: "async batch wait", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT=soon", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT"},
		{name: "async batch records", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=many", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS"},
		{name: "async batch bytes", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES=large", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES"},
		{name: "send timeout", line: "WK_GATEWAY_SEND_TIMEOUT=soon", wantKey: "WK_GATEWAY_SEND_TIMEOUT"},
		{name: "bench api enable", line: "WK_BENCH_API_ENABLE=maybe", wantKey: "WK_BENCH_API_ENABLE"},
		{name: "bench api max batch size", line: "WK_BENCH_API_MAX_BATCH_SIZE=many", wantKey: "WK_BENCH_API_MAX_BATCH_SIZE"},
		{name: "bench api max payload bytes", line: "WK_BENCH_API_MAX_PAYLOAD_BYTES=large", wantKey: "WK_BENCH_API_MAX_PAYLOAD_BYTES"},
		{name: "metrics enable", line: "WK_METRICS_ENABLE=maybe", wantKey: "WK_METRICS_ENABLE"},
		{name: "pprof enable", line: "WK_PPROF_ENABLE=maybe", wantKey: "WK_PPROF_ENABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			lines := requiredConfigLines(dir)
			key := strings.SplitN(tc.line, "=", 2)[0]
			replaced := false
			for i, line := range lines {
				if strings.HasPrefix(line, key+"=") {
					lines[i] = tc.line
					replaced = true
					break
				}
			}
			if !replaced {
				lines = append(lines, tc.line)
			}
			writeConf(t, path, lines...)

			_, err := loadConfig([]string{"-config", path})
			if err == nil {
				t.Fatalf("loadConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("loadConfig() error = %v, want key %s", err, tc.wantKey)
			}
		})
	}
}

func TestLoadConfigRejectsMissingRequiredValues(t *testing.T) {
	cases := []struct {
		name    string
		wantKey string
	}{
		{name: "node id", wantKey: "WK_NODE_ID"},
		{name: "data dir", wantKey: "WK_NODE_DATA_DIR"},
		{name: "cluster listen addr", wantKey: "WK_CLUSTER_LISTEN_ADDR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			lines := requiredConfigLines(dir)
			filtered := lines[:0]
			for _, line := range lines {
				if !strings.HasPrefix(line, tc.wantKey+"=") {
					filtered = append(filtered, line)
				}
			}
			writeConf(t, path, filtered...)

			_, err := loadConfig([]string{"-config", path})
			if err == nil {
				t.Fatalf("loadConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("loadConfig() error = %v, want key %s", err, tc.wantKey)
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

func requiredConfigLines(dir string) []string {
	return []string{
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=" + filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
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
