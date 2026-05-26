package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unsafe"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestNewBuildsDBClusterStoreMessageAndGatewayAdapter(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.RaftDB().Close())
		require.NoError(t, app.DB().Close())
	})

	require.NotNil(t, app.DB())
	require.NotNil(t, app.RaftDB())
	require.NotNil(t, app.Cluster())
	require.NotNil(t, app.Store())
	require.NotNil(t, app.Message())
	require.NotNil(t, app.channelPlane)
	require.Same(t, app.channelPlane, unexportedFieldForTest(t, app.messageApp, "appender"))
	requireAppFieldNonNil(t, app, "conversationActiveHints")
	require.NotNil(t, app.GatewayHandler())
	require.NotNil(t, app.Gateway())
	require.Nil(t, app.API())
}

func TestNewWiresMessagePermissionConfig(t *testing.T) {
	cfg := testConfig(t)
	cfg.Message.PersonWhitelistEnabled = true
	cfg.Message.SystemDeviceID = "custom-device"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.RaftDB().Close())
		require.NoError(t, app.DB().Close())
	})

	msg := reflect.ValueOf(app.messageApp).Elem()
	require.True(t, msg.FieldByName("personWhitelistEnabled").Bool())
	require.Equal(t, "custom-device", msg.FieldByName("systemDeviceID").String())
}

func TestNewBuildsOptionalAPIServerWhenConfigured(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.RaftDB().Close())
		require.NoError(t, app.DB().Close())
	})

	require.NotNil(t, app.API())
}

func TestNewBuildsOptionalManagerServerWhenConfigured(t *testing.T) {
	cfg := testConfig(t)
	cfg.Manager = validManagerConfigForTest()
	cfg.Manager.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.NotNil(t, app.Manager())
}

func TestNewBuildsRootLogger(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.NotNil(t, app.logger)
	require.NotNil(t, app.logger.Named("child"))
}

func TestNewBuildsChannelLogDataPlane(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.NotNil(t, app.ChannelLogDB())
	require.NotNil(t, app.ISRRuntime())
	require.NotNil(t, app.ChannelLog())
}

func TestNewBuildsDefaultChannelReplicaExecutionPool(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, "pooled", app.cfg.Cluster.ChannelExecutionMode)
	require.NotNil(t, app.replicaExecutionPool)
}

func TestNewBuildsDefaultChannelReplicaExecutionPoolWhenMetricsDisabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Observability.MetricsEnabled = false
	cfg.Observability.SetExplicitFlags(true, false, false)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, "pooled", app.cfg.Cluster.ChannelExecutionMode)
	require.Nil(t, app.metrics)
	require.NotNil(t, app.replicaExecutionPool)
}

func TestNewSkipsNetworkObservabilityWhenDisabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Observability.NetworkEnabled = false
	cfg.Observability.SetNetworkExplicitFlag(true)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Nil(t, app.networkObservability)
}

func TestNewPreservesDedicatedChannelReplicaExecutionMode(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cluster.ChannelExecutionMode = "dedicated"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, "dedicated", app.cfg.Cluster.ChannelExecutionMode)
	require.Nil(t, app.replicaExecutionPool)
}

func TestNewBuildsDeliveryRuntimeLifecycle(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.NotNil(t, app.deliveryRuntime)
	require.NotNil(t, app.deliveryRuntimeLifecycle)
}

func TestNewBuildsChannelRetentionWorkerWhenTTLEnabled(t *testing.T) {
	cfg := testConfig(t)
	setChannelMessageRetentionTTLForTest(t, &cfg, time.Hour)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.NotNil(t, app.channelRetentionWorker)
}

func TestNewConfiguresISRMaxFetchInflightPeerWithMinimumConcurrency(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cluster.PoolSize = 1

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, 2, appISRMaxFetchInflightPeerLimit(t, app))
}

func TestNewConfiguresISRMaxFetchInflightPeerFromClusterPoolSize(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cluster.PoolSize = 4

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, 4, appISRMaxFetchInflightPeerLimit(t, app))
}

func TestNewConfiguresIndependentDataPlaneLimits(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cluster.PoolSize = 1
	setClusterConfigIntField(t, &cfg.Cluster, "DataPlaneMaxFetchInflight", 7)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, 7, appISRMaxFetchInflightPeerLimit(t, app))
}

func TestNewConfiguresSendPathTuning(t *testing.T) {
	cfg := testConfig(t)
	setClusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval", 250*time.Millisecond)
	setClusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait", 2*time.Millisecond)
	setClusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords", 128)
	setClusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes", 256*1024)
	cfg.Cluster.CommitCoordinatorFlushWindow = 500 * time.Microsecond
	cfg.Cluster.CommitCoordinatorMaxRequests = 32
	cfg.Cluster.CommitCoordinatorMaxRecords = 512
	cfg.Cluster.CommitCoordinatorMaxBytes = 512 * 1024
	cfg.Cluster.DataPlanePoolSize = 8
	cfg.Cluster.DataPlaneMaxFetchInflight = 16
	cfg.Cluster.DataPlaneMaxPendingFetch = 16

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, 250*time.Millisecond, appISRFollowerReplicationRetryInterval(t, app))
	maxWait, maxRecords, maxBytes := appReplicaAppendGroupCommitConfig(t, app)
	require.Equal(t, 2*time.Millisecond, maxWait)
	require.Equal(t, 128, maxRecords)
	require.Equal(t, 256*1024, maxBytes)
	commitCfg := appChannelStoreCommitCoordinatorConfig(t, app)
	require.Equal(t, 500*time.Microsecond, commitCfg.FlushWindow)
	require.Equal(t, 32, commitCfg.MaxRequests)
	require.Equal(t, 512, commitCfg.MaxRecords)
	require.Equal(t, 512*1024, commitCfg.MaxBytes)
}

func TestStartChannelMetaSyncUsesExplicitDataPlaneSettings(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cluster.PoolSize = 1
	setClusterConfigIntField(t, &cfg.Cluster, "DataPlanePoolSize", 9)
	setClusterConfigIntField(t, &cfg.Cluster, "DataPlaneMaxPendingFetch", 11)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.NoError(t, app.startCluster())
	app.clusterOn.Store(true)
	require.NoError(t, app.startChannelMetaSync())
	app.channelMetaOn.Store(true)
	require.Equal(t, 9, appDataPlanePoolSize(t, app))
	require.Equal(t, 11, appDataPlaneAdapterMaxPendingFetch(t, app))
}

func TestBuildCreatesPresenceAppAndNodeAccess(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	requireAppFieldNonNil(t, app, "presenceApp")
	requireAppFieldNonNil(t, app, "nodeClient")
	requireAppFieldNonNil(t, app, "nodeAccess")
	requireAppFieldNonNil(t, app, "presenceWorker")
}

