package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	"github.com/spf13/viper"
)

var defaultConfigPaths = []string{
	"./wukongim.conf",
	"./conf/wukongim.conf",
	"/etc/wukongim/wukongim.conf",
}

func loadConfig(path string) (app.Config, error) {
	cfgv, foundFile, attemptedPaths, err := readConfig(path)
	if err != nil {
		return app.Config{}, err
	}

	cfg, err := buildAppConfig(cfgv)
	if err != nil {
		if !foundFile {
			return app.Config{}, missingDefaultConfigError(attemptedPaths, err)
		}
		return app.Config{}, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.ApplyDefaultsAndValidate(); err != nil {
		if !foundFile {
			return app.Config{}, missingDefaultConfigError(attemptedPaths, err)
		}
		return app.Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func readConfig(path string) (*viper.Viper, bool, []string, error) {
	v := viper.New()
	v.SetConfigType("env")
	v.AutomaticEnv()

	path = strings.TrimSpace(path)
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, false, nil, fmt.Errorf("load config: read %s: %w", path, err)
		}
		return v, true, nil, nil
	}

	attemptedPaths := append([]string(nil), defaultConfigPaths...)
	for _, candidate := range attemptedPaths {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, false, attemptedPaths, fmt.Errorf("load config: stat %s: %w", candidate, err)
		}
		v.SetConfigFile(candidate)
		if err := v.ReadInConfig(); err != nil {
			return nil, false, attemptedPaths, fmt.Errorf("load config: read %s: %w", candidate, err)
		}
		return v, true, attemptedPaths, nil
	}

	return v, false, attemptedPaths, nil
}

