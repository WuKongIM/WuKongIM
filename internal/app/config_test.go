package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"
	"time"
	"unsafe"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
)

func TestConfigExampleMentionsEverySupportedKey(t *testing.T) {
	content, err := os.ReadFile("../../wukongim.conf.example")
	require.NoError(t, err)

	keyPattern := regexp.MustCompile(`\bWK_[A-Z0-9_]+\b`)
	documented := make(map[string]struct{})
	for _, key := range keyPattern.FindAllString(string(content), -1) {
		documented[key] = struct{}{}
	}

	var missing []string
	for _, key := range supportedConfigExampleKeys() {
		if _, ok := documented[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "wukongim.conf.example must mention every supported WK_ config key")
}

func supportedConfigExampleKeys() []string {
	return []string{
		"WK_API_LISTEN_ADDR",
		"WK_CHANNEL_MIGRATION_COMPLETED_RETENTION_TTL",
		"WK_CHANNEL_MIGRATION_FENCE_TTL",
		"WK_CHANNEL_MIGRATION_GC_LIMIT",
		"WK_CHANNEL_MIGRATION_LEADER_LEASE_TTL",
		"WK_CHANNEL_MIGRATION_CATCH_UP_STABLE_WINDOW",
		"WK_CHANNEL_MIGRATION_CATCH_UP_LAG_THRESHOLD",
		"WK_CHANNEL_MIGRATION_MAX_CONCURRENT",
		"WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_SOURCE",
		"WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_TARGET",
		"WK_CHANNEL_MIGRATION_OWNER_LEASE_TTL",
		"WK_CHANNEL_MIGRATION_RETRY_BACKOFF",
		"WK_CHANNEL_MIGRATION_SCAN_INTERVAL",
		"WK_CHANNEL_MIGRATION_SCAN_LIMIT",
		"WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL",
		"WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE",
		"WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES",
		"WK_CHANNEL_MESSAGE_RETENTION_TTL",
		"WK_CHANNEL_PLANE_PEER_BATCH_MAX_WAIT",
		"WK_CHANNEL_PLANE_PEER_LANE_COUNT",
		"WK_CHANNEL_PLANE_REACTOR_COUNT",
		"WK_CONVERSATION_ACTIVE_HINT_BARRIER_TTL",
		"WK_CONVERSATION_ACTIVE_HINT_FLUSH_BATCH_SIZE",
		"WK_CONVERSATION_ACTIVE_HINT_FLUSH_INTERVAL",
		"WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS",
		"WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS_PER_UID",
		"WK_CONVERSATION_ACTIVE_HINT_TTL",
		"WK_CONVERSATION_GROUP_ACTIVE_FANOUT_INTERVAL",
		"WK_CONVERSATION_GROUP_ACTIVE_FANOUT_MAX_SUBSCRIBERS",
		"WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_BYTES",
		"WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_RECORDS",
		"WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_WAIT",
		"WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS",
		"WK_CLUSTER_ADVERTISE_ADDR",
		"WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR",
		"WK_CLUSTER_CHANNEL_EXECUTION_MODE",
		"WK_CLUSTER_CHANNEL_EXECUTION_QUEUE_SIZE",
		"WK_CLUSTER_CHANNEL_EXECUTION_WORKERS",
		"WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL",
		"WK_CLUSTER_CHANNEL_IDLE_TIMEOUT",
		"WK_CLUSTER_CONFIG_CHANGE_RETRY_BUDGET",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES",
		"WK_CLUSTER_CONTROLLER_LEADER_WAIT_TIMEOUT",
		"WK_CLUSTER_CONTROLLER_OBSERVATION_INTERVAL",
		"WK_CLUSTER_CONTROLLER_REPLICA_N",
		"WK_CLUSTER_CONTROLLER_REQUEST_TIMEOUT",
		"WK_CLUSTER_DATA_PLANE_MAX_FETCH_INFLIGHT",
		"WK_CLUSTER_DATA_PLANE_MAX_PENDING_FETCH",
		"WK_CLUSTER_DATA_PLANE_POOL_SIZE",
		"WK_CLUSTER_DATA_PLANE_RPC_TIMEOUT",
		"WK_CLUSTER_DIAL_TIMEOUT",
		"WK_CLUSTER_ELECTION_TICK",
		"WK_CLUSTER_FOLLOWER_REPLICATION_RETRY_INTERVAL",
		"WK_CLUSTER_FORWARD_RETRY_BUDGET",
		"WK_CLUSTER_FORWARD_TIMEOUT",
		"WK_CLUSTER_HASH_SLOT_COUNT",
		"WK_CLUSTER_HEARTBEAT_TICK",
		"WK_CLUSTER_INITIAL_SLOT_COUNT",
		"WK_CLUSTER_JOIN_TOKEN",
		"WK_CLUSTER_LEADER_TRANSFER_RETRY_BUDGET",
		"WK_CLUSTER_LISTEN_ADDR",
		"WK_CLUSTER_LONG_POLL_LANE_COUNT",
		"WK_CLUSTER_LONG_POLL_MAX_BYTES",
		"WK_CLUSTER_LONG_POLL_MAX_CHANNELS",
		"WK_CLUSTER_LONG_POLL_MAX_WAIT",
		"WK_CLUSTER_MANAGED_SLOT_CATCH_UP_TIMEOUT",
		"WK_CLUSTER_MANAGED_SLOT_LEADER_MOVE_TIMEOUT",
		"WK_CLUSTER_MANAGED_SLOT_LEADER_WAIT_TIMEOUT",
		"WK_CLUSTER_MAX_CHANNELS",
		"WK_CLUSTER_NODES",
		"WK_CLUSTER_OBSERVATION_HEARTBEAT_INTERVAL",
		"WK_CLUSTER_OBSERVATION_RUNTIME_FLUSH_DEBOUNCE",
		"WK_CLUSTER_OBSERVATION_RUNTIME_FULL_SYNC_INTERVAL",
		"WK_CLUSTER_OBSERVATION_RUNTIME_SCAN_INTERVAL",
		"WK_CLUSTER_POOL_SIZE",
		"WK_CLUSTER_RAFT_WORKERS",
		"WK_CLUSTER_SEEDS",
		"WK_CLUSTER_SLOT_COUNT",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES",
		"WK_CLUSTER_SLOT_REPLICA_N",
		"WK_CLUSTER_TICK_INTERVAL",
		"WK_EXTERNAL_TCPADDR",
		"WK_EXTERNAL_WSADDR",
		"WK_EXTERNAL_WSSADDR",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT",
		"WK_GATEWAY_DEFAULT_SESSION_CLOSE_ON_HANDLER_ERROR",
		"WK_GATEWAY_DEFAULT_SESSION_IDLE_TIMEOUT",
		"WK_GATEWAY_DEFAULT_SESSION_MAX_INBOUND_BYTES",
		"WK_GATEWAY_DEFAULT_SESSION_MAX_OUTBOUND_BYTES",
		"WK_GATEWAY_LISTENERS",
		"WK_GATEWAY_SEND_TIMEOUT",
		"WK_GATEWAY_TOKEN_AUTH_ON",
		"WK_HEALTH_DEBUG_ENABLE",
		"WK_HEALTH_DETAIL_ENABLE",
		"WK_LOG_COMPRESS",
		"WK_LOG_CONSOLE",
		"WK_LOG_DIR",
		"WK_LOG_FORMAT",
		"WK_LOG_LEVEL",
		"WK_LOG_MAX_AGE",
		"WK_LOG_MAX_BACKUPS",
		"WK_LOG_MAX_SIZE",
		"WK_MANAGER_AUTH_ON",
		"WK_MANAGER_JWT_EXPIRE",
		"WK_MANAGER_JWT_ISSUER",
		"WK_MANAGER_JWT_SECRET",
		"WK_MANAGER_LISTEN_ADDR",
		"WK_MANAGER_USERS",
		"WK_METRICS_ENABLE",
		"WK_NODE_DATA_DIR",
		"WK_NODE_ID",
		"WK_NODE_NAME",
		"WK_PLUGIN_DIR",
		"WK_PLUGIN_ENABLE",
		"WK_PLUGIN_FAIL_OPEN",
		"WK_PLUGIN_HOT_RELOAD",
		"WK_PLUGIN_SANDBOX_DIR",
		"WK_PLUGIN_SOCKET_PATH",
		"WK_PLUGIN_STATE_DIR",
		"WK_PLUGIN_TIMEOUT",
		"WK_STORAGE_CHANNEL_LOG_PATH",
		"WK_STORAGE_CONTROLLER_META_PATH",
		"WK_STORAGE_CONTROLLER_RAFT_PATH",
		"WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH",
		"WK_STORAGE_DB_PATH",
		"WK_STORAGE_RAFT_PATH",
		"WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE",
		"WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE",
		"WK_STORAGE_RAFT_SNAPSHOT_PATH",
		"WK_TEST_MODE",
	}
}

func clusterConfigDurationField(t *testing.T, cfg *ClusterConfig, name string) time.Duration {
	t.Helper()

	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("ClusterConfig is missing field %s", name)
	}
	value := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	return time.Duration(value.Int())
}

