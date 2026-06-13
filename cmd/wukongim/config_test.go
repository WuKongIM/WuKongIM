package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	app "github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/stretchr/testify/require"
)

func clusterConfigDurationField(t *testing.T, cfg *app.ClusterConfig, name string) time.Duration {
	t.Helper()

	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("ClusterConfig is missing field %s", name)
	}
	value := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	return time.Duration(value.Int())
}

func clusterConfigIntField(t *testing.T, cfg *app.ClusterConfig, name string) int {
	t.Helper()

	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("ClusterConfig is missing field %s", name)
	}
	value := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	return int(value.Int())
}

func writeConf(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	body := strings.Join(lines, "\n") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})
}

func TestLoadConfigParsesConfFileIntoAppConfig(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "node-1")
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_NAME=node-1",
		"WK_NODE_DATA_DIR="+dataDir,
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_MAX_CHANNELS=2048",
		"WK_CLUSTER_CHANNEL_IDLE_TIMEOUT=30m",
		"WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL=1m",
		"WK_CLUSTER_CHANNEL_EXECUTION_MODE=pooled",
		"WK_CLUSTER_CHANNEL_EXECUTION_WORKERS=8",
		"WK_CLUSTER_CHANNEL_EXECUTION_QUEUE_SIZE=4096",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
		"WK_API_LISTEN_ADDR=127.0.0.1:8080",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cfg.Node.ID)
	require.Equal(t, "node-1", cfg.Node.Name)
	require.Equal(t, dataDir, cfg.Node.DataDir)
	require.Equal(t, "127.0.0.1:7000", cfg.Cluster.ListenAddr)
	require.Equal(t, 2048, cfg.Cluster.MaxChannels)
	require.Equal(t, 30*time.Minute, cfg.Cluster.ChannelIdleTimeout)
	require.Equal(t, time.Minute, cfg.Cluster.ChannelIdleScanInterval)
	require.Equal(t, "pooled", cfg.Cluster.ChannelExecutionMode)
	require.Equal(t, 8, cfg.Cluster.ChannelExecutionWorkers)
	require.Equal(t, 4096, cfg.Cluster.ChannelExecutionQueueSize)
	require.Equal(t, "127.0.0.1:8080", cfg.API.ListenAddr)
	require.Len(t, cfg.Cluster.Nodes, 1)
	require.Empty(t, cfg.Cluster.Slots)
	require.Len(t, cfg.Gateway.Listeners, 1)
}

func TestLoadConfigDefaultsChannelExecutionModeToPooled(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "pooled", cfg.Cluster.ChannelExecutionMode)
}

func TestLoadConfigParsesTestMode(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
		"WK_TEST_MODE=true",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.True(t, cfg.TestMode)
}

func TestLoadConfigParsesBenchAPIConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:11110",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:11110"}]`,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
		"WK_BENCH_API_ENABLE=true",
		"WK_BENCH_API_MAX_BATCH_SIZE=123",
		"WK_BENCH_API_MAX_PAYLOAD_BYTES=456789",
	)

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.Bench.APIEnabled)
	require.Equal(t, 123, cfg.Bench.APIMaxBatchSize)
	require.Equal(t, int64(456789), cfg.Bench.APIMaxPayloadBytes)
}

func TestLoadConfigParsesPluginConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_PLUGIN_ENABLE=true",
		"WK_PLUGIN_DIR="+filepath.Join(dir, "plugins"),
		"WK_PLUGIN_SOCKET_PATH="+filepath.Join(dir, "run", "plugin.sock"),
		"WK_PLUGIN_SANDBOX_DIR="+filepath.Join(dir, "sandbox"),
		"WK_PLUGIN_STATE_DIR="+filepath.Join(dir, "state"),
		"WK_PLUGIN_TIMEOUT=7s",
		"WK_PLUGIN_HOT_RELOAD=false",
		"WK_PLUGIN_FAIL_OPEN=true",
	)

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.Plugin.Enable)
	require.Equal(t, filepath.Join(dir, "plugins"), cfg.Plugin.Dir)
	require.Equal(t, filepath.Join(dir, "run", "plugin.sock"), cfg.Plugin.SocketPath)
	require.Equal(t, filepath.Join(dir, "sandbox"), cfg.Plugin.SandboxDir)
	require.Equal(t, filepath.Join(dir, "state"), cfg.Plugin.StateDir)
	require.Equal(t, 7*time.Second, cfg.Plugin.Timeout)
	require.False(t, cfg.Plugin.HotReload)
	require.True(t, cfg.Plugin.FailOpen)
}

func TestLoadConfigPrefersEnvironmentVariablesForPluginConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_PLUGIN_ENABLE=false",
		"WK_PLUGIN_DIR="+filepath.Join(dir, "file-plugins"),
		"WK_PLUGIN_TIMEOUT=7s",
		"WK_PLUGIN_HOT_RELOAD=true",
		"WK_PLUGIN_FAIL_OPEN=false",
	)
	t.Setenv("WK_PLUGIN_ENABLE", "true")
	t.Setenv("WK_PLUGIN_DIR", filepath.Join(dir, "env-plugins"))
	t.Setenv("WK_PLUGIN_SOCKET_PATH", filepath.Join(dir, "env-run", "plugin.sock"))
	t.Setenv("WK_PLUGIN_SANDBOX_DIR", filepath.Join(dir, "env-sandbox"))
	t.Setenv("WK_PLUGIN_STATE_DIR", filepath.Join(dir, "env-state"))
	t.Setenv("WK_PLUGIN_TIMEOUT", "9s")
	t.Setenv("WK_PLUGIN_HOT_RELOAD", "false")
	t.Setenv("WK_PLUGIN_FAIL_OPEN", "true")

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.Plugin.Enable)
	require.Equal(t, filepath.Join(dir, "env-plugins"), cfg.Plugin.Dir)
	require.Equal(t, filepath.Join(dir, "env-run", "plugin.sock"), cfg.Plugin.SocketPath)
	require.Equal(t, filepath.Join(dir, "env-sandbox"), cfg.Plugin.SandboxDir)
	require.Equal(t, filepath.Join(dir, "env-state"), cfg.Plugin.StateDir)
	require.Equal(t, 9*time.Second, cfg.Plugin.Timeout)
	require.False(t, cfg.Plugin.HotReload)
	require.True(t, cfg.Plugin.FailOpen)
}

func TestLoadConfigRejectsInvalidPluginTimeout(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_PLUGIN_TIMEOUT=-1s",
	)

	_, err := loadConfig(path)
	require.ErrorContains(t, err, "plugin timeout")
}

func TestLoadConfigParsesClusterSeedJoinKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=4",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-4"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7003",
		"WK_CLUSTER_ADVERTISE_ADDR=wk-node4:7000",
		`WK_CLUSTER_SEEDS=["wk-node1:7000","wk-node2:7000"]`,
		"WK_CLUSTER_JOIN_TOKEN=join-secret",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=1",
		"WK_CLUSTER_HASH_SLOT_COUNT=256",
		"WK_CLUSTER_CONTROLLER_REPLICA_N=1",
		"WK_CLUSTER_SLOT_REPLICA_N=1",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5103","transport":"gnet","protocol":"wkproto"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, []string{"wk-node1:7000", "wk-node2:7000"}, cfg.Cluster.Seeds)
	require.Equal(t, "wk-node4:7000", cfg.Cluster.AdvertiseAddr)
	require.Equal(t, "join-secret", cfg.Cluster.JoinToken)
}

func TestLoadConfigUsesBuiltInDefaultsWhenOptionalConfKeysAreMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0:5001", cfg.API.ListenAddr)
	require.Len(t, cfg.Gateway.Listeners, 2)
	require.True(t, cfg.Observability.MetricsEnabled)
	require.True(t, cfg.Observability.NetworkEnabled)
	require.True(t, cfg.Observability.HealthDetailEnabled)
	require.False(t, cfg.Observability.DebugAPIEnabled)
	require.True(t, cfg.Observability.Diagnostics.Enabled)
	require.Equal(t, 50000, cfg.Observability.Diagnostics.BufferSize)
	require.Equal(t, 0.01, cfg.Observability.Diagnostics.SampleRate)
	require.Equal(t, 500*time.Millisecond, cfg.Observability.Diagnostics.SlowThreshold)
	require.Equal(t, 1.0, cfg.Observability.Diagnostics.ErrorSampleRate)
}

func TestLoadConfigParsesObservabilityFlags(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_METRICS_ENABLE=false",
		"WK_NETWORK_OBSERVABILITY_ENABLE=false",
		"WK_HEALTH_DETAIL_ENABLE=false",
		"WK_DEBUG_API_ENABLE=true",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.False(t, cfg.Observability.MetricsEnabled)
	require.False(t, cfg.Observability.NetworkEnabled)
	require.False(t, cfg.Observability.HealthDetailEnabled)
	require.True(t, cfg.Observability.DebugAPIEnabled)
}

func TestLoadConfigParsesDiagnosticsConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_DIAGNOSTICS_ENABLE=false",
		"WK_DIAGNOSTICS_BUFFER_SIZE=1234",
		"WK_DIAGNOSTICS_SAMPLE_RATE=0.25",
		"WK_DIAGNOSTICS_SLOW_THRESHOLD_MS=750",
		"WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=0.5",
		`WK_DIAGNOSTICS_DEBUG_MATCHES=[{"client_msg_no":"c1","ttl_seconds":60,"sample_rate":1.0}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.False(t, cfg.Observability.Diagnostics.Enabled)
	require.Equal(t, 1234, cfg.Observability.Diagnostics.BufferSize)
	require.Equal(t, 0.25, cfg.Observability.Diagnostics.SampleRate)
	require.Equal(t, 750*time.Millisecond, cfg.Observability.Diagnostics.SlowThreshold)
	require.Equal(t, 0.5, cfg.Observability.Diagnostics.ErrorSampleRate)
	require.Len(t, cfg.Observability.Diagnostics.DebugMatches, 1)
	require.Equal(t, "c1", cfg.Observability.Diagnostics.DebugMatches[0].ClientMsgNo)
	require.Equal(t, 60, cfg.Observability.Diagnostics.DebugMatches[0].TTLSeconds)
	require.Equal(t, 1.0, cfg.Observability.Diagnostics.DebugMatches[0].SampleRate)
}

func TestLoadConfigParsesConversationActiveHintTuning(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CONVERSATION_ACTIVE_HINT_FLUSH_INTERVAL=3s",
		"WK_CONVERSATION_ACTIVE_HINT_TTL=45m",
		"WK_CONVERSATION_ACTIVE_HINT_BARRIER_TTL=50m",
		"WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS=12345",
		"WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS_PER_UID=321",
		"WK_CONVERSATION_ACTIVE_HINT_FLUSH_BATCH_SIZE=64",
		"WK_CONVERSATION_GROUP_ACTIVE_FANOUT_INTERVAL=7m",
		"WK_CONVERSATION_GROUP_ACTIVE_FANOUT_MAX_SUBSCRIBERS=42",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, cfg.Conversation.ActiveHintFlushInterval)
	require.Equal(t, 45*time.Minute, cfg.Conversation.ActiveHintTTL)
	require.Equal(t, 50*time.Minute, cfg.Conversation.ActiveHintBarrierTTL)
	require.Equal(t, 12345, cfg.Conversation.ActiveHintMaxHints)
	require.Equal(t, 321, cfg.Conversation.ActiveHintMaxHintsPerUID)
	require.Equal(t, 64, cfg.Conversation.ActiveHintFlushBatchSize)
	require.Equal(t, 7*time.Minute, cfg.Conversation.GroupActiveFanoutInterval)
	require.Equal(t, 42, cfg.Conversation.GroupActiveFanoutMaxSubscribers)
}

func TestLoadConfigParsesChannelBootstrapDefaultMinISR(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)
	t.Setenv("WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR", "3")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 3, cfg.Cluster.ChannelBootstrapDefaultMinISR)
}

