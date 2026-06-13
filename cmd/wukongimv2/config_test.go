package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/app"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
)

func TestLoadConfigDefaultValues(t *testing.T) {
	unsetLoadConfigEnv(t)
	chdir(t, t.TempDir())
	dir := t.TempDir()
	t.Setenv("WK_NODE_ID", "1")
	t.Setenv("WK_NODE_DATA_DIR", filepath.Join(dir, "node-1"))
	t.Setenv("WK_CLUSTER_LISTEN_ADDR", "127.0.0.1:7001")

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != filepath.Join(dir, "node-1") || cfg.Cluster.DataDir != filepath.Join(dir, "node-1") {
		t.Fatalf("DataDir = %q/%q", cfg.DataDir, cfg.Cluster.DataDir)
	}
	assertListeners(t, cfg.Gateway.Listeners, []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100"),
		binding.WSMux("ws-gateway", "0.0.0.0:5200"),
	})
	wantLoops := adaptiveGatewayGnetEventLoops(runtime.GOMAXPROCS(0))
	if cfg.Gateway.Transport.Gnet.NumEventLoop != wantLoops {
		t.Fatalf("Gnet.NumEventLoop = %d, want adaptive %d", cfg.Gateway.Transport.Gnet.NumEventLoop, wantLoops)
	}
	if wantLoops > 1 && !cfg.Gateway.Transport.Gnet.Multicore {
		t.Fatalf("Gnet.Multicore = false, want true for %d event loops", wantLoops)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxWait != time.Millisecond {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want 1ms", cfg.Gateway.Session.AsyncSendBatchMaxWait)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxRecords != 512 {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want 512", cfg.Gateway.Session.AsyncSendBatchMaxRecords)
	}
	if cfg.Channel.LargeGroupSubscriberThreshold != 500 {
		t.Fatalf("Channel.LargeGroupSubscriberThreshold = %d, want 500", cfg.Channel.LargeGroupSubscriberThreshold)
	}
	if !cfg.Delivery.Enabled {
		t.Fatalf("Delivery.Enabled = false, want true by default")
	}
	if cfg.Observability.Prometheus.Enabled {
		t.Fatalf("Observability.Prometheus.Enabled = true, want false by default")
	}
}

func TestAdaptiveGatewayGnetEventLoops(t *testing.T) {
	tests := []struct {
		name       string
		gomaxprocs int
		want       int
	}{
		{name: "invalid clamps to one", gomaxprocs: 0, want: 1},
		{name: "small server keeps one", gomaxprocs: 2, want: 1},
		{name: "medium server uses half", gomaxprocs: 6, want: 3},
		{name: "larger server caps at four", gomaxprocs: 16, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveGatewayGnetEventLoops(tt.gomaxprocs); got != tt.want {
				t.Fatalf("adaptiveGatewayGnetEventLoops(%d) = %d, want %d", tt.gomaxprocs, got, tt.want)
			}
		})
	}
}