func clusterConfigIntField(t *testing.T, cfg *ClusterConfig, name string) int {
	t.Helper()

	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("ClusterConfig is missing field %s", name)
	}
	value := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	return int(value.Int())
}

func raftClusterConfigBoolField(t *testing.T, cfg *raftcluster.Config, name string) bool {
	t.Helper()

	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("cluster.Config is missing field %s", name)
	}
	value := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	return value.Bool()
}

func TestConfigValidateRequiresNodeAndClusterIdentity(t *testing.T) {
	t.Run("missing node id", func(t *testing.T) {
		cfg := validConfig()
		cfg.Node.ID = 0

		require.Error(t, cfg.ApplyDefaultsAndValidate())
	})

	t.Run("missing node data dir", func(t *testing.T) {
		cfg := validConfig()
		cfg.Node.DataDir = ""

		require.Error(t, cfg.ApplyDefaultsAndValidate())
	})

	t.Run("missing cluster listen addr", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.ListenAddr = ""

		require.Error(t, cfg.ApplyDefaultsAndValidate())
	})
}

func TestConfigApplyDefaultsDerivesStoragePathsFromDataDir(t *testing.T) {
	cfg := validConfig()
	cfg.Storage = StorageConfig{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, "/tmp/wukong-node-1/data", cfg.Storage.DBPath)
	require.Equal(t, "/tmp/wukong-node-1/raft", cfg.Storage.RaftPath)
	require.Equal(t, "/tmp/wukong-node-1/channellog", cfg.Storage.ChannelLogPath)
	require.Equal(t, "/tmp/wukong-node-1/controller-meta", cfg.Storage.ControllerMetaPath)
	require.Equal(t, "/tmp/wukong-node-1/controller-raft", cfg.Storage.ControllerRaftPath)
}

func TestConfigApplyDefaultsDerivesRaftSnapshotPathsFromDataDir(t *testing.T) {
	cfg := validConfig()
	cfg.Storage = StorageConfig{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, "/tmp/wukong-node-1/raft-snapshots", cfg.Storage.RaftSnapshotPath)
	require.Equal(t, "/tmp/wukong-node-1/controller-raft-snapshots", cfg.Storage.ControllerRaftSnapshotPath)
	require.Equal(t, uint64(8<<20), cfg.Storage.RaftSnapshotChunkSize)
	require.Equal(t, 30*time.Minute, cfg.Storage.RaftSnapshotGCGrace)
}

func TestConfigApplyDefaultsDerivesPluginPathsFromDataDir(t *testing.T) {
	cfg := validConfig()
	cfg.Plugin.Enable = true

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.True(t, cfg.Plugin.Enable)
	require.Equal(t, filepath.Join(cfg.Node.DataDir, "plugins"), cfg.Plugin.Dir)
	require.Equal(t, filepath.Join(cfg.Node.DataDir, "run", "plugin.sock"), cfg.Plugin.SocketPath)
	require.Equal(t, filepath.Join(cfg.Node.DataDir, "plugin-sandbox"), cfg.Plugin.SandboxDir)
	require.Equal(t, filepath.Join(cfg.Node.DataDir, "plugin-state"), cfg.Plugin.StateDir)
	require.Equal(t, 5*time.Second, cfg.Plugin.Timeout)
	require.True(t, cfg.Plugin.HotReload)
	require.False(t, cfg.Plugin.FailOpen)
}

func TestConfigApplyDefaultsKeepsPluginDisabledByDefault(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.False(t, cfg.Plugin.Enable)
}

func TestConfigValidateRejectsInvalidPluginTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Plugin.Timeout = -time.Second

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "plugin timeout")
}

func TestConfigUsesConfiguredRaftSnapshotPaths(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotPath = "/tmp/wukong-node-1/custom-slot-snapshots"
	cfg.Storage.ControllerRaftSnapshotPath = "/tmp/wukong-node-1/custom-controller-snapshots"
	cfg.Storage.RaftSnapshotChunkSize = 16 << 20
	cfg.Storage.RaftSnapshotGCGrace = time.Hour
	cfg.Storage.SetRaftSnapshotExplicitFlags(true, true)

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, "/tmp/wukong-node-1/custom-slot-snapshots", cfg.Storage.RaftSnapshotPath)
	require.Equal(t, "/tmp/wukong-node-1/custom-controller-snapshots", cfg.Storage.ControllerRaftSnapshotPath)
	require.Equal(t, uint64(16<<20), cfg.Storage.RaftSnapshotChunkSize)
	require.Equal(t, time.Hour, cfg.Storage.RaftSnapshotGCGrace)
}

func TestConfigUsesExplicitZeroRaftSnapshotGCGrace(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotGCGrace = 0
	cfg.Storage.SetRaftSnapshotExplicitFlags(false, true)

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, time.Duration(0), cfg.Storage.RaftSnapshotGCGrace)
}

func TestConfigValidateRejectsSnapshotPathOverlappingStoragePaths(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotPath = "/tmp/wukong-node-1/data"

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "snapshot")
}

func TestConfigValidateRejectsSnapshotPathAncestorDescendantOverlap(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotPath = "/tmp/wukong-node-1/raft/snapshots"

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "overlap")
}

func TestConfigValidateRejectsSymlinkSnapshotPathOverlap(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	snapshotPath := filepath.Join(dir, "slot-snapshots")
	require.NoError(t, os.Symlink(dataPath, snapshotPath))

	cfg := validConfig()
	cfg.Node.DataDir = dir
	cfg.Storage.DBPath = dataPath
	cfg.Storage.RaftPath = filepath.Join(dir, "raft")
	cfg.Storage.ChannelLogPath = filepath.Join(dir, "channel")
	cfg.Storage.ControllerMetaPath = filepath.Join(dir, "controller-meta")
	cfg.Storage.ControllerRaftPath = filepath.Join(dir, "controller-raft")
	cfg.Storage.RaftSnapshotPath = snapshotPath
	cfg.Storage.ControllerRaftSnapshotPath = filepath.Join(dir, "controller-snapshots")

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "overlap")
}

func TestConfigValidateRejectsSymlinkParentSnapshotPathOverlap(t *testing.T) {
	dir := t.TempDir()
	realRoot := filepath.Join(dir, "real")
	linkRoot := filepath.Join(dir, "link")
	dataPath := filepath.Join(realRoot, "data")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.Symlink(realRoot, linkRoot))

	cfg := validConfig()
	cfg.Node.DataDir = dir
	cfg.Storage.DBPath = dataPath
	cfg.Storage.RaftPath = filepath.Join(dir, "raft")
	cfg.Storage.ChannelLogPath = filepath.Join(dir, "channel")
	cfg.Storage.ControllerMetaPath = filepath.Join(dir, "controller-meta")
	cfg.Storage.ControllerRaftPath = filepath.Join(dir, "controller-raft")
	cfg.Storage.RaftSnapshotPath = filepath.Join(linkRoot, "data", "snapshots")
	cfg.Storage.ControllerRaftSnapshotPath = filepath.Join(dir, "controller-snapshots")

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "overlap")
}