func TestLoadConfigRejectsExplicitZeroChannelBootstrapDefaultMinISR(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)
	t.Setenv("WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR", "0")

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "channel bootstrap default min isr")
}

func TestLoadConfigRejectsExplicitNegativeChannelBootstrapDefaultMinISR(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)
	t.Setenv("WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR", "-1")

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "channel bootstrap default min isr")
}

func TestLoadConfigPrefersEnvironmentVariablesOverConfValues(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_API_LISTEN_ADDR=127.0.0.1:8080",
	)
	t.Setenv("WK_API_LISTEN_ADDR", "127.0.0.1:9090")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9090", cfg.API.ListenAddr)
}

func TestLoadConfigParsesRaftSnapshotStoragePaths(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_PATH="+filepath.Join(dir, "slot-snapshots"),
		"WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH="+filepath.Join(dir, "controller-snapshots"),
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "slot-snapshots"), cfg.Storage.RaftSnapshotPath)
	require.Equal(t, filepath.Join(dir, "controller-snapshots"), cfg.Storage.ControllerRaftSnapshotPath)
}

func TestConfigExampleDocumentsRaftSnapshotStorageKeys(t *testing.T) {
	content, err := os.ReadFile("../../wukongim.conf.example")
	require.NoError(t, err)

	example := string(content)
	require.Contains(t, example, "WK_STORAGE_RAFT_SNAPSHOT_PATH")
	require.Contains(t, example, "WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH")
	require.Contains(t, example, "WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=8MiB")
	require.Contains(t, example, "WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=30m")
}

func TestLoadConfigParsesRaftSnapshotChunkSizeAsMiB(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=8MiB",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, uint64(8<<20), cfg.Storage.RaftSnapshotChunkSize)
}

func TestLoadConfigParsesRaftSnapshotChunkSizeAsDecimalBytes(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=8388608",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, uint64(8<<20), cfg.Storage.RaftSnapshotChunkSize)
}

func TestLoadConfigRejectsZeroRaftSnapshotChunkSize(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=0",
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE")
}

func TestLoadConfigRejectsInvalidRaftSnapshotChunkSizeUnits(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=8MB",
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE")
}

func TestLoadConfigParsesRaftSnapshotGCGrace(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=45m",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 45*time.Minute, cfg.Storage.RaftSnapshotGCGrace)
}

func TestLoadConfigKeepsExplicitZeroRaftSnapshotGCGrace(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=0",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, time.Duration(0), cfg.Storage.RaftSnapshotGCGrace)
}

func TestLoadConfigRejectsNegativeRaftSnapshotGCGrace(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=-1s",
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "snapshot gc grace")
}

func TestLoadConfigPrefersEnvironmentVariablesForRaftSnapshotStorage(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_STORAGE_RAFT_SNAPSHOT_PATH="+filepath.Join(dir, "file-slot-snapshots"),
		"WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH="+filepath.Join(dir, "file-controller-snapshots"),
		"WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=4MiB",
		"WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=15m",
	)
	t.Setenv("WK_STORAGE_RAFT_SNAPSHOT_PATH", filepath.Join(dir, "env-slot-snapshots"))
	t.Setenv("WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH", filepath.Join(dir, "env-controller-snapshots"))
	t.Setenv("WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE", "16777216")
	t.Setenv("WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE", "1h")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "env-slot-snapshots"), cfg.Storage.RaftSnapshotPath)
	require.Equal(t, filepath.Join(dir, "env-controller-snapshots"), cfg.Storage.ControllerRaftSnapshotPath)
	require.Equal(t, uint64(16<<20), cfg.Storage.RaftSnapshotChunkSize)
	require.Equal(t, time.Hour, cfg.Storage.RaftSnapshotGCGrace)
}

func TestLoadConfigParsesExternalRouteAddresses(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_EXTERNAL_TCPADDR=im.example.com:15100",
		"WK_EXTERNAL_WSADDR=ws://im.example.com:15200",
		"WK_EXTERNAL_WSSADDR=wss://im.example.com:15300",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "im.example.com:15100", cfg.API.ExternalTCPAddr)
	require.Equal(t, "ws://im.example.com:15200", cfg.API.ExternalWSAddr)
	require.Equal(t, "wss://im.example.com:15300", cfg.API.ExternalWSSAddr)
}

func TestLoadConfigParsesManagerSettings(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
		"WK_MANAGER_LISTEN_ADDR=127.0.0.1:5301",
		"WK_MANAGER_AUTH_ON=true",
		"WK_MANAGER_JWT_SECRET=test-secret",
		"WK_MANAGER_JWT_ISSUER=wukongim-manager",
		"WK_MANAGER_JWT_EXPIRE=24h",
		`WK_MANAGER_USERS=[{"username":"admin","password":"secret","permissions":[{"resource":"cluster.node","actions":["r"]}]}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:5301", cfg.Manager.ListenAddr)
	require.True(t, cfg.Manager.AuthOn)
	require.Equal(t, "test-secret", cfg.Manager.JWTSecret)
	require.Equal(t, "wukongim-manager", cfg.Manager.JWTIssuer)
	require.Equal(t, 24*time.Hour, cfg.Manager.JWTExpire)
	require.Len(t, cfg.Manager.Users, 1)
	require.Equal(t, "admin", cfg.Manager.Users[0].Username)
	require.Len(t, cfg.Manager.Users[0].Permissions, 1)
	require.Equal(t, "cluster.node", cfg.Manager.Users[0].Permissions[0].Resource)
	require.Equal(t, []string{"r"}, cfg.Manager.Users[0].Permissions[0].Actions)
}

func TestLoadConfigLeavesManagerDisabledWhenListenAddrIsEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
		"WK_MANAGER_AUTH_ON=true",
		"WK_MANAGER_JWT_SECRET=test-secret",
		"WK_MANAGER_JWT_ISSUER=wukongim-manager",
		"WK_MANAGER_JWT_EXPIRE=24h",
		`WK_MANAGER_USERS=[{"username":"admin","password":"secret","permissions":[{"resource":"cluster.node","actions":["r"]}]}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "", cfg.Manager.ListenAddr)
}