func TestLoadConfigDefaultPathSearch(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	writeConf(t, filepath.Join(dir, "conf", "wukongim.conf"),
		"WK_NODE_ID=7",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-7"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7007",
	)

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 7 || cfg.Cluster.NodeID != 7 {
		t.Fatalf("NodeID = %d/%d, want 7", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.Cluster.ListenAddr != "127.0.0.1:7007" {
		t.Fatalf("ListenAddr = %q", cfg.Cluster.ListenAddr)
	}
}

func TestLoadConfigExplicitConfigFile(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"# single-node cluster skeleton",
		"",
		"WK_NODE_ID=42",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-42"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7042",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=3",
		"WK_CLUSTER_HASH_SLOT_COUNT=64",
		"WK_CLUSTER_SLOT_REPLICA_N=1",
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT=12",
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=24",
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=20",
		"WK_CLUSTER_CHANNEL_RPC_WORKERS=18",
		"WK_CLUSTER_MAX_CHANNELS=10000",
		"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=1",
		"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=500us",
		"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL=2s",
		"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER=1s",
		"WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=750us",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=16",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=256",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=131072",
		"WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=4",
		"WK_API_LISTEN_ADDR=127.0.0.1:5042",
		"WK_BENCH_API_ENABLE=true",
		"WK_BENCH_API_MAX_BATCH_SIZE=123",
		"WK_BENCH_API_MAX_PAYLOAD_BYTES=456789",
		"WK_METRICS_ENABLE=true",
		"WK_PROMETHEUS_ENABLE=true",
		"WK_PROMETHEUS_BINARY_PATH=/opt/prometheus/prometheus",
		"WK_PROMETHEUS_LISTEN_ADDR=127.0.0.1:9091",
		"WK_PROMETHEUS_DATA_DIR="+filepath.Join(dir, "prometheus"),
		"WK_PROMETHEUS_RETENTION_TIME=48h",
		"WK_PROMETHEUS_RETENTION_SIZE=2GB",
		"WK_PROMETHEUS_SCRAPE_INTERVAL=10s",
		`WK_PROMETHEUS_SCRAPE_TARGETS=["127.0.0.1:5042","127.0.0.1:5043"]`,
		"WK_DEBUG_API_ENABLE=true",
		"WK_EXTERNAL_TCPADDR=127.0.0.1:5142",
		"WK_EXTERNAL_WSADDR=ws://127.0.0.1:5242",
		"WK_EXTERNAL_WSSADDR=wss://127.0.0.1:5342",
		"WK_GATEWAY_GNET_MULTICORE=true",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP=4",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS=128",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT=750us",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=64",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES=262144",
		"WK_GATEWAY_SEND_TIMEOUT=5s",
		"WK_PRESENCE_ACTIVATION_TIMEOUT=2s",
		"WK_PRESENCE_TOUCH_FLUSH_INTERVAL=2s",
		"WK_PRESENCE_TOUCH_BATCH_SIZE=1024",
		"WK_PRESENCE_ROUTE_TTL=2m",
		"WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY=48",
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID=8192",
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS=200000",
		"WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX=1500",
		"WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT=4s",
		"WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL=1500ms",
		"WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS=384",
		"WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS=256",
		"WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY=8",
		"WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD=600",
		"WK_DELIVERY_ENABLE=true",
		"WK_CHANNEL_APPEND_SHARD_COUNT=10",
		"WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE=11",
		"WK_CHANNEL_APPEND_EFFECT_POOL_SIZE=21",
		"WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY=6",
		"WK_DELIVERY_FANOUT_PAGE_SIZE=256",
		"WK_DELIVERY_PUSH_BATCH_SIZE=128",
		"WK_DELIVERY_PENDING_ACK_TTL=45s",
		"WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION=777",
		"WK_DELIVERY_EVENT_QUEUE_SIZE=2048",
		"WK_LOG_LEVEL=debug",
		"WK_LOG_DIR="+filepath.Join(dir, "logs"),
		"WK_LOG_MAX_SIZE=64",
		"WK_LOG_MAX_AGE=7",
		"WK_LOG_MAX_BACKUPS=3",
		"WK_LOG_COMPRESS=false",
		"WK_LOG_CONSOLE=false",
		"WK_LOG_FORMAT=json",
	)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 42 || cfg.Cluster.NodeID != 42 {
		t.Fatalf("NodeID = %d/%d, want 42", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != filepath.Join(dir, "node-42") || cfg.Cluster.DataDir != filepath.Join(dir, "node-42") {
		t.Fatalf("DataDir = %q/%q", cfg.DataDir, cfg.Cluster.DataDir)
	}
	if cfg.Cluster.ListenAddr != "127.0.0.1:7042" {
		t.Fatalf("ListenAddr = %q", cfg.Cluster.ListenAddr)
	}
	if cfg.Cluster.Slots.InitialSlotCount != 3 {
		t.Fatalf("InitialSlotCount = %d", cfg.Cluster.Slots.InitialSlotCount)
	}
	if cfg.Cluster.Slots.HashSlotCount != 64 {
		t.Fatalf("HashSlotCount = %d", cfg.Cluster.Slots.HashSlotCount)
	}
	if cfg.Cluster.Slots.ReplicaCount != 1 {
		t.Fatalf("ReplicaCount = %d", cfg.Cluster.Slots.ReplicaCount)
	}
	if cfg.Cluster.Channel.ReactorCount != 12 {
		t.Fatalf("Channel.ReactorCount = %d, want 12", cfg.Cluster.Channel.ReactorCount)
	}
	if cfg.Cluster.Channel.StoreAppendWorkers != 24 {
		t.Fatalf("Channel.StoreAppendWorkers = %d, want 24", cfg.Cluster.Channel.StoreAppendWorkers)
	}
	if cfg.Cluster.Channel.StoreApplyWorkers != 20 {
		t.Fatalf("Channel.StoreApplyWorkers = %d, want 20", cfg.Cluster.Channel.StoreApplyWorkers)
	}
	if cfg.Cluster.Channel.RPCWorkers != 18 {
		t.Fatalf("Channel.RPCWorkers = %d, want 18", cfg.Cluster.Channel.RPCWorkers)
	}
	if cfg.Cluster.Channel.MaxChannels != 10000 {
		t.Fatalf("Channel.MaxChannels = %d, want 10000", cfg.Cluster.Channel.MaxChannels)
	}
	if cfg.Cluster.Channel.AppendBatchMaxRecords != 1 {
		t.Fatalf("Channel.AppendBatchMaxRecords = %d, want 1", cfg.Cluster.Channel.AppendBatchMaxRecords)
	}
	if cfg.Cluster.Channel.AppendBatchMaxWait != 500*time.Microsecond {
		t.Fatalf("Channel.AppendBatchMaxWait = %s, want 500us", cfg.Cluster.Channel.AppendBatchMaxWait)
	}
	if cfg.Cluster.Channel.FollowerRecoveryProbeInterval != 2*time.Second {
		t.Fatalf("Channel.FollowerRecoveryProbeInterval = %s, want 2s", cfg.Cluster.Channel.FollowerRecoveryProbeInterval)
	}
	if cfg.Cluster.Channel.FollowerRecoveryProbeJitter != time.Second {
		t.Fatalf("Channel.FollowerRecoveryProbeJitter = %s, want 1s", cfg.Cluster.Channel.FollowerRecoveryProbeJitter)
	}
	if cfg.Cluster.Storage.CommitFlushWindow != 750*time.Microsecond {
		t.Fatalf("Storage.CommitFlushWindow = %s, want 750us", cfg.Cluster.Storage.CommitFlushWindow)
	}
	if cfg.Cluster.Storage.CommitMaxRequests != 16 {
		t.Fatalf("Storage.CommitMaxRequests = %d, want 16", cfg.Cluster.Storage.CommitMaxRequests)
	}
	if cfg.Cluster.Storage.CommitMaxRecords != 256 {
		t.Fatalf("Storage.CommitMaxRecords = %d, want 256", cfg.Cluster.Storage.CommitMaxRecords)
	}
	if cfg.Cluster.Storage.CommitMaxBytes != 131072 {
		t.Fatalf("Storage.CommitMaxBytes = %d, want 131072", cfg.Cluster.Storage.CommitMaxBytes)
	}
	if cfg.Cluster.Storage.CommitShards != 4 {
		t.Fatalf("Storage.CommitShards = %d, want 4", cfg.Cluster.Storage.CommitShards)
	}
	if cfg.Gateway.SendTimeout != 5*time.Second {
		t.Fatalf("SendTimeout = %s", cfg.Gateway.SendTimeout)
	}
	if cfg.Presence.ActivationTimeout != 2*time.Second {
		t.Fatalf("Presence.ActivationTimeout = %s, want 2s", cfg.Presence.ActivationTimeout)
	}
	if cfg.Presence.TouchFlushInterval != 2*time.Second {
		t.Fatalf("Presence.TouchFlushInterval = %s, want 2s", cfg.Presence.TouchFlushInterval)
	}
	if cfg.Presence.TouchBatchSize != 1024 {
		t.Fatalf("Presence.TouchBatchSize = %d, want 1024", cfg.Presence.TouchBatchSize)
	}
	if cfg.Presence.RouteTTL != 2*time.Minute {
		t.Fatalf("Presence.RouteTTL = %s, want 2m", cfg.Presence.RouteTTL)
	}
	if cfg.Conversation.MaxLastMessageConcurrency != 48 {
		t.Fatalf("Conversation.MaxLastMessageConcurrency = %d, want 48", cfg.Conversation.MaxLastMessageConcurrency)
	}
	assertConversationAuthorityConfig(t, cfg.Conversation, app.ConversationConfig{
		AuthorityCacheMaxRowsPerUID: 8192,
		AuthorityCacheMaxRows:       200000,
		AuthorityListDBWindowMax:    1500,
		AuthorityHandoffTimeout:     4 * time.Second,
		AuthorityFlushInterval:      1500 * time.Millisecond,
		AuthorityFlushBatchRows:     384,
		AuthorityAdmitBatchRows:     256,
		AuthorityAdmitConcurrency:   8,
	})
	if cfg.Channel.LargeGroupSubscriberThreshold != 600 {
		t.Fatalf("Channel.LargeGroupSubscriberThreshold = %d, want 600", cfg.Channel.LargeGroupSubscriberThreshold)
	}
	if !cfg.Delivery.Enabled {
		t.Fatalf("Delivery.Enabled = false, want true")
	}
	if cfg.ChannelAppend.AuthorityShardCount != 10 {
		t.Fatalf("ChannelAppend.AuthorityShardCount = %d, want 10", cfg.ChannelAppend.AuthorityShardCount)
	}
	if cfg.ChannelAppend.AdvancePoolSize != 11 {
		t.Fatalf("ChannelAppend.AdvancePoolSize = %d, want 11", cfg.ChannelAppend.AdvancePoolSize)
	}
	if cfg.ChannelAppend.EffectPoolSize != 21 {
		t.Fatalf("ChannelAppend.EffectPoolSize = %d, want 21", cfg.ChannelAppend.EffectPoolSize)
	}
	if cfg.ChannelAppend.RecipientAuthorityDispatchConcurrency != 6 {
		t.Fatalf("ChannelAppend.RecipientAuthorityDispatchConcurrency = %d, want 6", cfg.ChannelAppend.RecipientAuthorityDispatchConcurrency)
	}
	if cfg.Delivery.FanoutPageSize != 256 {
		t.Fatalf("Delivery.FanoutPageSize = %d, want 256", cfg.Delivery.FanoutPageSize)
	}
	if cfg.Delivery.PushBatchSize != 128 {
		t.Fatalf("Delivery.PushBatchSize = %d, want 128", cfg.Delivery.PushBatchSize)
	}
	if cfg.Delivery.PendingAckTTL != 45*time.Second {
		t.Fatalf("Delivery.PendingAckTTL = %s, want 45s", cfg.Delivery.PendingAckTTL)
	}
	if cfg.Delivery.PendingAckMaxPerSession != 777 {
		t.Fatalf("Delivery.PendingAckMaxPerSession = %d, want 777", cfg.Delivery.PendingAckMaxPerSession)
	}
	if cfg.Delivery.EventQueueSize != 2048 {
		t.Fatalf("Delivery.EventQueueSize = %d, want 2048", cfg.Delivery.EventQueueSize)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.Log.Dir != filepath.Join(dir, "logs") {
		t.Fatalf("Log.Dir = %q", cfg.Log.Dir)
	}
	if cfg.Log.MaxSize != 64 || cfg.Log.MaxAge != 7 || cfg.Log.MaxBackups != 3 {
		t.Fatalf("Log rotation = size:%d age:%d backups:%d", cfg.Log.MaxSize, cfg.Log.MaxAge, cfg.Log.MaxBackups)
	}
	if cfg.Log.Compress || cfg.Log.Console {
		t.Fatalf("Log booleans = compress:%t console:%t, want both false", cfg.Log.Compress, cfg.Log.Console)
	}
	if cfg.Log.Format != "json" {
		t.Fatalf("Log.Format = %q, want json", cfg.Log.Format)
	}
	if cfg.API.ListenAddr != "127.0.0.1:5042" {
		t.Fatalf("API.ListenAddr = %q", cfg.API.ListenAddr)
	}
	if !cfg.Bench.APIEnabled {
		t.Fatalf("Bench.APIEnabled = false, want true")
	}
	if cfg.Bench.APIMaxBatchSize != 123 {
		t.Fatalf("Bench.APIMaxBatchSize = %d, want 123", cfg.Bench.APIMaxBatchSize)
	}
	if cfg.Bench.APIMaxPayloadBytes != 456789 {
		t.Fatalf("Bench.APIMaxPayloadBytes = %d, want 456789", cfg.Bench.APIMaxPayloadBytes)
	}
	if !cfg.Observability.MetricsEnabled {
		t.Fatalf("Observability.MetricsEnabled = false, want true")
	}
	if !cfg.Observability.Prometheus.Enabled {
		t.Fatalf("Observability.Prometheus.Enabled = false, want true")
	}
	if cfg.Observability.Prometheus.BinaryPath != "/opt/prometheus/prometheus" {
		t.Fatalf("Prometheus.BinaryPath = %q", cfg.Observability.Prometheus.BinaryPath)
	}
	if cfg.Observability.Prometheus.ListenAddr != "127.0.0.1:9091" {
		t.Fatalf("Prometheus.ListenAddr = %q", cfg.Observability.Prometheus.ListenAddr)
	}
	if cfg.Observability.Prometheus.DataDir != filepath.Join(dir, "prometheus") {
		t.Fatalf("Prometheus.DataDir = %q", cfg.Observability.Prometheus.DataDir)
	}
	if cfg.Observability.Prometheus.RetentionTime != 48*time.Hour {
		t.Fatalf("Prometheus.RetentionTime = %s, want 48h", cfg.Observability.Prometheus.RetentionTime)
	}
	if cfg.Observability.Prometheus.RetentionSize != "2GB" {
		t.Fatalf("Prometheus.RetentionSize = %q", cfg.Observability.Prometheus.RetentionSize)
	}
	if cfg.Observability.Prometheus.ScrapeInterval != 10*time.Second {
		t.Fatalf("Prometheus.ScrapeInterval = %s, want 10s", cfg.Observability.Prometheus.ScrapeInterval)
	}
	if got := strings.Join(cfg.Observability.Prometheus.ScrapeTargets, ","); got != "127.0.0.1:5042,127.0.0.1:5043" {
		t.Fatalf("Prometheus.ScrapeTargets = %q", got)
	}
	if !cfg.Observability.DebugAPIEnabled {
		t.Fatalf("Observability.DebugAPIEnabled = false, want true")
	}
	if cfg.API.ExternalTCPAddr != "127.0.0.1:5142" || cfg.API.ExternalWSAddr != "ws://127.0.0.1:5242" || cfg.API.ExternalWSSAddr != "wss://127.0.0.1:5342" {
		t.Fatalf("external gateway addrs = %#v", cfg.API)
	}
	if !cfg.Gateway.Transport.Gnet.Multicore {
		t.Fatalf("Gnet.Multicore = false, want true")
	}
	if cfg.Gateway.Transport.Gnet.NumEventLoop != 4 {
		t.Fatalf("Gnet.NumEventLoop = %d, want 4", cfg.Gateway.Transport.Gnet.NumEventLoop)
	}
	if cfg.Gateway.Session.AsyncSendDispatchWorkers != 128 {
		t.Fatalf("AsyncSendDispatchWorkers = %d, want 128", cfg.Gateway.Session.AsyncSendDispatchWorkers)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxWait != 750*time.Microsecond {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want 750us", cfg.Gateway.Session.AsyncSendBatchMaxWait)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxRecords != 64 {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want 64", cfg.Gateway.Session.AsyncSendBatchMaxRecords)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxBytes != 262144 {
		t.Fatalf("AsyncSendBatchMaxBytes = %d, want 262144", cfg.Gateway.Session.AsyncSendBatchMaxBytes)
	}
}

func TestLoadConfigConversationAuthorityEnvOverridesFile(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	lines := append(requiredConfigLines(dir),
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID=4096",
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS=100000",
		"WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX=1000",
		"WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT=3s",
		"WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL=1s",
		"WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS=512",
		"WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS=512",
		"WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY=16",
	)
	writeConf(t, path, lines...)
	t.Setenv("WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID", "2048")
	t.Setenv("WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS", "50000")
	t.Setenv("WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX", "750")
	t.Setenv("WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT", "2s")
	t.Setenv("WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL", "750ms")
	t.Setenv("WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS", "96")
	t.Setenv("WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS", "128")
	t.Setenv("WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY", "4")

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	assertConversationAuthorityConfig(t, cfg.Conversation, app.ConversationConfig{
		AuthorityCacheMaxRowsPerUID: 2048,
		AuthorityCacheMaxRows:       50000,
		AuthorityListDBWindowMax:    750,
		AuthorityHandoffTimeout:     2 * time.Second,
		AuthorityFlushInterval:      750 * time.Millisecond,
		AuthorityFlushBatchRows:     96,
		AuthorityAdmitBatchRows:     128,
		AuthorityAdmitConcurrency:   4,
	})
}

func TestLoadConfigExplicitDiagnosticsConfigFile(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	lines := append(requiredConfigLines(dir),
		"WK_DIAGNOSTICS_ENABLE=false",
		"WK_DIAGNOSTICS_BUFFER_SIZE=12345",
		"WK_DIAGNOSTICS_SAMPLE_RATE=0.25",
		"WK_DIAGNOSTICS_SLOW_THRESHOLD_MS=250",
		"WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=0.75",
		"WK_DIAGNOSTICS_DEEP_SAMPLE_RATE=0.02",
		"WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS=125",
		"WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH=7",
		`WK_DIAGNOSTICS_DEBUG_MATCHES=[{"uid":"u1","channel_key":"person:u1:u2","client_msg_no":"c1","trace_id":"trace-1","ttl_seconds":60,"sample_rate":1},{"uid":"u2","sample_rate":0.5}]`,
	)
	writeConf(t, path, lines...)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	diagnostics := cfg.Observability.Diagnostics
	if diagnostics.Enabled {
		t.Fatalf("Diagnostics.Enabled = true, want false")
	}
	if diagnostics.BufferSize != 12345 {
		t.Fatalf("Diagnostics.BufferSize = %d, want 12345", diagnostics.BufferSize)
	}
	if diagnostics.SampleRate != 0.25 {
		t.Fatalf("Diagnostics.SampleRate = %v, want 0.25", diagnostics.SampleRate)
	}
	if diagnostics.SlowThreshold != 250*time.Millisecond {
		t.Fatalf("Diagnostics.SlowThreshold = %s, want 250ms", diagnostics.SlowThreshold)
	}
	if diagnostics.ErrorSampleRate != 0.75 {
		t.Fatalf("Diagnostics.ErrorSampleRate = %v, want 0.75", diagnostics.ErrorSampleRate)
	}
	if diagnostics.DeepSampleRate != 0.02 {
		t.Fatalf("Diagnostics.DeepSampleRate = %v, want 0.02", diagnostics.DeepSampleRate)
	}
	if diagnostics.DeepSlowThreshold != 125*time.Millisecond {
		t.Fatalf("Diagnostics.DeepSlowThreshold = %s, want 125ms", diagnostics.DeepSlowThreshold)
	}
	if diagnostics.DeepMaxItemsPerBatch != 7 {
		t.Fatalf("Diagnostics.DeepMaxItemsPerBatch = %d, want 7", diagnostics.DeepMaxItemsPerBatch)
	}
	if len(diagnostics.DebugMatches) != 2 {
		t.Fatalf("Diagnostics.DebugMatches len = %d, want 2: %#v", len(diagnostics.DebugMatches), diagnostics.DebugMatches)
	}
	first := diagnostics.DebugMatches[0]
	if first.UID != "u1" || first.ChannelKey != "person:u1:u2" ||
		first.ClientMsgNo != "c1" || first.TraceID != "trace-1" ||
		first.TTLSeconds != 60 || first.SampleRate != 1 {
		t.Fatalf("Diagnostics.DebugMatches[0] = %#v", first)
	}
	second := diagnostics.DebugMatches[1]
	if second.UID != "u2" || second.SampleRate != 0.5 {
		t.Fatalf("Diagnostics.DebugMatches[1] = %#v", second)
	}
}

func TestLoadConfigStaticMultiNodeCluster(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=2",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-2"),
		"WK_CLUSTER_LISTEN_ADDR=0.0.0.0:7000",
		"WK_CLUSTER_ID=dev-three",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=2",
		"WK_CLUSTER_HASH_SLOT_COUNT=32",
		"WK_CLUSTER_SLOT_REPLICA_N=3",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"wk-node1:7000"},{"id":2,"addr":"wk-node2:7000"},{"id":3,"addr":"wk-node3:7000"}]`,
	)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.Cluster.Control.ClusterID != "dev-three" {
		t.Fatalf("Control.ClusterID = %q, want dev-three", cfg.Cluster.Control.ClusterID)
	}
	if !cfg.Cluster.Control.AllowBootstrap {
		t.Fatal("Control.AllowBootstrap = false, want true for static multi-node bootstrap")
	}
	wantVoters := []struct {
		id   uint64
		addr string
	}{
		{id: 1, addr: "wk-node1:7000"},
		{id: 2, addr: "wk-node2:7000"},
		{id: 3, addr: "wk-node3:7000"},
	}
	if len(cfg.Cluster.Control.Voters) != len(wantVoters) {
		t.Fatalf("Control.Voters len = %d, want %d: %#v", len(cfg.Cluster.Control.Voters), len(wantVoters), cfg.Cluster.Control.Voters)
	}
	for i, want := range wantVoters {
		got := cfg.Cluster.Control.Voters[i]
		if got.NodeID != want.id || got.Addr != want.addr {
			t.Fatalf("Control.Voters[%d] = %#v, want id=%d addr=%q", i, got, want.id, want.addr)
		}
	}
	if cfg.Cluster.Slots.ReplicaCount != 3 {
		t.Fatalf("Slots.ReplicaCount = %d, want 3", cfg.Cluster.Slots.ReplicaCount)
	}
}

func TestLoadConfigDerivesStaticMultiNodeClusterID(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		`WK_CLUSTER_NODES=[{"id":3,"addr":"127.0.0.1:7003"},{"id":1,"addr":"127.0.0.1:7001"},{"id":2,"addr":"127.0.0.1:7002"}]`,
	)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.Cluster.Control.ClusterID != "wk-clusterv2-static-1-2-3" {
		t.Fatalf("Control.ClusterID = %q", cfg.Cluster.Control.ClusterID)
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "file-node"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_GATEWAY_SEND_TIMEOUT=1s",
	)
	t.Setenv("WK_NODE_ID", "2")
	t.Setenv("WK_NODE_DATA_DIR", filepath.Join(dir, "env-node"))
	t.Setenv("WK_CLUSTER_LISTEN_ADDR", "127.0.0.1:7002")
	t.Setenv("WK_CLUSTER_CHANNEL_REACTOR_COUNT", "6")
	t.Setenv("WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS", "11")
	t.Setenv("WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS", "13")
	t.Setenv("WK_CLUSTER_CHANNEL_RPC_WORKERS", "17")
	t.Setenv("WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS", "2")
	t.Setenv("WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT", "750us")
	t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW", "1ms")
	t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS", "32")
	t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS", "512")
	t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES", "262144")
	t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_SHARDS", "8")
	t.Setenv("WK_GATEWAY_GNET_NUM_EVENT_LOOP", "5")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS", "256")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT", "1ms")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS", "96")
	t.Setenv("WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES", "131072")
	t.Setenv("WK_GATEWAY_SEND_TIMEOUT", "2s")
	t.Setenv("WK_PRESENCE_ACTIVATION_TIMEOUT", "1500ms")
	t.Setenv("WK_PRESENCE_TOUCH_FLUSH_INTERVAL", "1500ms")
	t.Setenv("WK_PRESENCE_TOUCH_BATCH_SIZE", "128")
	t.Setenv("WK_PRESENCE_ROUTE_TTL", "3m")
	t.Setenv("WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY", "16")
	t.Setenv("WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD", "700")
	t.Setenv("WK_DELIVERY_ENABLE", "true")
	t.Setenv("WK_CHANNEL_APPEND_SHARD_COUNT", "7")
	t.Setenv("WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE", "9")
	t.Setenv("WK_CHANNEL_APPEND_EFFECT_POOL_SIZE", "15")
	t.Setenv("WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY", "5")
	t.Setenv("WK_DELIVERY_FANOUT_PAGE_SIZE", "64")
	t.Setenv("WK_DELIVERY_PUSH_BATCH_SIZE", "32")
	t.Setenv("WK_DELIVERY_PENDING_ACK_TTL", "10s")
	t.Setenv("WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION", "256")
	t.Setenv("WK_DELIVERY_EVENT_QUEUE_SIZE", "512")
	t.Setenv("WK_LOG_LEVEL", "warn")
	t.Setenv("WK_LOG_DIR", filepath.Join(dir, "env-logs"))
	t.Setenv("WK_LOG_MAX_SIZE", "32")
	t.Setenv("WK_LOG_MAX_AGE", "6")
	t.Setenv("WK_LOG_MAX_BACKUPS", "2")
	t.Setenv("WK_LOG_COMPRESS", "false")
	t.Setenv("WK_LOG_CONSOLE", "false")
	t.Setenv("WK_LOG_FORMAT", "json")
	t.Setenv("WK_API_LISTEN_ADDR", "127.0.0.1:5002")
	t.Setenv("WK_BENCH_API_ENABLE", "true")
	t.Setenv("WK_METRICS_ENABLE", "true")
	t.Setenv("WK_DEBUG_API_ENABLE", "true")
	t.Setenv("WK_DIAGNOSTICS_ENABLE", "false")
	t.Setenv("WK_DIAGNOSTICS_BUFFER_SIZE", "32100")
	t.Setenv("WK_DIAGNOSTICS_SAMPLE_RATE", "0.35")
	t.Setenv("WK_DIAGNOSTICS_SLOW_THRESHOLD_MS", "275")
	t.Setenv("WK_DIAGNOSTICS_ERROR_SAMPLE_RATE", "0.85")
	t.Setenv("WK_DIAGNOSTICS_DEEP_SAMPLE_RATE", "0.02")
	t.Setenv("WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS", "125")
	t.Setenv("WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH", "7")
	t.Setenv("WK_DIAGNOSTICS_DEBUG_MATCHES", `[{"trace_id":"env-trace","ttl_seconds":30,"sample_rate":1}]`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.NodeID != 2 || cfg.Cluster.NodeID != 2 {
		t.Fatalf("NodeID = %d/%d, want 2", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != filepath.Join(dir, "env-node") || cfg.Cluster.DataDir != filepath.Join(dir, "env-node") {
		t.Fatalf("DataDir = %q/%q", cfg.DataDir, cfg.Cluster.DataDir)
	}
	if cfg.Cluster.ListenAddr != "127.0.0.1:7002" {
		t.Fatalf("ListenAddr = %q", cfg.Cluster.ListenAddr)
	}
	if cfg.Cluster.Channel.ReactorCount != 6 {
		t.Fatalf("Channel.ReactorCount = %d, want 6", cfg.Cluster.Channel.ReactorCount)
	}
	if cfg.Cluster.Channel.StoreAppendWorkers != 11 || cfg.Cluster.Channel.StoreApplyWorkers != 13 || cfg.Cluster.Channel.RPCWorkers != 17 {
		t.Fatalf("Channel workers = append:%d apply:%d rpc:%d, want append:11 apply:13 rpc:17", cfg.Cluster.Channel.StoreAppendWorkers, cfg.Cluster.Channel.StoreApplyWorkers, cfg.Cluster.Channel.RPCWorkers)
	}
	if cfg.Cluster.Channel.AppendBatchMaxRecords != 2 {
		t.Fatalf("Channel.AppendBatchMaxRecords = %d, want 2", cfg.Cluster.Channel.AppendBatchMaxRecords)
	}
	if cfg.Cluster.Channel.AppendBatchMaxWait != 750*time.Microsecond {
		t.Fatalf("Channel.AppendBatchMaxWait = %s, want 750us", cfg.Cluster.Channel.AppendBatchMaxWait)
	}
	if cfg.Cluster.Storage.CommitFlushWindow != time.Millisecond {
		t.Fatalf("Storage.CommitFlushWindow = %s, want 1ms", cfg.Cluster.Storage.CommitFlushWindow)
	}
	if cfg.Cluster.Storage.CommitMaxRequests != 32 || cfg.Cluster.Storage.CommitMaxRecords != 512 || cfg.Cluster.Storage.CommitMaxBytes != 262144 || cfg.Cluster.Storage.CommitShards != 8 {
		t.Fatalf("Storage commit env override = requests:%d records:%d bytes:%d shards:%d", cfg.Cluster.Storage.CommitMaxRequests, cfg.Cluster.Storage.CommitMaxRecords, cfg.Cluster.Storage.CommitMaxBytes, cfg.Cluster.Storage.CommitShards)
	}
	if cfg.Gateway.SendTimeout != 2*time.Second {
		t.Fatalf("SendTimeout = %s", cfg.Gateway.SendTimeout)
	}
	if cfg.Presence.ActivationTimeout != 1500*time.Millisecond {
		t.Fatalf("Presence.ActivationTimeout = %s, want 1500ms", cfg.Presence.ActivationTimeout)
	}
	if cfg.Presence.TouchFlushInterval != 1500*time.Millisecond {
		t.Fatalf("Presence.TouchFlushInterval = %s, want 1500ms", cfg.Presence.TouchFlushInterval)
	}
	if cfg.Presence.TouchBatchSize != 128 {
		t.Fatalf("Presence.TouchBatchSize = %d, want 128", cfg.Presence.TouchBatchSize)
	}
	if cfg.Presence.RouteTTL != 3*time.Minute {
		t.Fatalf("Presence.RouteTTL = %s, want 3m", cfg.Presence.RouteTTL)
	}
	if cfg.Conversation.MaxLastMessageConcurrency != 16 {
		t.Fatalf("Conversation env override = %#v", cfg.Conversation)
	}
	if cfg.Channel.LargeGroupSubscriberThreshold != 700 {
		t.Fatalf("Channel.LargeGroupSubscriberThreshold = %d, want 700", cfg.Channel.LargeGroupSubscriberThreshold)
	}
	if cfg.ChannelAppend.AuthorityShardCount != 7 ||
		cfg.ChannelAppend.AdvancePoolSize != 9 ||
		cfg.ChannelAppend.EffectPoolSize != 15 ||
		cfg.ChannelAppend.RecipientAuthorityDispatchConcurrency != 5 {
		t.Fatalf("ChannelAppend env override = %#v", cfg.ChannelAppend)
	}
	if !cfg.Delivery.Enabled || cfg.Delivery.FanoutPageSize != 64 || cfg.Delivery.PushBatchSize != 32 ||
		cfg.Delivery.PendingAckTTL != 10*time.Second || cfg.Delivery.PendingAckMaxPerSession != 256 ||
		cfg.Delivery.EventQueueSize != 512 {
		t.Fatalf("Delivery env override = %#v", cfg.Delivery)
	}
	if cfg.Log.Level != "warn" || cfg.Log.Dir != filepath.Join(dir, "env-logs") ||
		cfg.Log.MaxSize != 32 || cfg.Log.MaxAge != 6 || cfg.Log.MaxBackups != 2 ||
		cfg.Log.Compress || cfg.Log.Console || cfg.Log.Format != "json" {
		t.Fatalf("Log env override = %#v", cfg.Log)
	}
	if cfg.Gateway.Transport.Gnet.NumEventLoop != 5 {
		t.Fatalf("Gnet.NumEventLoop = %d, want 5", cfg.Gateway.Transport.Gnet.NumEventLoop)
	}
	if cfg.Gateway.Session.AsyncSendDispatchWorkers != 256 {
		t.Fatalf("AsyncSendDispatchWorkers = %d, want 256", cfg.Gateway.Session.AsyncSendDispatchWorkers)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxWait != time.Millisecond {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want 1ms", cfg.Gateway.Session.AsyncSendBatchMaxWait)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxRecords != 96 {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want 96", cfg.Gateway.Session.AsyncSendBatchMaxRecords)
	}
	if cfg.Gateway.Session.AsyncSendBatchMaxBytes != 131072 {
		t.Fatalf("AsyncSendBatchMaxBytes = %d, want 131072", cfg.Gateway.Session.AsyncSendBatchMaxBytes)
	}
	if cfg.API.ListenAddr != "127.0.0.1:5002" {
		t.Fatalf("API.ListenAddr = %q", cfg.API.ListenAddr)
	}
	if !cfg.Bench.APIEnabled {
		t.Fatalf("Bench.APIEnabled = false, want true")
	}
	if !cfg.Observability.MetricsEnabled {
		t.Fatalf("Observability.MetricsEnabled = false, want true")
	}
	if !cfg.Observability.DebugAPIEnabled {
		t.Fatalf("Observability.DebugAPIEnabled = false, want true")
	}
	if cfg.Observability.Diagnostics.Enabled {
		t.Fatalf("Diagnostics.Enabled = true, want env false")
	}
	if cfg.Observability.Diagnostics.BufferSize != 32100 {
		t.Fatalf("Diagnostics.BufferSize = %d, want 32100", cfg.Observability.Diagnostics.BufferSize)
	}
	if cfg.Observability.Diagnostics.SampleRate != 0.35 {
		t.Fatalf("Diagnostics.SampleRate = %v, want 0.35", cfg.Observability.Diagnostics.SampleRate)
	}
	if cfg.Observability.Diagnostics.SlowThreshold != 275*time.Millisecond {
		t.Fatalf("Diagnostics.SlowThreshold = %s, want 275ms", cfg.Observability.Diagnostics.SlowThreshold)
	}
	if cfg.Observability.Diagnostics.ErrorSampleRate != 0.85 {
		t.Fatalf("Diagnostics.ErrorSampleRate = %v, want 0.85", cfg.Observability.Diagnostics.ErrorSampleRate)
	}
	if cfg.Observability.Diagnostics.DeepSampleRate != 0.02 {
		t.Fatalf("Diagnostics.DeepSampleRate = %v, want 0.02", cfg.Observability.Diagnostics.DeepSampleRate)
	}
	if cfg.Observability.Diagnostics.DeepSlowThreshold != 125*time.Millisecond {
		t.Fatalf("Diagnostics.DeepSlowThreshold = %s, want 125ms", cfg.Observability.Diagnostics.DeepSlowThreshold)
	}
	if cfg.Observability.Diagnostics.DeepMaxItemsPerBatch != 7 {
		t.Fatalf("Diagnostics.DeepMaxItemsPerBatch = %d, want 7", cfg.Observability.Diagnostics.DeepMaxItemsPerBatch)
	}
	if len(cfg.Observability.Diagnostics.DebugMatches) != 1 ||
		cfg.Observability.Diagnostics.DebugMatches[0].TraceID != "env-trace" ||
		cfg.Observability.Diagnostics.DebugMatches[0].TTLSeconds != 30 ||
		cfg.Observability.Diagnostics.DebugMatches[0].SampleRate != 1 {
		t.Fatalf("Diagnostics.DebugMatches env override = %#v", cfg.Observability.Diagnostics.DebugMatches)
	}
}

func TestLoadConfigJSONListeners(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-test","network":"tcp","address":"127.0.0.1:5101","transport":"gnet","protocol":"wkproto"}]`,
	)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	assertListeners(t, cfg.Gateway.Listeners, []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-test", "127.0.0.1:5101"),
	})
}