func TestNewReturnsConfigErrorsBeforeOpeningResources(t *testing.T) {
	cfg := testConfig(t)
	cfg.Node.ID = 0

	_, err := New(cfg)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidConfig)

	_, dbErr := os.Stat(cfg.Storage.DBPath)
	require.ErrorIs(t, dbErr, os.ErrNotExist)

	_, raftErr := os.Stat(cfg.Storage.RaftPath)
	require.ErrorIs(t, raftErr, os.ErrNotExist)
}

func TestAccessorsExposeBuiltRuntime(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Same(t, app.db, app.DB())
	require.Same(t, app.raftDB, app.RaftDB())
	require.Same(t, app.cluster, app.Cluster())
	require.Same(t, app.channelLogDB, app.ChannelLogDB())
	require.Same(t, app.isrRuntime, app.ISRRuntime())
	require.Same(t, app.channelLog, app.ChannelLog())
	require.Same(t, app.store, app.Store())
	require.Same(t, app.messageApp, app.Message())
	require.Same(t, app.gatewayHandler, app.GatewayHandler())
	require.Same(t, app.gateway, app.Gateway())
	require.Same(t, app.api, app.API())
	require.Same(t, app.manager, app.Manager())
}

func TestNewClosesOpenedStoresWhenGatewayBuildFails(t *testing.T) {
	cfg := testConfig(t)
	dup := cfg.Gateway.Listeners[0]
	dup.Name = dup.Name + "-dup"
	cfg.Gateway.Listeners = append(cfg.Gateway.Listeners, dup)

	_, err := New(cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "duplicate listener address")

	reopenedDB, dbOpenErr := openWKDBForTest(cfg.Storage.DBPath)
	require.NoError(t, dbOpenErr)
	require.NoError(t, reopenedDB.Close())

	reopenedRaft, raftOpenErr := openRaftDBForTest(cfg.Storage.RaftPath)
	require.NoError(t, raftOpenErr)
	require.NoError(t, reopenedRaft.Close())
}

func TestStartStartsClusterBeforeGateway(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "gateway.start"}, calls)
	require.True(t, app.started.Load())
}

func TestStartStartsAPIAfterGatewayWhenEnabled(t *testing.T) {
	var calls []string

	app := &App{
		cluster:         &raftcluster.Cluster{},
		channelMetaSync: &channelMetaSync{},
		gateway:         &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.start")
			return nil
		},
		startAPIFn: func() error {
			calls = append(calls, "api.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "meta.start", "gateway.start", "api.start"}, calls)
}

func TestStartStartsManagerAfterAPIWhenEnabled(t *testing.T) {
	var calls []string

	app := &App{
		cluster:         &raftcluster.Cluster{},
		channelMetaSync: &channelMetaSync{},
		gateway:         &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		startAPIFn: func() error {
			calls = append(calls, "api.start")
			return nil
		},
		startManagerFn: func() error {
			calls = append(calls, "manager.start")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "meta.start", "gateway.start", "api.start", "manager.start"}, calls)
}

func TestStartStartsChannelMetaSyncAfterClusterBeforeGateway(t *testing.T) {
	var calls []string

	app := &App{
		cluster:         &raftcluster.Cluster{},
		channelMetaSync: &channelMetaSync{},
		gateway:         &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "meta.start", "gateway.start"}, calls)
}

func TestAppLifecycleStartsPresenceWorkerBeforeGateway(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
	}
	setAppFuncField(t, app, "startPresenceFn", func() error {
		calls = append(calls, "presence.start")
		return nil
	})

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "presence.start", "gateway.start"}, calls)
}

func TestAppLifecycleStartsChannelPlaneAfterChannelMetaBeforeGateway(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &appLifecycleTestCluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "channelmeta.start")
			return nil
		},
		startChannelPlaneFn: func() error {
			calls = append(calls, "channelplane.start")
			return nil
		},
		startPresenceFn: func() error {
			calls = append(calls, "presence.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{
		"cluster.start",
		"channelmeta.start",
		"channelplane.start",
		"presence.start",
		"gateway.start",
	}, calls)
}

func TestAppLifecycleUsesDeclaredComponentOrder(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &appLifecycleTestCluster{
			waitFn: func(context.Context) error {
				calls = append(calls, "managed_slots_ready.start")
				return nil
			},
		},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "channelmeta.start")
			return nil
		},
		startChannelPlaneFn: func() error {
			calls = append(calls, "channelplane.start")
			return nil
		},
		startPresenceFn: func() error {
			calls = append(calls, "presence.start")
			return nil
		},
		startConversationActiveHintsFn: func() error {
			calls = append(calls, "conversation_active_hints.start")
			return nil
		},
		startConversationProjectorFn: func() error {
			calls = append(calls, "conversation_projector.start")
			return nil
		},
		startCMDConversationUpdaterFn: func() error {
			calls = append(calls, "cmd_conversation_updater.start")
			return nil
		},
		startDeliveryRuntimeFn: func() error {
			calls = append(calls, "delivery_runtime.start")
			return nil
		},
		startCommittedDispatcherFn: func() error {
			calls = append(calls, "committed_dispatcher.start")
			return nil
		},
		startCommittedReplayFn: func(context.Context) error {
			calls = append(calls, "committed_replay.start")
			return nil
		},
		startChannelMigrationFn: func(context.Context) error {
			calls = append(calls, "channel_migration.start")
			return nil
		},
		startChannelRetentionFn: func(context.Context) error {
			calls = append(calls, "channel_retention.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		startAPIFn: func() error {
			calls = append(calls, "api.start")
			return nil
		},
		startManagerFn: func() error {
			calls = append(calls, "manager.start")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{
		"cluster.start",
		"managed_slots_ready.start",
		"channelmeta.start",
		"channelplane.start",
		"presence.start",
		"conversation_active_hints.start",
		"conversation_projector.start",
		"cmd_conversation_updater.start",
		"delivery_runtime.start",
		"committed_dispatcher.start",
		"committed_replay.start",
		"channel_migration.start",
		"channel_retention.start",
		"gateway.start",
		"api.start",
		"manager.start",
	}, calls)
}