func TestConfigValidateAcceptsRaftSnapshotChunkSizeUnits(t *testing.T) {
	tests := map[string]uint64{
		"8MiB":     8 << 20,
		"8192KiB":  8 << 20,
		"1GiB":     1 << 30,
		"8388608B": 8 << 20,
		"8388608":  8 << 20,
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			got, err := ParseRaftSnapshotChunkSize(raw)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestConfigValidateRejectsInvalidRaftSnapshotChunkSizeUnits(t *testing.T) {
	for _, raw := range []string{"8MB", "8M", "1.5MiB", "MiB", "-1KiB"} {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseRaftSnapshotChunkSize(raw)
			require.Error(t, err)
		})
	}
}

func TestConfigValidateRejectsZeroRaftSnapshotChunkSize(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotChunkSize = 0
	cfg.Storage.SetRaftSnapshotExplicitFlags(true, false)

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "snapshot chunk size")
}

func TestConfigValidateRejectsNegativeRaftSnapshotGCGrace(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotGCGrace = -time.Second
	cfg.Storage.SetRaftSnapshotExplicitFlags(false, true)

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "snapshot gc grace")
}

func TestConfigApplyDefaultsKeepsTestModeDisabledByDefault(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.False(t, cfg.TestMode)
}

func TestConfigApplyDefaultsSetsBenchAPILimits(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.False(t, cfg.Bench.APIEnabled)
	require.Equal(t, 10000, cfg.Bench.APIMaxBatchSize)
	require.Equal(t, int64(10<<20), cfg.Bench.APIMaxPayloadBytes)
}

func TestConfigApplyDefaultsSetsBenchAPILimitsWhenEnabled(t *testing.T) {
	cfg := validConfig()
	cfg.Bench.APIEnabled = true

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, 10000, cfg.Bench.APIMaxBatchSize)
	require.Equal(t, int64(10<<20), cfg.Bench.APIMaxPayloadBytes)
}

func TestConfigRejectsInvalidBenchAPILimitsWhenEnabled(t *testing.T) {
	t.Run("batch size", func(t *testing.T) {
		cfg := validConfig()
		cfg.Bench.APIEnabled = true
		cfg.Bench.APIMaxBatchSize = -1
		cfg.Bench.APIMaxPayloadBytes = 1024

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "bench api max batch size")
	})

	t.Run("payload bytes", func(t *testing.T) {
		cfg := validConfig()
		cfg.Bench.APIEnabled = true
		cfg.Bench.APIMaxBatchSize = 10
		cfg.Bench.APIMaxPayloadBytes = -1

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "bench api max payload bytes")
	})
}

func TestConfigRejectsNodeIDSnowflakeOverflow(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 1024

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsZeroSlotCount(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.SlotCount = 0

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsHashSlotCountBelowInitialSlotCount(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.SlotCount = 0
	cfg.Cluster.InitialSlotCount = 4
	cfg.Cluster.HashSlotCount = 3

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsMismatchedLegacyAndInitialSlotCount(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.SlotCount = 2
	cfg.Cluster.InitialSlotCount = 3
	cfg.Cluster.HashSlotCount = 3

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsStaticClusterSlots(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Slots = []SlotConfig{{ID: 1, Peers: []uint64{1}}}

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateAllowsNilStaticSlotsWithExplicitSlotCount(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Slots = nil
	cfg.Cluster.SlotCount = 1

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsInvalidControllerReplicaN(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ControllerReplicaN = 4
	cfg.Cluster.SlotReplicaN = 3
	cfg.Cluster.Nodes = []NodeConfigRef{
		{ID: 3, Addr: "127.0.0.1:7002"},
		{ID: 1, Addr: "127.0.0.1:7000"},
		{ID: 2, Addr: "127.0.0.1:7001"},
	}

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsSharedStoragePaths(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.DBPath = "/tmp/wukong-node-1/shared"
	cfg.Storage.RaftPath = "/tmp/wukong-node-1/shared"

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsAliasedSharedStoragePaths(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.DBPath = "/tmp/wukong-node-1/data"
	cfg.Storage.RaftPath = "/tmp/wukong-node-1/data/"

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsSharedChannelLogPath(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.ChannelLogPath = "/tmp/wukong-node-1/data"

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsDuplicateClusterNodeIDs(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Nodes = []NodeConfigRef{
		{ID: 1, Addr: "127.0.0.1:7000"},
		{ID: 1, Addr: "127.0.0.1:7001"},
	}

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsNodeMissingFromClusterNodes(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 2

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsLocalNodeMissingFromClusterNodes(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 9
	cfg.Cluster.ControllerReplicaN = 3
	cfg.Cluster.SlotReplicaN = 3

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateStaticClusterNodesStillDefaultReplicasAndRequireLocalNode(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Nodes = []NodeConfigRef{
		{ID: 3, Addr: "127.0.0.1:7002"},
		{ID: 1, Addr: "127.0.0.1:7000"},
		{ID: 2, Addr: "127.0.0.1:7001"},
	}
	cfg.Cluster.ControllerReplicaN = 0
	cfg.Cluster.SlotReplicaN = 0

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, 3, cfg.Cluster.ControllerReplicaN)
	require.Equal(t, 3, cfg.Cluster.SlotReplicaN)

	cfg = validConfig()
	cfg.Node.ID = 4
	cfg.Cluster.Nodes = []NodeConfigRef{
		{ID: 1, Addr: "127.0.0.1:7000"},
		{ID: 2, Addr: "127.0.0.1:7001"},
		{ID: 3, Addr: "127.0.0.1:7002"},
	}
	cfg.Cluster.ControllerReplicaN = 0
	cfg.Cluster.SlotReplicaN = 0

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "not found in cluster nodes")
}

func TestConfigValidateSeedJoinAllowsEmptyNodesWithCompleteJoinSettings(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 4
	cfg.Cluster.Nodes = nil
	cfg.Cluster.Seeds = []string{"wk-node1:7000", "wk-node2:7000"}
	cfg.Cluster.AdvertiseAddr = "wk-node4:7000"
	cfg.Cluster.JoinToken = "join-secret"
	cfg.Cluster.ControllerReplicaN = 1
	cfg.Cluster.SlotReplicaN = 1

	require.True(t, cfg.Cluster.JoinModeEnabled())
	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Empty(t, cfg.Cluster.Nodes)
	require.Equal(t, []string{"wk-node1:7000", "wk-node2:7000"}, cfg.Cluster.Seeds)
}

func TestConfigValidateSeedJoinRejectsEmptyAdvertiseAddr(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 4
	cfg.Cluster.Nodes = nil
	cfg.Cluster.Seeds = []string{"wk-node1:7000"}
	cfg.Cluster.AdvertiseAddr = ""
	cfg.Cluster.JoinToken = "join-secret"
	cfg.Cluster.ControllerReplicaN = 1
	cfg.Cluster.SlotReplicaN = 1

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "cluster advertise addr")
}

func TestConfigValidateSeedJoinRejectsEmptyJoinToken(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 4
	cfg.Cluster.Nodes = nil
	cfg.Cluster.Seeds = []string{"wk-node1:7000"}
	cfg.Cluster.AdvertiseAddr = "wk-node4:7000"
	cfg.Cluster.JoinToken = ""
	cfg.Cluster.ControllerReplicaN = 1
	cfg.Cluster.SlotReplicaN = 1

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "cluster join token")
}

func TestConfigValidateSeedJoinRejectsMissingReplicaCounts(t *testing.T) {
	t.Run("controller replica count", func(t *testing.T) {
		cfg := validConfig()
		cfg.Node.ID = 4
		cfg.Cluster.Nodes = nil
		cfg.Cluster.Seeds = []string{"wk-node1:7000"}
		cfg.Cluster.AdvertiseAddr = "wk-node4:7000"
		cfg.Cluster.JoinToken = "join-secret"
		cfg.Cluster.ControllerReplicaN = 0
		cfg.Cluster.SlotReplicaN = 1

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "controller replica count")
	})

	t.Run("slot replica count", func(t *testing.T) {
		cfg := validConfig()
		cfg.Node.ID = 4
		cfg.Cluster.Nodes = nil
		cfg.Cluster.Seeds = []string{"wk-node1:7000"}
		cfg.Cluster.AdvertiseAddr = "wk-node4:7000"
		cfg.Cluster.JoinToken = "join-secret"
		cfg.Cluster.ControllerReplicaN = 1
		cfg.Cluster.SlotReplicaN = 0

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "slot replica count")
	})
}

func TestConfigValidateSeedJoinRejectsInvalidSeeds(t *testing.T) {
	t.Run("empty seed", func(t *testing.T) {
		cfg := validConfig()
		cfg.Node.ID = 4
		cfg.Cluster.Nodes = nil
		cfg.Cluster.Seeds = []string{"wk-node1:7000", ""}
		cfg.Cluster.AdvertiseAddr = "wk-node4:7000"
		cfg.Cluster.JoinToken = "join-secret"
		cfg.Cluster.ControllerReplicaN = 1
		cfg.Cluster.SlotReplicaN = 1

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "cluster seed addr")
	})

	t.Run("duplicate seed", func(t *testing.T) {
		cfg := validConfig()
		cfg.Node.ID = 4
		cfg.Cluster.Nodes = nil
		cfg.Cluster.Seeds = []string{"wk-node1:7000", "wk-node1:7000"}
		cfg.Cluster.AdvertiseAddr = "wk-node4:7000"
		cfg.Cluster.JoinToken = "join-secret"
		cfg.Cluster.ControllerReplicaN = 1
		cfg.Cluster.SlotReplicaN = 1

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "duplicate cluster seed")
	})
}

func TestConfigValidateStaticNodesRemainAuthoritativeWhenSeedsAreAlsoSet(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Nodes = []NodeConfigRef{
		{ID: 3, Addr: "127.0.0.1:7002"},
		{ID: 1, Addr: "127.0.0.1:7000"},
		{ID: 2, Addr: "127.0.0.1:7001"},
	}
	cfg.Cluster.Seeds = []string{"wk-node4:7000"}
	cfg.Cluster.ControllerReplicaN = 0
	cfg.Cluster.SlotReplicaN = 0

	require.True(t, cfg.Cluster.JoinModeEnabled())
	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, 3, cfg.Cluster.ControllerReplicaN)
	require.Equal(t, 3, cfg.Cluster.SlotReplicaN)
	require.Equal(t, []string{"wk-node4:7000"}, cfg.Cluster.Seeds)
}

func TestConfigGatewayDefaultsSessionOptions(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.DefaultSession = gateway.SessionOptions{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.NotNil(t, cfg.Gateway.DefaultSession.CloseOnHandlerError)
	require.True(t, *cfg.Gateway.DefaultSession.CloseOnHandlerError)
}

func TestConfigGatewayDefaultsSendBatchOptions(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.DefaultSession = gateway.SessionOptions{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, 500*time.Microsecond, cfg.Gateway.DefaultSession.AsyncSendBatchMaxWait)
	require.Equal(t, 128, cfg.Gateway.DefaultSession.AsyncSendBatchMaxRecords)
	require.Equal(t, 512*1024, cfg.Gateway.DefaultSession.AsyncSendBatchMaxBytes)
}

func TestConfigGatewayRejectsNegativeSendBatchBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "records",
			mutate: func(cfg *Config) {
				cfg.Gateway.DefaultSession.AsyncSendBatchMaxRecords = -1
			},
			want: "gateway send batch max records",
		},
		{
			name: "bytes",
			mutate: func(cfg *Config) {
				cfg.Gateway.DefaultSession.AsyncSendBatchMaxBytes = -1
			},
			want: "gateway send batch max bytes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), tc.want)
		})
	}
}

func TestConfigGatewayPreservesExplicitFalseCloseOnHandlerError(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.DefaultSession = gateway.SessionOptions{
		CloseOnHandlerError: boolPtr(false),
	}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.NotNil(t, cfg.Gateway.DefaultSession.CloseOnHandlerError)
	require.False(t, *cfg.Gateway.DefaultSession.CloseOnHandlerError)
}

func TestConfigGatewayDefaultsSendTimeout(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, defaultGatewaySendTimeout, cfg.Gateway.SendTimeout)
}