func TestLoadConfigRejectsCommitCoordinatorSyncFalseFromEnv(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("WK_CLUSTER_COMMIT_COORDINATOR_SYNC", "false")
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path, requiredConfigLines(dir)...)

	_, err := loadConfig([]string{"-config", path})
	if err == nil {
		t.Fatal("loadConfig() error = nil, want WK_CLUSTER_COMMIT_COORDINATOR_SYNC=false rejected")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_COMMIT_COORDINATOR_SYNC") {
		t.Fatalf("loadConfig() error = %v, want WK_CLUSTER_COMMIT_COORDINATOR_SYNC", err)
	}
}

func TestLoadConfigRejectsCommitCoordinatorSyncFalseInFile(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	lines := append(requiredConfigLines(dir), "WK_CLUSTER_COMMIT_COORDINATOR_SYNC=false")
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path, lines...)

	_, err := loadConfig([]string{"-config", path})
	if err == nil {
		t.Fatal("loadConfig() error = nil, want WK_CLUSTER_COMMIT_COORDINATOR_SYNC=false rejected")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_COMMIT_COORDINATOR_SYNC") {
		t.Fatalf("loadConfig() error = %v, want WK_CLUSTER_COMMIT_COORDINATOR_SYNC", err)
	}
}

func TestConfigExamplesDoNotExposeCommitCoordinatorSync(t *testing.T) {
	files := []string{
		"wukongimv2.conf.example",
		"wukongimv2-node1.conf.example",
		"wukongimv2-node2.conf.example",
		"wukongimv2-node3.conf.example",
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", file, err)
			}
			if strings.Contains(string(content), "WK_CLUSTER_COMMIT_COORDINATOR_SYNC") {
				t.Fatalf("%s exposes WK_CLUSTER_COMMIT_COORDINATOR_SYNC", file)
			}
		})
	}
}

