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
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"stdnet","protocol":"wkproto"}]`,
		"WK_API_LISTEN_ADDR=127.0.0.1:8080",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cfg.Node.ID)
	require.Equal(t, "node-1", cfg.Node.Name)
	require.Equal(t, dataDir, cfg.Node.DataDir)
	require.Equal(t, "127.0.0.1:7000", cfg.Cluster.ListenAddr)
	require.Equal(t, "127.0.0.1:8080", cfg.API.ListenAddr)
	require.Len(t, cfg.Cluster.Nodes, 1)
	require.Empty(t, cfg.Cluster.Slots)
	require.Len(t, cfg.Gateway.Listeners, 1)
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
	require.True(t, cfg.Observability.HealthDetailEnabled)
	require.False(t, cfg.Observability.HealthDebugEnabled)
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
		"WK_HEALTH_DETAIL_ENABLE=false",
		"WK_HEALTH_DEBUG_ENABLE=true",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.False(t, cfg.Observability.MetricsEnabled)
	require.False(t, cfg.Observability.HealthDetailEnabled)
	require.True(t, cfg.Observability.HealthDebugEnabled)
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
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"stdnet","protocol":"wkproto"}]`,
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
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"stdnet","protocol":"wkproto"}]`,
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

		cfg, err := loadConfig(basePath)
		require.NoError(t, err)

		require.Equal(t, 250*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval"))
		require.Equal(t, 2*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait"))
		require.Equal(t, 128, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords"))
		require.Equal(t, 256*1024, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes"))
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
	require.Equal(t, 600*time.Millisecond, cfg.Cluster.Timeouts.ForwardRetryBudget)
	require.Equal(t, 6*time.Second, cfg.Cluster.Timeouts.ManagedSlotLeaderWait)
	require.Equal(t, 7*time.Second, cfg.Cluster.Timeouts.ManagedSlotCatchUp)
	require.Equal(t, 8*time.Second, cfg.Cluster.Timeouts.ManagedSlotLeaderMove)
	require.Equal(t, 700*time.Millisecond, cfg.Cluster.Timeouts.ConfigChangeRetryBudget)
	require.Equal(t, 800*time.Millisecond, cfg.Cluster.Timeouts.LeaderTransferRetryBudget)
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

func TestLoadConfigParsesGatewayAsyncSendDispatchFromConf(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_SLOT_COUNT=1",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH=true",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.True(t, cfg.Gateway.DefaultSession.AsyncSendDispatch)
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

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 16, cfg.Cluster.LongPollLaneCount)
	require.Equal(t, 2*time.Millisecond, cfg.Cluster.LongPollMaxWait)
	require.Equal(t, 128*1024, cfg.Cluster.LongPollMaxBytes)
	require.Equal(t, 32, cfg.Cluster.LongPollMaxChannels)
}
