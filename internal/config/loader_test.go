package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
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
	t.Chdir(dir)

	cfg, err := Load(Options{Args: []string{"-config", filepath.Base(path)}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != dir+"/node1" || cfg.Cluster.ListenAddr != "127.0.0.1:7001" {
		t.Fatalf("loaded config = %#v", cfg)
	}
	if cfg.ConfigPath != path {
		t.Fatalf("ConfigPath = %q, want %q", cfg.ConfigPath, path)
	}
	if cfg.Cluster.Channel.StoreAppendWorkers != 0 || cfg.Cluster.Channel.StoreApplyWorkers != 0 || cfg.Cluster.Channel.RPCWorkers != 0 {
		t.Fatalf("omitted Channel workers = %d/%d/%d, want runtime-derived zero values",
			cfg.Cluster.Channel.StoreAppendWorkers, cfg.Cluster.Channel.StoreApplyWorkers, cfg.Cluster.Channel.RPCWorkers)
	}
}

func TestLoadRecordsMatchedDefaultConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"
`)

	cfg, err := Load(Options{Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ConfigPath != path {
		t.Fatalf("ConfigPath = %q, want matched default %q", cfg.ConfigPath, path)
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
	if cfg.ConfigPath != "" {
		t.Fatalf("ConfigPath = %q, want empty for env-only startup", cfg.ConfigPath)
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

func TestLoadPresenceTouchMaxRoutesPerFlushFromTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"

[presence]
touch_batch_size = 1024
touch_max_routes_per_flush = 4096
`)

	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Presence.TouchMaxRoutesPerFlush != 4096 {
		t.Fatalf("Presence.TouchMaxRoutesPerFlush = %d, want 4096", cfg.Presence.TouchMaxRoutesPerFlush)
	}
}

func TestLoadPresenceTouchMaxRoutesPerFlushFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=" + dir + "/node1",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_PRESENCE_TOUCH_BATCH_SIZE=1024",
		"WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH=8192",
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Presence.TouchMaxRoutesPerFlush != 8192 {
		t.Fatalf("Presence.TouchMaxRoutesPerFlush = %d, want 8192", cfg.Presence.TouchMaxRoutesPerFlush)
	}
}

func TestLoadRejectsInvalidPresenceTouchMaxRoutesPerFlush(t *testing.T) {
	tests := []struct {
		name      string
		batchSize string
		maxRoutes string
	}{
		{name: "zero", batchSize: "512", maxRoutes: "0"},
		{name: "negative", batchSize: "512", maxRoutes: "-1"},
		{name: "below batch", batchSize: "1024", maxRoutes: "1023"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(Options{Environ: []string{
				"PATH=" + os.Getenv("PATH"),
				"WK_NODE_ID=1",
				"WK_NODE_DATA_DIR=/tmp/node1",
				"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
				"WK_PRESENCE_TOUCH_BATCH_SIZE=" + tt.batchSize,
				"WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH=" + tt.maxRoutes,
			}})
			if err == nil {
				t.Fatal("Load() error = nil, want touch flush budget validation error")
			}
			if !strings.Contains(err.Error(), "WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH") {
				t.Fatalf("Load() error = %v, want WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH", err)
			}
		})
	}
}

func TestLoadPrometheusQueryBaseURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"

[prometheus]
query_base_url = "http://prometheus:9090/"
`)
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_PROMETHEUS_QUERY_BASE_URL=http://prometheus:9090",
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Observability.Prometheus.QueryBaseURL != "http://prometheus:9090" {
		t.Fatalf("QueryBaseURL = %q, want env override", cfg.Observability.Prometheus.QueryBaseURL)
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

func TestSchemaAndBuilderKeysMatch(t *testing.T) {
	schema := schemaByEnvKey()
	builder := make(map[string]struct{}, len(supportedConfigKeysForBuilder()))
	for _, key := range supportedConfigKeysForBuilder() {
		if _, duplicate := builder[key]; duplicate {
			t.Fatalf("builder key inventory contains duplicate %s", key)
		}
		builder[key] = struct{}{}
		if _, ok := schema[key]; !ok {
			t.Fatalf("schema missing builder key %s", key)
		}
	}
	for key := range schema {
		if _, ok := builder[key]; !ok {
			t.Fatalf("builder key inventory missing schema key %s", key)
		}
	}
}

func TestLoadEnvJSONListOverridesTOMLNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"

[[cluster.nodes]]
id = 1
addr = "old-node:7000"
`)
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		`WK_CLUSTER_NODES=[{"id":1,"addr":"new-node:7000"}]`,
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Cluster.Control.Voters) != 1 || cfg.Cluster.Control.Voters[0].Addr != "new-node:7000" {
		t.Fatalf("voters = %#v, want env replacement", cfg.Cluster.Control.Voters)
	}
}