func TestLoadConfigExampleFile(t *testing.T) {
	unsetLoadConfigEnv(t)

	cfg, err := loadConfig([]string{"-config", "wukongimv2.conf.example"})
	if err != nil {
		t.Fatalf("loadConfig(example) error = %v", err)
	}

	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.Cluster.ListenAddr != "127.0.0.1:7001" {
		t.Fatalf("Cluster.ListenAddr = %q", cfg.Cluster.ListenAddr)
	}
	if cfg.Cluster.Control.ClusterID != "wukongimv2-single" {
		t.Fatalf("Control.ClusterID = %q", cfg.Cluster.Control.ClusterID)
	}
	if cfg.API.ListenAddr != "127.0.0.1:5001" {
		t.Fatalf("API.ListenAddr = %q", cfg.API.ListenAddr)
	}
	if !cfg.Bench.APIEnabled {
		t.Fatalf("Bench.APIEnabled = false, want true")
	}
	if !cfg.Observability.MetricsEnabled {
		t.Fatalf("Observability.MetricsEnabled = false, want true")
	}
	if cfg.Observability.DebugAPIEnabled {
		t.Fatalf("Observability.DebugAPIEnabled = true, want false")
	}
	assertExampleDiagnostics(t, cfg.Observability.Diagnostics)
}