func TestAppLifecycleStartsPluginBeforeDeliveryRuntimeAndStopsAfter(t *testing.T) {
	var startCalls []string
	var stopCalls []string

	app := &App{
		cluster: &appLifecycleTestCluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			startCalls = append(startCalls, "cluster.start")
			return nil
		},
		startPluginFn: func(context.Context) error {
			startCalls = append(startCalls, "plugin_runtime.start")
			return nil
		},
		startDeliveryRuntimeFn: func() error {
			startCalls = append(startCalls, "delivery_runtime.start")
			return nil
		},
		startCommittedDispatcherFn: func() error {
			startCalls = append(startCalls, "committed_dispatcher.start")
			return nil
		},
		startCommittedReplayFn: func(context.Context) error {
			startCalls = append(startCalls, "committed_replay.start")
			return nil
		},
		startGatewayFn: func() error {
			startCalls = append(startCalls, "gateway.start")
			return nil
		},
		stopGatewayFn: func() error {
			stopCalls = append(stopCalls, "gateway.stop")
			return nil
		},
		stopCommittedReplayFn: func(context.Context) error {
			stopCalls = append(stopCalls, "committed_replay.stop")
			return nil
		},
		stopCommittedDispatcherFn: func(context.Context) error {
			stopCalls = append(stopCalls, "committed_dispatcher.stop")
			return nil
		},
		stopDeliveryRuntimeFn: func() error {
			stopCalls = append(stopCalls, "delivery_runtime.stop")
			return nil
		},
		stopPluginFn: func(context.Context) error {
			stopCalls = append(stopCalls, "plugin_runtime.stop")
			return nil
		},
		stopClusterFn: func() {
			stopCalls = append(stopCalls, "cluster.stop")
		},
		closeRaftDBFn: func() error {
			stopCalls = append(stopCalls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			stopCalls = append(stopCalls, "metadb.close")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{
		"cluster.start",
		"plugin_runtime.start",
		"delivery_runtime.start",
		"committed_dispatcher.start",
		"committed_replay.start",
		"gateway.start",
	}, startCalls)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{
		"gateway.stop",
		"committed_replay.stop",
		"committed_dispatcher.stop",
		"delivery_runtime.stop",
		"plugin_runtime.stop",
		"cluster.stop",
		"raft.close",
		"metadb.close",
	}, stopCalls)
}

