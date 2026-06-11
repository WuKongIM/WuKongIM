package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/app"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
)

// defaultConfigPaths preserves the legacy wukongim.conf lookup order.
var defaultConfigPaths = []string{
	"./wukongim.conf",
	"./conf/wukongim.conf",
	"/etc/wukongim/wukongim.conf",
}

// supportedConfigKeys lists the WK_ keys accepted by the wukongimv2 skeleton.
var supportedConfigKeys = []string{
	"WK_NODE_ID",
	"WK_NODE_DATA_DIR",
	"WK_CLUSTER_LISTEN_ADDR",
	"WK_CLUSTER_ID",
	"WK_CLUSTER_NODES",
	"WK_CLUSTER_INITIAL_SLOT_COUNT",
	"WK_CLUSTER_HASH_SLOT_COUNT",
	"WK_CLUSTER_SLOT_REPLICA_N",
	"WK_CLUSTER_CHANNEL_REACTOR_COUNT",
	"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS",
	"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS",
	"WK_CLUSTER_MAX_CHANNELS",
	"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS",
	"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT",
	"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL",
	"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER",
	"WK_CLUSTER_COMMIT_COORDINATOR_SYNC",
	"WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW",
	"WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS",
	"WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS",
	"WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES",
	"WK_CLUSTER_COMMIT_COORDINATOR_SHARDS",
	"WK_API_LISTEN_ADDR",
	"WK_BENCH_API_ENABLE",
	"WK_BENCH_API_MAX_BATCH_SIZE",
	"WK_BENCH_API_MAX_PAYLOAD_BYTES",
	"WK_METRICS_ENABLE",
	"WK_PPROF_ENABLE",
	"WK_HEALTH_DEBUG_ENABLE",
	"WK_DIAGNOSTICS_ENABLE",
	"WK_DIAGNOSTICS_BUFFER_SIZE",
	"WK_DIAGNOSTICS_SAMPLE_RATE",
	"WK_DIAGNOSTICS_SLOW_THRESHOLD_MS",
	"WK_DIAGNOSTICS_ERROR_SAMPLE_RATE",
	"WK_DIAGNOSTICS_DEEP_SAMPLE_RATE",
	"WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS",
	"WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH",
	"WK_DIAGNOSTICS_DEBUG_API_ENABLE",
	"WK_DIAGNOSTICS_DEBUG_MATCHES",
	"WK_EXTERNAL_TCPADDR",
	"WK_EXTERNAL_WSADDR",
	"WK_EXTERNAL_WSSADDR",
	"WK_GATEWAY_GNET_MULTICORE",
	"WK_GATEWAY_GNET_NUM_EVENT_LOOP",
	"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS",
	"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT",
	"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS",
	"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES",
	"WK_GATEWAY_LISTENERS",
	"WK_GATEWAY_SEND_TIMEOUT",
	"WK_PRESENCE_ACTIVATION_TIMEOUT",
	"WK_PRESENCE_TOUCH_FLUSH_INTERVAL",
	"WK_PRESENCE_TOUCH_BATCH_SIZE",
	"WK_PRESENCE_ROUTE_TTL",
	"WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY",
	"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID",
	"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS",
	"WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX",
	"WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT",
	"WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL",
	"WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS",
	"WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS",
	"WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY",
	"WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD",
	"WK_DELIVERY_ENABLE",
	"WK_DELIVERY_CHANNEL_WRITE_SHARD_COUNT",
	"WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS",
	"WK_DELIVERY_CHANNEL_WRITE_POST_COMMIT_WORKERS",
	"WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY",
	"WK_DELIVERY_FANOUT_PAGE_SIZE",
	"WK_DELIVERY_PUSH_BATCH_SIZE",
	"WK_DELIVERY_PENDING_ACK_TTL",
	"WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION",
	"WK_DELIVERY_EVENT_QUEUE_SIZE",
	"WK_LOG_LEVEL",
	"WK_LOG_DIR",
	"WK_LOG_MAX_SIZE",
	"WK_LOG_MAX_AGE",
	"WK_LOG_MAX_BACKUPS",
	"WK_LOG_COMPRESS",
	"WK_LOG_CONSOLE",
	"WK_LOG_FORMAT",
}