func TestLoadConfigMultiNodeExampleFiles(t *testing.T) {
	unsetLoadConfigEnv(t)

	files := []string{
		"wukongimv2-node1.conf.example",
		"wukongimv2-node2.conf.example",
		"wukongimv2-node3.conf.example",
	}
	for i, file := range files {
		t.Run(file, func(t *testing.T) {
			cfg, err := loadConfig([]string{"-config", file})
			if err != nil {
				t.Fatalf("loadConfig(%s) error = %v", file, err)
			}
			wantNodeID := uint64(i + 1)
			if cfg.NodeID != wantNodeID || cfg.Cluster.NodeID != wantNodeID {
				t.Fatalf("NodeID = %d/%d, want %d", cfg.NodeID, cfg.Cluster.NodeID, wantNodeID)
			}
			if cfg.Cluster.Control.ClusterID != "wukongimv2-dev-three" {
				t.Fatalf("Control.ClusterID = %q", cfg.Cluster.Control.ClusterID)
			}
			if len(cfg.Cluster.Control.Voters) != 3 {
				t.Fatalf("Control.Voters len = %d, want 3", len(cfg.Cluster.Control.Voters))
			}
			if cfg.Cluster.Slots.ReplicaCount != 3 {
				t.Fatalf("Slots.ReplicaCount = %d, want 3", cfg.Cluster.Slots.ReplicaCount)
			}
			if len(cfg.Gateway.Listeners) != 2 {
				t.Fatalf("Gateway.Listeners len = %d, want 2", len(cfg.Gateway.Listeners))
			}
			if cfg.Observability.DebugAPIEnabled {
				t.Fatalf("Observability.DebugAPIEnabled = true, want false")
			}
			assertExampleDiagnostics(t, cfg.Observability.Diagnostics)
		})
	}
}

