package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/app"
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
	"WK_CLUSTER_INITIAL_SLOT_COUNT",
	"WK_CLUSTER_HASH_SLOT_COUNT",
	"WK_CLUSTER_SLOT_REPLICA_N",
	"WK_CLUSTER_CHANNEL_REACTOR_COUNT",
	"WK_API_LISTEN_ADDR",
	"WK_BENCH_API_ENABLE",
	"WK_BENCH_API_MAX_BATCH_SIZE",
	"WK_BENCH_API_MAX_PAYLOAD_BYTES",
	"WK_METRICS_ENABLE",
	"WK_PPROF_ENABLE",
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
}

const (
	defaultBenchAPIMaxBatchSize    = 10000
	defaultBenchAPIMaxPayloadBytes = 10 * 1024 * 1024
)

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