func buildAppConfig(v *viper.Viper) (app.Config, error) {
	if raw := strings.TrimSpace(stringValue(v, "WK_CLUSTER_GROUP_COUNT")); raw != "" {
		return app.Config{}, fmt.Errorf("%w: WK_CLUSTER_GROUP_COUNT is no longer supported; use WK_CLUSTER_INITIAL_SLOT_COUNT", app.ErrInvalidConfig)
	}
	if raw := strings.TrimSpace(stringValue(v, "WK_CLUSTER_GROUP_REPLICA_N")); raw != "" {
		return app.Config{}, fmt.Errorf("%w: WK_CLUSTER_GROUP_REPLICA_N is no longer supported; use WK_CLUSTER_SLOT_REPLICA_N", app.ErrInvalidConfig)
	}
	nodeID, err := parseUint64(v, "WK_NODE_ID")
	if err != nil {
		return app.Config{}, err
	}
	slotCount, err := parseUint32(v, "WK_CLUSTER_SLOT_COUNT")
	if err != nil {
		return app.Config{}, err
	}
	initialSlotCount, err := parseUint32(v, "WK_CLUSTER_INITIAL_SLOT_COUNT")
	if err != nil {
		return app.Config{}, err
	}
	longPollLaneCount, err := parseInt(v, "WK_CLUSTER_LONG_POLL_LANE_COUNT")
	if err != nil {
		return app.Config{}, err
	}
	longPollMaxWait, err := parseDuration(v, "WK_CLUSTER_LONG_POLL_MAX_WAIT")
	if err != nil {
		return app.Config{}, err
	}
	longPollMaxBytes, err := parseInt(v, "WK_CLUSTER_LONG_POLL_MAX_BYTES")
	if err != nil {
		return app.Config{}, err
	}
	longPollMaxChannels, err := parseInt(v, "WK_CLUSTER_LONG_POLL_MAX_CHANNELS")
	if err != nil {
		return app.Config{}, err
	}
	longPollDataNotifyDelay, err := parseDuration(v, "WK_CLUSTER_LONG_POLL_DATA_NOTIFY_DELAY")
	if err != nil {
		return app.Config{}, err
	}
	maxChannels, err := parseInt(v, "WK_CLUSTER_MAX_CHANNELS")
	if err != nil {
		return app.Config{}, err
	}
	channelIdleTimeout, err := parseDuration(v, "WK_CLUSTER_CHANNEL_IDLE_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	channelIdleScanInterval, err := parseDuration(v, "WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	channelExecutionWorkers, err := parseInt(v, "WK_CLUSTER_CHANNEL_EXECUTION_WORKERS")
	if err != nil {
		return app.Config{}, err
	}
	channelExecutionQueueSize, err := parseInt(v, "WK_CLUSTER_CHANNEL_EXECUTION_QUEUE_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	channelBootstrapDefaultMinISR, err := parseInt(v, "WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR")
	if err != nil {
		return app.Config{}, err
	}
	channelBootstrapDefaultMinISRSet := stringValue(v, "WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR") != ""
	channelPlaneReactorCount, err := parseInt(v, "WK_CHANNEL_PLANE_REACTOR_COUNT")
	if err != nil {
		return app.Config{}, err
	}
	channelPlanePeerLaneCount, err := parseInt(v, "WK_CHANNEL_PLANE_PEER_LANE_COUNT")
	if err != nil {
		return app.Config{}, err
	}
	channelPlanePeerBatchMaxWait, err := parseDuration(v, "WK_CHANNEL_PLANE_PEER_BATCH_MAX_WAIT")
	if err != nil {
		return app.Config{}, err
	}
	hashSlotCount, err := parseUint16(v, "WK_CLUSTER_HASH_SLOT_COUNT")
	if err != nil {
		return app.Config{}, err
	}
	hashSlotMigrationEnabled, err := parseBool(v, "WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED")
	if err != nil {
		return app.Config{}, err
	}
	if initialSlotCount == 0 {
		initialSlotCount = slotCount
	}
	forwardTimeout, err := parseDuration(v, "WK_CLUSTER_FORWARD_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	poolSize, err := parseInt(v, "WK_CLUSTER_POOL_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	tickInterval, err := parseDuration(v, "WK_CLUSTER_TICK_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	raftWorkers, err := parseInt(v, "WK_CLUSTER_RAFT_WORKERS")
	if err != nil {
		return app.Config{}, err
	}
	electionTick, err := parseInt(v, "WK_CLUSTER_ELECTION_TICK")
	if err != nil {
		return app.Config{}, err
	}
	heartbeatTick, err := parseInt(v, "WK_CLUSTER_HEARTBEAT_TICK")
	if err != nil {
		return app.Config{}, err
	}
	dialTimeout, err := parseDuration(v, "WK_CLUSTER_DIAL_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	dataPlaneRPCTimeout, err := parseDuration(v, "WK_CLUSTER_DATA_PLANE_RPC_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	followerReplicationRetryInterval, err := parseDuration(v, "WK_CLUSTER_FOLLOWER_REPLICATION_RETRY_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	appendGroupCommitMaxWait, err := parseDuration(v, "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_WAIT")
	if err != nil {
		return app.Config{}, err
	}
	appendGroupCommitMaxRecords, err := parseInt(v, "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_RECORDS")
	if err != nil {
		return app.Config{}, err
	}
	appendGroupCommitMaxBytes, err := parseInt(v, "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_BYTES")
	if err != nil {
		return app.Config{}, err
	}
	commitCoordinatorFlushWindow, err := parseDuration(v, "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW")
	if err != nil {
		return app.Config{}, err
	}
	commitCoordinatorMaxRequests, err := parseInt(v, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS")
	if err != nil {
		return app.Config{}, err
	}
	commitCoordinatorMaxRecords, err := parseInt(v, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS")
	if err != nil {
		return app.Config{}, err
	}
	commitCoordinatorMaxBytes, err := parseInt(v, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES")
	if err != nil {
		return app.Config{}, err
	}
	dataPlanePoolSize, err := parseInt(v, "WK_CLUSTER_DATA_PLANE_POOL_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	dataPlaneMaxFetchInflight, err := parseInt(v, "WK_CLUSTER_DATA_PLANE_MAX_FETCH_INFLIGHT")
	if err != nil {
		return app.Config{}, err
	}
	dataPlaneMaxPendingFetch, err := parseInt(v, "WK_CLUSTER_DATA_PLANE_MAX_PENDING_FETCH")
	if err != nil {
		return app.Config{}, err
	}
	controllerObservationInterval, err := parseDuration(v, "WK_CLUSTER_CONTROLLER_OBSERVATION_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	controllerRequestTimeout, err := parseDuration(v, "WK_CLUSTER_CONTROLLER_REQUEST_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	controllerLeaderWaitTimeout, err := parseDuration(v, "WK_CLUSTER_CONTROLLER_LEADER_WAIT_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	managedSlotsReadyTimeout, err := parseDuration(v, "WK_CLUSTER_MANAGED_SLOTS_READY_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	controllerLogCompactionEnabled, err := parseBool(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED")
	if err != nil {
		return app.Config{}, err
	}
	controllerLogCompactionTriggerEntries, err := parseUint64(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES")
	if err != nil {
		return app.Config{}, err
	}
	controllerLogCompactionCheckInterval, err := parseDuration(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	slotLogCompactionEnabled, err := parseBool(v, "WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED")
	if err != nil {
		return app.Config{}, err
	}
	slotLogCompactionTriggerEntries, err := parseUint64(v, "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES")
	if err != nil {
		return app.Config{}, err
	}
	slotLogCompactionCheckInterval, err := parseDuration(v, "WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	raftSnapshotChunkSize, err := parseRaftSnapshotChunkSize(v, "WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	raftSnapshotGCGrace, err := parseDuration(v, "WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE")
	if err != nil {
		return app.Config{}, err
	}
	observationHeartbeatInterval, err := parseDuration(v, "WK_CLUSTER_OBSERVATION_HEARTBEAT_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	observationRuntimeScanInterval, err := parseDuration(v, "WK_CLUSTER_OBSERVATION_RUNTIME_SCAN_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	observationRuntimeFlushDebounce, err := parseDuration(v, "WK_CLUSTER_OBSERVATION_RUNTIME_FLUSH_DEBOUNCE")
	if err != nil {
		return app.Config{}, err
	}
	observationRuntimeFullSyncInterval, err := parseDuration(v, "WK_CLUSTER_OBSERVATION_RUNTIME_FULL_SYNC_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	forwardRetryBudget, err := parseDuration(v, "WK_CLUSTER_FORWARD_RETRY_BUDGET")
	if err != nil {
		return app.Config{}, err
	}
	managedSlotLeaderWaitTimeout, err := parseDuration(v, "WK_CLUSTER_MANAGED_SLOT_LEADER_WAIT_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	managedSlotCatchUpTimeout, err := parseDuration(v, "WK_CLUSTER_MANAGED_SLOT_CATCH_UP_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	managedSlotLeaderMoveTimeout, err := parseDuration(v, "WK_CLUSTER_MANAGED_SLOT_LEADER_MOVE_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	configChangeRetryBudget, err := parseDuration(v, "WK_CLUSTER_CONFIG_CHANGE_RETRY_BUDGET")
	if err != nil {
		return app.Config{}, err
	}
	leaderTransferRetryBudget, err := parseDuration(v, "WK_CLUSTER_LEADER_TRANSFER_RETRY_BUDGET")
	if err != nil {
		return app.Config{}, err
	}
	controllerReplicaN, err := parseInt(v, "WK_CLUSTER_CONTROLLER_REPLICA_N")
	if err != nil {
		return app.Config{}, err
	}
	slotReplicaN, err := parseInt(v, "WK_CLUSTER_SLOT_REPLICA_N")
	if err != nil {
		return app.Config{}, err
	}
	tokenAuthOn, err := parseBool(v, "WK_GATEWAY_TOKEN_AUTH_ON")
	if err != nil {
		return app.Config{}, err
	}
	testMode, err := parseBool(v, "WK_TEST_MODE")
	if err != nil {
		return app.Config{}, err
	}
	benchAPIEnable, err := parseBool(v, "WK_BENCH_API_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	benchAPIMaxBatchSize, err := parseInt(v, "WK_BENCH_API_MAX_BATCH_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	benchAPIMaxPayloadBytes, err := parseInt64(v, "WK_BENCH_API_MAX_PAYLOAD_BYTES")
	if err != nil {
		return app.Config{}, err
	}
	pluginEnable, err := parseBool(v, "WK_PLUGIN_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	pluginTimeout, err := parseDuration(v, "WK_PLUGIN_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	pluginHotReload, err := parseBool(v, "WK_PLUGIN_HOT_RELOAD")
	if err != nil {
		return app.Config{}, err
	}
	pluginFailOpen, err := parseBool(v, "WK_PLUGIN_FAIL_OPEN")
	if err != nil {
		return app.Config{}, err
	}

	nodes, err := parseJSONValue[[]app.NodeConfigRef](v, "WK_CLUSTER_NODES")
	if err != nil {
		return app.Config{}, err
	}
	seeds, err := parseJSONStringList(v, "WK_CLUSTER_SEEDS")
	if err != nil {
		return app.Config{}, err
	}
	if raw := strings.TrimSpace(stringValue(v, "WK_CLUSTER_GROUPS")); raw != "" {
		return app.Config{}, fmt.Errorf("%w: WK_CLUSTER_GROUPS is no longer supported; remove static slot peers and keep WK_CLUSTER_INITIAL_SLOT_COUNT only", app.ErrInvalidConfig)
	}
	listeners, err := parseListeners(v)
	if err != nil {
		return app.Config{}, err
	}
	closeOnHandlerError, err := parseOptionalBool(v, "WK_GATEWAY_DEFAULT_SESSION_CLOSE_ON_HANDLER_ERROR")
	if err != nil {
		return app.Config{}, err
	}
	maxInboundBytes, err := parseInt(v, "WK_GATEWAY_DEFAULT_SESSION_MAX_INBOUND_BYTES")
	if err != nil {
		return app.Config{}, err
	}
	maxOutboundBytes, err := parseInt(v, "WK_GATEWAY_DEFAULT_SESSION_MAX_OUTBOUND_BYTES")
	if err != nil {
		return app.Config{}, err
	}
	idleTimeout, err := parseDuration(v, "WK_GATEWAY_DEFAULT_SESSION_IDLE_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	asyncSendDispatchWorkers, err := parseInt(v, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS")
	if err != nil {
		return app.Config{}, err
	}
	asyncSendBatchMaxWait, err := parseDuration(v, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT")
	if err != nil {
		return app.Config{}, err
	}
	asyncSendBatchMaxRecords, err := parseInt(v, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS")
	if err != nil {
		return app.Config{}, err
	}
	asyncSendBatchMaxBytes, err := parseInt(v, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES")
	if err != nil {
		return app.Config{}, err
	}
	gatewayGnetMulticore, err := parseBool(v, "WK_GATEWAY_GNET_MULTICORE")
	if err != nil {
		return app.Config{}, err
	}
	gatewayGnetNumEventLoop, err := parseInt(v, "WK_GATEWAY_GNET_NUM_EVENT_LOOP")
	if err != nil {
		return app.Config{}, err
	}
	gatewayGnetReusePort, err := parseBool(v, "WK_GATEWAY_GNET_REUSE_PORT")
	if err != nil {
		return app.Config{}, err
	}
	gatewayGnetReadBufferCap, err := parseInt(v, "WK_GATEWAY_GNET_READ_BUFFER_CAP")
	if err != nil {
		return app.Config{}, err
	}
	gatewayGnetWriteBufferCap, err := parseInt(v, "WK_GATEWAY_GNET_WRITE_BUFFER_CAP")
	if err != nil {
		return app.Config{}, err
	}
	gatewaySendTimeout, err := parseDuration(v, "WK_GATEWAY_SEND_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	logMaxSize, err := parseInt(v, "WK_LOG_MAX_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	logMaxAge, err := parseInt(v, "WK_LOG_MAX_AGE")
	if err != nil {
		return app.Config{}, err
	}
	logMaxBackups, err := parseInt(v, "WK_LOG_MAX_BACKUPS")
	if err != nil {
		return app.Config{}, err
	}
	logCompress, err := parseBool(v, "WK_LOG_COMPRESS")
	if err != nil {
		return app.Config{}, err
	}
	logConsole, err := parseBool(v, "WK_LOG_CONSOLE")
	if err != nil {
		return app.Config{}, err
	}
	metricsEnable, err := parseBool(v, "WK_METRICS_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	networkObservabilityEnable, err := parseBool(v, "WK_NETWORK_OBSERVABILITY_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	diagnosticsEnable, err := parseBool(v, "WK_DIAGNOSTICS_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	diagnosticsBufferSize, err := parseInt(v, "WK_DIAGNOSTICS_BUFFER_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	diagnosticsSampleRate, err := parseFloat(v, "WK_DIAGNOSTICS_SAMPLE_RATE")
	if err != nil {
		return app.Config{}, err
	}
	diagnosticsSlowThresholdMS, err := parseInt(v, "WK_DIAGNOSTICS_SLOW_THRESHOLD_MS")
	if err != nil {
		return app.Config{}, err
	}
	diagnosticsErrorSampleRate, err := parseFloat(v, "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE")
	if err != nil {
		return app.Config{}, err
	}
	diagnosticsDebugAPIEnable, err := parseBool(v, "WK_DIAGNOSTICS_DEBUG_API_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	diagnosticsDebugMatches, err := parseJSONValue[[]app.DiagnosticsDebugMatchConfig](v, "WK_DIAGNOSTICS_DEBUG_MATCHES")
	if err != nil {
		return app.Config{}, err
	}
	healthDetailEnable, err := parseBool(v, "WK_HEALTH_DETAIL_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	healthDebugEnable, err := parseBool(v, "WK_HEALTH_DEBUG_ENABLE")
	if err != nil {
		return app.Config{}, err
	}
	managerAuthOn, err := parseBool(v, "WK_MANAGER_AUTH_ON")
	if err != nil {
		return app.Config{}, err
	}
	managerJWTExpire, err := parseDuration(v, "WK_MANAGER_JWT_EXPIRE")
	if err != nil {
		return app.Config{}, err
	}
	managerUsers, err := parseJSONValue[[]app.ManagerUserConfig](v, "WK_MANAGER_USERS")
	if err != nil {
		return app.Config{}, err
	}
	messagePersonWhitelistEnabled, err := parseBool(v, "WK_MESSAGE_PERSON_WHITELIST_ENABLED")
	if err != nil {
		return app.Config{}, err
	}
	messageSystemDeviceID := stringValue(v, "WK_MESSAGE_SYSTEM_DEVICE_ID")
	messagePermissionCacheTTL, err := parseDuration(v, "WK_MESSAGE_PERMISSION_CACHE_TTL")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitEnabled, err := parseBool(v, "WK_MESSAGE_USER_RATE_LIMIT_ENABLED")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitRate, err := parseFloat(v, "WK_MESSAGE_USER_RATE_LIMIT_RATE")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitBurst, err := parseInt(v, "WK_MESSAGE_USER_RATE_LIMIT_BURST")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitBucketShards, err := parseInt(v, "WK_MESSAGE_USER_RATE_LIMIT_BUCKET_SHARDS")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitIdleTTL, err := parseDuration(v, "WK_MESSAGE_USER_RATE_LIMIT_IDLE_TTL")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitMaxBuckets, err := parseInt(v, "WK_MESSAGE_USER_RATE_LIMIT_MAX_BUCKETS")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitSystemUIDBypass, err := parseBool(v, "WK_MESSAGE_USER_RATE_LIMIT_SYSTEM_UID_BYPASS")
	if err != nil {
		return app.Config{}, err
	}
	messageUserRateLimitPluginBypass, err := parseBool(v, "WK_MESSAGE_USER_RATE_LIMIT_PLUGIN_BYPASS")
	if err != nil {
		return app.Config{}, err
	}
	deliveryPresenceCacheTTL, err := parseDuration(v, "WK_DELIVERY_PRESENCE_CACHE_TTL")
	if err != nil {
		return app.Config{}, err
	}
	deliveryAckBatchMaxWait, err := parseDuration(v, "WK_DELIVERY_ACK_BATCH_MAX_WAIT")
	if err != nil {
		return app.Config{}, err
	}
	deliveryAckBatchMaxSize, err := parseInt(v, "WK_DELIVERY_ACK_BATCH_MAX_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationScanInterval, err := parseDuration(v, "WK_CHANNEL_MIGRATION_SCAN_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationScanLimit, err := parseInt(v, "WK_CHANNEL_MIGRATION_SCAN_LIMIT")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationOwnerLeaseTTL, err := parseDuration(v, "WK_CHANNEL_MIGRATION_OWNER_LEASE_TTL")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationRetryBackoff, err := parseDuration(v, "WK_CHANNEL_MIGRATION_RETRY_BACKOFF")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationFenceTTL, err := parseDuration(v, "WK_CHANNEL_MIGRATION_FENCE_TTL")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationLeaderLeaseTTL, err := parseDuration(v, "WK_CHANNEL_MIGRATION_LEADER_LEASE_TTL")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationCatchUpStableWindow, err := parseDuration(v, "WK_CHANNEL_MIGRATION_CATCH_UP_STABLE_WINDOW")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationCatchUpLagThreshold, err := parseInt(v, "WK_CHANNEL_MIGRATION_CATCH_UP_LAG_THRESHOLD")
	if err != nil {
		return app.Config{}, err
	}
	if channelMigrationCatchUpLagThreshold < 0 {
		return app.Config{}, fmt.Errorf("%w: channel migration catch-up lag threshold must be >= 0", app.ErrInvalidConfig)
	}
	channelMigrationMaxConcurrent, err := parseInt(v, "WK_CHANNEL_MIGRATION_MAX_CONCURRENT")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationMaxConcurrentPerSource, err := parseInt(v, "WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_SOURCE")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationMaxConcurrentPerTarget, err := parseInt(v, "WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_TARGET")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationCompletedRetentionTTL, err := parseDuration(v, "WK_CHANNEL_MIGRATION_COMPLETED_RETENTION_TTL")
	if err != nil {
		return app.Config{}, err
	}
	channelMigrationGCLimit, err := parseInt(v, "WK_CHANNEL_MIGRATION_GC_LIMIT")
	if err != nil {
		return app.Config{}, err
	}
	channelMessageRetentionTTL, err := parseDuration(v, "WK_CHANNEL_MESSAGE_RETENTION_TTL")
	if err != nil {
		return app.Config{}, err
	}
	channelMessageRetentionScanInterval, err := parseDuration(v, "WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	channelMessageRetentionChannelBatchSize, err := parseInt(v, "WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	channelMessageRetentionMaxTrimMessages, err := parseInt(v, "WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES")
	if err != nil {
		return app.Config{}, err
	}
	activeHintFlushInterval, err := parseDuration(v, "WK_CONVERSATION_ACTIVE_HINT_FLUSH_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	activeHintTTL, err := parseDuration(v, "WK_CONVERSATION_ACTIVE_HINT_TTL")
	if err != nil {
		return app.Config{}, err
	}
	activeHintBarrierTTL, err := parseDuration(v, "WK_CONVERSATION_ACTIVE_HINT_BARRIER_TTL")
	if err != nil {
		return app.Config{}, err
	}
	activeHintMaxHints, err := parseInt(v, "WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS")
	if err != nil {
		return app.Config{}, err
	}
	activeHintMaxHintsPerUID, err := parseInt(v, "WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS_PER_UID")
	if err != nil {
		return app.Config{}, err
	}
	activeHintFlushBatchSize, err := parseInt(v, "WK_CONVERSATION_ACTIVE_HINT_FLUSH_BATCH_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	groupActiveFanoutInterval, err := parseDuration(v, "WK_CONVERSATION_GROUP_ACTIVE_FANOUT_INTERVAL")
	if err != nil {
		return app.Config{}, err
	}
	groupActiveFanoutMaxSubscribers, err := parseInt(v, "WK_CONVERSATION_GROUP_ACTIVE_FANOUT_MAX_SUBSCRIBERS")
	if err != nil {
		return app.Config{}, err
	}

	cfg := app.Config{
		TestMode: testMode,
		Node: app.NodeConfig{
			ID:      nodeID,
			Name:    stringValue(v, "WK_NODE_NAME"),
			DataDir: stringValue(v, "WK_NODE_DATA_DIR"),
		},
		Storage: app.StorageConfig{
			DBPath:                     stringValue(v, "WK_STORAGE_DB_PATH"),
			RaftPath:                   stringValue(v, "WK_STORAGE_RAFT_PATH"),
			ChannelLogPath:             stringValue(v, "WK_STORAGE_CHANNEL_LOG_PATH"),
			ControllerMetaPath:         stringValue(v, "WK_STORAGE_CONTROLLER_META_PATH"),
			ControllerRaftPath:         stringValue(v, "WK_STORAGE_CONTROLLER_RAFT_PATH"),
			RaftSnapshotPath:           stringValue(v, "WK_STORAGE_RAFT_SNAPSHOT_PATH"),
			ControllerRaftSnapshotPath: stringValue(v, "WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH"),
			RaftSnapshotChunkSize:      raftSnapshotChunkSize,
			RaftSnapshotGCGrace:        raftSnapshotGCGrace,
		},
		Cluster: app.ClusterConfig{
			ListenAddr:                       stringValue(v, "WK_CLUSTER_LISTEN_ADDR"),
			SlotCount:                        slotCount,
			HashSlotCount:                    hashSlotCount,
			EnableHashSlotMigration:          hashSlotMigrationEnabled,
			InitialSlotCount:                 initialSlotCount,
			ChannelBootstrapDefaultMinISR:    channelBootstrapDefaultMinISR,
			MaxChannels:                      maxChannels,
			ChannelIdleTimeout:               channelIdleTimeout,
			ChannelIdleScanInterval:          channelIdleScanInterval,
			ChannelExecutionMode:             stringValue(v, "WK_CLUSTER_CHANNEL_EXECUTION_MODE"),
			ChannelExecutionWorkers:          channelExecutionWorkers,
			ChannelExecutionQueueSize:        channelExecutionQueueSize,
			LongPollLaneCount:                longPollLaneCount,
			LongPollMaxWait:                  longPollMaxWait,
			LongPollMaxBytes:                 longPollMaxBytes,
			LongPollMaxChannels:              longPollMaxChannels,
			LongPollDataNotifyDelay:          longPollDataNotifyDelay,
			Nodes:                            nodes,
			ControllerReplicaN:               controllerReplicaN,
			SlotReplicaN:                     slotReplicaN,
			ForwardTimeout:                   forwardTimeout,
			PoolSize:                         poolSize,
			DataPlanePoolSize:                dataPlanePoolSize,
			TickInterval:                     tickInterval,
			RaftWorkers:                      raftWorkers,
			ElectionTick:                     electionTick,
			HeartbeatTick:                    heartbeatTick,
			DialTimeout:                      dialTimeout,
			ManagedSlotsReadyTimeout:         managedSlotsReadyTimeout,
			FollowerReplicationRetryInterval: followerReplicationRetryInterval,
			AppendGroupCommitMaxWait:         appendGroupCommitMaxWait,
			AppendGroupCommitMaxRecords:      appendGroupCommitMaxRecords,
			AppendGroupCommitMaxBytes:        appendGroupCommitMaxBytes,
			CommitCoordinatorFlushWindow:     commitCoordinatorFlushWindow,
			CommitCoordinatorMaxRequests:     commitCoordinatorMaxRequests,
			CommitCoordinatorMaxRecords:      commitCoordinatorMaxRecords,
			CommitCoordinatorMaxBytes:        commitCoordinatorMaxBytes,
			Seeds:                            seeds,
			AdvertiseAddr:                    stringValue(v, "WK_CLUSTER_ADVERTISE_ADDR"),
			JoinToken:                        stringValue(v, "WK_CLUSTER_JOIN_TOKEN"),
			ControllerLogCompaction: app.ControllerLogCompactionConfig{
				Enabled:        controllerLogCompactionEnabled,
				TriggerEntries: controllerLogCompactionTriggerEntries,
				CheckInterval:  controllerLogCompactionCheckInterval,
			},
			SlotLogCompaction: app.SlotLogCompactionConfig{
				Enabled:        slotLogCompactionEnabled,
				TriggerEntries: slotLogCompactionTriggerEntries,
				CheckInterval:  slotLogCompactionCheckInterval,
			},
			Timeouts: raftcluster.Timeouts{
				ControllerObservation:              controllerObservationInterval,
				ControllerRequest:                  controllerRequestTimeout,
				ControllerLeaderWait:               controllerLeaderWaitTimeout,
				ObservationHeartbeatInterval:       observationHeartbeatInterval,
				ObservationRuntimeScanInterval:     observationRuntimeScanInterval,
				ObservationRuntimeFlushDebounce:    observationRuntimeFlushDebounce,
				ObservationRuntimeFullSyncInterval: observationRuntimeFullSyncInterval,
				ForwardRetryBudget:                 forwardRetryBudget,
				ManagedSlotLeaderWait:              managedSlotLeaderWaitTimeout,
				ManagedSlotCatchUp:                 managedSlotCatchUpTimeout,
				ManagedSlotLeaderMove:              managedSlotLeaderMoveTimeout,
				ConfigChangeRetryBudget:            configChangeRetryBudget,
				LeaderTransferRetryBudget:          leaderTransferRetryBudget,
			},
			DataPlaneRPCTimeout:       dataPlaneRPCTimeout,
			DataPlaneMaxFetchInflight: dataPlaneMaxFetchInflight,
			DataPlaneMaxPendingFetch:  dataPlaneMaxPendingFetch,
		},
		ChannelPlane: app.ChannelPlaneConfig{
			ReactorCount:     channelPlaneReactorCount,
			PeerLaneCount:    channelPlanePeerLaneCount,
			PeerBatchMaxWait: channelPlanePeerBatchMaxWait,
		},
		ChannelMigration: app.ChannelMigrationConfig{
			ScanInterval:           channelMigrationScanInterval,
			ScanLimit:              channelMigrationScanLimit,
			OwnerLeaseTTL:          channelMigrationOwnerLeaseTTL,
			RetryBackoff:           channelMigrationRetryBackoff,
			FenceTTL:               channelMigrationFenceTTL,
			LeaderLeaseTTL:         channelMigrationLeaderLeaseTTL,
			CatchUpStableWindow:    channelMigrationCatchUpStableWindow,
			CatchUpLagThreshold:    uint64(channelMigrationCatchUpLagThreshold),
			MaxConcurrent:          channelMigrationMaxConcurrent,
			MaxConcurrentPerSource: channelMigrationMaxConcurrentPerSource,
			MaxConcurrentPerTarget: channelMigrationMaxConcurrentPerTarget,
			CompletedRetentionTTL:  channelMigrationCompletedRetentionTTL,
			GCLimit:                channelMigrationGCLimit,
		},
		ChannelMessageRetention: app.ChannelMessageRetentionConfig{
			TTL:              channelMessageRetentionTTL,
			ScanInterval:     channelMessageRetentionScanInterval,
			ChannelBatchSize: channelMessageRetentionChannelBatchSize,
			MaxTrimMessages:  channelMessageRetentionMaxTrimMessages,
		},
		Bench: app.BenchConfig{
			APIEnabled:         benchAPIEnable,
			APIMaxBatchSize:    benchAPIMaxBatchSize,
			APIMaxPayloadBytes: benchAPIMaxPayloadBytes,
		},
		Message: app.MessageConfig{
			PersonWhitelistEnabled:       messagePersonWhitelistEnabled,
			SystemDeviceID:               messageSystemDeviceID,
			PermissionCacheTTL:           messagePermissionCacheTTL,
			UserRateLimitEnabled:         messageUserRateLimitEnabled,
			UserRateLimitRate:            messageUserRateLimitRate,
			UserRateLimitBurst:           messageUserRateLimitBurst,
			UserRateLimitBucketShards:    messageUserRateLimitBucketShards,
			UserRateLimitIdleTTL:         messageUserRateLimitIdleTTL,
			UserRateLimitMaxBuckets:      messageUserRateLimitMaxBuckets,
			UserRateLimitSystemUIDBypass: messageUserRateLimitSystemUIDBypass,
			UserRateLimitPluginBypass:    messageUserRateLimitPluginBypass,
		},
		Delivery: app.DeliveryConfig{
			PresenceCacheTTL: deliveryPresenceCacheTTL,
			AckBatchMaxWait:  deliveryAckBatchMaxWait,
			AckBatchMaxSize:  deliveryAckBatchMaxSize,
		},
		Plugin: app.PluginConfig{
			Enable:     pluginEnable,
			Dir:        stringValue(v, "WK_PLUGIN_DIR"),
			SocketPath: stringValue(v, "WK_PLUGIN_SOCKET_PATH"),
			SandboxDir: stringValue(v, "WK_PLUGIN_SANDBOX_DIR"),
			StateDir:   stringValue(v, "WK_PLUGIN_STATE_DIR"),
			Timeout:    pluginTimeout,
			HotReload:  pluginHotReload,
			FailOpen:   pluginFailOpen,
		},
		Conversation: app.ConversationConfig{
			ActiveHintFlushInterval:         activeHintFlushInterval,
			ActiveHintTTL:                   activeHintTTL,
			ActiveHintBarrierTTL:            activeHintBarrierTTL,
			ActiveHintMaxHints:              activeHintMaxHints,
			ActiveHintMaxHintsPerUID:        activeHintMaxHintsPerUID,
			ActiveHintFlushBatchSize:        activeHintFlushBatchSize,
			GroupActiveFanoutInterval:       groupActiveFanoutInterval,
			GroupActiveFanoutMaxSubscribers: groupActiveFanoutMaxSubscribers,
		},
		API: app.APIConfig{
			ListenAddr:      defaultAPIListenAddr,
			ExternalTCPAddr: stringValue(v, "WK_EXTERNAL_TCPADDR"),
			ExternalWSAddr:  stringValue(v, "WK_EXTERNAL_WSADDR"),
			ExternalWSSAddr: stringValue(v, "WK_EXTERNAL_WSSADDR"),
		},
		Manager: app.ManagerConfig{
			ListenAddr: stringValue(v, "WK_MANAGER_LISTEN_ADDR"),
			AuthOn:     managerAuthOn,
			JWTSecret:  stringValue(v, "WK_MANAGER_JWT_SECRET"),
			JWTIssuer:  stringValue(v, "WK_MANAGER_JWT_ISSUER"),
			JWTExpire:  managerJWTExpire,
			Users:      managerUsers,
		},
		Gateway: app.GatewayConfig{
			TokenAuthOn: tokenAuthOn,
			SendTimeout: gatewaySendTimeout,
			DefaultSession: gateway.SessionOptions{
				MaxInboundBytes:          maxInboundBytes,
				MaxOutboundBytes:         maxOutboundBytes,
				IdleTimeout:              idleTimeout,
				AsyncSendDispatchWorkers: asyncSendDispatchWorkers,
				AsyncSendBatchMaxWait:    asyncSendBatchMaxWait,
				AsyncSendBatchMaxRecords: asyncSendBatchMaxRecords,
				AsyncSendBatchMaxBytes:   asyncSendBatchMaxBytes,
				CloseOnHandlerError:      closeOnHandlerError,
			},
			Transport: gateway.TransportOptions{
				Gnet: gateway.GnetTransportOptions{
					Multicore:      gatewayGnetMulticore,
					NumEventLoop:   gatewayGnetNumEventLoop,
					ReusePort:      gatewayGnetReusePort,
					ReadBufferCap:  gatewayGnetReadBufferCap,
					WriteBufferCap: gatewayGnetWriteBufferCap,
				},
			},
			Listeners: listeners,
		},
		Observability: app.ObservabilityConfig{
			MetricsEnabled:      metricsEnable,
			NetworkEnabled:      networkObservabilityEnable,
			HealthDetailEnabled: healthDetailEnable,
			HealthDebugEnabled:  healthDebugEnable,
			Diagnostics: app.DiagnosticsConfig{
				Enabled:         diagnosticsEnable,
				BufferSize:      diagnosticsBufferSize,
				SampleRate:      diagnosticsSampleRate,
				SlowThreshold:   time.Duration(diagnosticsSlowThresholdMS) * time.Millisecond,
				ErrorSampleRate: diagnosticsErrorSampleRate,
				DebugAPIEnabled: diagnosticsDebugAPIEnable,
				DebugMatches:    diagnosticsDebugMatches,
			},
		},
		Log: app.LogConfig{
			Level:      stringValue(v, "WK_LOG_LEVEL"),
			Dir:        stringValue(v, "WK_LOG_DIR"),
			MaxSize:    logMaxSize,
			MaxAge:     logMaxAge,
			MaxBackups: logMaxBackups,
			Compress:   logCompress,
			Console:    logConsole,
			Format:     stringValue(v, "WK_LOG_FORMAT"),
		},
	}
	cfg.Cluster.SetExplicitFlags(
		channelBootstrapDefaultMinISRSet,
		stringValue(v, "WK_CLUSTER_FOLLOWER_REPLICATION_RETRY_INTERVAL") != "",
		stringValue(v, "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_WAIT") != "",
		stringValue(v, "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_RECORDS") != "",
		stringValue(v, "WK_CLUSTER_APPEND_GROUP_COMMIT_MAX_BYTES") != "",
	)
	cfg.Cluster.SetReplicationExplicitFlags(
		stringValue(v, "WK_CLUSTER_LONG_POLL_LANE_COUNT") != "",
		stringValue(v, "WK_CLUSTER_LONG_POLL_MAX_WAIT") != "",
		stringValue(v, "WK_CLUSTER_LONG_POLL_MAX_BYTES") != "",
		stringValue(v, "WK_CLUSTER_LONG_POLL_MAX_CHANNELS") != "",
	)
	cfg.Cluster.SetCommitCoordinatorExplicitFlags(
		stringValue(v, "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW") != "",
		stringValue(v, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS") != "",
		stringValue(v, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS") != "",
		stringValue(v, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES") != "",
	)
	cfg.Cluster.SetControllerLogCompactionExplicitFlags(
		stringValue(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED") != "",
		stringValue(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES") != "",
		stringValue(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL") != "",
	)
	cfg.Cluster.SetSlotLogCompactionExplicitFlags(
		stringValue(v, "WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED") != "",
		stringValue(v, "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES") != "",
		stringValue(v, "WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL") != "",
	)
	cfg.Storage.SetRaftSnapshotExplicitFlags(
		stringValue(v, "WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE") != "",
		stringValue(v, "WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE") != "",
	)
	cfg.Plugin.SetExplicitFlags(
		stringValue(v, "WK_PLUGIN_TIMEOUT") != "",
		stringValue(v, "WK_PLUGIN_HOT_RELOAD") != "",
		stringValue(v, "WK_PLUGIN_FAIL_OPEN") != "",
	)
	cfg.Message.SetExplicitFlags(
		stringValue(v, "WK_MESSAGE_USER_RATE_LIMIT_RATE") != "",
		stringValue(v, "WK_MESSAGE_USER_RATE_LIMIT_BURST") != "",
		stringValue(v, "WK_MESSAGE_USER_RATE_LIMIT_BUCKET_SHARDS") != "",
		stringValue(v, "WK_MESSAGE_USER_RATE_LIMIT_IDLE_TTL") != "",
		stringValue(v, "WK_MESSAGE_USER_RATE_LIMIT_MAX_BUCKETS") != "",
		stringValue(v, "WK_MESSAGE_USER_RATE_LIMIT_SYSTEM_UID_BYPASS") != "",
	)
	cfg.Gateway.SetExplicitFlags(stringValue(v, "WK_GATEWAY_SEND_TIMEOUT") != "")
	cfg.Log.SetExplicitFlags(stringValue(v, "WK_LOG_COMPRESS") != "", stringValue(v, "WK_LOG_CONSOLE") != "")
	cfg.Observability.SetExplicitFlags(
		stringValue(v, "WK_METRICS_ENABLE") != "",
		stringValue(v, "WK_HEALTH_DETAIL_ENABLE") != "",
		stringValue(v, "WK_HEALTH_DEBUG_ENABLE") != "",
	)
	cfg.Observability.SetNetworkExplicitFlag(stringValue(v, "WK_NETWORK_OBSERVABILITY_ENABLE") != "")
	cfg.Observability.SetDiagnosticsExplicitFlags(
		stringValue(v, "WK_DIAGNOSTICS_ENABLE") != "",
		stringValue(v, "WK_DIAGNOSTICS_SAMPLE_RATE") != "",
		stringValue(v, "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE") != "",
		stringValue(v, "WK_DIAGNOSTICS_DEBUG_API_ENABLE") != "",
	)
	cfg.ChannelMigration.SetExplicitFlags(
		stringValue(v, "WK_CHANNEL_MIGRATION_SCAN_INTERVAL") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_SCAN_LIMIT") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_OWNER_LEASE_TTL") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_RETRY_BACKOFF") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_FENCE_TTL") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_LEADER_LEASE_TTL") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_CATCH_UP_STABLE_WINDOW") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_CATCH_UP_LAG_THRESHOLD") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_MAX_CONCURRENT") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_SOURCE") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_MAX_CONCURRENT_PER_TARGET") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_COMPLETED_RETENTION_TTL") != "",
		stringValue(v, "WK_CHANNEL_MIGRATION_GC_LIMIT") != "",
	)
	cfg.ChannelMessageRetention.SetExplicitFlags(
		stringValue(v, "WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL") != "",
		stringValue(v, "WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE") != "",
		stringValue(v, "WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES") != "",
	)

	if listenAddr := stringValue(v, "WK_API_LISTEN_ADDR"); listenAddr != "" {
		cfg.API.ListenAddr = listenAddr
	}

	return cfg, nil
}

func missingDefaultConfigError(attemptedPaths []string, err error) error {
	return fmt.Errorf(
		"load config: no config file found in default paths %s: %w",
		strings.Join(attemptedPaths, ", "),
		err,
	)
}

const defaultAPIListenAddr = "0.0.0.0:5001"

func defaultGatewayListeners() []gateway.ListenerOptions {
	return []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100"),
		binding.WSMux("ws-gateway", "0.0.0.0:5200"),
	}
}

func parseListeners(v *viper.Viper) ([]gateway.ListenerOptions, error) {
	raw := stringValue(v, "WK_GATEWAY_LISTENERS")
	if raw == "" {
		return defaultGatewayListeners(), nil
	}

	listeners, err := parseJSONValue[[]gateway.ListenerOptions](v, "WK_GATEWAY_LISTENERS")
	if err != nil {
		return nil, err
	}
	return listeners, nil
}

func parseJSONValue[T any](v *viper.Viper, key string) (T, error) {
	var zero T

	raw := stringValue(v, key)
	if raw == "" {
		return zero, nil
	}

	var value T
	if err := v.UnmarshalKey(key, &value); err == nil {
		return value, nil
	}

	if err := jsonUnmarshalString(raw, &value); err != nil {
		return zero, fmt.Errorf("parse %s as JSON: %w", key, err)
	}
	return value, nil
}

func parseJSONStringList(v *viper.Viper, key string) ([]string, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return nil, nil
	}

	var value []string
	if err := jsonUnmarshalString(raw, &value); err != nil {
		return nil, fmt.Errorf("parse %s as JSON: %w", key, err)
	}
	return value, nil
}

func parseUint64(v *viper.Viper, key string) (uint64, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseUint32(v *viper.Viper, key string) (uint32, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return uint32(value), nil
}

func parseUint16(v *viper.Viper, key string) (uint16, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return uint16(value), nil
}

func parseInt(v *viper.Viper, key string) (int, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseInt64(v *viper.Viper, key string) (int64, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseFloat(v *viper.Viper, key string) (float64, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseBool(v *viper.Viper, key string) (bool, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return false, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseOptionalBool(v *viper.Viper, key string) (*bool, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return nil, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", key, err)
	}
	return &value, nil
}

func parseDuration(v *viper.Viper, key string) (time.Duration, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseRaftSnapshotChunkSize(v *viper.Viper, key string) (uint64, error) {
	raw := stringValue(v, key)
	if raw == "" {
		return 0, nil
	}

	value, err := app.ParseRaftSnapshotChunkSize(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func stringValue(v *viper.Viper, key string) string {
	return strings.TrimSpace(v.GetString(key))
}

func jsonUnmarshalString[T any](raw string, value *T) error {
	return json.Unmarshal([]byte(raw), value)
}