func TestLoadConfigScriptFiles(t *testing.T) {
	unsetLoadConfigEnv(t)

	files := []string{
		filepath.Join("..", "..", "scripts", "wukongimv2", "wukongimv2.conf"),
		filepath.Join("..", "..", "scripts", "wukongimv2", "wukongimv2-node1.conf"),
		filepath.Join("..", "..", "scripts", "wukongimv2", "wukongimv2-node2.conf"),
		filepath.Join("..", "..", "scripts", "wukongimv2", "wukongimv2-node3.conf"),
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			cfg, err := loadConfig([]string{"-config", file})
			if err != nil {
				t.Fatalf("loadConfig(%s) error = %v", file, err)
			}
			assertExampleDiagnostics(t, cfg.Observability.Diagnostics)
		})
	}
}

func TestLoadConfigRejectsBadValues(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantKey string
	}{
		{name: "node id", line: "WK_NODE_ID=bad", wantKey: "WK_NODE_ID"},
		{name: "slot count", line: "WK_CLUSTER_INITIAL_SLOT_COUNT=-1", wantKey: "WK_CLUSTER_INITIAL_SLOT_COUNT"},
		{name: "channel reactor count", line: "WK_CLUSTER_CHANNEL_REACTOR_COUNT=many", wantKey: "WK_CLUSTER_CHANNEL_REACTOR_COUNT"},
		{name: "store append workers", line: "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=many", wantKey: "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS"},
		{name: "store append workers negative", line: "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=-1", wantKey: "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS"},
		{name: "store apply workers", line: "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=many", wantKey: "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS"},
		{name: "store apply workers negative", line: "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=-1", wantKey: "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS"},
		{name: "rpc workers", line: "WK_CLUSTER_CHANNEL_RPC_WORKERS=many", wantKey: "WK_CLUSTER_CHANNEL_RPC_WORKERS"},
		{name: "rpc workers negative", line: "WK_CLUSTER_CHANNEL_RPC_WORKERS=-1", wantKey: "WK_CLUSTER_CHANNEL_RPC_WORKERS"},
		{name: "max channels", line: "WK_CLUSTER_MAX_CHANNELS=many", wantKey: "WK_CLUSTER_MAX_CHANNELS"},
		{name: "append batch max records", line: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=many", wantKey: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS"},
		{name: "append batch max wait", line: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=soon", wantKey: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT"},
		{name: "follower recovery probe interval", line: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL=soon", wantKey: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL"},
		{name: "follower recovery probe jitter", line: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER=soon", wantKey: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER"},
		{name: "commit coordinator sync", line: "WK_CLUSTER_COMMIT_COORDINATOR_SYNC=maybe", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_SYNC"},
		{name: "commit coordinator flush window", line: "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=soon", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW"},
		{name: "commit coordinator flush window zero", line: "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=0s", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW"},
		{name: "commit coordinator max requests", line: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=many", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS"},
		{name: "commit coordinator max requests negative", line: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=-1", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS"},
		{name: "commit coordinator max records", line: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=many", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS"},
		{name: "commit coordinator max records negative", line: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=-1", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS"},
		{name: "commit coordinator max bytes", line: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=many", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES"},
		{name: "commit coordinator max bytes negative", line: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=-1", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES"},
		{name: "commit coordinator shards", line: "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=many", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS"},
		{name: "commit coordinator shards negative", line: "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=-1", wantKey: "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS"},
		{name: "cluster nodes json", line: "WK_CLUSTER_NODES=not-json", wantKey: "WK_CLUSTER_NODES"},
		{name: "listener json", line: "WK_GATEWAY_LISTENERS=not-json", wantKey: "WK_GATEWAY_LISTENERS"},
		{name: "gnet multicore", line: "WK_GATEWAY_GNET_MULTICORE=maybe", wantKey: "WK_GATEWAY_GNET_MULTICORE"},
		{name: "gnet event loop", line: "WK_GATEWAY_GNET_NUM_EVENT_LOOP=many", wantKey: "WK_GATEWAY_GNET_NUM_EVENT_LOOP"},
		{name: "async dispatch workers", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS=many", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS"},
		{name: "async batch wait", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT=soon", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT"},
		{name: "async batch records", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=many", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS"},
		{name: "async batch bytes", line: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES=large", wantKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES"},
		{name: "send timeout", line: "WK_GATEWAY_SEND_TIMEOUT=soon", wantKey: "WK_GATEWAY_SEND_TIMEOUT"},
		{name: "presence activation timeout", line: "WK_PRESENCE_ACTIVATION_TIMEOUT=soon", wantKey: "WK_PRESENCE_ACTIVATION_TIMEOUT"},
		{name: "presence touch flush interval", line: "WK_PRESENCE_TOUCH_FLUSH_INTERVAL=soon", wantKey: "WK_PRESENCE_TOUCH_FLUSH_INTERVAL"},
		{name: "presence touch flush interval negative", line: "WK_PRESENCE_TOUCH_FLUSH_INTERVAL=-1s", wantKey: "WK_PRESENCE_TOUCH_FLUSH_INTERVAL"},
		{name: "presence touch batch size", line: "WK_PRESENCE_TOUCH_BATCH_SIZE=many", wantKey: "WK_PRESENCE_TOUCH_BATCH_SIZE"},
		{name: "presence touch batch size negative", line: "WK_PRESENCE_TOUCH_BATCH_SIZE=-1", wantKey: "WK_PRESENCE_TOUCH_BATCH_SIZE"},
		{name: "presence route ttl", line: "WK_PRESENCE_ROUTE_TTL=soon", wantKey: "WK_PRESENCE_ROUTE_TTL"},
		{name: "presence route ttl negative", line: "WK_PRESENCE_ROUTE_TTL=-1s", wantKey: "WK_PRESENCE_ROUTE_TTL"},
		{name: "conversation max last message concurrency", line: "WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY=many", wantKey: "WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY"},
		{name: "conversation max last message concurrency negative", line: "WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY=-1", wantKey: "WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY"},
		{name: "conversation authority cache max rows per uid", line: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID=many", wantKey: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID"},
		{name: "conversation authority cache max rows per uid zero", line: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID=0", wantKey: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID"},
		{name: "conversation authority cache max rows", line: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS=many", wantKey: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS"},
		{name: "conversation authority cache max rows zero", line: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS=0", wantKey: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS"},
		{name: "conversation authority list db window max", line: "WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX=many", wantKey: "WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX"},
		{name: "conversation authority list db window max zero", line: "WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX=0", wantKey: "WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX"},
		{name: "conversation authority handoff timeout", line: "WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT=soon", wantKey: "WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT"},
		{name: "conversation authority handoff timeout zero", line: "WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT=0s", wantKey: "WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT"},
		{name: "conversation authority flush interval", line: "WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL=soon", wantKey: "WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL"},
		{name: "conversation authority flush interval zero", line: "WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL=0s", wantKey: "WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL"},
		{name: "conversation authority flush batch rows", line: "WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS=many", wantKey: "WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS"},
		{name: "conversation authority flush batch rows zero", line: "WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS=0", wantKey: "WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS"},
		{name: "conversation authority admit batch rows", line: "WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS=many", wantKey: "WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS"},
		{name: "conversation authority admit batch rows zero", line: "WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS=0", wantKey: "WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS"},
		{name: "conversation authority admit concurrency", line: "WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY=many", wantKey: "WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY"},
		{name: "conversation authority admit concurrency zero", line: "WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY=0", wantKey: "WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY"},
		{name: "channel large group subscriber threshold", line: "WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD=many", wantKey: "WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD"},
		{name: "channel large group subscriber threshold negative", line: "WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD=-1", wantKey: "WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD"},
		{name: "delivery enable", line: "WK_DELIVERY_ENABLE=maybe", wantKey: "WK_DELIVERY_ENABLE"},
		{name: "channel append shard count", line: "WK_CHANNEL_APPEND_SHARD_COUNT=many", wantKey: "WK_CHANNEL_APPEND_SHARD_COUNT"},
		{name: "channel append shard count negative", line: "WK_CHANNEL_APPEND_SHARD_COUNT=-1", wantKey: "WK_CHANNEL_APPEND_SHARD_COUNT"},
		{name: "channel append advance pool size", line: "WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE=many", wantKey: "WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE"},
		{name: "channel append advance pool size negative", line: "WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE=-1", wantKey: "WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE"},
		{name: "channel append effect pool size", line: "WK_CHANNEL_APPEND_EFFECT_POOL_SIZE=many", wantKey: "WK_CHANNEL_APPEND_EFFECT_POOL_SIZE"},
		{name: "channel append effect pool size negative", line: "WK_CHANNEL_APPEND_EFFECT_POOL_SIZE=-1", wantKey: "WK_CHANNEL_APPEND_EFFECT_POOL_SIZE"},
		{name: "channel append recipient authority dispatch concurrency", line: "WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY=many", wantKey: "WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY"},
		{name: "channel append recipient authority dispatch concurrency negative", line: "WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY=-1", wantKey: "WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY"},
		{name: "delivery fanout page size", line: "WK_DELIVERY_FANOUT_PAGE_SIZE=many", wantKey: "WK_DELIVERY_FANOUT_PAGE_SIZE"},
		{name: "delivery fanout page size negative", line: "WK_DELIVERY_FANOUT_PAGE_SIZE=-1", wantKey: "WK_DELIVERY_FANOUT_PAGE_SIZE"},
		{name: "delivery push batch size", line: "WK_DELIVERY_PUSH_BATCH_SIZE=many", wantKey: "WK_DELIVERY_PUSH_BATCH_SIZE"},
		{name: "delivery push batch size negative", line: "WK_DELIVERY_PUSH_BATCH_SIZE=-1", wantKey: "WK_DELIVERY_PUSH_BATCH_SIZE"},
		{name: "delivery pending ack ttl", line: "WK_DELIVERY_PENDING_ACK_TTL=soon", wantKey: "WK_DELIVERY_PENDING_ACK_TTL"},
		{name: "delivery pending ack ttl negative", line: "WK_DELIVERY_PENDING_ACK_TTL=-1s", wantKey: "WK_DELIVERY_PENDING_ACK_TTL"},
		{name: "delivery pending ack max per session", line: "WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION=many", wantKey: "WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION"},
		{name: "delivery pending ack max per session negative", line: "WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION=-1", wantKey: "WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION"},
		{name: "delivery event queue size", line: "WK_DELIVERY_EVENT_QUEUE_SIZE=many", wantKey: "WK_DELIVERY_EVENT_QUEUE_SIZE"},
		{name: "delivery event queue size negative", line: "WK_DELIVERY_EVENT_QUEUE_SIZE=-1", wantKey: "WK_DELIVERY_EVENT_QUEUE_SIZE"},
		{name: "log max size", line: "WK_LOG_MAX_SIZE=many", wantKey: "WK_LOG_MAX_SIZE"},
		{name: "log max age", line: "WK_LOG_MAX_AGE=many", wantKey: "WK_LOG_MAX_AGE"},
		{name: "log max backups", line: "WK_LOG_MAX_BACKUPS=many", wantKey: "WK_LOG_MAX_BACKUPS"},
		{name: "log compress", line: "WK_LOG_COMPRESS=maybe", wantKey: "WK_LOG_COMPRESS"},
		{name: "log console", line: "WK_LOG_CONSOLE=maybe", wantKey: "WK_LOG_CONSOLE"},
		{name: "bench api enable", line: "WK_BENCH_API_ENABLE=maybe", wantKey: "WK_BENCH_API_ENABLE"},
		{name: "bench api max batch size", line: "WK_BENCH_API_MAX_BATCH_SIZE=many", wantKey: "WK_BENCH_API_MAX_BATCH_SIZE"},
		{name: "bench api max payload bytes", line: "WK_BENCH_API_MAX_PAYLOAD_BYTES=large", wantKey: "WK_BENCH_API_MAX_PAYLOAD_BYTES"},
		{name: "metrics enable", line: "WK_METRICS_ENABLE=maybe", wantKey: "WK_METRICS_ENABLE"},
		{name: "prometheus enable", line: "WK_PROMETHEUS_ENABLE=maybe", wantKey: "WK_PROMETHEUS_ENABLE"},
		{name: "prometheus retention time", line: "WK_PROMETHEUS_RETENTION_TIME=forever", wantKey: "WK_PROMETHEUS_RETENTION_TIME"},
		{name: "prometheus retention time negative", line: "WK_PROMETHEUS_RETENTION_TIME=-1s", wantKey: "WK_PROMETHEUS_RETENTION_TIME"},
		{name: "prometheus scrape interval", line: "WK_PROMETHEUS_SCRAPE_INTERVAL=often", wantKey: "WK_PROMETHEUS_SCRAPE_INTERVAL"},
		{name: "prometheus scrape interval negative", line: "WK_PROMETHEUS_SCRAPE_INTERVAL=-1s", wantKey: "WK_PROMETHEUS_SCRAPE_INTERVAL"},
		{name: "prometheus scrape targets", line: "WK_PROMETHEUS_SCRAPE_TARGETS=not-json", wantKey: "WK_PROMETHEUS_SCRAPE_TARGETS"},
		{name: "debug api enable", line: "WK_DEBUG_API_ENABLE=maybe", wantKey: "WK_DEBUG_API_ENABLE"},
		{name: "diagnostics enable", line: "WK_DIAGNOSTICS_ENABLE=maybe", wantKey: "WK_DIAGNOSTICS_ENABLE"},
		{name: "diagnostics buffer size", line: "WK_DIAGNOSTICS_BUFFER_SIZE=many", wantKey: "WK_DIAGNOSTICS_BUFFER_SIZE"},
		{name: "diagnostics buffer size negative", line: "WK_DIAGNOSTICS_BUFFER_SIZE=-1", wantKey: "WK_DIAGNOSTICS_BUFFER_SIZE"},
		{name: "diagnostics sample rate", line: "WK_DIAGNOSTICS_SAMPLE_RATE=often", wantKey: "WK_DIAGNOSTICS_SAMPLE_RATE"},
		{name: "diagnostics sample rate negative", line: "WK_DIAGNOSTICS_SAMPLE_RATE=-0.1", wantKey: "WK_DIAGNOSTICS_SAMPLE_RATE"},
		{name: "diagnostics sample rate nan", line: "WK_DIAGNOSTICS_SAMPLE_RATE=NaN", wantKey: "WK_DIAGNOSTICS_SAMPLE_RATE"},
		{name: "diagnostics sample rate inf", line: "WK_DIAGNOSTICS_SAMPLE_RATE=+Inf", wantKey: "WK_DIAGNOSTICS_SAMPLE_RATE"},
		{name: "diagnostics slow threshold", line: "WK_DIAGNOSTICS_SLOW_THRESHOLD_MS=slow", wantKey: "WK_DIAGNOSTICS_SLOW_THRESHOLD_MS"},
		{name: "diagnostics slow threshold negative", line: "WK_DIAGNOSTICS_SLOW_THRESHOLD_MS=-1", wantKey: "WK_DIAGNOSTICS_SLOW_THRESHOLD_MS"},
		{name: "diagnostics error sample rate", line: "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=always", wantKey: "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE"},
		{name: "diagnostics error sample rate negative", line: "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=-0.1", wantKey: "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE"},
		{name: "diagnostics error sample rate nan", line: "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=NaN", wantKey: "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE"},
		{name: "diagnostics deep sample rate", line: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE=often", wantKey: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE"},
		{name: "diagnostics deep sample rate high", line: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE=1.5", wantKey: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE"},
		{name: "diagnostics deep slow threshold negative", line: "WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS=-1", wantKey: "WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS"},
		{name: "diagnostics deep max items negative", line: "WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH=-1", wantKey: "WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH"},
		{name: "diagnostics debug matches", line: "WK_DIAGNOSTICS_DEBUG_MATCHES=not-json", wantKey: "WK_DIAGNOSTICS_DEBUG_MATCHES"},
		{name: "diagnostics debug match sample rate negative", line: `WK_DIAGNOSTICS_DEBUG_MATCHES=[{"trace_id":"bad","ttl_seconds":1,"sample_rate":-0.1}]`, wantKey: "WK_DIAGNOSTICS_DEBUG_MATCHES"},
		{name: "diagnostics debug match ttl negative", line: `WK_DIAGNOSTICS_DEBUG_MATCHES=[{"trace_id":"bad","ttl_seconds":-1,"sample_rate":1}]`, wantKey: "WK_DIAGNOSTICS_DEBUG_MATCHES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			lines := requiredConfigLines(dir)
			key := strings.SplitN(tc.line, "=", 2)[0]
			replaced := false
			for i, line := range lines {
				if strings.HasPrefix(line, key+"=") {
					lines[i] = tc.line
					replaced = true
					break
				}
			}
			if !replaced {
				lines = append(lines, tc.line)
			}
			writeConf(t, path, lines...)

			_, err := loadConfig([]string{"-config", path})
			if err == nil {
				t.Fatalf("loadConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("loadConfig() error = %v, want key %s", err, tc.wantKey)
			}
		})
	}
}

func TestLoadConfigRejectsNegativeChannelV2Limits(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		key  string
	}{
		{name: "max channels", line: "WK_CLUSTER_MAX_CHANNELS=-1", key: "WK_CLUSTER_MAX_CHANNELS"},
		{name: "append batch max records", line: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=-1", key: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS"},
		{name: "append batch max wait", line: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=-1ms", key: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			writeConf(t, path,
				"WK_NODE_ID=1",
				"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
				"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
				tt.line,
			)
			_, err := loadConfig([]string{"-config", path})
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("loadConfig() error = %v, want %s validation", err, tt.key)
			}
		})
	}
}