const (
	defaultBenchAPIMaxBatchSize    = 10000
	defaultBenchAPIMaxPayloadBytes = 10 * 1024 * 1024
)

// clusterNodeConfig describes one static clusterv2 node from WK_CLUSTER_NODES.
type clusterNodeConfig struct {
	// ID is the stable cluster node identity.
	ID uint64 `json:"id"`
	// Addr is the node-to-node clusterv2 RPC address advertised to peers.
	Addr string `json:"addr"`
}

// loadConfig reads the minimal wukongimv2 configuration from args, files, and env.
func loadConfig(args []string) (app.Config, error) {
	configPath, err := parseConfigPath(args)
	if err != nil {
		return app.Config{}, err
	}

	values, err := readConfigValues(configPath)
	if err != nil {
		return app.Config{}, err
	}
	overlayEnv(values)

	cfg, err := buildConfig(values)
	if err != nil {
		return app.Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func parseConfigPath(args []string) (string, error) {
	fs := flag.NewFlagSet("wukongimv2", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to wukongim.conf file")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("parse flags: %w", err)
	}
	return strings.TrimSpace(*configPath), nil
}

func readConfigValues(configPath string) (map[string]string, error) {
	if configPath != "" {
		return readKeyValueFile(configPath)
	}

	for _, candidate := range defaultConfigPaths {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", candidate, err)
		}
		return readKeyValueFile(candidate)
	}
	return map[string]string{}, nil
}

func readKeyValueFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()
	values, err := readKeyValues(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return values, nil
}

func readKeyValues(r io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=value", lineNo)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func overlayEnv(values map[string]string) {
	for _, key := range supportedConfigKeys {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
}

func buildConfig(values map[string]string) (app.Config, error) {
	cfg := app.Config{
		Gateway: app.GatewayConfig{
			Listeners: defaultGatewayListeners(),
			Session:   gateway.DefaultSessionOptions(),
			Transport: gateway.TransportOptions{
				Gnet: defaultGatewayGnetOptions(),
			},
		},
		Bench: app.BenchConfig{
			APIMaxBatchSize:    defaultBenchAPIMaxBatchSize,
			APIMaxPayloadBytes: defaultBenchAPIMaxPayloadBytes,
		},
		Channel: app.ChannelConfig{
			LargeGroupSubscriberThreshold: 500,
		},
		Delivery: app.DeliveryConfig{
			Enabled: true,
		},
	}
	rawNodeID, err := requiredConfigValue(values, "WK_NODE_ID")
	if err != nil {
		return app.Config{}, err
	}
	nodeID, err := parseUint64("WK_NODE_ID", rawNodeID)
	if err != nil {
		return app.Config{}, err
	}
	cfg.NodeID = nodeID
	cfg.Cluster.NodeID = nodeID

	dataDir, err := requiredConfigValue(values, "WK_NODE_DATA_DIR")
	if err != nil {
		return app.Config{}, err
	}
	cfg.DataDir = dataDir
	cfg.Cluster.DataDir = dataDir

	listenAddr, err := requiredConfigValue(values, "WK_CLUSTER_LISTEN_ADDR")
	if err != nil {
		return app.Config{}, err
	}
	cfg.Cluster.ListenAddr = listenAddr

	cfg.Cluster.Control.ClusterID = configValue(values, "WK_CLUSTER_ID")
	if raw := configValue(values, "WK_CLUSTER_NODES"); raw != "" {
		nodes, err := parseClusterNodes(raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Cluster.Control.Voters = clusterVoters(nodes)
		cfg.Cluster.Control.AllowBootstrap = true
		if cfg.Cluster.Control.ClusterID == "" {
			cfg.Cluster.Control.ClusterID = deriveStaticClusterID(nodes)
		}
	}

	if raw := configValue(values, "WK_CLUSTER_INITIAL_SLOT_COUNT"); raw != "" {
		initialSlotCount, err := parseUint32("WK_CLUSTER_INITIAL_SLOT_COUNT", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Cluster.Slots.InitialSlotCount = initialSlotCount
	}
	if raw := configValue(values, "WK_CLUSTER_HASH_SLOT_COUNT"); raw != "" {
		hashSlotCount, err := parseUint16("WK_CLUSTER_HASH_SLOT_COUNT", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Cluster.Slots.HashSlotCount = hashSlotCount
	}
	if raw := configValue(values, "WK_CLUSTER_SLOT_REPLICA_N"); raw != "" {
		replicaCount, err := parseUint16("WK_CLUSTER_SLOT_REPLICA_N", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Cluster.Slots.ReplicaCount = replicaCount
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_REACTOR_COUNT"); raw != "" {
		reactorCount, err := parseInt("WK_CLUSTER_CHANNEL_REACTOR_COUNT", raw)
		if err != nil {
			return app.Config{}, err
		}
		if reactorCount < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_REACTOR_COUNT: value must be >= 0")
		}
		cfg.Cluster.Channel.ReactorCount = reactorCount
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS"); raw != "" {
		workers, err := parseInt("WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if workers < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS: value must be >= 0")
		}
		cfg.Cluster.Channel.StoreAppendWorkers = workers
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS"); raw != "" {
		workers, err := parseInt("WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if workers < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS: value must be >= 0")
		}
		cfg.Cluster.Channel.StoreApplyWorkers = workers
	}
	if raw := configValue(values, "WK_CLUSTER_MAX_CHANNELS"); raw != "" {
		maxChannels, err := parseInt("WK_CLUSTER_MAX_CHANNELS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxChannels < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_MAX_CHANNELS: value must be >= 0")
		}
		cfg.Cluster.Channel.MaxChannels = maxChannels
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS"); raw != "" {
		maxRecords, err := parseInt("WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxRecords < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS: value must be >= 0")
		}
		cfg.Cluster.Channel.AppendBatchMaxRecords = maxRecords
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT"); raw != "" {
		maxWait, err := parseDuration("WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxWait < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT: value must be >= 0")
		}
		cfg.Cluster.Channel.AppendBatchMaxWait = maxWait
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL"); raw != "" {
		interval, err := parseDuration("WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL", raw)
		if err != nil {
			return app.Config{}, err
		}
		if interval < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL: value must be >= 0")
		}
		cfg.Cluster.Channel.FollowerRecoveryProbeInterval = interval
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER"); raw != "" {
		jitter, err := parseDuration("WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER", raw)
		if err != nil {
			return app.Config{}, err
		}
		if jitter < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER: value must be >= 0")
		}
		cfg.Cluster.Channel.FollowerRecoveryProbeJitter = jitter
	}
	if raw := configValue(values, "WK_CLUSTER_COMMIT_COORDINATOR_SYNC"); raw != "" {
		syncCommit, err := parseBool("WK_CLUSTER_COMMIT_COORDINATOR_SYNC", raw)
		if err != nil {
			return app.Config{}, err
		}
		if !syncCommit {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_COMMIT_COORDINATOR_SYNC: durable sync is always enabled")
		}
	}
	if raw := configValue(values, "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW"); raw != "" {
		flushWindow, err := parseDuration("WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW", raw)
		if err != nil {
			return app.Config{}, err
		}
		if flushWindow <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW: value must be > 0")
		}
		cfg.Cluster.Storage.CommitFlushWindow = flushWindow
	}
	if raw := configValue(values, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS"); raw != "" {
		maxRequests, err := parseInt("WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxRequests < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS: value must be >= 0")
		}
		cfg.Cluster.Storage.CommitMaxRequests = maxRequests
	}
	if raw := configValue(values, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS"); raw != "" {
		maxRecords, err := parseInt("WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxRecords < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS: value must be >= 0")
		}
		cfg.Cluster.Storage.CommitMaxRecords = maxRecords
	}
	if raw := configValue(values, "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES"); raw != "" {
		maxBytes, err := parseInt("WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxBytes < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES: value must be >= 0")
		}
		cfg.Cluster.Storage.CommitMaxBytes = maxBytes
	}
	if raw := configValue(values, "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS"); raw != "" {
		shards, err := parseInt("WK_CLUSTER_COMMIT_COORDINATOR_SHARDS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if shards < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_COMMIT_COORDINATOR_SHARDS: value must be >= 0")
		}
		cfg.Cluster.Storage.CommitShards = shards
	}
	cfg.API.ListenAddr = configValue(values, "WK_API_LISTEN_ADDR")
	cfg.API.ExternalTCPAddr = configValue(values, "WK_EXTERNAL_TCPADDR")
	cfg.API.ExternalWSAddr = configValue(values, "WK_EXTERNAL_WSADDR")
	cfg.API.ExternalWSSAddr = configValue(values, "WK_EXTERNAL_WSSADDR")
	if raw := configValue(values, "WK_BENCH_API_ENABLE"); raw != "" {
		benchAPIEnable, err := parseBool("WK_BENCH_API_ENABLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Bench.APIEnabled = benchAPIEnable
	}
	if raw := configValue(values, "WK_BENCH_API_MAX_BATCH_SIZE"); raw != "" {
		maxBatchSize, err := parseInt("WK_BENCH_API_MAX_BATCH_SIZE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Bench.APIMaxBatchSize = maxBatchSize
	}
	if raw := configValue(values, "WK_BENCH_API_MAX_PAYLOAD_BYTES"); raw != "" {
		maxPayloadBytes, err := parseInt64("WK_BENCH_API_MAX_PAYLOAD_BYTES", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Bench.APIMaxPayloadBytes = maxPayloadBytes
	}
	if raw := configValue(values, "WK_METRICS_ENABLE"); raw != "" {
		metricsEnable, err := parseBool("WK_METRICS_ENABLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Observability.MetricsEnabled = metricsEnable
	}
	if raw := configValue(values, "WK_PPROF_ENABLE"); raw != "" {
		pprofEnable, err := parseBool("WK_PPROF_ENABLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Observability.PProfEnabled = pprofEnable
	}
	if raw := configValue(values, "WK_HEALTH_DEBUG_ENABLE"); raw != "" {
		healthDebugEnable, err := parseBool("WK_HEALTH_DEBUG_ENABLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Observability.HealthDebugEnabled = healthDebugEnable
	}
	diagnosticsEnabledSet := configValue(values, "WK_DIAGNOSTICS_ENABLE") != ""
	diagnosticsSampleRateSet := configValue(values, "WK_DIAGNOSTICS_SAMPLE_RATE") != ""
	diagnosticsErrorSampleRateSet := configValue(values, "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE") != ""
	if raw := configValue(values, "WK_DIAGNOSTICS_ENABLE"); raw != "" {
		enabled, err := parseBool("WK_DIAGNOSTICS_ENABLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Observability.Diagnostics.Enabled = enabled
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_BUFFER_SIZE"); raw != "" {
		bufferSize, err := parseInt("WK_DIAGNOSTICS_BUFFER_SIZE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if bufferSize < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_BUFFER_SIZE: value must be >= 0")
		}
		cfg.Observability.Diagnostics.BufferSize = bufferSize
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_SAMPLE_RATE"); raw != "" {
		sampleRate, err := parseFloat("WK_DIAGNOSTICS_SAMPLE_RATE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if !validSampleRate(sampleRate) {
			return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_SAMPLE_RATE: value must be between 0 and 1")
		}
		cfg.Observability.Diagnostics.SampleRate = sampleRate
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_SLOW_THRESHOLD_MS"); raw != "" {
		slowThresholdMS, err := parseInt("WK_DIAGNOSTICS_SLOW_THRESHOLD_MS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if slowThresholdMS < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_SLOW_THRESHOLD_MS: value must be >= 0")
		}
		cfg.Observability.Diagnostics.SlowThreshold = time.Duration(slowThresholdMS) * time.Millisecond
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE"); raw != "" {
		errorSampleRate, err := parseFloat("WK_DIAGNOSTICS_ERROR_SAMPLE_RATE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if !validSampleRate(errorSampleRate) {
			return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_ERROR_SAMPLE_RATE: value must be between 0 and 1")
		}
		cfg.Observability.Diagnostics.ErrorSampleRate = errorSampleRate
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE"); raw != "" {
		deepSampleRate, err := parseFloat("WK_DIAGNOSTICS_DEEP_SAMPLE_RATE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if deepSampleRate < 0 || deepSampleRate > 1 {
			return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_DEEP_SAMPLE_RATE: value must be between 0 and 1")
		}
		cfg.Observability.Diagnostics.DeepSampleRate = deepSampleRate
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS"); raw != "" {
		deepSlowThresholdMS, err := parseInt("WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if deepSlowThresholdMS < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS: value must be >= 0")
		}
		cfg.Observability.Diagnostics.DeepSlowThreshold = time.Duration(deepSlowThresholdMS) * time.Millisecond
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH"); raw != "" {
		deepMaxItems, err := parseInt("WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH", raw)
		if err != nil {
			return app.Config{}, err
		}
		if deepMaxItems < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH: value must be >= 0")
		}
		cfg.Observability.Diagnostics.DeepMaxItemsPerBatch = deepMaxItems
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_DEBUG_API_ENABLE"); raw != "" {
		debugAPIEnable, err := parseBool("WK_DIAGNOSTICS_DEBUG_API_ENABLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Observability.Diagnostics.DebugAPIEnabled = debugAPIEnable
	}
	if raw := configValue(values, "WK_DIAGNOSTICS_DEBUG_MATCHES"); raw != "" {
		debugMatches, err := parseDiagnosticsDebugMatches(raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Observability.Diagnostics.DebugMatches = debugMatches
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(diagnosticsEnabledSet, diagnosticsSampleRateSet, diagnosticsErrorSampleRateSet)
	if raw := configValue(values, "WK_GATEWAY_LISTENERS"); raw != "" {
		listeners, err := parseListeners(raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Gateway.Listeners = listeners
	}
	if raw := configValue(values, "WK_GATEWAY_GNET_MULTICORE"); raw != "" {
		multicore, err := parseBool("WK_GATEWAY_GNET_MULTICORE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Gateway.Transport.Gnet.Multicore = multicore
	}
	if raw := configValue(values, "WK_GATEWAY_GNET_NUM_EVENT_LOOP"); raw != "" {
		numEventLoop, err := parseInt("WK_GATEWAY_GNET_NUM_EVENT_LOOP", raw)
		if err != nil {
			return app.Config{}, err
		}
		if numEventLoop < 0 {
			return app.Config{}, fmt.Errorf("parse WK_GATEWAY_GNET_NUM_EVENT_LOOP: value must be >= 0")
		}
		if numEventLoop > 0 {
			cfg.Gateway.Transport.Gnet.NumEventLoop = numEventLoop
		}
	}
	if raw := configValue(values, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS"); raw != "" {
		workers, err := parseInt("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Gateway.Session.AsyncSendDispatchWorkers = workers
	}
	if raw := configValue(values, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT"); raw != "" {
		maxWait, err := parseDuration("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Gateway.Session.AsyncSendBatchMaxWait = maxWait
	}
	if raw := configValue(values, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS"); raw != "" {
		maxRecords, err := parseInt("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxRecords < 0 {
			return app.Config{}, fmt.Errorf("parse WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS: value must be >= 0")
		}
		cfg.Gateway.Session.AsyncSendBatchMaxRecords = maxRecords
	}
	if raw := configValue(values, "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES"); raw != "" {
		maxBytes, err := parseInt("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxBytes < 0 {
			return app.Config{}, fmt.Errorf("parse WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES: value must be >= 0")
		}
		cfg.Gateway.Session.AsyncSendBatchMaxBytes = maxBytes
	}
	if raw := configValue(values, "WK_GATEWAY_SEND_TIMEOUT"); raw != "" {
		sendTimeout, err := parseDuration("WK_GATEWAY_SEND_TIMEOUT", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Gateway.SendTimeout = sendTimeout
	}
	cfg.Log.Level = configValue(values, "WK_LOG_LEVEL")
	cfg.Log.Dir = configValue(values, "WK_LOG_DIR")
	if raw := configValue(values, "WK_LOG_MAX_SIZE"); raw != "" {
		maxSize, err := parseInt("WK_LOG_MAX_SIZE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Log.MaxSize = maxSize
	}
	if raw := configValue(values, "WK_LOG_MAX_AGE"); raw != "" {
		maxAge, err := parseInt("WK_LOG_MAX_AGE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Log.MaxAge = maxAge
	}
	if raw := configValue(values, "WK_LOG_MAX_BACKUPS"); raw != "" {
		maxBackups, err := parseInt("WK_LOG_MAX_BACKUPS", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Log.MaxBackups = maxBackups
	}
	if raw := configValue(values, "WK_LOG_COMPRESS"); raw != "" {
		compress, err := parseBool("WK_LOG_COMPRESS", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Log.Compress = compress
	}
	if raw := configValue(values, "WK_LOG_CONSOLE"); raw != "" {
		console, err := parseBool("WK_LOG_CONSOLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Log.Console = console
	}
	cfg.Log.Format = configValue(values, "WK_LOG_FORMAT")
	cfg.Log.SetExplicitFlags(configValue(values, "WK_LOG_COMPRESS") != "", configValue(values, "WK_LOG_CONSOLE") != "")
	if raw := configValue(values, "WK_PRESENCE_ACTIVATION_TIMEOUT"); raw != "" {
		activationTimeout, err := parseDuration("WK_PRESENCE_ACTIVATION_TIMEOUT", raw)
		if err != nil {
			return app.Config{}, err
		}
		if activationTimeout < 0 {
			return app.Config{}, fmt.Errorf("parse WK_PRESENCE_ACTIVATION_TIMEOUT: value must be >= 0")
		}
		cfg.Presence.ActivationTimeout = activationTimeout
	}
	if raw := configValue(values, "WK_PRESENCE_TOUCH_FLUSH_INTERVAL"); raw != "" {
		interval, err := parseDuration("WK_PRESENCE_TOUCH_FLUSH_INTERVAL", raw)
		if err != nil {
			return app.Config{}, err
		}
		if interval < 0 {
			return app.Config{}, fmt.Errorf("parse WK_PRESENCE_TOUCH_FLUSH_INTERVAL: value must be >= 0")
		}
		cfg.Presence.TouchFlushInterval = interval
	}
	if raw := configValue(values, "WK_PRESENCE_TOUCH_BATCH_SIZE"); raw != "" {
		batchSize, err := parseInt("WK_PRESENCE_TOUCH_BATCH_SIZE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if batchSize < 0 {
			return app.Config{}, fmt.Errorf("parse WK_PRESENCE_TOUCH_BATCH_SIZE: value must be >= 0")
		}
		cfg.Presence.TouchBatchSize = batchSize
	}
	if raw := configValue(values, "WK_PRESENCE_ROUTE_TTL"); raw != "" {
		ttl, err := parseDuration("WK_PRESENCE_ROUTE_TTL", raw)
		if err != nil {
			return app.Config{}, err
		}
		if ttl < 0 {
			return app.Config{}, fmt.Errorf("parse WK_PRESENCE_ROUTE_TTL: value must be >= 0")
		}
		cfg.Presence.RouteTTL = ttl
	}
	if raw := configValue(values, "WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY"); raw != "" {
		limit, err := parseInt("WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY", raw)
		if err != nil {
			return app.Config{}, err
		}
		if limit < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY: value must be >= 0")
		}
		cfg.Conversation.MaxLastMessageConcurrency = limit
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID"); raw != "" {
		limit, err := parseInt("WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID", raw)
		if err != nil {
			return app.Config{}, err
		}
		if limit <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID: value must be > 0")
		}
		cfg.Conversation.AuthorityCacheMaxRowsPerUID = limit
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS"); raw != "" {
		limit, err := parseInt("WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if limit <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS: value must be > 0")
		}
		cfg.Conversation.AuthorityCacheMaxRows = limit
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX"); raw != "" {
		limit, err := parseInt("WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX", raw)
		if err != nil {
			return app.Config{}, err
		}
		if limit <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX: value must be > 0")
		}
		cfg.Conversation.AuthorityListDBWindowMax = limit
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT"); raw != "" {
		timeout, err := parseDuration("WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT", raw)
		if err != nil {
			return app.Config{}, err
		}
		if timeout <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT: value must be > 0")
		}
		cfg.Conversation.AuthorityHandoffTimeout = timeout
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL"); raw != "" {
		interval, err := parseDuration("WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL", raw)
		if err != nil {
			return app.Config{}, err
		}
		if interval <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL: value must be > 0")
		}
		cfg.Conversation.AuthorityFlushInterval = interval
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS"); raw != "" {
		rows, err := parseInt("WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if rows <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS: value must be > 0")
		}
		cfg.Conversation.AuthorityFlushBatchRows = rows
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS"); raw != "" {
		rows, err := parseInt("WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if rows <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS: value must be > 0")
		}
		cfg.Conversation.AuthorityAdmitBatchRows = rows
	}
	if raw := configValue(values, "WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY"); raw != "" {
		concurrency, err := parseInt("WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY", raw)
		if err != nil {
			return app.Config{}, err
		}
		if concurrency <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY: value must be > 0")
		}
		cfg.Conversation.AuthorityAdmitConcurrency = concurrency
	}
	if raw := configValue(values, "WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD"); raw != "" {
		threshold, err := parseInt("WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD", raw)
		if err != nil {
			return app.Config{}, err
		}
		if threshold <= 0 {
			return app.Config{}, fmt.Errorf("parse WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD: value must be > 0")
		}
		cfg.Channel.LargeGroupSubscriberThreshold = threshold
	}
	if raw := configValue(values, "WK_DELIVERY_ENABLE"); raw != "" {
		enabled, err := parseBool("WK_DELIVERY_ENABLE", raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Delivery.Enabled = enabled
	}
	if raw := configValue(values, "WK_DELIVERY_CHANNEL_WRITE_SHARD_COUNT"); raw != "" {
		shards, err := parseInt("WK_DELIVERY_CHANNEL_WRITE_SHARD_COUNT", raw)
		if err != nil {
			return app.Config{}, err
		}
		if shards < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_CHANNEL_WRITE_SHARD_COUNT: value must be >= 0")
		}
		cfg.Delivery.ChannelWriteShardCount = shards
	}
	if raw := configValue(values, "WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS"); raw != "" {
		workers, err := parseInt("WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if workers < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS: value must be >= 0")
		}
		cfg.Delivery.ChannelWriteAppendWorkers = workers
	}
	if raw := configValue(values, "WK_DELIVERY_CHANNEL_WRITE_POST_COMMIT_WORKERS"); raw != "" {
		workers, err := parseInt("WK_DELIVERY_CHANNEL_WRITE_POST_COMMIT_WORKERS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if workers < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_CHANNEL_WRITE_POST_COMMIT_WORKERS: value must be >= 0")
		}
		cfg.Delivery.ChannelWritePostCommitWorkers = workers
	}
	if raw := configValue(values, "WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY"); raw != "" {
		concurrency, err := parseInt("WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY", raw)
		if err != nil {
			return app.Config{}, err
		}
		if concurrency < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY: value must be >= 0")
		}
		cfg.Delivery.ChannelWriteRecipientDispatchConcurrency = concurrency
	}
	if raw := configValue(values, "WK_DELIVERY_FANOUT_PAGE_SIZE"); raw != "" {
		pageSize, err := parseInt("WK_DELIVERY_FANOUT_PAGE_SIZE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if pageSize < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_FANOUT_PAGE_SIZE: value must be >= 0")
		}
		cfg.Delivery.FanoutPageSize = pageSize
	}
	if raw := configValue(values, "WK_DELIVERY_PUSH_BATCH_SIZE"); raw != "" {
		batchSize, err := parseInt("WK_DELIVERY_PUSH_BATCH_SIZE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if batchSize < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_PUSH_BATCH_SIZE: value must be >= 0")
		}
		cfg.Delivery.PushBatchSize = batchSize
	}
	if raw := configValue(values, "WK_DELIVERY_PENDING_ACK_TTL"); raw != "" {
		ttl, err := parseDuration("WK_DELIVERY_PENDING_ACK_TTL", raw)
		if err != nil {
			return app.Config{}, err
		}
		if ttl < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_PENDING_ACK_TTL: value must be >= 0")
		}
		cfg.Delivery.PendingAckTTL = ttl
	}
	if raw := configValue(values, "WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION"); raw != "" {
		maxPending, err := parseInt("WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxPending < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION: value must be >= 0")
		}
		cfg.Delivery.PendingAckMaxPerSession = maxPending
	}
	if raw := configValue(values, "WK_DELIVERY_EVENT_QUEUE_SIZE"); raw != "" {
		queueSize, err := parseInt("WK_DELIVERY_EVENT_QUEUE_SIZE", raw)
		if err != nil {
			return app.Config{}, err
		}
		if queueSize < 0 {
			return app.Config{}, fmt.Errorf("parse WK_DELIVERY_EVENT_QUEUE_SIZE: value must be >= 0")
		}
		cfg.Delivery.EventQueueSize = queueSize
	}

	return cfg, nil
}