func TestConfigGatewayAllowsZeroValueGnetOptions(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Zero(t, cfg.Gateway.Transport.Gnet.NumEventLoop)
	require.Zero(t, cfg.Gateway.Transport.Gnet.ReadBufferCap)
	require.Zero(t, cfg.Gateway.Transport.Gnet.WriteBufferCap)
}

func TestConfigGatewayRejectsNegativeGnetOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "num event loop",
			mutate: func(cfg *Config) {
				cfg.Gateway.Transport.Gnet.NumEventLoop = -1
			},
			want: "gateway gnet num event loop",
		},
		{
			name: "read buffer cap",
			mutate: func(cfg *Config) {
				cfg.Gateway.Transport.Gnet.ReadBufferCap = -1
			},
			want: "gateway gnet read buffer cap",
		},
		{
			name: "write buffer cap",
			mutate: func(cfg *Config) {
				cfg.Gateway.Transport.Gnet.WriteBufferCap = -1
			},
			want: "gateway gnet write buffer cap",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), tc.want)
		})
	}
}

func TestConfigValidateRejectsExplicitNonPositiveGatewaySendTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.SendTimeout = 0
	cfg.Gateway.SetExplicitFlags(true)

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "gateway send timeout")
}

func TestConfigValidateRejectsTokenAuthWithoutHooks(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.TokenAuthOn = true

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigAllowsDisabledAPIWhenListenAddrEmpty(t *testing.T) {
	cfg := validConfig()
	cfg.API = APIConfig{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, "", cfg.API.ListenAddr)
}

func TestConfigAllowsDisabledManagerWhenListenAddrEmpty(t *testing.T) {
	cfg := validConfig()
	cfg.Manager = ManagerConfig{
		AuthOn:    true,
		JWTSecret: "",
		JWTIssuer: "wukongim-manager",
		JWTExpire: 24 * time.Hour,
	}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, "", cfg.Manager.ListenAddr)
}

func TestConfigValidateRejectsManagerWithoutJWTSecretWhenAuthEnabled(t *testing.T) {
	cfg := validConfig()
	cfg.Manager = ManagerConfig{
		ListenAddr: "127.0.0.1:5301",
		AuthOn:     true,
		JWTIssuer:  "wukongim-manager",
		JWTExpire:  24 * time.Hour,
		Users: []ManagerUserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []ManagerPermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}},
	}

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "manager jwt secret")
}

func TestConfigValidateRejectsManagerWithoutUsersWhenAuthEnabled(t *testing.T) {
	cfg := validConfig()
	cfg.Manager = ManagerConfig{
		ListenAddr: "127.0.0.1:5301",
		AuthOn:     true,
		JWTSecret:  "test-secret",
		JWTIssuer:  "wukongim-manager",
		JWTExpire:  24 * time.Hour,
	}

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "manager users")
}

func TestConfigValidateRejectsManagerPermissionWithInvalidAction(t *testing.T) {
	cfg := validConfig()
	cfg.Manager = ManagerConfig{
		ListenAddr: "127.0.0.1:5301",
		AuthOn:     true,
		JWTSecret:  "test-secret",
		JWTIssuer:  "wukongim-manager",
		JWTExpire:  24 * time.Hour,
		Users: []ManagerUserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []ManagerPermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"delete"},
			}},
		}},
	}

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "manager permission action")
}