func TestLoadConfigRejectsNegativeChannelV2RecoveryProbeDurations(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantKey string
	}{
		{name: "interval", line: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL=-1s", wantKey: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL"},
		{name: "jitter", line: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER=-1s", wantKey: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			lines := append(requiredConfigLines(dir), tc.line)
			writeConf(t, path, lines...)
			_, err := loadConfig([]string{"-config", path})
			if err == nil || !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("loadConfig() error = %v, want %s validation", err, tc.wantKey)
			}
		})
	}
}

func TestLoadConfigRejectsMissingRequiredValues(t *testing.T) {
	cases := []struct {
		name    string
		wantKey string
	}{
		{name: "node id", wantKey: "WK_NODE_ID"},
		{name: "data dir", wantKey: "WK_NODE_DATA_DIR"},
		{name: "cluster listen addr", wantKey: "WK_CLUSTER_LISTEN_ADDR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			lines := requiredConfigLines(dir)
			filtered := lines[:0]
			for _, line := range lines {
				if !strings.HasPrefix(line, tc.wantKey+"=") {
					filtered = append(filtered, line)
				}
			}
			writeConf(t, path, filtered...)

			_, err := loadConfig([]string{"-config", path})
			if err == nil {
				t.Fatalf("loadConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("loadConfig() error = %v, want key %s", err, tc.wantKey)
			}
		})
	}
}