func TestLoadRejectsEmptyRequiredEnvValue(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want empty required value")
	}
	if !strings.Contains(err.Error(), "WK_NODE_DATA_DIR") {
		t.Fatalf("Load() error = %v, want required key", err)
	}
}

func TestLoadRejectsMalformedJSONListEnv(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_NODES=[",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want malformed JSON list")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_NODES") {
		t.Fatalf("Load() error = %v, want WK_CLUSTER_NODES", err)
	}
}

func TestLoadBuildsRedactedStartupConfigSnapshot(t *testing.T) {
	cfg, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/wukongim-node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_JOIN_TOKEN=join-secret",
		"WK_MANAGER_JWT_SECRET=jwt-secret",
		`WK_MANAGER_USERS=[{"username":"admin","password":"plain"}]`,
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	snapshot := cfg.StartupConfigSnapshot
	if snapshot.NodeID != 1 || snapshot.Source != "effective_startup_config" || !snapshot.RequiresRestart {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	text := snapshotText(snapshot)
	for _, forbidden := range []string{"join-secret", "jwt-secret", "plain", "/tmp/wukongim-node"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("snapshot leaked %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{"WK_NODE_ID=1", "WK_CLUSTER_JOIN_TOKEN=******", "WK_MANAGER_JWT_SECRET=******", "WK_MANAGER_USERS=******"} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot text %q missing %q", text, want)
		}
	}
}

func TestLoadBuildsNormalizedEffectiveCriticalConfigSnapshot(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	cfg, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/wukongim-node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7001"},{"id":2,"addr":"127.0.0.1:7002"},{"id":3,"addr":"127.0.0.1:7003"}]`,
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT=0",
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=0",
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=0",
		"WK_CLUSTER_CHANNEL_RPC_WORKERS=50",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP=0",
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wants := map[string]struct {
		value  string
		source string
	}{
		"WK_CLUSTER_INITIAL_SLOT_COUNT":                {value: "1", source: managementusecase.NodeConfigValueSourceDefault},
		"WK_CLUSTER_HASH_SLOT_COUNT":                   {value: "256", source: managementusecase.NodeConfigValueSourceDefault},
		"WK_CLUSTER_SLOT_REPLICA_N":                    {value: "3", source: managementusecase.NodeConfigValueSourceDerived},
		"WK_CLUSTER_CHANNEL_REPLICA_N":                 {value: "3", source: managementusecase.NodeConfigValueSourceDerived},
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT":             {value: "4", source: managementusecase.NodeConfigValueSourceDerived},
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS":      {value: "8", source: managementusecase.NodeConfigValueSourceDerived},
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS":       {value: "8", source: managementusecase.NodeConfigValueSourceDerived},
		"WK_CLUSTER_CHANNEL_RPC_WORKERS":               {value: "50", source: managementusecase.NodeConfigValueSourceEnvironment},
		"WK_GATEWAY_GNET_MULTICORE":                    {value: "true", source: managementusecase.NodeConfigValueSourceDerived},
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP":               {value: "2", source: managementusecase.NodeConfigValueSourceDerived},
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS":        {value: "128", source: managementusecase.NodeConfigValueSourceDefault},
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY": {value: "131072", source: managementusecase.NodeConfigValueSourceDefault},
		"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY":     {value: "100", source: managementusecase.NodeConfigValueSourceDefault},
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS":     {value: "100000", source: managementusecase.NodeConfigValueSourceDefault},
	}
	for key, want := range wants {
		item, ok := snapshotItem(cfg.StartupConfigSnapshot, key)
		if !ok {
			t.Errorf("snapshot missing effective key %s", key)
			continue
		}
		if item.Value != want.value || item.Source != want.source {
			t.Errorf("snapshot %s = value %q source %q, want value %q source %q", key, item.Value, item.Source, want.value, want.source)
		}
	}
}

func snapshotItem(snapshot managementusecase.NodeConfigSnapshot, key string) (managementusecase.NodeConfigItem, bool) {
	for _, group := range snapshot.Groups {
		for _, item := range group.Items {
			if item.Key == key {
				return item, true
			}
		}
	}
	return managementusecase.NodeConfigItem{}, false
}

func snapshotText(snapshot managementusecase.NodeConfigSnapshot) string {
	var b strings.Builder
	for _, group := range snapshot.Groups {
		b.WriteString(group.ID)
		b.WriteByte(' ')
		for _, item := range group.Items {
			b.WriteString(item.Key)
			b.WriteByte('=')
			b.WriteString(item.Value)
			b.WriteByte(' ')
		}
	}
	return b.String()
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