func TestConfigValidateAllowsManagerPermissionWildcardResource(t *testing.T) {
	cfg := validConfig()
	cfg.Manager = ManagerConfig{
		ListenAddr: "127.0.0.1:5301",
		AuthOn:     true,
		JWTSecret:  "test-secret",
		JWTIssuer:  "wukongim-manager",
		JWTExpire:  24 * time.Hour,
		Users: []ManagerUserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []ManagerPermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}},
	}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
}

func TestLegacyRouteAddressesPreferExplicitExternalConfig(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.Listeners = []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "127.0.0.1:5100"),
		binding.WSJSONRPC("ws-jsonrpc", "127.0.0.1:5200"),
	}
	cfg.API.ExternalTCPAddr = "im.example.com:15100"
	cfg.API.ExternalWSSAddr = "wss://im.example.com:15300"

	external, intranet := legacyRouteAddresses(cfg.API, cfg.Gateway.Listeners)

	require.Equal(t, accessapi.LegacyRouteAddresses{
		TCPAddr: "im.example.com:15100",
		WSAddr:  "ws://127.0.0.1:5200",
		WSSAddr: "wss://im.example.com:15300",
	}, external)
	require.Equal(t, accessapi.LegacyRouteAddresses{
		TCPAddr: "127.0.0.1:5100",
	}, intranet)
}

func TestLegacyRouteNodeAddressesDeriveRemoteHosts(t *testing.T) {
	cfg := validConfig()
	cfg.API.ExternalTCPAddr = "im-node1.example.com:15100"
	cfg.API.ExternalWSAddr = "ws://im-node1.example.com:15200"
	cfg.API.ExternalWSSAddr = "wss://im-node1.example.com:15300"
	cfg.Cluster.Nodes = []NodeConfigRef{
		{ID: 1, Addr: "node1.internal:7000"},
		{ID: 2, Addr: "node2.internal:7000"},
	}
	cfg.Gateway.Listeners = []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "10.0.0.1:5100"),
	}
	external, intranet := legacyRouteAddresses(cfg.API, cfg.Gateway.Listeners)

	nodes := legacyRouteNodeAddresses(cfg.Node.ID, cfg.Cluster.Nodes, external, intranet)

	require.Equal(t, accessapi.LegacyRouteNodeAddresses{
		External: accessapi.LegacyRouteAddresses{
			TCPAddr: "im-node1.example.com:15100",
			WSAddr:  "ws://im-node1.example.com:15200",
			WSSAddr: "wss://im-node1.example.com:15300",
		},
		Intranet: accessapi.LegacyRouteAddresses{
			TCPAddr: "10.0.0.1:5100",
		},
	}, nodes[1])
	require.Equal(t, accessapi.LegacyRouteNodeAddresses{
		External: accessapi.LegacyRouteAddresses{
			TCPAddr: "node2.internal:15100",
			WSAddr:  "ws://node2.internal:15200",
			WSSAddr: "wss://node2.internal:15300",
		},
		Intranet: accessapi.LegacyRouteAddresses{
			TCPAddr: "node2.internal:5100",
		},
	}, nodes[2])
}

func TestConfigPreservesExplicitDataPlaneRPCTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.DataPlaneRPCTimeout = 250 * time.Millisecond

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, 250*time.Millisecond, cfg.Cluster.DataPlaneRPCTimeout)
}

func TestConfigDefaultsSendPathTuning(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 1*time.Second, clusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval"))
	require.Equal(t, 1*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait"))
	require.Equal(t, 64, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords"))
	require.Equal(t, 64*1024, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes"))
	require.Equal(t, 200*time.Microsecond, cfg.Cluster.CommitCoordinatorFlushWindow)
	require.Zero(t, cfg.Cluster.CommitCoordinatorMaxRequests)
	require.Zero(t, cfg.Cluster.CommitCoordinatorMaxRecords)
	require.Zero(t, cfg.Cluster.CommitCoordinatorMaxBytes)
	require.Equal(t, 1*time.Second, cfg.Cluster.DataPlaneRPCTimeout)
	require.Equal(t, 4, cfg.Cluster.DataPlanePoolSize)
	require.Equal(t, 4, cfg.Cluster.DataPlaneMaxFetchInflight)
	require.Equal(t, 4, cfg.Cluster.DataPlaneMaxPendingFetch)
}

func TestConfigDefaultsConversationActiveHints(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 10*time.Second, cfg.Conversation.ActiveHintFlushInterval)
	require.Equal(t, 30*time.Minute, cfg.Conversation.ActiveHintTTL)
	require.Equal(t, 30*time.Minute, cfg.Conversation.ActiveHintBarrierTTL)
	require.Equal(t, 100000, cfg.Conversation.ActiveHintMaxHints)
	require.Equal(t, 1000, cfg.Conversation.ActiveHintMaxHintsPerUID)
	require.Equal(t, 32, cfg.Conversation.ActiveHintFlushBatchSize)
	require.Equal(t, 5*time.Minute, cfg.Conversation.GroupActiveFanoutInterval)
	require.Zero(t, cfg.Conversation.GroupActiveFanoutMaxSubscribers)
}

func TestConfigDefaultsNetworkObservabilityEnabled(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.True(t, cfg.Observability.NetworkEnabled)
}

func TestConfigAlwaysAppliesLongPollDefaults(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 8, cfg.Cluster.LongPollLaneCount)
	require.Equal(t, 200*time.Millisecond, cfg.Cluster.LongPollMaxWait)
	require.Equal(t, 64*1024, cfg.Cluster.LongPollMaxBytes)
	require.Equal(t, 64, cfg.Cluster.LongPollMaxChannels)
}

func TestConfigDefaultsClusterMaxChannelsToUnlimited(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Zero(t, cfg.Cluster.MaxChannels)
	require.Zero(t, cfg.Cluster.ChannelIdleTimeout)
	require.Zero(t, cfg.Cluster.ChannelIdleScanInterval)
}

func TestConfigChannelPlaneDefaults(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.GreaterOrEqual(t, cfg.ChannelPlane.ReactorCount, 4)
	require.GreaterOrEqual(t, cfg.ChannelPlane.PeerLaneCount, 1)
	require.LessOrEqual(t, cfg.ChannelPlane.PeerLaneCount, 8)
	require.Equal(t, 500*time.Microsecond, cfg.ChannelPlane.PeerBatchMaxWait)
}

func TestConfigRejectsInvalidChannelPlaneValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "negative reactor count",
			mutate: func(cfg *Config) {
				cfg.ChannelPlane.ReactorCount = -1
			},
			wantErr: "channel plane reactor count",
		},
		{
			name: "negative peer lane count",
			mutate: func(cfg *Config) {
				cfg.ChannelPlane.PeerLaneCount = -1
			},
			wantErr: "channel plane peer lane count",
		},
		{
			name: "negative peer batch max wait",
			mutate: func(cfg *Config) {
				cfg.ChannelPlane.PeerBatchMaxWait = -time.Microsecond
			},
			wantErr: "channel plane peer batch max wait",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), tt.wantErr)
		})
	}
}

func TestConfigDefaultsChannelExecutionModeToPooled(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, "pooled", cfg.Cluster.ChannelExecutionMode)
}

func TestConfigPreservesExplicitClusterMaxChannels(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.MaxChannels = 4096
	cfg.Cluster.ChannelIdleTimeout = 30 * time.Minute
	cfg.Cluster.ChannelIdleScanInterval = time.Minute

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 4096, cfg.Cluster.MaxChannels)
	require.Equal(t, 30*time.Minute, cfg.Cluster.ChannelIdleTimeout)
	require.Equal(t, time.Minute, cfg.Cluster.ChannelIdleScanInterval)
}