func assertExampleDiagnostics(t *testing.T, diagnostics app.DiagnosticsConfig) {
	t.Helper()
	if !diagnostics.Enabled {
		t.Fatalf("Diagnostics.Enabled = false, want true")
	}
	if diagnostics.BufferSize != 50000 {
		t.Fatalf("Diagnostics.BufferSize = %d, want 50000", diagnostics.BufferSize)
	}
	if diagnostics.SampleRate != 0.01 {
		t.Fatalf("Diagnostics.SampleRate = %v, want 0.01", diagnostics.SampleRate)
	}
	if diagnostics.SlowThreshold != 500*time.Millisecond {
		t.Fatalf("Diagnostics.SlowThreshold = %s, want 500ms", diagnostics.SlowThreshold)
	}
	if diagnostics.ErrorSampleRate != 1 {
		t.Fatalf("Diagnostics.ErrorSampleRate = %v, want 1", diagnostics.ErrorSampleRate)
	}
	if diagnostics.DeepSampleRate != 0 {
		t.Fatalf("Diagnostics.DeepSampleRate = %v, want 0", diagnostics.DeepSampleRate)
	}
	if diagnostics.DeepSlowThreshold != 500*time.Millisecond {
		t.Fatalf("Diagnostics.DeepSlowThreshold = %s, want 500ms", diagnostics.DeepSlowThreshold)
	}
	if diagnostics.DeepMaxItemsPerBatch != 16 {
		t.Fatalf("Diagnostics.DeepMaxItemsPerBatch = %d, want 16", diagnostics.DeepMaxItemsPerBatch)
	}
	if len(diagnostics.DebugMatches) != 0 {
		t.Fatalf("Diagnostics.DebugMatches len = %d, want 0", len(diagnostics.DebugMatches))
	}
}

func assertConversationAuthorityConfig(t *testing.T, got, want app.ConversationConfig) {
	t.Helper()
	if got.AuthorityCacheMaxRowsPerUID != want.AuthorityCacheMaxRowsPerUID ||
		got.AuthorityCacheMaxRows != want.AuthorityCacheMaxRows ||
		got.AuthorityListDBWindowMax != want.AuthorityListDBWindowMax ||
		got.AuthorityHandoffTimeout != want.AuthorityHandoffTimeout ||
		got.AuthorityFlushInterval != want.AuthorityFlushInterval ||
		got.AuthorityFlushBatchRows != want.AuthorityFlushBatchRows ||
		got.AuthorityAdmitBatchRows != want.AuthorityAdmitBatchRows ||
		got.AuthorityAdmitConcurrency != want.AuthorityAdmitConcurrency {
		t.Fatalf("conversation authority config = %#v, want %#v", got, want)
	}
}

func assertListeners(t *testing.T, got, want []gateway.ListenerOptions) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("listeners len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listener[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func requiredConfigLines(dir string) []string {
	return []string{
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=" + filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
	}
}

func writeConf(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(): %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func unsetLoadConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range supportedConfigKeys {
		old, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%s): %v", key, err)
		}
		t.Cleanup(func() {
			if ok {
				if err := os.Setenv(key, old); err != nil {
					t.Fatalf("restore env %s: %v", key, err)
				}
			} else if err := os.Unsetenv(key); err != nil {
				t.Fatalf("restore unset env %s: %v", key, err)
			}
		})
	}
}
