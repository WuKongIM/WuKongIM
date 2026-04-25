package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
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
	channelBootstrapDefaultMinISR, err := parseInt(v, "WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR")
	if err != nil {
		return app.Config{}, err
	}
	channelBootstrapDefaultMinISRSet := stringValue(v, "WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR") != ""
	hashSlotCount, err := parseUint16(v, "WK_CLUSTER_HASH_SLOT_COUNT")
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

	nodes, err := parseJSONValue[[]app.NodeConfigRef](v, "WK_CLUSTER_NODES")
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
	readBufferSize, err := parseInt(v, "WK_GATEWAY_DEFAULT_SESSION_READ_BUFFER_SIZE")
	if err != nil {
		return app.Config{}, err
	}
	writeQueueSize, err := parseInt(v, "WK_GATEWAY_DEFAULT_SESSION_WRITE_QUEUE_SIZE")
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
	writeTimeout, err := parseDuration(v, "WK_GATEWAY_DEFAULT_SESSION_WRITE_TIMEOUT")
	if err != nil {
		return app.Config{}, err
	}
	asyncSendDispatch, err := parseBool(v, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH")
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

	cfg := app.Config{
		Node: app.NodeConfig{
			ID:      nodeID,
			Name:    stringValue(v, "WK_NODE_NAME"),
			DataDir: stringValue(v, "WK_NODE_DATA_DIR"),
		},
		Storage: app.StorageConfig{
			DBPath:             stringValue(v, "WK_STORAGE_DB_PATH"),
			RaftPath:           stringValue(v, "WK_STORAGE_RAFT_PATH"),
			ChannelLogPath:     stringValue(v, "WK_STORAGE_CHANNEL_LOG_PATH"),
			ControllerMetaPath: stringValue(v, "WK_STORAGE_CONTROLLER_META_PATH"),
			ControllerRaftPath: stringValue(v, "WK_STORAGE_CONTROLLER_RAFT_PATH"),
		},
		Cluster: app.ClusterConfig{
			ListenAddr:                       stringValue(v, "WK_CLUSTER_LISTEN_ADDR"),
			SlotCount:                        slotCount,
			HashSlotCount:                    hashSlotCount,
			InitialSlotCount:                 initialSlotCount,
			ChannelBootstrapDefaultMinISR:    channelBootstrapDefaultMinISR,
			LongPollLaneCount:                longPollLaneCount,
			LongPollMaxWait:                  longPollMaxWait,
			LongPollMaxBytes:                 longPollMaxBytes,
			LongPollMaxChannels:              longPollMaxChannels,
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
			FollowerReplicationRetryInterval: followerReplicationRetryInterval,
			AppendGroupCommitMaxWait:         appendGroupCommitMaxWait,
			AppendGroupCommitMaxRecords:      appendGroupCommitMaxRecords,
			AppendGroupCommitMaxBytes:        appendGroupCommitMaxBytes,
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
				ReadBufferSize:      readBufferSize,
				WriteQueueSize:      writeQueueSize,
				MaxInboundBytes:     maxInboundBytes,
				MaxOutboundBytes:    maxOutboundBytes,
				IdleTimeout:         idleTimeout,
				WriteTimeout:        writeTimeout,
				AsyncSendDispatch:   asyncSendDispatch,
				CloseOnHandlerError: closeOnHandlerError,
			},
			Listeners: listeners,
		},
		Observability: app.ObservabilityConfig{
			MetricsEnabled:      metricsEnable,
			HealthDetailEnabled: healthDetailEnable,
			HealthDebugEnabled:  healthDebugEnable,
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
	cfg.Gateway.SetExplicitFlags(stringValue(v, "WK_GATEWAY_SEND_TIMEOUT") != "")
	cfg.Log.SetExplicitFlags(stringValue(v, "WK_LOG_COMPRESS") != "", stringValue(v, "WK_LOG_CONSOLE") != "")
	cfg.Observability.SetExplicitFlags(
		stringValue(v, "WK_METRICS_ENABLE") != "",
		stringValue(v, "WK_HEALTH_DETAIL_ENABLE") != "",
		stringValue(v, "WK_HEALTH_DEBUG_ENABLE") != "",
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

func stringValue(v *viper.Viper, key string) string {
	return strings.TrimSpace(v.GetString(key))
}

func jsonUnmarshalString[T any](raw string, value *T) error {
	return json.Unmarshal([]byte(raw), value)
}