func TestConfigPreservesDedicatedChannelExecutionMode(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ChannelExecutionMode = "dedicated"

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, "dedicated", cfg.Cluster.ChannelExecutionMode)
}

func TestConfigPreservesChannelExecutionPoolSettings(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ChannelExecutionMode = "pooled"
	cfg.Cluster.ChannelExecutionWorkers = 8
	cfg.Cluster.ChannelExecutionQueueSize = 4096

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, "pooled", cfg.Cluster.ChannelExecutionMode)
	require.Equal(t, 8, cfg.Cluster.ChannelExecutionWorkers)
	require.Equal(t, 4096, cfg.Cluster.ChannelExecutionQueueSize)
}

func TestConfigRejectsInvalidChannelExecutionSettings(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ClusterConfig)
		want string
	}{
		{
			name: "invalid mode",
			edit: func(cfg *ClusterConfig) {
				cfg.ChannelExecutionMode = "bogus"
			},
			want: "channel execution mode",
		},
		{
			name: "negative workers",
			edit: func(cfg *ClusterConfig) {
				cfg.ChannelExecutionWorkers = -1
			},
			want: "channel execution worker",
		},
		{
			name: "negative queue size",
			edit: func(cfg *ClusterConfig) {
				cfg.ChannelExecutionQueueSize = -1
			},
			want: "channel execution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.edit(&cfg.Cluster)

			require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), tt.want)
		})
	}
}

func TestConfigRejectsNegativeClusterMaxChannels(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.MaxChannels = -1

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "cluster max channels")
}

func TestConfigRejectsNegativeChannelIdleEviction(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.ChannelIdleTimeout = -time.Second

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "channel idle timeout")
	})

	t.Run("scan interval", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.ChannelIdleScanInterval = -time.Second

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "channel idle scan interval")
	})
}

func TestConfigDefaultsChannelMessageRetentionDisabled(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Zero(t, cfg.ChannelMessageRetention.TTL)
	require.Equal(t, time.Hour, cfg.ChannelMessageRetention.ScanInterval)
	require.Equal(t, 128, cfg.ChannelMessageRetention.ChannelBatchSize)
	require.Equal(t, 10000, cfg.ChannelMessageRetention.MaxTrimMessages)
}

func TestChannelMigrationConfigDefaults(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, time.Second, cfg.ChannelMigration.ScanInterval)
	require.Equal(t, 64, cfg.ChannelMigration.ScanLimit)
	require.Equal(t, 30*time.Second, cfg.ChannelMigration.OwnerLeaseTTL)
	require.Equal(t, time.Minute, cfg.ChannelMigration.RetryBackoff)
	require.Equal(t, time.Minute, cfg.ChannelMigration.FenceTTL)
	require.Equal(t, time.Minute, cfg.ChannelMigration.LeaderLeaseTTL)
	require.Equal(t, time.Second, cfg.ChannelMigration.CatchUpStableWindow)
	require.Equal(t, uint64(0), cfg.ChannelMigration.CatchUpLagThreshold)
	require.Equal(t, 64, cfg.ChannelMigration.MaxConcurrent)
	require.Equal(t, 1, cfg.ChannelMigration.MaxConcurrentPerSource)
	require.Equal(t, 1, cfg.ChannelMigration.MaxConcurrentPerTarget)
	require.Equal(t, 24*time.Hour, cfg.ChannelMigration.CompletedRetentionTTL)
	require.Equal(t, 128, cfg.ChannelMigration.GCLimit)
}

func TestChannelMigrationConfigIncludesSourceTargetLimitsAndRetention(t *testing.T) {
	cfg := validConfig()
	cfg.ChannelMigration.ScanInterval = 250 * time.Millisecond
	cfg.ChannelMigration.ScanLimit = 17
	cfg.ChannelMigration.OwnerLeaseTTL = 45 * time.Second
	cfg.ChannelMigration.RetryBackoff = 3 * time.Second
	cfg.ChannelMigration.FenceTTL = 90 * time.Second
	cfg.ChannelMigration.LeaderLeaseTTL = 2 * time.Minute
	cfg.ChannelMigration.CatchUpStableWindow = 5 * time.Second
	cfg.ChannelMigration.CatchUpLagThreshold = 4
	cfg.ChannelMigration.MaxConcurrent = 8
	cfg.ChannelMigration.MaxConcurrentPerSource = 2
	cfg.ChannelMigration.MaxConcurrentPerTarget = 3
	cfg.ChannelMigration.CompletedRetentionTTL = 72 * time.Hour
	cfg.ChannelMigration.GCLimit = 9

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

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

func TestConfigValidateRejectsInvalidChannelMessageRetentionWhenEnabled(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Config)
		wantErr   string
	}{
		{
			name: "negative ttl",
			configure: func(cfg *Config) {
				cfg.ChannelMessageRetention.TTL = -time.Second
			},
			wantErr: "channel message retention ttl",
		},
		{
			name: "negative scan interval",
			configure: func(cfg *Config) {
				cfg.ChannelMessageRetention.TTL = time.Hour
				cfg.ChannelMessageRetention.ScanInterval = -time.Second
			},
			wantErr: "channel message retention scan interval",
		},
		{
			name: "explicit zero scan interval",
			configure: func(cfg *Config) {
				cfg.ChannelMessageRetention.TTL = time.Hour
				cfg.ChannelMessageRetention.SetExplicitFlags(true, false, false)
			},
			wantErr: "channel message retention scan interval",
		},
		{
			name: "negative channel batch size",
			configure: func(cfg *Config) {
				cfg.ChannelMessageRetention.TTL = time.Hour
				cfg.ChannelMessageRetention.ChannelBatchSize = -1
			},
			wantErr: "channel message retention channel batch size",
		},
		{
			name: "explicit zero channel batch size",
			configure: func(cfg *Config) {
				cfg.ChannelMessageRetention.TTL = time.Hour
				cfg.ChannelMessageRetention.SetExplicitFlags(false, true, false)
			},
			wantErr: "channel message retention channel batch size",
		},
		{
			name: "negative max trim messages",
			configure: func(cfg *Config) {
				cfg.ChannelMessageRetention.TTL = time.Hour
				cfg.ChannelMessageRetention.MaxTrimMessages = -1
			},
			wantErr: "channel message retention max trim messages",
		},
		{
			name: "explicit zero max trim messages",
			configure: func(cfg *Config) {
				cfg.ChannelMessageRetention.TTL = time.Hour
				cfg.ChannelMessageRetention.SetExplicitFlags(false, false, true)
			},
			wantErr: "channel message retention max trim messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.configure(&cfg)

			require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), tt.wantErr)
		})
	}
}

func TestConfigLongPollPreservesExplicitOverridesWithoutReplicationMode(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.LongPollLaneCount = 16
	cfg.Cluster.LongPollMaxWait = 2 * time.Millisecond
	cfg.Cluster.LongPollMaxBytes = 128 * 1024
	cfg.Cluster.LongPollMaxChannels = 32

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 16, cfg.Cluster.LongPollLaneCount)
	require.Equal(t, 2*time.Millisecond, cfg.Cluster.LongPollMaxWait)
	require.Equal(t, 128*1024, cfg.Cluster.LongPollMaxBytes)
	require.Equal(t, 32, cfg.Cluster.LongPollMaxChannels)
}