func TestLoadConfigParsesSendPathTuning(t *testing.T) {
	dir := t.TempDir()
	basePath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	t.Run("defaults", func(t *testing.T) {
		cfg, err := loadConfig(basePath)
		require.NoError(t, err)

		require.Equal(t, 1*time.Second, clusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval"))
		require.Equal(t, 1*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait"))
		require.Equal(t, 64, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords"))
		require.Equal(t, 64*1024, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes"))
		require.Equal(t, 200*time.Microsecond, cfg.Cluster.CommitCoordinatorFlushWindow)
		require.Zero(t, cfg.Cluster.CommitCoordinatorMaxRequests)
		require.Zero(t, cfg.Cluster.CommitCoordinatorMaxRecords)
		require.Zero(t, cfg.Cluster.CommitCoordinatorMaxBytes)
		require.Equal(t, 1*time.Second, cfg.Cluster.DataPlaneRPCTimeout)
		require.Equal(t, 1, cfg.Cluster.DataPlanePoolSize)
		require.Equal(t, 2, cfg.Cluster.DataPlaneMaxFetchInflight)
		require.Equal(t, 2, cfg.Cluster.DataPlaneMaxPendingFetch)
	})

	t.Run("explicit overrides", func(t *testing.T) {
		t.Setenv("WK_CLUSTER_FOLLOWER_REPLICATION_RETRY_INTERVAL", "250ms")
		t.Setenv("WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_WAIT", "2ms")
		t.Setenv("WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_RECORDS", "128")
		t.Setenv("WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_BYTES", "262144")
		t.Setenv("WK_CLUSTER_DATA_PLANE_POOL_SIZE", "8")
		t.Setenv("WK_CLUSTER_DATA_PLANE_MAX_FETCH_INFLIGHT", "16")
		t.Setenv("WK_CLUSTER_DATA_PLANE_MAX_PENDING_FETCH", "16")
		t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW", "500us")
		t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS", "32")
		t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS", "512")
		t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES", "524288")

		cfg, err := loadConfig(basePath)
		require.NoError(t, err)

		require.Equal(t, 250*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval"))
		require.Equal(t, 2*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait"))
		require.Equal(t, 128, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords"))
		require.Equal(t, 256*1024, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes"))
		require.Equal(t, 500*time.Microsecond, cfg.Cluster.CommitCoordinatorFlushWindow)
		require.Equal(t, 32, cfg.Cluster.CommitCoordinatorMaxRequests)
		require.Equal(t, 512, cfg.Cluster.CommitCoordinatorMaxRecords)
		require.Equal(t, 512*1024, cfg.Cluster.CommitCoordinatorMaxBytes)
		require.Equal(t, 8, cfg.Cluster.DataPlanePoolSize)
		require.Equal(t, 16, cfg.Cluster.DataPlaneMaxFetchInflight)
		require.Equal(t, 16, cfg.Cluster.DataPlaneMaxPendingFetch)
	})
}

func TestLoadConfigRejectsExplicitInvalidSendPathTuning(t *testing.T) {
	dir := t.TempDir()
	basePath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{
			name:    "follower replication retry interval",
			key:     "WK_CLUSTER_FOLLOWER_REPLICATION_RETRY_INTERVAL",
			value:   "0s",
			wantErr: "follower replication retry interval",
		},
		{
			name:    "append group commit max wait",
			key:     "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_WAIT",
			value:   "0s",
			wantErr: "append group commit max wait",
		},
		{
			name:    "append group commit max records",
			key:     "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_RECORDS",
			value:   "0",
			wantErr: "append group commit max records",
		},
		{
			name:    "append group commit max bytes",
			key:     "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_BYTES",
			value:   "0",
			wantErr: "append group commit max bytes",
		},
		{
			name:    "commit coordinator flush window",
			key:     "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW",
			value:   "0s",
			wantErr: "commit coordinator flush window",
		},
		{
			name:    "negative commit coordinator max requests",
			key:     "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS",
			value:   "-1",
			wantErr: "commit coordinator max requests",
		},
		{
			name:    "negative commit coordinator max records",
			key:     "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS",
			value:   "-1",
			wantErr: "commit coordinator max records",
		},
		{
			name:    "negative commit coordinator max bytes",
			key:     "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES",
			value:   "-1",
			wantErr: "commit coordinator max bytes",
		},
		{
			name:    "negative follower replication retry interval",
			key:     "WK_CLUSTER_FOLLOWER_REPLICATION_RETRY_INTERVAL",
			value:   "-1s",
			wantErr: "follower replication retry interval",
		},
		{
			name:    "negative append group commit max records",
			key:     "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_RECORDS",
			value:   "-1",
			wantErr: "append group commit max records",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)

			_, err := loadConfig(basePath)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLoadConfigParsesChannelMessageRetention(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CHANNEL_MESSAGE_RETENTION_TTL=168h",
		"WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL=30m",
		"WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE=64",
		"WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES=5000",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 168*time.Hour, cfg.ChannelMessageRetention.TTL)
	require.Equal(t, 30*time.Minute, cfg.ChannelMessageRetention.ScanInterval)
	require.Equal(t, 64, cfg.ChannelMessageRetention.ChannelBatchSize)
	require.Equal(t, 5000, cfg.ChannelMessageRetention.MaxTrimMessages)
}

func TestLoadConfigPrefersEnvironmentVariablesForChannelMessageRetention(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CHANNEL_MESSAGE_RETENTION_TTL=168h",
		"WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL=30m",
		"WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE=64",
		"WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES=5000",
	)
	t.Setenv("WK_CHANNEL_MESSAGE_RETENTION_TTL", "24h")
	t.Setenv("WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL", "5m")
	t.Setenv("WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE", "32")
	t.Setenv("WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES", "2500")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 24*time.Hour, cfg.ChannelMessageRetention.TTL)
	require.Equal(t, 5*time.Minute, cfg.ChannelMessageRetention.ScanInterval)
	require.Equal(t, 32, cfg.ChannelMessageRetention.ChannelBatchSize)
	require.Equal(t, 2500, cfg.ChannelMessageRetention.MaxTrimMessages)
}

func TestLoadConfigParsesChannelPlaneConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CHANNEL_PLANE_REACTOR_COUNT=12",
		"WK_CHANNEL_PLANE_PEER_LANE_COUNT=3",
		"WK_CHANNEL_PLANE_PEER_BATCH_MAX_WAIT=750us",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 12, cfg.ChannelPlane.ReactorCount)
	require.Equal(t, 3, cfg.ChannelPlane.PeerLaneCount)
	require.Equal(t, 750*time.Microsecond, cfg.ChannelPlane.PeerBatchMaxWait)
}

func TestLoadConfigPrefersEnvironmentVariablesForChannelPlane(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CHANNEL_PLANE_REACTOR_COUNT=12",
		"WK_CHANNEL_PLANE_PEER_LANE_COUNT=3",
		"WK_CHANNEL_PLANE_PEER_BATCH_MAX_WAIT=750us",
	)
	t.Setenv("WK_CHANNEL_PLANE_REACTOR_COUNT", "16")
	t.Setenv("WK_CHANNEL_PLANE_PEER_LANE_COUNT", "5")
	t.Setenv("WK_CHANNEL_PLANE_PEER_BATCH_MAX_WAIT", "1ms")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 16, cfg.ChannelPlane.ReactorCount)
	require.Equal(t, 5, cfg.ChannelPlane.PeerLaneCount)
	require.Equal(t, time.Millisecond, cfg.ChannelPlane.PeerBatchMaxWait)
}

func TestLoadConfigParsesMessagePermissionConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_MESSAGE_PERSON_WHITELIST_ENABLED=true",
		"WK_MESSAGE_SYSTEM_DEVICE_ID=custom-device",
		"WK_MESSAGE_PERMISSION_CACHE_TTL=5s",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.True(t, cfg.Message.PersonWhitelistEnabled)
	require.Equal(t, "custom-device", cfg.Message.SystemDeviceID)
	require.Equal(t, 5*time.Second, cfg.Message.PermissionCacheTTL)
}

func TestLoadConfigPrefersEnvironmentVariablesForMessagePermissionConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_MESSAGE_PERSON_WHITELIST_ENABLED=false",
		"WK_MESSAGE_SYSTEM_DEVICE_ID=file-device",
		"WK_MESSAGE_PERMISSION_CACHE_TTL=1s",
	)
	t.Setenv("WK_MESSAGE_PERSON_WHITELIST_ENABLED", "true")
	t.Setenv("WK_MESSAGE_SYSTEM_DEVICE_ID", "env-device")
	t.Setenv("WK_MESSAGE_PERMISSION_CACHE_TTL", "3s")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.True(t, cfg.Message.PersonWhitelistEnabled)
	require.Equal(t, "env-device", cfg.Message.SystemDeviceID)
	require.Equal(t, 3*time.Second, cfg.Message.PermissionCacheTTL)
}

