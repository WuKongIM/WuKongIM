package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
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
	if cfg.Cluster.Slots.InitialSlotCount != 10 {
		t.Fatalf("InitialSlotCount = %d, want 10", cfg.Cluster.Slots.InitialSlotCount)
	}
	assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
	if cfg.Cluster.Storage.CommitShards != 1 {
		t.Fatalf("CommitShards = %d, want 1", cfg.Cluster.Storage.CommitShards)
	}
}

func TestScriptThreeNodeClusterUsesQPSValidatedRPCDefaults(t *testing.T) {
	for node := 1; node <= 3; node++ {
		path := filepath.Join("..", "..", "scripts", "wukongim", fmt.Sprintf("wukongim-node%d.toml", node))
		cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
		if err != nil {
			t.Fatalf("Load(%s) error = %v", path, err)
		}
		assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
		if cfg.Cluster.Storage.CommitShards != 1 {
			t.Fatalf("%s CommitShards = %d, want 1", path, cfg.Cluster.Storage.CommitShards)
		}
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
	if cfg.Cluster.Slots.InitialSlotCount != 10 || cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("cmd topology = logical Slot Groups %d / physical hash slots %d, want 10 / 256", cfg.Cluster.Slots.InitialSlotCount, cfg.Cluster.Slots.HashSlotCount)
	}
	assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
}

func TestScriptSingleNodeClusterUsesTenLogicalAndDefaultPhysicalSlots(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "wukongim", "wukongim.toml")
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(script example) error = %v", err)
	}
	if cfg.Cluster.Slots.InitialSlotCount != 10 || cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("script topology = logical Slot Groups %d / physical hash slots %d, want 10 / 256", cfg.Cluster.Slots.InitialSlotCount, cfg.Cluster.Slots.HashSlotCount)
	}
	assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
	if cfg.Cluster.Storage.CommitShards != 1 {
		t.Fatalf("%s CommitShards = %d, want 1", path, cfg.Cluster.Storage.CommitShards)
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

func TestGatewayExamplesUseQualifiedAsyncSendBatchLimit(t *testing.T) {
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

	foundGateway := 0
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		if !strings.Contains(string(content), "[gateway]") {
			continue
		}
		foundGateway++
		want := "# Maximum SEND frames coalesced into one asynchronous gateway dispatch batch.\n" +
			"# The 128-record limit is qualified for sustained high-QPS workloads.\n" +
			"default_session_async_send_batch_max_records = 128"
		if strings.Contains(filepath.ToSlash(file), "scripts/wukongim/wukongim-node") {
			want = "# Maximum SEND frames coalesced into one asynchronous gateway dispatch batch.\n" +
				"# The reviewed chat-lifecycle profile keeps one SEND per dispatch because each\n" +
				"# sender already allows only one in-flight SENDACK operation.\n" +
				"default_session_async_send_batch_max_records = 1"
		}
		if !strings.Contains(string(content), want) {
			t.Errorf("%s must document the qualified gateway async SEND batch limit", file)
		}
	}
	if foundGateway == 0 {
		t.Fatal("no shipped [gateway] examples found")
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

	want := "# Number of stable Channel-order delivery shards processed concurrently by this node.\n" +
		"# Plans for one Channel stay FIFO on one shard; different Channels may run in parallel.\n" +
		"# This is independent from channel_append.recipient_authority_dispatch_concurrency.\n" +
		"recipient_worker_concurrency = 320"
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

func TestDockerThreeNodeClusterUsesQualified2000QPSRuntimeProfile(t *testing.T) {
	for node := 1; node <= 3; node++ {
		path := filepath.Join("..", "..", "docker", "conf", fmt.Sprintf("node%d.toml", node))
		cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
		if err != nil {
			t.Fatalf("Load(%s) error = %v", path, err)
		}
		assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
	}
}

func assertQualified2000QPSRuntimeProfile(t *testing.T, snapshot managementusecase.NodeConfigSnapshot) {
	t.Helper()
	wants := map[string]string{
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS":      "128",
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS":       "8",
		"WK_CLUSTER_CHANNEL_RPC_WORKERS":               "96",
		"WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS":       "8",
		"WK_GATEWAY_GNET_MULTICORE":                    "true",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP":               "4",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS":        "1000",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY": "131072",
		"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY":     "320",
	}
	for key, want := range wants {
		item, ok := snapshotItem(snapshot, key)
		if !ok {
			t.Errorf("startup snapshot missing %s", key)
			continue
		}
		if item.Value != want {
			t.Errorf("startup snapshot %s = %s, want %s", key, item.Value, want)
		}
	}
}