func TestConfigPreservesExplicitSendPathTuning(t *testing.T) {
	cfg := validConfig()
	setClusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval", 250*time.Millisecond)
	setClusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait", 2*time.Millisecond)
	setClusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords", 128)
	setClusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes", 256*1024)
	cfg.Cluster.DataPlanePoolSize = 8
	cfg.Cluster.DataPlaneMaxFetchInflight = 16
	cfg.Cluster.DataPlaneMaxPendingFetch = 16
	cfg.Cluster.CommitCoordinatorFlushWindow = 500 * time.Microsecond
	cfg.Cluster.CommitCoordinatorMaxRequests = 32
	cfg.Cluster.CommitCoordinatorMaxRecords = 512
	cfg.Cluster.CommitCoordinatorMaxBytes = 512 * 1024

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 250*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval"))
	require.Equal(t, 2*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait"))
	require.Equal(t, 128, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords"))
	require.Equal(t, 256*1024, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes"))
	require.Equal(t, 8, cfg.Cluster.DataPlanePoolSize)
	require.Equal(t, 16, cfg.Cluster.DataPlaneMaxFetchInflight)
	require.Equal(t, 16, cfg.Cluster.DataPlaneMaxPendingFetch)
	require.Equal(t, 500*time.Microsecond, cfg.Cluster.CommitCoordinatorFlushWindow)
	require.Equal(t, 32, cfg.Cluster.CommitCoordinatorMaxRequests)
	require.Equal(t, 512, cfg.Cluster.CommitCoordinatorMaxRecords)
	require.Equal(t, 512*1024, cfg.Cluster.CommitCoordinatorMaxBytes)
}

func TestConfigRejectsExplicitInvalidSendPathTuning(t *testing.T) {
	t.Run("follower replication retry interval", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.FollowerReplicationRetryInterval = 0
		cfg.Cluster.SetExplicitFlags(false, true, false, false, false)

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "follower replication retry interval")
	})

	t.Run("append group commit max wait", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.AppendGroupCommitMaxWait = 0
		cfg.Cluster.SetExplicitFlags(false, false, true, false, false)

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "append group commit max wait")
	})

	t.Run("append group commit max records", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.AppendGroupCommitMaxRecords = 0
		cfg.Cluster.SetExplicitFlags(false, false, false, true, false)

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "append group commit max records")
	})

	t.Run("append group commit max bytes", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.AppendGroupCommitMaxBytes = 0
		cfg.Cluster.SetExplicitFlags(false, false, false, false, true)

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "append group commit max bytes")
	})

	t.Run("negative follower replication retry interval", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.FollowerReplicationRetryInterval = -time.Second
		cfg.Cluster.SetExplicitFlags(false, true, false, false, false)

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "follower replication retry interval")
	})

	t.Run("negative append group commit max records", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.AppendGroupCommitMaxRecords = -1
		cfg.Cluster.SetExplicitFlags(false, false, false, true, false)

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "append group commit max records")
	})
	t.Run("commit coordinator flush window", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.CommitCoordinatorFlushWindow = 0
		cfg.Cluster.SetCommitCoordinatorExplicitFlags(true, false, false, false)

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "commit coordinator flush window")
	})

	t.Run("negative commit coordinator max requests", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.CommitCoordinatorMaxRequests = -1

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "commit coordinator max requests")
	})

	t.Run("negative commit coordinator max records", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.CommitCoordinatorMaxRecords = -1

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "commit coordinator max records")
	})

	t.Run("negative commit coordinator max bytes", func(t *testing.T) {
		cfg := validConfig()
		cfg.Cluster.CommitCoordinatorMaxBytes = -1

		require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "commit coordinator max bytes")
	})
}

func TestConfigDefaultsChannelBootstrapMinISR(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ChannelBootstrapDefaultMinISR = 0
	cfg.Cluster.SetExplicitFlags(false, false, false, false, false)

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, 2, cfg.Cluster.ChannelBootstrapDefaultMinISR)
}

func TestConfigRejectsExplicitNonPositiveChannelBootstrapMinISR(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ChannelBootstrapDefaultMinISR = 0
	cfg.Cluster.SetExplicitFlags(true, false, false, false, false)

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "channel bootstrap default min isr")
}

func TestConfigRejectsExplicitNegativeChannelBootstrapMinISR(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ChannelBootstrapDefaultMinISR = -1
	cfg.Cluster.SetExplicitFlags(true, false, false, false, false)

	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "channel bootstrap default min isr")
}

func TestConfigApplyDefaultsEnablesControllerLogCompaction(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ControllerLogCompaction = ControllerLogCompactionConfig{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.True(t, cfg.Cluster.ControllerLogCompaction.Enabled)
	require.Equal(t, uint64(10000), cfg.Cluster.ControllerLogCompaction.TriggerEntries)
	require.Equal(t, 30*time.Second, cfg.Cluster.ControllerLogCompaction.CheckInterval)
}

func TestConfigApplyDefaultsPreservesControllerLogCompactionDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ControllerLogCompaction.Enabled = false
	cfg.Cluster.SetControllerLogCompactionExplicitFlags(true, false, false)

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.False(t, cfg.Cluster.ControllerLogCompaction.Enabled)
}

func TestConfigApplyDefaultsEnablesSlotLogCompaction(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.SlotLogCompaction = SlotLogCompactionConfig{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.True(t, cfg.Cluster.SlotLogCompaction.Enabled)
	require.Equal(t, uint64(10000), cfg.Cluster.SlotLogCompaction.TriggerEntries)
	require.Equal(t, 30*time.Second, cfg.Cluster.SlotLogCompaction.CheckInterval)
}

func TestConfigApplyDefaultsPreservesSlotLogCompactionDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.SlotLogCompaction.Enabled = false
	cfg.Cluster.SetSlotLogCompactionExplicitFlags(true, false, false)

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.False(t, cfg.Cluster.SlotLogCompaction.Enabled)
}

func TestConfigRejectsInvalidEnabledSlotLogCompaction(t *testing.T) {
	tests := []struct {
		name           string
		triggerEntries uint64
		checkInterval  time.Duration
		triggerSet     bool
		checkSet       bool
	}{
		{name: "explicit zero trigger entries", triggerEntries: 0, checkInterval: time.Second, triggerSet: true},
		{name: "explicit zero check interval", triggerEntries: 1, checkInterval: 0, checkSet: true},
		{name: "explicit negative check interval", triggerEntries: 1, checkInterval: -time.Second, checkSet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Cluster.SlotLogCompaction = SlotLogCompactionConfig{
				Enabled:        true,
				TriggerEntries: tt.triggerEntries,
				CheckInterval:  tt.checkInterval,
			}
			cfg.Cluster.SetSlotLogCompactionExplicitFlags(true, tt.triggerSet, tt.checkSet)

			require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "slot log compaction")
		})
	}
}

func TestConfigRejectsInvalidEnabledControllerLogCompaction(t *testing.T) {
	tests := []struct {
		name           string
		triggerEntries uint64
		checkInterval  time.Duration
		triggerSet     bool
		checkSet       bool
	}{
		{name: "explicit zero trigger entries", triggerEntries: 0, checkInterval: time.Second, triggerSet: true},
		{name: "explicit zero check interval", triggerEntries: 1, checkInterval: 0, checkSet: true},
		{name: "explicit negative check interval", triggerEntries: 1, checkInterval: -time.Second, checkSet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Cluster.ControllerLogCompaction = ControllerLogCompactionConfig{
				Enabled:        true,
				TriggerEntries: tt.triggerEntries,
				CheckInterval:  tt.checkInterval,
			}
			cfg.Cluster.SetControllerLogCompactionExplicitFlags(true, tt.triggerSet, tt.checkSet)

			require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "controller log compaction")
		})
	}
}

func TestClusterRuntimeConfigIncludesTimeoutOverrides(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Timeouts = raftcluster.Timeouts{
		ControllerObservation:     350 * time.Millisecond,
		ControllerRequest:         3 * time.Second,
		ControllerLeaderWait:      9 * time.Second,
		ForwardRetryBudget:        600 * time.Millisecond,
		ManagedSlotLeaderWait:     6 * time.Second,
		ManagedSlotCatchUp:        7 * time.Second,
		ManagedSlotLeaderMove:     8 * time.Second,
		ConfigChangeRetryBudget:   700 * time.Millisecond,
		LeaderTransferRetryBudget: 800 * time.Millisecond,
	}

	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)

	require.Equal(t, cfg.Cluster.Timeouts, runtimeCfg.Timeouts)
}