func TestLoadConfigParsesMessageUserRateLimitConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_MESSAGE_USER_RATE_LIMIT_ENABLED=true",
		"WK_MESSAGE_USER_RATE_LIMIT_RATE=42.5",
		"WK_MESSAGE_USER_RATE_LIMIT_BURST=77",
		"WK_MESSAGE_USER_RATE_LIMIT_BUCKET_SHARDS=64",
		"WK_MESSAGE_USER_RATE_LIMIT_IDLE_TTL=2m",
		"WK_MESSAGE_USER_RATE_LIMIT_MAX_BUCKETS=1234",
		"WK_MESSAGE_USER_RATE_LIMIT_SYSTEM_UID_BYPASS=false",
		"WK_MESSAGE_USER_RATE_LIMIT_PLUGIN_BYPASS=true",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.True(t, cfg.Message.UserRateLimitEnabled)
	require.Equal(t, 42.5, cfg.Message.UserRateLimitRate)
	require.Equal(t, 77, cfg.Message.UserRateLimitBurst)
	require.Equal(t, 64, cfg.Message.UserRateLimitBucketShards)
	require.Equal(t, 2*time.Minute, cfg.Message.UserRateLimitIdleTTL)
	require.Equal(t, 1234, cfg.Message.UserRateLimitMaxBuckets)
	require.False(t, cfg.Message.UserRateLimitSystemUIDBypass)
	require.True(t, cfg.Message.UserRateLimitPluginBypass)
}

func TestLoadConfigParsesDeliveryPresenceCacheConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_DELIVERY_PRESENCE_CACHE_TTL=5s",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, cfg.Delivery.PresenceCacheTTL)
}

func TestLoadConfigParsesDeliveryAckBatchConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_DELIVERY_ACK_BATCH_MAX_WAIT=15ms",
		"WK_DELIVERY_ACK_BATCH_MAX_SIZE=32",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 15*time.Millisecond, cfg.Delivery.AckBatchMaxWait)
	require.Equal(t, 32, cfg.Delivery.AckBatchMaxSize)
}

func TestLoadConfigPrefersEnvironmentVariablesForDeliveryPresenceCacheConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_DELIVERY_PRESENCE_CACHE_TTL=1s",
	)
	t.Setenv("WK_DELIVERY_PRESENCE_CACHE_TTL", "3s")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, cfg.Delivery.PresenceCacheTTL)
}

func TestChannelMigrationConfigFromEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_CHANNEL_MIGRATION_SCAN_INTERVAL=1s",
		"WK_CHANNEL_MIGRATION_SCAN_LIMIT=64",
		"WK_CHANNEL_MIGRATION_OWNER_LEASE_TTL=30s",
		"WK_CHANNEL_MIGRATION_RETRY_BACKOFF=1m",
		"WK_CHANNEL_MIGRATION_FENCE_TTL=1m",
		"WK_CHANNEL_MIGRATION_LEADER_LEASE_TTL=1m",
		"WK_CHANNEL_MIGRATION_CATCH_UP_STABLE_WINDOW=1s",
		"WK_CHANNEL_MIGRATION_CATCH_UP_LAG_THRESHOLD=0",
		"WK_CHANNEL_MIGRATION_MAX_CONCURRENT=4",
		"WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_SOURCE=1",
		"WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_TARGET=1",
		"WK_CHANNEL_MIGRATION_COMPLETED_RETENTION_TTL=24h",
		"WK_CHANNEL_MIGRATION_GC_LIMIT=128",
	)
	t.Setenv("WK_CHANNEL_MIGRATION_SCAN_INTERVAL", "250ms")
	t.Setenv("WK_CHANNEL_MIGRATION_SCAN_LIMIT", "17")
	t.Setenv("WK_CHANNEL_MIGRATION_OWNER_LEASE_TTL", "45s")
	t.Setenv("WK_CHANNEL_MIGRATION_RETRY_BACKOFF", "3s")
	t.Setenv("WK_CHANNEL_MIGRATION_FENCE_TTL", "90s")
	t.Setenv("WK_CHANNEL_MIGRATION_LEADER_LEASE_TTL", "2m")
	t.Setenv("WK_CHANNEL_MIGRATION_CATCH_UP_STABLE_WINDOW", "5s")
	t.Setenv("WK_CHANNEL_MIGRATION_CATCH_UP_LAG_THRESHOLD", "4")
	t.Setenv("WK_CHANNEL_MIGRATION_MAX_CONCURRENT", "8")
	t.Setenv("WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_SOURCE", "2")
	t.Setenv("WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_TARGET", "3")
	t.Setenv("WK_CHANNEL_MIGRATION_COMPLETED_RETENTION_TTL", "72h")
	t.Setenv("WK_CHANNEL_MIGRATION_GC_LIMIT", "9")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 250*time.Millisecond, cfg.ChannelMigration.ScanInterval)
	require.Equal(t, 17, cfg.ChannelMigration.ScanLimit)
	require.Equal(t, 45*time.Second, cfg.ChannelMigration.OwnerLeaseTTL)
	require.Equal(t, 3*time.Second, cfg.ChannelMigration.RetryBackoff)
	require.Equal(t, 90*time.Second, cfg.ChannelMigration.FenceTTL)
	require.Equal(t, 2*time.Minute, cfg.ChannelMigration.LeaderLeaseTTL)
	require.Equal(t, 5*time.Second, cfg.ChannelMigration.CatchUpStableWindow)
	require.Equal(t, uint64(4), cfg.ChannelMigration.CatchUpLagThreshold)
	require.Equal(t, 8, cfg.ChannelMigration.MaxConcurrent)
	require.Equal(t, 2, cfg.ChannelMigration.MaxConcurrentPerSource)
	require.Equal(t, 3, cfg.ChannelMigration.MaxConcurrentPerTarget)
	require.Equal(t, 72*time.Hour, cfg.ChannelMigration.CompletedRetentionTTL)
	require.Equal(t, 9, cfg.ChannelMigration.GCLimit)
}

func TestLoadConfigUsesDefaultSearchPathsWhenFlagPathIsEmpty(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "conf")
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	writeConf(t, confDir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)
	chdirForTest(t, dir)

	cfg, err := loadConfig("")
	require.NoError(t, err)
	require.Equal(t, uint64(1), cfg.Node.ID)
}

func TestLoadConfigAcceptsEnvironmentOnlyConfigurationWhenNoFileExists(t *testing.T) {
	dir := t.TempDir()
	chdirForTest(t, dir)

	t.Setenv("WK_NODE_ID", "1")
	t.Setenv("WK_NODE_DATA_DIR", filepath.Join(dir, "node-1"))
	t.Setenv("WK_CLUSTER_LISTEN_ADDR", "127.0.0.1:7000")
	t.Setenv("WK_CLUSTER_SLOT_COUNT", "1")
	t.Setenv("WK_CLUSTER_NODES", `[{"id":1,"addr":"127.0.0.1:7000"}]`)

	cfg, err := loadConfig("")
	require.NoError(t, err)
	require.Equal(t, uint64(1), cfg.Node.ID)
}

func TestLoadConfigReportsAttemptedDefaultPathsWhenConfigIsMissing(t *testing.T) {
	dir := t.TempDir()
	chdirForTest(t, dir)

	_, err := loadConfig("")
	require.ErrorContains(t, err, "./wukongim.conf")
	require.ErrorContains(t, err, "./conf/wukongim.conf")
	require.ErrorContains(t, err, "/etc/wukongim/wukongim.conf")
}

func TestLoadConfigRejectsLegacyClusterGroupsKey(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		`WK_CLUSTER_GROUPS=[{"id":1,"peers":[1]}]`,
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "WK_CLUSTER_GROUPS")
}

func TestLoadConfigRejectsLegacyClusterGroupCountKey(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_GROUP_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "WK_CLUSTER_GROUP_COUNT")
}

func TestLoadConfigRejectsLegacyClusterGroupReplicaNKey(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_GROUP_REPLICA_N=3",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "WK_CLUSTER_GROUP_REPLICA_N")
}

func TestLoadConfigParsesDataPlaneRPCTimeoutFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_DATA_PLANE_RPC_TIMEOUT=250ms",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 250*time.Millisecond, cfg.Cluster.DataPlaneRPCTimeout)
}