func defaultGatewayListeners() []gateway.ListenerOptions {
	return []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100"),
		binding.WSMux("ws-gateway", "0.0.0.0:5200"),
	}
}

func defaultGatewayGnetOptions() gateway.GnetTransportOptions {
	loops := adaptiveGatewayGnetEventLoops(runtime.GOMAXPROCS(0))
	return gateway.GnetTransportOptions{
		Multicore:    loops > 1,
		NumEventLoop: loops,
		ReusePort:    true,
	}
}

func adaptiveGatewayGnetEventLoops(gomaxprocs int) int {
	if gomaxprocs <= 2 {
		return 1
	}
	loops := gomaxprocs / 2
	if loops < 1 {
		return 1
	}
	if loops > 4 {
		return 4
	}
	return loops
}

func parseListeners(raw string) ([]gateway.ListenerOptions, error) {
	var listeners []gateway.ListenerOptions
	if err := json.Unmarshal([]byte(raw), &listeners); err != nil {
		return nil, fmt.Errorf("parse WK_GATEWAY_LISTENERS as JSON: %w", err)
	}
	return listeners, nil
}

func parseClusterNodes(raw string) ([]clusterNodeConfig, error) {
	var nodes []clusterNodeConfig
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return nil, fmt.Errorf("parse WK_CLUSTER_NODES as JSON: %w", err)
	}
	return nodes, nil
}

