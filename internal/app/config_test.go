package app

import (
	"reflect"
	"testing"
	"time"
	"unsafe"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
)

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

func TestConfigGatewayDefaultsSessionOptions(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.DefaultSession = gateway.SessionOptions{}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.NotNil(t, cfg.Gateway.DefaultSession.CloseOnHandlerError)
	require.True(t, *cfg.Gateway.DefaultSession.CloseOnHandlerError)
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
	require.Equal(t, 1*time.Second, cfg.Cluster.DataPlaneRPCTimeout)
	require.Equal(t, 4, cfg.Cluster.DataPlanePoolSize)
	require.Equal(t, 4, cfg.Cluster.DataPlaneMaxFetchInflight)
	require.Equal(t, 4, cfg.Cluster.DataPlaneMaxPendingFetch)
}

func TestConfigAlwaysAppliesLongPollDefaults(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 8, cfg.Cluster.LongPollLaneCount)
	require.Equal(t, 200*time.Millisecond, cfg.Cluster.LongPollMaxWait)
	require.Equal(t, 64*1024, cfg.Cluster.LongPollMaxBytes)
	require.Equal(t, 64, cfg.Cluster.LongPollMaxChannels)
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

	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	require.Equal(t, 250*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval"))
	require.Equal(t, 2*time.Millisecond, clusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait"))
	require.Equal(t, 128, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords"))
	require.Equal(t, 256*1024, clusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes"))
	require.Equal(t, 8, cfg.Cluster.DataPlanePoolSize)
	require.Equal(t, 16, cfg.Cluster.DataPlaneMaxFetchInflight)
	require.Equal(t, 16, cfg.Cluster.DataPlaneMaxPendingFetch)
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

	runtimeCfg := cfg.Cluster.runtimeConfig(cfg.Storage, nil, nil, cfg.Node.ID, nil)

	require.Equal(t, cfg.Cluster.Timeouts, runtimeCfg.Timeouts)
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