func TestLoadConfigParsesClusterTimeoutOverridesFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_CONTROLLER_OBSERVATION_INTERVAL=350ms",
		"WK_CLUSTER_CONTROLLER_REQUEST_TIMEOUT=3s",
		"WK_CLUSTER_CONTROLLER_LEADER_WAIT_TIMEOUT=9s",
		"WK_CLUSTER_MANAGED_SLOTS_READY_TIMEOUT=45s",
		"WK_CLUSTER_FORWARD_RETRY_BUDGET=600ms",
		"WK_CLUSTER_MANAGED_SLOT_LEADER_WAIT_TIMEOUT=6s",
		"WK_CLUSTER_MANAGED_SLOT_CATCH_UP_TIMEOUT=7s",
		"WK_CLUSTER_MANAGED_SLOT_LEADER_MOVE_TIMEOUT=8s",
		"WK_CLUSTER_CONFIG_CHANGE_RETRY_BUDGET=700ms",
		"WK_CLUSTER_LEADER_TRANSFER_RETRY_BUDGET=800ms",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 350*time.Millisecond, cfg.Cluster.Timeouts.ControllerObservation)
	require.Equal(t, 3*time.Second, cfg.Cluster.Timeouts.ControllerRequest)
	require.Equal(t, 9*time.Second, cfg.Cluster.Timeouts.ControllerLeaderWait)
	require.Equal(t, 45*time.Second, cfg.Cluster.ManagedSlotsReadyTimeout)
	require.Equal(t, 600*time.Millisecond, cfg.Cluster.Timeouts.ForwardRetryBudget)
	require.Equal(t, 6*time.Second, cfg.Cluster.Timeouts.ManagedSlotLeaderWait)
	require.Equal(t, 7*time.Second, cfg.Cluster.Timeouts.ManagedSlotCatchUp)
	require.Equal(t, 8*time.Second, cfg.Cluster.Timeouts.ManagedSlotLeaderMove)
	require.Equal(t, 700*time.Millisecond, cfg.Cluster.Timeouts.ConfigChangeRetryBudget)
	require.Equal(t, 800*time.Millisecond, cfg.Cluster.Timeouts.LeaderTransferRetryBudget)
}

func TestLoadConfigParsesControllerLogCompaction(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED=false",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES=25",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL=2s",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.False(t, cfg.Cluster.ControllerLogCompaction.Enabled)
	require.Equal(t, uint64(25), cfg.Cluster.ControllerLogCompaction.TriggerEntries)
	require.Equal(t, 2*time.Second, cfg.Cluster.ControllerLogCompaction.CheckInterval)
}

func TestLoadConfigParsesSlotLogCompaction(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED=false",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES=25",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL=2s",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.False(t, cfg.Cluster.SlotLogCompaction.Enabled)
	require.Equal(t, uint64(25), cfg.Cluster.SlotLogCompaction.TriggerEntries)
	require.Equal(t, 2*time.Second, cfg.Cluster.SlotLogCompaction.CheckInterval)
}

func TestLoadConfigPrefersEnvironmentVariablesForControllerLogCompaction(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED=true",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES=25",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL=2s",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)
	t.Setenv("WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED", "false")
	t.Setenv("WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES", "50")
	t.Setenv("WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL", "5s")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.False(t, cfg.Cluster.ControllerLogCompaction.Enabled)
	require.Equal(t, uint64(50), cfg.Cluster.ControllerLogCompaction.TriggerEntries)
	require.Equal(t, 5*time.Second, cfg.Cluster.ControllerLogCompaction.CheckInterval)
}

func TestLoadConfigPrefersEnvironmentVariablesForSlotLogCompaction(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED=true",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES=25",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL=2s",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)
	t.Setenv("WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED", "false")
	t.Setenv("WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES", "50")
	t.Setenv("WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL", "5s")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.False(t, cfg.Cluster.SlotLogCompaction.Enabled)
	require.Equal(t, uint64(50), cfg.Cluster.SlotLogCompaction.TriggerEntries)
	require.Equal(t, 5*time.Second, cfg.Cluster.SlotLogCompaction.CheckInterval)
}

func TestLoadConfigRejectsInvalidControllerLogCompaction(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "zero trigger entries",
			lines: []string{
				"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED=true",
				"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES=0",
			},
		},
		{
			name: "negative check interval",
			lines: []string{
				"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED=true",
				"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL=-1s",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			lines := []string{
				"WK_NODE_ID=1",
				"WK_NODE_DATA_DIR=" + filepath.Join(dir, "node-1"),
				"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
				"WK_CLUSTER_SLOT_COUNT=1",
				`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
			}
			lines = append(lines, tt.lines...)
			configPath := writeConf(t, dir, "wukongim.conf", lines...)

			_, err := loadConfig(configPath)
			require.ErrorContains(t, err, "controller log compaction")
		})
	}
}

func TestLoadConfigRejectsInvalidSlotLogCompaction(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "zero trigger entries",
			lines: []string{
				"WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED=true",
				"WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES=0",
			},
		},
		{
			name: "negative check interval",
			lines: []string{
				"WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED=true",
				"WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL=-1s",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			lines := []string{
				"WK_NODE_ID=1",
				"WK_NODE_DATA_DIR=" + filepath.Join(dir, "node-1"),
				"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
				"WK_CLUSTER_SLOT_COUNT=1",
				`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
			}
			lines = append(lines, tt.lines...)
			configPath := writeConf(t, dir, "wukongim.conf", lines...)

			_, err := loadConfig(configPath)
			require.ErrorContains(t, err, "slot log compaction")
		})
	}
}

func TestLoadConfigParsesObservationCadence(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_OBSERVATION_HEARTBEAT_INTERVAL=2s",
		"WK_CLUSTER_OBSERVATION_RUNTIME_SCAN_INTERVAL=1s",
		"WK_CLUSTER_OBSERVATION_RUNTIME_FLUSH_DEBOUNCE=150ms",
		"WK_CLUSTER_OBSERVATION_RUNTIME_FULL_SYNC_INTERVAL=60s",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, cfg.Cluster.Timeouts.ObservationHeartbeatInterval)
	require.Equal(t, time.Second, cfg.Cluster.Timeouts.ObservationRuntimeScanInterval)
	require.Equal(t, 150*time.Millisecond, cfg.Cluster.Timeouts.ObservationRuntimeFlushDebounce)
	require.Equal(t, 60*time.Second, cfg.Cluster.Timeouts.ObservationRuntimeFullSyncInterval)
}