func parseDiagnosticsDebugMatches(raw string) ([]app.DiagnosticsDebugMatchConfig, error) {
	var matches []app.DiagnosticsDebugMatchConfig
	if err := json.Unmarshal([]byte(raw), &matches); err != nil {
		return nil, fmt.Errorf("parse WK_DIAGNOSTICS_DEBUG_MATCHES as JSON: %w", err)
	}
	for _, match := range matches {
		if !validSampleRate(match.SampleRate) {
			return nil, fmt.Errorf("parse WK_DIAGNOSTICS_DEBUG_MATCHES: sample_rate must be between 0 and 1")
		}
		if match.TTLSeconds < 0 {
			return nil, fmt.Errorf("parse WK_DIAGNOSTICS_DEBUG_MATCHES: ttl_seconds must be >= 0")
		}
	}
	return matches, nil
}

func clusterVoters(nodes []clusterNodeConfig) []clusterv2.ControlVoter {
	voters := make([]clusterv2.ControlVoter, 0, len(nodes))
	for _, node := range nodes {
		voters = append(voters, clusterv2.ControlVoter{NodeID: node.ID, Addr: strings.TrimSpace(node.Addr)})
	}
	return voters
}

func deriveStaticClusterID(nodes []clusterNodeConfig) string {
	ids := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		if node.ID != 0 {
			ids = append(ids, node.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatUint(id, 10))
	}
	return "wk-clusterv2-static-" + strings.Join(parts, "-")
}

func parseUint64(key, raw string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseUint32(key, raw string) (uint32, error) {
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return uint32(value), nil
}

func parseUint16(key, raw string) (uint16, error) {
	value, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return uint16(value), nil
}

func parseBool(key, raw string) (bool, error) {
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseInt(key, raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseInt64(key, raw string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseFloat(key, raw string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("parse %s: value must be finite", key)
	}
	return value, nil
}

func validSampleRate(rate float64) bool {
	return !math.IsNaN(rate) && !math.IsInf(rate, 0) && rate >= 0 && rate <= 1
}

func parseDuration(key, raw string) (time.Duration, error) {
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func configValue(values map[string]string, key string) string {
	return strings.TrimSpace(values[key])
}

func requiredConfigValue(values map[string]string, key string) (string, error) {
	value := configValue(values, key)
	if value == "" {
		return "", fmt.Errorf("missing required config key %s", key)
	}
	return value, nil
}