func TestClusterRuntimeConfigIncludesControllerLogCompaction(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)

	require.Equal(t, controllerraft.LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 10000,
		CheckInterval:  30 * time.Second,
	}, runtimeCfg.ControllerLogCompaction)
}

func TestClusterRuntimeConfigIncludesSlotLogCompaction(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)

	require.Equal(t, multiraft.LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 10000,
		CheckInterval:  30 * time.Second,
	}, runtimeCfg.SlotLogCompaction)
}

func TestClusterRuntimeConfigIncludesHashSlotMigrationGate(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.EnableHashSlotMigration = true

	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)

	require.True(t, runtimeCfg.EnableHashSlotMigration)
}

func TestClusterRuntimeConfigIncludesControllerSnapshotStorage(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotPath = "/tmp/wukong-node-1/slot-snapshots"
	cfg.Storage.ControllerRaftSnapshotPath = "/tmp/wukong-node-1/controller-snapshots"
	cfg.Storage.RaftSnapshotChunkSize = 4 << 20
	cfg.Storage.RaftSnapshotGCGrace = 45 * time.Minute
	cfg.Storage.SetRaftSnapshotExplicitFlags(true, true)

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)

	require.Equal(t, "/tmp/wukong-node-1/controller-snapshots", runtimeCfg.ControllerRaftSnapshotPath)
	require.Equal(t, uint64(4<<20), runtimeCfg.RaftSnapshotChunkSize)
	require.Equal(t, 45*time.Minute, runtimeCfg.RaftSnapshotGCGrace)
}

func TestClusterRuntimeConfigPreservesExplicitZeroSnapshotGCGrace(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.RaftSnapshotGCGrace = 0
	cfg.Storage.SetRaftSnapshotExplicitFlags(false, true)

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)

	require.Equal(t, time.Duration(0), runtimeCfg.RaftSnapshotGCGrace)
	require.True(t, raftClusterConfigBoolField(t, &runtimeCfg, "raftSnapshotGCGraceSet"))
}

func TestBuildPassesSnapshotOptionsToSlotAndControllerRaftlog(t *testing.T) {
	cfg := validConfig()
	cfg.Node.DataDir = t.TempDir()
	cfg.Storage = StorageConfig{
		DBPath:                     filepath.Join(cfg.Node.DataDir, "data"),
		RaftPath:                   filepath.Join(cfg.Node.DataDir, "raft"),
		ChannelLogPath:             filepath.Join(cfg.Node.DataDir, "channel"),
		ControllerMetaPath:         filepath.Join(cfg.Node.DataDir, "controller-meta"),
		ControllerRaftPath:         filepath.Join(cfg.Node.DataDir, "controller-raft"),
		RaftSnapshotPath:           filepath.Join(cfg.Node.DataDir, "slot-snapshots"),
		ControllerRaftSnapshotPath: filepath.Join(cfg.Node.DataDir, "controller-snapshots"),
		RaftSnapshotChunkSize:      2 << 20,
		RaftSnapshotGCGrace:        10 * time.Minute,
	}
	cfg.Storage.SetRaftSnapshotExplicitFlags(true, true)

	stopErr := errors.New("stop after raft open")
	var capturedPath string
	var capturedOptions raftstorage.Options
	originalOpen := openRaftLogDB
	openRaftLogDB = func(path string, opts raftstorage.Options) (*raftstorage.DB, error) {
		capturedPath = path
		capturedOptions = opts
		return nil, stopErr
	}
	t.Cleanup(func() { openRaftLogDB = originalOpen })

	_, err := build(cfg)
	require.ErrorIs(t, err, stopErr)
	require.Equal(t, cfg.Storage.RaftPath, capturedPath)
	require.Equal(t, raftstorage.Options{
		SnapshotPath:      cfg.Storage.RaftSnapshotPath,
		SnapshotChunkSize: cfg.Storage.RaftSnapshotChunkSize,
		SnapshotGCGrace:   cfg.Storage.RaftSnapshotGCGrace,
	}, capturedOptions)

	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)
	require.Equal(t, cfg.Storage.ControllerRaftSnapshotPath, runtimeCfg.ControllerRaftSnapshotPath)
	require.Equal(t, cfg.Storage.RaftSnapshotChunkSize, runtimeCfg.RaftSnapshotChunkSize)
	require.Equal(t, cfg.Storage.RaftSnapshotGCGrace, runtimeCfg.RaftSnapshotGCGrace)
}

func TestClusterRuntimeConfigIncludesDynamicJoinSettings(t *testing.T) {
	cfg := validConfig()
	cfg.Node.Name = "worker-4"
	cfg.Cluster.Seeds = []string{"wk-node1:7000", "wk-node2:7000"}
	cfg.Cluster.AdvertiseAddr = "wk-node4:7000"
	cfg.Cluster.JoinToken = "join-secret"

	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, cfg.Node.Name, nil)

	require.Equal(t, "worker-4", runtimeCfg.Name)
	require.Equal(t, "wk-node4:7000", runtimeCfg.AdvertiseAddr)
	require.Equal(t, "join-secret", runtimeCfg.JoinToken)
	require.Len(t, runtimeCfg.Seeds, 2)
	require.Equal(t, "wk-node1:7000", runtimeCfg.Seeds[0].Addr)
	require.Equal(t, "wk-node2:7000", runtimeCfg.Seeds[1].Addr)
	require.NotZero(t, runtimeCfg.Seeds[0].ID)
	require.NotEqual(t, runtimeCfg.Seeds[0].ID, runtimeCfg.Seeds[1].ID)
}

func validConfig() Config {
	return Config{
		Node: NodeConfig{
			ID:      1,
			Name:    "node-1",
			DataDir: "/tmp/wukong-node-1",
		},
		Cluster: ClusterConfig{
			ListenAddr:                    "127.0.0.1:7000",
			SlotCount:                     1,
			Nodes:                         []NodeConfigRef{{ID: 1, Addr: "127.0.0.1:7000"}},
			ControllerReplicaN:            1,
			SlotReplicaN:                  1,
			ForwardTimeout:                5 * time.Second,
			PoolSize:                      4,
			TickInterval:                  100 * time.Millisecond,
			RaftWorkers:                   2,
			ElectionTick:                  10,
			HeartbeatTick:                 1,
			ChannelBootstrapDefaultMinISR: 2,
			DialTimeout:                   5 * time.Second,
		},
		API: APIConfig{},
		Gateway: GatewayConfig{
			Listeners: []gateway.ListenerOptions{
				binding.TCPWKProto("tcp-wkproto", "127.0.0.1:5100"),
			},
		},
	}
}

func boolPtr(v bool) *bool { return &v }

func TestConfigValidateMessageUserRateLimitDefaultsAndRejectsInvalidValues(t *testing.T) {
	cfg := validConfig()
	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.False(t, cfg.Message.UserRateLimitEnabled)
	require.Equal(t, 100.0, cfg.Message.UserRateLimitRate)
	require.Equal(t, 200, cfg.Message.UserRateLimitBurst)
	require.Equal(t, 256, cfg.Message.UserRateLimitBucketShards)
	require.Equal(t, 10*time.Minute, cfg.Message.UserRateLimitIdleTTL)
	require.Equal(t, 100000, cfg.Message.UserRateLimitMaxBuckets)
	require.True(t, cfg.Message.UserRateLimitSystemUIDBypass)
	require.False(t, cfg.Message.UserRateLimitPluginBypass)

	cfg = validConfig()
	cfg.Message.UserRateLimitEnabled = true
	cfg.Message.UserRateLimitRate = 0
	cfg.Message.SetExplicitFlags(true, false, false, false, false, false)
	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "message user rate limit rate")

	cfg = validConfig()
	cfg.Message.UserRateLimitBucketShards = -1
	require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "message user rate limit bucket shards")
}