func TestBuildAppConfigParsesAutomaticSlotManagementKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_STORAGE_CONTROLLER_META_PATH="+filepath.Join(dir, "controller-meta"),
		"WK_STORAGE_CONTROLLER_RAFT_PATH="+filepath.Join(dir, "controller-raft"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_CONTROLLER_REPLICA_N=3",
		"WK_CLUSTER_SLOT_REPLICA_N=3",
		`WK_CLUSTER_NODES=[{"id":3,"addr":"127.0.0.1:7002"},{"id":1,"addr":"127.0.0.1:7000"},{"id":2,"addr":"127.0.0.1:7001"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 3, cfg.Cluster.ControllerReplicaN)
	require.Equal(t, 3, cfg.Cluster.SlotReplicaN)
	require.Equal(t, filepath.Join(dir, "controller-meta"), cfg.Storage.ControllerMetaPath)
	require.Equal(t, filepath.Join(dir, "controller-raft"), cfg.Storage.ControllerRaftPath)
	require.Empty(t, cfg.Cluster.Slots)
}

func TestLoadConfigParsesHashSlotAndInitialSlotCounts(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_HASH_SLOT_COUNT=256",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=4",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, uint16(256), cfg.Cluster.HashSlotCount)
	require.Equal(t, uint32(4), cfg.Cluster.InitialSlotCount)
}

func TestLoadConfigParsesHashSlotMigrationGate(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_HASH_SLOT_COUNT=256",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=1",
		"WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED=true",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.True(t, cfg.Cluster.EnableHashSlotMigration)
}

func TestLoadConfigParsesGatewayRuntimeAsyncSendWorkersFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=64",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 64, cfg.Gateway.Runtime.AsyncSendWorkers)
}

func TestLoadConfigParsesGatewayAsyncSendBatchOptionsFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT=750us",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=64",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES=262144",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 750*time.Microsecond, cfg.Gateway.DefaultSession.AsyncSendBatchMaxWait)
	require.Equal(t, 64, cfg.Gateway.DefaultSession.AsyncSendBatchMaxRecords)
	require.Equal(t, 262144, cfg.Gateway.DefaultSession.AsyncSendBatchMaxBytes)
}

func TestLoadConfigParsesGatewayGnetOptionsFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_GATEWAY_GNET_MULTICORE=true",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP=4",
		"WK_GATEWAY_GNET_REUSE_PORT=true",
		"WK_GATEWAY_GNET_READ_BUFFER_CAP=8192",
		"WK_GATEWAY_GNET_WRITE_BUFFER_CAP=16384",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.True(t, cfg.Gateway.Transport.Gnet.Multicore)
	require.Equal(t, 4, cfg.Gateway.Transport.Gnet.NumEventLoop)
	require.True(t, cfg.Gateway.Transport.Gnet.ReusePort)
	require.Equal(t, 8192, cfg.Gateway.Transport.Gnet.ReadBufferCap)
	require.Equal(t, 16384, cfg.Gateway.Transport.Gnet.WriteBufferCap)
}

func TestLoadConfigParsesGatewaySendTimeoutFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_GATEWAY_SEND_TIMEOUT=25s",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 25*time.Second, cfg.Gateway.SendTimeout)
}

func TestLoadConfigParsesLogSettingsFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"WK_LOG_LEVEL=debug",
		"WK_LOG_DIR="+filepath.Join(dir, "logs"),
		"WK_LOG_MAX_SIZE=64",
		"WK_LOG_MAX_AGE=7",
		"WK_LOG_MAX_BACKUPS=3",
		"WK_LOG_COMPRESS=false",
		"WK_LOG_CONSOLE=false",
		"WK_LOG_FORMAT=json",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "debug", cfg.Log.Level)
	require.Equal(t, filepath.Join(dir, "logs"), cfg.Log.Dir)
	require.Equal(t, 64, cfg.Log.MaxSize)
	require.Equal(t, 7, cfg.Log.MaxAge)
	require.Equal(t, 3, cfg.Log.MaxBackups)
	require.False(t, cfg.Log.Compress)
	require.False(t, cfg.Log.Console)
	require.Equal(t, "json", cfg.Log.Format)
}

func TestLoadConfigUsesLogDefaultsWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "info", cfg.Log.Level)
	require.Equal(t, "./logs", cfg.Log.Dir)
	require.Equal(t, 100, cfg.Log.MaxSize)
	require.Equal(t, 30, cfg.Log.MaxAge)
	require.Equal(t, 10, cfg.Log.MaxBackups)
	require.True(t, cfg.Log.Compress)
	require.True(t, cfg.Log.Console)
	require.Equal(t, "console", cfg.Log.Format)
}

func TestLoadConfigParsesLongPollDefaultsFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 8, cfg.Cluster.LongPollLaneCount)
	require.Equal(t, 200*time.Millisecond, cfg.Cluster.LongPollMaxWait)
	require.Equal(t, 64*1024, cfg.Cluster.LongPollMaxBytes)
	require.Equal(t, 64, cfg.Cluster.LongPollMaxChannels)
}

func TestLoadConfigIgnoresLegacyReplicationModeAndStillAppliesLongPollDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_CLUSTER_REPLICATION_MODE=progress_ack",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 8, cfg.Cluster.LongPollLaneCount)
	require.Equal(t, 200*time.Millisecond, cfg.Cluster.LongPollMaxWait)
	require.Equal(t, 64*1024, cfg.Cluster.LongPollMaxBytes)
	require.Equal(t, 64, cfg.Cluster.LongPollMaxChannels)
}

func TestLoadConfigParsesLongPollOverridesFromEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)
	t.Setenv("WK_CLUSTER_LONG_POLL_LANE_COUNT", "16")
	t.Setenv("WK_CLUSTER_LONG_POLL_MAX_WAIT", "2ms")
	t.Setenv("WK_CLUSTER_LONG_POLL_MAX_BYTES", "131072")
	t.Setenv("WK_CLUSTER_LONG_POLL_MAX_CHANNELS", "32")
	t.Setenv("WK_CLUSTER_LONG_POLL_DATA_NOTIFY_DELAY", "5ms")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 16, cfg.Cluster.LongPollLaneCount)
	require.Equal(t, 2*time.Millisecond, cfg.Cluster.LongPollMaxWait)
	require.Equal(t, 128*1024, cfg.Cluster.LongPollMaxBytes)
	require.Equal(t, 32, cfg.Cluster.LongPollMaxChannels)
	require.Equal(t, 5*time.Millisecond, cfg.Cluster.LongPollDataNotifyDelay)
}