func TestAppLifecycleStopsChannelMigrationAfterRetentionAndBeforeReplay(t *testing.T) {
	var startCalls []string
	var stopCalls []string

	app := &App{
		cluster: &appLifecycleTestCluster{
			waitFn: func(context.Context) error {
				startCalls = append(startCalls, "managed_slots_ready.start")
				return nil
			},
		},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			startCalls = append(startCalls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			startCalls = append(startCalls, "channelmeta.start")
			return nil
		},
		startCommittedReplayFn: func(context.Context) error {
			startCalls = append(startCalls, "committed_replay.start")
			return nil
		},
		startChannelMigrationFn: func(context.Context) error {
			startCalls = append(startCalls, "channel_migration.start")
			return nil
		},
		startChannelRetentionFn: func(context.Context) error {
			startCalls = append(startCalls, "channel_retention.start")
			return nil
		},
		startGatewayFn: func() error {
			startCalls = append(startCalls, "gateway.start")
			return nil
		},
		stopGatewayFn: func() error {
			stopCalls = append(stopCalls, "gateway.stop")
			return nil
		},
		stopChannelRetentionFn: func(context.Context) error {
			stopCalls = append(stopCalls, "channel_retention.stop")
			return nil
		},
		stopChannelMigrationFn: func(context.Context) error {
			stopCalls = append(stopCalls, "channel_migration.stop")
			return nil
		},
		stopCommittedReplayFn: func(context.Context) error {
			stopCalls = append(stopCalls, "committed_replay.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			stopCalls = append(stopCalls, "channelmeta.stop")
			return nil
		},
		stopClusterFn: func() {
			stopCalls = append(stopCalls, "cluster.stop")
		},
		closeChannelLogDBFn: func() error {
			stopCalls = append(stopCalls, "channellog.close")
			return nil
		},
		closeRaftDBFn: func() error {
			stopCalls = append(stopCalls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			stopCalls = append(stopCalls, "metadb.close")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{
		"cluster.start",
		"managed_slots_ready.start",
		"channelmeta.start",
		"committed_replay.start",
		"channel_migration.start",
		"channel_retention.start",
		"gateway.start",
	}, startCalls)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{
		"gateway.stop",
		"channel_retention.stop",
		"channel_migration.stop",
		"committed_replay.stop",
		"channelmeta.stop",
		"cluster.stop",
		"channellog.close",
		"raft.close",
		"metadb.close",
	}, stopCalls)
}

func TestAppLifecycleStopsChannelRetentionBeforeCommittedReplayAndStorage(t *testing.T) {
	var calls []string

	app := &App{
		stopChannelRetentionFn: func(ctx context.Context) error {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			calls = append(calls, "channel_retention.stop")
			return nil
		},
		stopCommittedReplayFn: func(context.Context) error {
			calls = append(calls, "committed_replay.stop")
			return nil
		},
		closeChannelLogDBFn: func() error {
			calls = append(calls, "channellog.close")
			return nil
		},
		closeRaftDBFn: func() error {
			calls = append(calls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			calls = append(calls, "metadb.close")
			return nil
		},
	}
	app.channelRetentionOn.Store(true)
	app.committedReplayOn.Store(true)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{
		"channel_retention.stop",
		"committed_replay.stop",
		"channellog.close",
		"raft.close",
		"metadb.close",
	}, calls)
}

func TestAppLifecycleWaitManagedSlotsReadyRollsBackCluster(t *testing.T) {
	waitErr := errors.New("managed slots not ready")
	var calls []string

	app := &App{
		cluster: &appLifecycleTestCluster{
			waitFn: func(context.Context) error {
				calls = append(calls, "managed_slots_ready.start")
				return waitErr
			},
		},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	err := app.Start()
	require.ErrorIs(t, err, waitErr)
	require.Equal(t, []string{"cluster.start", "managed_slots_ready.start", "cluster.stop"}, calls)
	require.False(t, app.started.Load())
}

func TestAppWaitManagedSlotsReadyUsesConfiguredTimeout(t *testing.T) {
	waitErr := errors.New("managed slots inspected")
	const configuredTimeout = 25 * time.Millisecond
	app := &App{
		cfg: Config{Cluster: ClusterConfig{ManagedSlotsReadyTimeout: configuredTimeout}},
		cluster: &appLifecycleTestCluster{
			waitFn: func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				require.True(t, ok, "WaitForManagedSlotsReady context should have a deadline")
				remaining := time.Until(deadline)
				require.Greater(t, remaining, time.Duration(0))
				require.LessOrEqual(t, remaining, configuredTimeout)
				return waitErr
			},
		},
	}

	require.ErrorIs(t, app.waitForManagedSlotsReady(), waitErr)
}

func TestAppLifecycleStopAggregatesComponentErrors(t *testing.T) {
	managerErr := errors.New("manager stop failed")
	apiErr := errors.New("api stop failed")
	gatewayErr := errors.New("gateway stop failed")
	conversationErr := errors.New("conversation stop failed")
	presenceErr := errors.New("presence stop failed")
	channelMetaErr := errors.New("channelmeta stop failed")

	app := &App{
		cluster:                      &appLifecycleTestCluster{},
		gateway:                      &gateway.Gateway{},
		startClusterFn:               func() error { return nil },
		startChannelMetaSyncFn:       func() error { return nil },
		startPresenceFn:              func() error { return nil },
		startConversationProjectorFn: func() error { return nil },
		startGatewayFn:               func() error { return nil },
		startAPIFn:                   func() error { return nil },
		startManagerFn:               func() error { return nil },
		stopManagerFn:                func() error { return managerErr },
		stopAPIFn:                    func() error { return apiErr },
		stopGatewayFn:                func() error { return gatewayErr },
		stopConversationProjectorFn:  func() error { return conversationErr },
		stopPresenceFn:               func() error { return presenceErr },
		stopChannelMetaSyncFn:        func() error { return channelMetaErr },
		stopClusterFn:                func() {},
		closeRaftDBFn:                func() error { return nil },
		closeWKDBFn:                  func() error { return nil },
	}

	require.NoError(t, app.Start())

	err := app.Stop()
	require.ErrorIs(t, err, managerErr)
	require.ErrorIs(t, err, apiErr)
	require.ErrorIs(t, err, gatewayErr)
	require.ErrorIs(t, err, conversationErr)
	require.ErrorIs(t, err, presenceErr)
	require.ErrorIs(t, err, channelMetaErr)
}

func TestAppLifecycleStopAfterPartialStartDoesNotDoubleStop(t *testing.T) {
	startErr := errors.New("api start failed")
	var calls []string

	app := &App{
		cluster: &appLifecycleTestCluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "channelmeta.start")
			return nil
		},
		startPresenceFn: func() error {
			calls = append(calls, "presence.start")
			return nil
		},
		startConversationProjectorFn: func() error {
			calls = append(calls, "conversation_projector.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		startAPIFn: func() error {
			calls = append(calls, "api.start")
			return startErr
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopConversationProjectorFn: func() error {
			calls = append(calls, "conversation_projector.stop")
			return nil
		},
		stopPresenceFn: func() error {
			calls = append(calls, "presence.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			calls = append(calls, "channelmeta.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
		closeRaftDBFn: func() error {
			calls = append(calls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			calls = append(calls, "metadb.close")
			return nil
		},
	}

	require.ErrorIs(t, app.Start(), startErr)
	require.NoError(t, app.Stop())
	require.Equal(t, []string{
		"cluster.start",
		"channelmeta.start",
		"presence.start",
		"conversation_projector.start",
		"gateway.start",
		"api.start",
		"gateway.stop",
		"conversation_projector.stop",
		"presence.stop",
		"channelmeta.stop",
		"cluster.stop",
		"raft.close",
		"metadb.close",
	}, calls)
}

func TestAppLifecycleStopUsesBoundedContextForContextAwareComponents(t *testing.T) {
	var apiCtx context.Context
	var managerCtx context.Context

	app := &App{
		cluster:        &appLifecycleTestCluster{},
		gateway:        &gateway.Gateway{},
		startClusterFn: func() error { return nil },
		startGatewayFn: func() error { return nil },
		startAPIFn:     func() error { return nil },
		startManagerFn: func() error { return nil },
		stopClusterFn:  func() {},
		stopAPIWithContextFn: func(ctx context.Context) error {
			apiCtx = ctx
			return nil
		},
		stopManagerWithContextFn: func(ctx context.Context) error {
			managerCtx = ctx
			return nil
		},
		closeRaftDBFn: func() error { return nil },
		closeWKDBFn:   func() error { return nil },
	}

	require.NoError(t, app.Start())
	require.NoError(t, app.Stop())
	require.NotNil(t, apiCtx)
	require.NotNil(t, managerCtx)
	require.Equal(t, managerCtx, apiCtx)
	deadline, ok := apiCtx.Deadline()
	require.True(t, ok, "stop context should be bounded")
	require.Positive(t, time.Until(deadline))
	require.LessOrEqual(t, time.Until(deadline), apiStopTimeout)
}

func TestStartStopIncludesConversationProjector(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startConversationProjectorFn: func() error {
			calls = append(calls, "conversation.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopConversationProjectorFn: func() error {
			calls = append(calls, "conversation.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "conversation.start", "gateway.start"}, calls)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{"cluster.start", "conversation.start", "gateway.start", "gateway.stop", "conversation.stop", "cluster.stop"}, calls)
}

func TestStartStopIncludesCMDConversationUpdaterAfterConversation(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startConversationProjectorFn: func() error {
			calls = append(calls, "conversation.start")
			return nil
		},
		startCMDConversationUpdaterFn: func() error {
			calls = append(calls, "cmdsync.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopCMDConversationUpdaterFn: func(context.Context) error {
			calls = append(calls, "cmdsync.stop")
			return nil
		},
		stopConversationProjectorFn: func() error {
			calls = append(calls, "conversation.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "conversation.start", "cmdsync.start", "gateway.start"}, calls)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{"cluster.start", "conversation.start", "cmdsync.start", "gateway.start", "gateway.stop", "cmdsync.stop", "conversation.stop", "cluster.stop"}, calls)
}

func TestStartStopIncludesConversationActiveHintCache(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startConversationActiveHintsFn: func() error {
			calls = append(calls, "active_hints.start")
			return nil
		},
		startConversationProjectorFn: func() error {
			calls = append(calls, "projector.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopConversationProjectorFn: func() error {
			calls = append(calls, "projector.stop")
			return nil
		},
		stopConversationActiveHintsFn: func(context.Context) error {
			calls = append(calls, "active_hints.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "active_hints.start", "projector.start", "gateway.start"}, calls)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{
		"cluster.start",
		"active_hints.start",
		"projector.start",
		"gateway.start",
		"gateway.stop",
		"projector.stop",
		"active_hints.stop",
		"cluster.stop",
	}, calls)
}

func TestStartRollsBackClusterWhenGatewayStartFails(t *testing.T) {
	var calls []string
	startErr := errors.New("gateway start failed")

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return startErr
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	err := app.Start()
	require.ErrorIs(t, err, startErr)
	require.Equal(t, []string{"cluster.start", "gateway.start", "cluster.stop"}, calls)
	require.False(t, app.started.Load())
}

func TestStartRollsBackConversationWhenAPIStartFails(t *testing.T) {
	var calls []string
	var conversationStops int
	startErr := errors.New("api start failed")

	app := &App{
		cluster:         &raftcluster.Cluster{},
		channelMetaSync: &channelMetaSync{},
		gateway:         &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.start")
			return nil
		},
		startPresenceFn: func() error {
			calls = append(calls, "presence.start")
			return nil
		},
		startConversationProjectorFn: func() error {
			calls = append(calls, "conversation.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		startAPIFn: func() error {
			calls = append(calls, "api.start")
			return startErr
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopConversationProjectorFn: func() error {
			conversationStops++
			calls = append(calls, "conversation.stop")
			return nil
		},
		stopPresenceFn: func() error {
			calls = append(calls, "presence.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	err := app.Start()
	require.ErrorIs(t, err, startErr)
	require.Equal(t, []string{
		"cluster.start",
		"meta.start",
		"presence.start",
		"conversation.start",
		"gateway.start",
		"api.start",
		"gateway.stop",
		"conversation.stop",
		"presence.stop",
		"meta.stop",
		"cluster.stop",
	}, calls)
	require.Equal(t, 1, conversationStops)
	require.False(t, app.started.Load())
}

func TestStartRollsBackConversationWhenManagerStartFails(t *testing.T) {
	var calls []string
	var conversationStops int
	startErr := errors.New("manager start failed")

	app := &App{
		cluster:         &raftcluster.Cluster{},
		channelMetaSync: &channelMetaSync{},
		gateway:         &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.start")
			return nil
		},
		startPresenceFn: func() error {
			calls = append(calls, "presence.start")
			return nil
		},
		startConversationProjectorFn: func() error {
			calls = append(calls, "conversation.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		startAPIFn: func() error {
			calls = append(calls, "api.start")
			return nil
		},
		startManagerFn: func() error {
			calls = append(calls, "manager.start")
			return startErr
		},
		stopAPIFn: func() error {
			calls = append(calls, "api.stop")
			return nil
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopConversationProjectorFn: func() error {
			conversationStops++
			calls = append(calls, "conversation.stop")
			return nil
		},
		stopPresenceFn: func() error {
			calls = append(calls, "presence.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	err := app.Start()
	require.ErrorIs(t, err, startErr)
	require.Equal(t, []string{
		"cluster.start",
		"meta.start",
		"presence.start",
		"conversation.start",
		"gateway.start",
		"api.start",
		"manager.start",
		"api.stop",
		"gateway.stop",
		"conversation.stop",
		"presence.stop",
		"meta.stop",
		"cluster.stop",
	}, calls)
	require.Equal(t, 1, conversationStops)
	require.False(t, app.started.Load())
}

func TestStartRollsBackAPIAndClusterWhenManagerStartFails(t *testing.T) {
	var calls []string
	startErr := errors.New("manager start failed")

	app := &App{
		cluster:         &raftcluster.Cluster{},
		channelMetaSync: &channelMetaSync{},
		gateway:         &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
		startAPIFn: func() error {
			calls = append(calls, "api.start")
			return nil
		},
		startManagerFn: func() error {
			calls = append(calls, "manager.start")
			return startErr
		},
		stopAPIFn: func() error {
			calls = append(calls, "api.stop")
			return nil
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
	}

	err := app.Start()
	require.ErrorIs(t, err, startErr)
	require.Equal(t, []string{"cluster.start", "meta.start", "gateway.start", "api.start", "manager.start", "api.stop", "gateway.stop", "meta.stop", "cluster.stop"}, calls)
	require.False(t, app.started.Load())
}

func TestStopIsSafeAfterFailedStartRollback(t *testing.T) {
	var calls []string
	startErr := errors.New("gateway start failed")

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return startErr
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		closeRaftDBFn: func() error {
			calls = append(calls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			calls = append(calls, "metadb.close")
			return nil
		},
	}

	require.ErrorIs(t, app.Start(), startErr)
	require.NoError(t, app.Stop())
	require.Equal(t, []string{
		"cluster.start",
		"gateway.start",
		"cluster.stop",
		"raft.close",
		"metadb.close",
	}, calls)
}

func TestStopStopsGatewayBeforeClosingStorage(t *testing.T) {
	var calls []string

	app := &App{
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
		closeChannelLogDBFn: func() error {
			calls = append(calls, "channellog.close")
			return nil
		},
		closeRaftDBFn: func() error {
			calls = append(calls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			calls = append(calls, "metadb.close")
			return nil
		},
	}
	app.started.Store(true)
	app.clusterOn.Store(true)
	app.gatewayOn.Store(true)
	app.channelMetaOn.Store(true)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{"gateway.stop", "meta.stop", "cluster.stop", "channellog.close", "raft.close", "metadb.close"}, calls)
	require.False(t, app.started.Load())
}

func TestStopSkipsChannelMetaCleanupBecauseClusterShutdownClosesRuntime(t *testing.T) {
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "local-stop", Type: 1})
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		resolver: runtimechannelmeta.NewSync(runtimechannelmeta.SyncOptions{
			Runtime:   cluster,
			LocalNode: 2,
		}),
	}
	_, err := syncer.applyAuthoritativeMeta(metadb.ChannelRuntimeMeta{
		ChannelID:   "local-stop",
		ChannelType: 1,
		Replicas:    []uint64{2},
		ISR:         []uint64{2},
		Leader:      2,
		Status:      uint8(channel.StatusActive),
	})
	require.NoError(t, err)

	app := &App{
		channelMetaSync: syncer,
		stopClusterFn:   func() {},
		closeRaftDBFn:   func() error { return nil },
		closeWKDBFn:     func() error { return nil },
	}
	app.started.Store(true)
	app.channelMetaOn.Store(true)
	app.clusterOn.Store(true)

	require.NoError(t, app.Stop())
	require.Equal(t, []channel.ChannelKey(nil), cluster.runtimeRemoved)
	require.Empty(t, cluster.removed)
	require.Equal(t, key, cluster.runtimeUpserts[0].Key)
}

func TestAppLifecycleStopsPresenceWorkerAfterGateway(t *testing.T) {
	var startCalls []string
	var stopCalls []string

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			startCalls = append(startCalls, "cluster.start")
			return nil
		},
		startGatewayFn: func() error {
			startCalls = append(startCalls, "gateway.start")
			return nil
		},
		stopGatewayFn: func() error {
			stopCalls = append(stopCalls, "gateway.stop")
			return nil
		},
		stopClusterFn: func() {
			stopCalls = append(stopCalls, "cluster.stop")
		},
		closeRaftDBFn: func() error {
			stopCalls = append(stopCalls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			stopCalls = append(stopCalls, "metadb.close")
			return nil
		},
	}
	setAppFuncField(t, app, "startPresenceFn", func() error {
		startCalls = append(startCalls, "presence.start")
		return nil
	})
	app.startCommittedDispatcherFn = func() error {
		startCalls = append(startCalls, "committed_dispatcher.start")
		return nil
	}
	setAppFuncField(t, app, "stopPresenceFn", func() error {
		stopCalls = append(stopCalls, "presence.stop")
		return nil
	})
	app.stopCommittedDispatcherFn = func(context.Context) error {
		stopCalls = append(stopCalls, "committed_dispatcher.stop")
		return nil
	}

	require.NoError(t, app.Start())
	require.Equal(t, []string{"cluster.start", "presence.start", "committed_dispatcher.start", "gateway.start"}, startCalls)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{"gateway.stop", "committed_dispatcher.stop", "presence.stop", "cluster.stop", "raft.close", "metadb.close"}, stopCalls)
}

func TestAppLifecyclePassesBoundedStopContextToActiveHintsReplayAndDispatcher(t *testing.T) {
	var activeHintsHasDeadline bool
	var cmdUpdaterHasDeadline bool
	var replayHasDeadline bool
	var dispatcherHasDeadline bool
	app := &App{
		stopConversationActiveHintsFn: func(ctx context.Context) error {
			_, activeHintsHasDeadline = ctx.Deadline()
			return nil
		},
		stopCMDConversationUpdaterFn: func(ctx context.Context) error {
			_, cmdUpdaterHasDeadline = ctx.Deadline()
			return nil
		},
		stopCommittedReplayFn: func(ctx context.Context) error {
			_, replayHasDeadline = ctx.Deadline()
			return nil
		},
		stopCommittedDispatcherFn: func(ctx context.Context) error {
			_, dispatcherHasDeadline = ctx.Deadline()
			return nil
		},
		closeRaftDBFn: func() error { return nil },
		closeWKDBFn:   func() error { return nil },
	}
	app.conversationHintsOn.Store(true)
	app.cmdConversationUpdaterOn.Store(true)
	app.committedReplayOn.Store(true)
	app.committedDispatcherOn.Store(true)

	require.NoError(t, app.Stop())
	require.True(t, activeHintsHasDeadline)
	require.True(t, cmdUpdaterHasDeadline)
	require.True(t, replayHasDeadline)
	require.True(t, dispatcherHasDeadline)
}

func TestAppLifecycleTreatsActiveHintStopTimeoutAsBestEffort(t *testing.T) {
	var activeHintsHasDeadline bool
	app := &App{
		stopConversationActiveHintsFn: func(ctx context.Context) error {
			_, activeHintsHasDeadline = ctx.Deadline()
			return context.DeadlineExceeded
		},
		closeRaftDBFn: func() error { return nil },
		closeWKDBFn:   func() error { return nil },
	}
	app.conversationHintsOn.Store(true)

	require.NoError(t, app.Stop())
	require.True(t, activeHintsHasDeadline)
}

func TestAppLifecyclePropagatesUnexpectedActiveHintStopError(t *testing.T) {
	stopErr := errors.New("active hint stop failed")
	app := &App{
		stopConversationActiveHintsFn: func(context.Context) error {
			return stopErr
		},
		closeRaftDBFn: func() error { return nil },
		closeWKDBFn:   func() error { return nil },
	}
	app.conversationHintsOn.Store(true)

	require.ErrorIs(t, app.Stop(), stopErr)
}

func TestAppLifecycleStartRollbackPassesBoundedStopContext(t *testing.T) {
	startErr := errors.New("gateway start failed")
	var replayHasDeadline bool
	var dispatcherHasDeadline bool
	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			return nil
		},
		startCommittedDispatcherFn: func() error {
			return nil
		},
		startCommittedReplayFn: func(context.Context) error {
			return nil
		},
		startGatewayFn: func() error {
			return startErr
		},
		stopCommittedReplayFn: func(ctx context.Context) error {
			_, replayHasDeadline = ctx.Deadline()
			return nil
		},
		stopCommittedDispatcherFn: func(ctx context.Context) error {
			_, dispatcherHasDeadline = ctx.Deadline()
			return nil
		},
		stopClusterFn: func() {},
	}

	err := app.Start()

	require.ErrorIs(t, err, startErr)
	require.True(t, replayHasDeadline)
	require.True(t, dispatcherHasDeadline)
	require.False(t, app.started.Load())
}

func TestStopStopsAPIBeforeGatewayAndClusterClose(t *testing.T) {
	var calls []string

	app := &App{
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopAPIFn: func() error {
			calls = append(calls, "api.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
		closeChannelLogDBFn: func() error {
			calls = append(calls, "channellog.close")
			return nil
		},
		closeRaftDBFn: func() error {
			calls = append(calls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			calls = append(calls, "metadb.close")
			return nil
		},
	}
	app.started.Store(true)
	app.clusterOn.Store(true)
	app.apiOn.Store(true)
	app.gatewayOn.Store(true)
	app.channelMetaOn.Store(true)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{"api.stop", "gateway.stop", "meta.stop", "cluster.stop", "channellog.close", "raft.close", "metadb.close"}, calls)
}

func TestStopStopsManagerBeforeAPIGatewayAndClusterClose(t *testing.T) {
	var calls []string

	app := &App{
		stopManagerFn: func() error {
			calls = append(calls, "manager.stop")
			return nil
		},
		stopAPIFn: func() error {
			calls = append(calls, "api.stop")
			return nil
		},
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopChannelMetaSyncFn: func() error {
			calls = append(calls, "meta.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
		closeChannelLogDBFn: func() error {
			calls = append(calls, "channellog.close")
			return nil
		},
		closeRaftDBFn: func() error {
			calls = append(calls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			calls = append(calls, "metadb.close")
			return nil
		},
	}
	app.started.Store(true)
	app.clusterOn.Store(true)
	app.managerOn.Store(true)
	app.apiOn.Store(true)
	app.gatewayOn.Store(true)
	app.channelMetaOn.Store(true)

	require.NoError(t, app.Stop())
	require.Equal(t, []string{"manager.stop", "api.stop", "gateway.stop", "meta.stop", "cluster.stop", "channellog.close", "raft.close", "metadb.close"}, calls)
}

func TestStopIsIdempotent(t *testing.T) {
	var calls []string

	app := &App{
		stopGatewayFn: func() error {
			calls = append(calls, "gateway.stop")
			return nil
		},
		stopClusterFn: func() {
			calls = append(calls, "cluster.stop")
		},
		closeRaftDBFn: func() error {
			calls = append(calls, "raft.close")
			return nil
		},
		closeWKDBFn: func() error {
			calls = append(calls, "metadb.close")
			return nil
		},
	}
	app.started.Store(true)
	app.clusterOn.Store(true)
	app.gatewayOn.Store(true)

	require.NoError(t, app.Stop())
	require.NoError(t, app.Stop())
	require.Equal(t, []string{"gateway.stop", "cluster.stop", "raft.close", "metadb.close"}, calls)
	require.False(t, app.started.Load())
}

func TestStartReturnsAlreadyStartedAfterSuccess(t *testing.T) {
	var calls []string

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			calls = append(calls, "cluster.start")
			return nil
		},
		startGatewayFn: func() error {
			calls = append(calls, "gateway.start")
			return nil
		},
	}

	require.NoError(t, app.Start())
	require.ErrorIs(t, app.Start(), ErrAlreadyStarted)
	require.Equal(t, []string{"cluster.start", "gateway.start"}, calls)
}

func TestStartReturnsStoppedAfterStop(t *testing.T) {
	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		closeRaftDBFn: func() error {
			return nil
		},
		closeWKDBFn: func() error {
			return nil
		},
	}

	require.NoError(t, app.Stop())
	require.ErrorIs(t, app.Start(), ErrStopped)
}

func TestStopWaitsForInFlightStart(t *testing.T) {
	startGatewayEntered := make(chan struct{})
	releaseGatewayStart := make(chan struct{})
	startDone := make(chan error, 1)
	stopDone := make(chan error, 1)
	closeCalls := make(chan string, 2)

	app := &App{
		cluster: &raftcluster.Cluster{},
		gateway: &gateway.Gateway{},
		startClusterFn: func() error {
			return nil
		},
		startGatewayFn: func() error {
			close(startGatewayEntered)
			<-releaseGatewayStart
			return nil
		},
		stopGatewayFn: func() error {
			return nil
		},
		stopClusterFn: func() {},
		closeRaftDBFn: func() error {
			closeCalls <- "raft.close"
			return nil
		},
		closeWKDBFn: func() error {
			closeCalls <- "metadb.close"
			return nil
		},
	}

	go func() {
		startDone <- app.Start()
	}()

	<-startGatewayEntered

	go func() {
		stopDone <- app.Stop()
	}()

	select {
	case call := <-closeCalls:
		t.Fatalf("cleanup ran before start finished: %s", call)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseGatewayStart)

	require.NoError(t, <-startDone)
	require.NoError(t, <-stopDone)
}

func TestStopJoinsCleanupErrors(t *testing.T) {
	errGateway := errors.New("gateway stop")
	errRaft := errors.New("raft close")
	errMetaDB := errors.New("metadb close")

	app := &App{
		stopGatewayFn: func() error {
			return errGateway
		},
		stopClusterFn: func() {},
		closeRaftDBFn: func() error {
			return errRaft
		},
		closeWKDBFn: func() error {
			return errMetaDB
		},
	}
	app.started.Store(true)
	app.clusterOn.Store(true)
	app.gatewayOn.Store(true)

	joinedErr := app.Stop()
	require.ErrorIs(t, joinedErr, errGateway)
	require.ErrorIs(t, joinedErr, errRaft)
	require.ErrorIs(t, joinedErr, errMetaDB)
}

func TestStopSyncsLogger(t *testing.T) {
	logger := &recordingLogger{}
	app := &App{
		logger:        logger,
		closeRaftDBFn: func() error { return nil },
		closeWKDBFn:   func() error { return nil },
	}
	app.started.Store(true)

	require.NoError(t, app.Stop())
	require.Equal(t, 1, logger.syncCalls)
}

func testConfig(t *testing.T) Config {
	t.Helper()

	cfg := validConfig()
	clusterAddr := reserveTestTCPAddrs(t, 1)[1]
	cfg.Node.DataDir = t.TempDir()
	cfg.Storage = StorageConfig{
		DBPath:   filepath.Join(cfg.Node.DataDir, "data"),
		RaftPath: filepath.Join(cfg.Node.DataDir, "raft"),
	}
	cfg.Cluster.ListenAddr = clusterAddr
	cfg.Cluster.Nodes = []NodeConfigRef{{ID: cfg.Node.ID, Addr: clusterAddr}}
	cfg.Gateway.Listeners[0].Address = "127.0.0.1:0"
	return cfg
}

func setChannelMessageRetentionTTLForTest(t *testing.T, cfg *Config, ttl time.Duration) {
	t.Helper()
	value := reflect.ValueOf(cfg).Elem().FieldByName("ChannelMessageRetention")
	if !value.IsValid() {
		t.Skip("ChannelMessageRetention config is owned by the config worker and is not available yet")
	}
	field := value.FieldByName("TTL")
	require.True(t, field.IsValid() && field.CanSet(), "ChannelMessageRetention.TTL must be settable")
	field.SetInt(int64(ttl))
}

func validManagerConfigForTest() ManagerConfig {
	return ManagerConfig{
		ListenAddr: "127.0.0.1:5301",
		AuthOn:     true,
		JWTSecret:  "test-secret",
		JWTIssuer:  "wukongim-manager",
		JWTExpire:  time.Hour,
		Users: []ManagerUserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []ManagerPermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}},
	}
}

func openWKDBForTest(path string) (interface{ Close() error }, error) {
	return metadb.Open(path)
}

func openRaftDBForTest(path string) (interface{ Close() error }, error) {
	return raftstorage.Open(path, raftstorage.Options{})
}

type recordingLogger struct {
	syncCalls int
}

func (r *recordingLogger) Debug(string, ...wklog.Field) {}
func (r *recordingLogger) Info(string, ...wklog.Field)  {}
func (r *recordingLogger) Warn(string, ...wklog.Field)  {}
func (r *recordingLogger) Error(string, ...wklog.Field) {}
func (r *recordingLogger) Fatal(string, ...wklog.Field) {}
func (r *recordingLogger) Named(string) wklog.Logger    { return r }
func (r *recordingLogger) With(...wklog.Field) wklog.Logger {
	return r
}
func (r *recordingLogger) Sync() error {
	r.syncCalls++
	return nil
}

type appLifecycleTestCluster struct {
	raftcluster.API
	waitFn func(context.Context) error
}

func (c *appLifecycleTestCluster) WaitForManagedSlotsReady(ctx context.Context) error {
	if c.waitFn != nil {
		return c.waitFn(ctx)
	}
	return nil
}

func requireAppFieldNonNil(t *testing.T, app *App, name string) {
	t.Helper()

	field := reflect.ValueOf(app).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("App is missing field %s", name)
	}
	switch field.Kind() {
	case reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.Func:
		require.Falsef(t, field.IsNil(), "App field %s should not be nil", name)
	default:
		t.Fatalf("App field %s is %s; expected a nil-able field", name, field.Kind())
	}
}

func requireMessageAppFieldNonNil(t *testing.T, app *App, name string) {
	t.Helper()

	require.NotNil(t, app.messageApp)
	field := reflect.ValueOf(app.messageApp).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("message.App is missing field %s", name)
	}
	switch field.Kind() {
	case reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.Func:
		require.Falsef(t, field.IsNil(), "message.App field %s should not be nil", name)
	default:
		t.Fatalf("message.App field %s is %s; expected a nil-able field", name, field.Kind())
	}
}

func setAppFuncField(t *testing.T, app *App, name string, fn any) {
	t.Helper()

	field := reflect.ValueOf(app).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("App is missing field %s", name)
	}
	ptr := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	ptr.Set(reflect.ValueOf(fn))
}

func appISRMaxFetchInflightPeerLimit(t *testing.T, app *App) int {
	t.Helper()

	rt := reflect.ValueOf(app.isrRuntime)
	if rt.Kind() != reflect.Pointer || rt.IsNil() {
		t.Fatalf("isrRuntime is %s, want non-nil pointer", rt.Kind())
	}
	cfgField := rt.Elem().FieldByName("cfg")
	if !cfgField.IsValid() {
		t.Fatal("isr runtime missing cfg field")
	}
	cfg := reflect.NewAt(cfgField.Type(), unsafe.Pointer(cfgField.UnsafeAddr())).Elem()
	limits := cfg.FieldByName("Limits")
	if !limits.IsValid() {
		t.Fatal("isr runtime config missing Limits field")
	}
	maxInflight := limits.FieldByName("MaxFetchInflightPeer")
	if !maxInflight.IsValid() {
		t.Fatal("isr runtime limits missing MaxFetchInflightPeer field")
	}
	return int(maxInflight.Int())
}

func setClusterConfigIntField(t *testing.T, cfg *ClusterConfig, name string, value int) {
	t.Helper()

	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("ClusterConfig is missing field %s", name)
	}
	ptr := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	ptr.SetInt(int64(value))
}

func setClusterConfigDurationField(t *testing.T, cfg *ClusterConfig, name string, value time.Duration) {
	t.Helper()

	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("ClusterConfig is missing field %s", name)
	}
	ptr := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	ptr.SetInt(int64(value))
}

func appISRFollowerReplicationRetryInterval(t *testing.T, app *App) time.Duration {
	t.Helper()

	rt := reflect.ValueOf(app.isrRuntime)
	if rt.Kind() != reflect.Pointer || rt.IsNil() {
		t.Fatalf("isrRuntime is %s, want non-nil pointer", rt.Kind())
	}
	cfgField := rt.Elem().FieldByName("cfg")
	if !cfgField.IsValid() {
		t.Fatal("isr runtime missing cfg field")
	}
	cfg := reflect.NewAt(cfgField.Type(), unsafe.Pointer(cfgField.UnsafeAddr())).Elem()
	interval := cfg.FieldByName("FollowerReplicationRetryInterval")
	if !interval.IsValid() {
		t.Fatal("isr runtime config missing FollowerReplicationRetryInterval field")
	}
	return time.Duration(interval.Int())
}

func appReplicaAppendGroupCommitConfig(t *testing.T, app *App) (time.Duration, int, int) {
	t.Helper()

	rt := reflect.ValueOf(app.isrRuntime)
	if rt.Kind() != reflect.Pointer || rt.IsNil() {
		t.Fatalf("isrRuntime is %s, want non-nil pointer", rt.Kind())
	}
	cfgField := rt.Elem().FieldByName("replicaFactory")
	if !cfgField.IsValid() {
		t.Fatal("isr runtime missing replicaFactory field")
	}
	factory, ok := reflect.NewAt(cfgField.Type(), unsafe.Pointer(cfgField.UnsafeAddr())).Elem().Interface().(channelruntime.ReplicaFactory)
	if !ok {
		t.Fatalf("isr runtime replicaFactory has unexpected type %T", reflect.NewAt(cfgField.Type(), unsafe.Pointer(cfgField.UnsafeAddr())).Elem().Interface())
	}

	replica, err := factory.New(channelruntime.ChannelConfig{
		ChannelKey: channel.ChannelKey("send-path-replica"),
		Meta: channel.Meta{
			ID: channel.ChannelID{
				ID:   "send-path-replica",
				Type: 1,
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, replica.Close())
	})

	value := reflect.ValueOf(replica)
	if value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("replica is %s, want non-nil pointer", value.Kind())
	}
	commit := value.Elem().FieldByName("appendGroupCommit")
	if !commit.IsValid() {
		t.Fatal("replica missing appendGroupCommit field")
	}
	commit = reflect.NewAt(commit.Type(), unsafe.Pointer(commit.UnsafeAddr())).Elem()

	maxWait := commit.FieldByName("maxWait")
	if !maxWait.IsValid() {
		t.Fatal("replica appendGroupCommit missing maxWait field")
	}
	maxRecords := commit.FieldByName("maxRecords")
	if !maxRecords.IsValid() {
		t.Fatal("replica appendGroupCommit missing maxRecords field")
	}
	maxBytes := commit.FieldByName("maxBytes")
	if !maxBytes.IsValid() {
		t.Fatal("replica appendGroupCommit missing maxBytes field")
	}

	return time.Duration(maxWait.Int()), int(maxRecords.Int()), int(maxBytes.Int())
}

func appDataPlanePoolSize(t *testing.T, app *App) int {
	t.Helper()

	require.NotNil(t, app.dataPlanePool)
	pool := reflect.ValueOf(app.dataPlanePool).Elem()
	size := pool.FieldByName("size")
	if !size.IsValid() {
		t.Fatal("dataPlanePool is missing size field")
	}
	return int(size.Int())
}

func appDataPlaneAdapterMaxPendingFetch(t *testing.T, app *App) int {
	t.Helper()

	require.NotNil(t, app.isrTransport)
	transport := reflect.ValueOf(app.isrTransport).Elem()
	maxPending := transport.FieldByName("maxPending")
	if !maxPending.IsValid() {
		t.Fatal("channel transport is missing maxPending field")
	}
	return int(maxPending.Int())
}

func appChannelStoreCommitCoordinatorConfig(t *testing.T, app *App) channelstore.CommitCoordinatorConfig {
	t.Helper()
	return app.channelLogDB.CommitCoordinatorConfig()
}
