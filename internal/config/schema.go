package config

type fieldKind string

const (
	kindString     fieldKind = "string"
	kindBool       fieldKind = "bool"
	kindInt        fieldKind = "int"
	kindUint64     fieldKind = "uint64"
	kindUint32     fieldKind = "uint32"
	kindUint16     fieldKind = "uint16"
	kindFloat      fieldKind = "float"
	kindDuration   fieldKind = "duration"
	kindStringList fieldKind = "string_list"
	kindObjectList fieldKind = "object_list"
)

// fieldSpec binds one public TOML path to its canonical WK_* environment key.
type fieldSpec struct {
	// TOMLPath is the dotted path accepted in wukongim.toml.
	TOMLPath string
	// EnvKey is the canonical WK_* override key.
	EnvKey string
	// Kind controls TOML value coercion before the legacy builder parses strings.
	Kind fieldKind
	// Group is used by redacted startup config snapshots.
	Group string
	// Label is a short human-readable field name for manager views.
	Label string
	// Description is the longer operator-facing field help text.
	Description string
	// Required marks keys that must be supplied by TOML or environment.
	Required bool
	// Nullable allows an explicitly empty value for optional fields.
	Nullable bool
	// Sensitive marks values that must be redacted in snapshots.
	Sensitive bool
	// DiagnosticSensitive marks values redacted from diagnostics without changing snapshot sensitivity.
	DiagnosticSensitive bool
}

// SchemaField describes one public startup config field.
type SchemaField struct {
	// TOMLPath is the dotted path accepted in wukongim.toml.
	TOMLPath string
	// EnvKey is the canonical WK_* override key.
	EnvKey string
	// Kind identifies the TOML value shape expected for this field.
	Kind string
	// Sensitive reports whether startup config snapshots redact this field.
	Sensitive bool
	// DiagnosticSensitive reports whether diagnostic artifacts redact this field.
	DiagnosticSensitive bool
}

// SchemaFields returns the public startup config schema.
func SchemaFields() []SchemaField {
	out := make([]SchemaField, 0, len(schemaFields))
	for _, field := range schemaFields {
		out = append(out, SchemaField{
			TOMLPath:            field.TOMLPath,
			EnvKey:              field.EnvKey,
			Kind:                string(field.Kind),
			Sensitive:           field.Sensitive,
			DiagnosticSensitive: field.Sensitive || field.DiagnosticSensitive,
		})
	}
	return out
}

var requiredConfigKeys = []string{
	"WK_NODE_ID",
	"WK_NODE_DATA_DIR",
	"WK_CLUSTER_LISTEN_ADDR",
}

var removedConfigKeyReplacements = map[string]string{
	"WK_CLUSTER_GROUP_COUNT":                 "WK_CLUSTER_INITIAL_SLOT_COUNT",
	"WK_CLUSTER_GROUP_REPLICA_N":             "WK_CLUSTER_SLOT_REPLICA_N",
	"WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED": "WK_CHANNEL_MIGRATION_*",
}

var schemaFields = []fieldSpec{
	{TOMLPath: "node.id", EnvKey: "WK_NODE_ID", Kind: kindUint64, Group: "node", Label: "Node ID", Required: true},
	{TOMLPath: "node.data_dir", EnvKey: "WK_NODE_DATA_DIR", Kind: kindString, Group: "node", Label: "Data directory", Required: true},

	{TOMLPath: "cluster.listen_addr", EnvKey: "WK_CLUSTER_LISTEN_ADDR", Kind: kindString, Group: "cluster", Label: "Cluster listen address", Required: true},
	{TOMLPath: "cluster.id", EnvKey: "WK_CLUSTER_ID", Kind: kindString, Group: "cluster", Label: "Cluster ID"},
	{TOMLPath: "cluster.seeds", EnvKey: "WK_CLUSTER_SEEDS", Kind: kindStringList, Group: "cluster", Label: "Seed addresses"},
	{TOMLPath: "cluster.advertise_addr", EnvKey: "WK_CLUSTER_ADVERTISE_ADDR", Kind: kindString, Group: "cluster", Label: "Cluster advertise address"},
	{TOMLPath: "cluster.join_token", EnvKey: "WK_CLUSTER_JOIN_TOKEN", Kind: kindString, Group: "cluster", Label: "Join token", Sensitive: true},
	{TOMLPath: "cluster.nodes", EnvKey: "WK_CLUSTER_NODES", Kind: kindObjectList, Group: "cluster", Label: "Static cluster nodes"},
	{TOMLPath: "cluster.initial_slot_count", EnvKey: "WK_CLUSTER_INITIAL_SLOT_COUNT", Kind: kindUint32, Group: "cluster", Label: "Initial slot count"},
	{TOMLPath: "cluster.hash_slot_count", EnvKey: "WK_CLUSTER_HASH_SLOT_COUNT", Kind: kindUint16, Group: "cluster", Label: "Hash slot count"},
	{TOMLPath: "cluster.slot_replica_n", EnvKey: "WK_CLUSTER_SLOT_REPLICA_N", Kind: kindUint16, Group: "cluster", Label: "Slot replica count"},
	{TOMLPath: "cluster.channel_replica_n", EnvKey: "WK_CLUSTER_CHANNEL_REPLICA_N", Kind: kindUint16, Group: "cluster", Label: "Channel replica count"},
	{TOMLPath: "cluster.slot_tick_interval", EnvKey: "WK_CLUSTER_SLOT_TICK_INTERVAL", Kind: kindDuration, Group: "cluster", Label: "Slot tick interval"},
	{TOMLPath: "cluster.slot_election_tick", EnvKey: "WK_CLUSTER_SLOT_ELECTION_TICK", Kind: kindInt, Group: "cluster", Label: "Slot election tick"},
	{TOMLPath: "cluster.slot_heartbeat_tick", EnvKey: "WK_CLUSTER_SLOT_HEARTBEAT_TICK", Kind: kindInt, Group: "cluster", Label: "Slot heartbeat tick"},
	{TOMLPath: "cluster.slot_log_compaction_enabled", EnvKey: "WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED", Kind: kindBool, Group: "cluster", Label: "Slot log compaction"},
	{TOMLPath: "cluster.slot_log_compaction_trigger_entries", EnvKey: "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES", Kind: kindUint64, Group: "cluster", Label: "Slot log compaction trigger entries"},
	{TOMLPath: "cluster.slot_log_compaction_check_interval", EnvKey: "WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL", Kind: kindDuration, Group: "cluster", Label: "Slot log compaction check interval"},
	{TOMLPath: "cluster.channel_reactor_count", EnvKey: "WK_CLUSTER_CHANNEL_REACTOR_COUNT", Kind: kindInt, Group: "cluster", Label: "Channel reactor count"},
	{TOMLPath: "cluster.channel_store_append_workers", EnvKey: "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS", Kind: kindInt, Group: "cluster", Label: "Channel store append workers"},
	{TOMLPath: "cluster.channel_store_append_batch_max_wait", EnvKey: "WK_CLUSTER_CHANNEL_STORE_APPEND_BATCH_MAX_WAIT", Kind: kindDuration, Group: "cluster", Label: "Channel store append batch max wait"},
	{TOMLPath: "cluster.channel_store_apply_workers", EnvKey: "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS", Kind: kindInt, Group: "cluster", Label: "Channel store apply workers"},
	{TOMLPath: "cluster.channel_rpc_workers", EnvKey: "WK_CLUSTER_CHANNEL_RPC_WORKERS", Kind: kindInt, Group: "cluster", Label: "Channel RPC workers"},
	{TOMLPath: "cluster.max_channels", EnvKey: "WK_CLUSTER_MAX_CHANNELS", Kind: kindInt, Group: "cluster", Label: "Max channels"},
	{TOMLPath: "cluster.channel_append_batch_max_records", EnvKey: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS", Kind: kindInt, Group: "cluster", Label: "Channel append batch max records"},
	{TOMLPath: "cluster.channel_append_batch_max_wait", EnvKey: "WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT", Kind: kindDuration, Group: "cluster", Label: "Channel append batch max wait"},
	{TOMLPath: "cluster.channel_append_batch_adaptive_flush", EnvKey: "WK_CLUSTER_CHANNEL_APPEND_BATCH_ADAPTIVE_FLUSH", Kind: kindBool, Group: "cluster", Label: "Channel append adaptive flush"},
	{TOMLPath: "cluster.channel_append_batch_cold_max_wait", EnvKey: "WK_CLUSTER_CHANNEL_APPEND_BATCH_COLD_MAX_WAIT", Kind: kindDuration, Group: "cluster", Label: "Channel append cold max wait"},
	{TOMLPath: "cluster.channel_follower_recovery_probe_interval", EnvKey: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL", Kind: kindDuration, Group: "cluster", Label: "Channel follower recovery probe interval"},
	{TOMLPath: "cluster.channel_follower_recovery_probe_jitter", EnvKey: "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER", Kind: kindDuration, Group: "cluster", Label: "Channel follower recovery probe jitter"},
	{TOMLPath: "cluster.node_health_report_interval", EnvKey: "WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL", Kind: kindDuration, Group: "cluster", Label: "Node health report interval"},
	{TOMLPath: "cluster.node_health_report_ttl", EnvKey: "WK_CLUSTER_NODE_HEALTH_REPORT_TTL", Kind: kindDuration, Group: "cluster", Label: "Node health report TTL"},
	{TOMLPath: "cluster.commit_coordinator_sync", EnvKey: "WK_CLUSTER_COMMIT_COORDINATOR_SYNC", Kind: kindBool, Group: "cluster", Label: "Commit coordinator sync"},
	{TOMLPath: "cluster.commit_coordinator_flush_window", EnvKey: "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW", Kind: kindDuration, Group: "cluster", Label: "Commit coordinator flush window"},
	{TOMLPath: "cluster.commit_coordinator_max_requests", EnvKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS", Kind: kindInt, Group: "cluster", Label: "Commit coordinator max requests"},
	{TOMLPath: "cluster.commit_coordinator_max_records", EnvKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS", Kind: kindInt, Group: "cluster", Label: "Commit coordinator max records"},
	{TOMLPath: "cluster.commit_coordinator_max_bytes", EnvKey: "WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES", Kind: kindInt, Group: "cluster", Label: "Commit coordinator max bytes"},
	{TOMLPath: "cluster.commit_coordinator_shards", EnvKey: "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS", Kind: kindInt, Group: "cluster", Label: "Commit coordinator shards"},

	{TOMLPath: "channel_migration.enable", EnvKey: "WK_CHANNEL_MIGRATION_ENABLE", Kind: kindBool, Group: "channel_migration", Label: "Channel migration enable"},
	{TOMLPath: "channel_migration.scan_interval", EnvKey: "WK_CHANNEL_MIGRATION_SCAN_INTERVAL", Kind: kindDuration, Group: "channel_migration", Label: "Channel migration scan interval"},
	{TOMLPath: "channel_migration.scan_limit", EnvKey: "WK_CHANNEL_MIGRATION_SCAN_LIMIT", Kind: kindInt, Group: "channel_migration", Label: "Channel migration scan limit"},
	{TOMLPath: "channel_migration.max_pages_per_tick", EnvKey: "WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK", Kind: kindInt, Group: "channel_migration", Label: "Channel migration max pages per tick"},
	{TOMLPath: "channel_migration.max_tasks_per_tick", EnvKey: "WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK", Kind: kindInt, Group: "channel_migration", Label: "Channel migration max tasks per tick"},
	{TOMLPath: "channel_migration.task_limit", EnvKey: "WK_CHANNEL_MIGRATION_TASK_LIMIT", Kind: kindInt, Group: "channel_migration", Label: "Channel migration task limit"},

	{TOMLPath: "channel.message_retention_physical_gc_enable", EnvKey: "WK_CHANNEL_MESSAGE_RETENTION_PHYSICAL_GC_ENABLE", Kind: kindBool, Group: "channel", Label: "Message retention physical GC"},
	{TOMLPath: "channel.message_retention_scan_interval", EnvKey: "WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL", Kind: kindDuration, Group: "channel", Label: "Message retention scan interval"},
	{TOMLPath: "channel.message_retention_channel_batch_size", EnvKey: "WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE", Kind: kindInt, Group: "channel", Label: "Message retention channel batch size"},
	{TOMLPath: "channel.message_retention_max_trim_messages", EnvKey: "WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES", Kind: kindInt, Group: "channel", Label: "Message retention max trim messages"},
	{TOMLPath: "channel.message_retention_max_trim_bytes", EnvKey: "WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_BYTES", Kind: kindInt, Group: "channel", Label: "Message retention max trim bytes"},
	{TOMLPath: "channel.large_group_subscriber_threshold", EnvKey: "WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD", Kind: kindInt, Group: "channel", Label: "Large group subscriber threshold"},

	{TOMLPath: "api.listen_addr", EnvKey: "WK_API_LISTEN_ADDR", Kind: kindString, Group: "api", Label: "API listen address"},
	{TOMLPath: "api.external_tcp_addr", EnvKey: "WK_EXTERNAL_TCPADDR", Kind: kindString, Group: "api", Label: "External TCP address"},
	{TOMLPath: "api.external_ws_addr", EnvKey: "WK_EXTERNAL_WSADDR", Kind: kindString, Group: "api", Label: "External WebSocket address", DiagnosticSensitive: true},
	{TOMLPath: "api.external_wss_addr", EnvKey: "WK_EXTERNAL_WSSADDR", Kind: kindString, Group: "api", Label: "External secure WebSocket address", DiagnosticSensitive: true},

	{TOMLPath: "manager.listen_addr", EnvKey: "WK_MANAGER_LISTEN_ADDR", Kind: kindString, Group: "manager", Label: "Manager listen address"},
	{TOMLPath: "manager.auth_on", EnvKey: "WK_MANAGER_AUTH_ON", Kind: kindBool, Group: "manager", Label: "Manager auth enabled"},
	{TOMLPath: "manager.jwt_secret", EnvKey: "WK_MANAGER_JWT_SECRET", Kind: kindString, Group: "manager", Label: "Manager JWT secret", Sensitive: true},
	{TOMLPath: "manager.jwt_issuer", EnvKey: "WK_MANAGER_JWT_ISSUER", Kind: kindString, Group: "manager", Label: "Manager JWT issuer"},
	{TOMLPath: "manager.jwt_expire", EnvKey: "WK_MANAGER_JWT_EXPIRE", Kind: kindDuration, Group: "manager", Label: "Manager JWT expiration"},
	{TOMLPath: "manager.users", EnvKey: "WK_MANAGER_USERS", Kind: kindObjectList, Group: "manager", Label: "Manager users", Sensitive: true},

	{TOMLPath: "bench.api_enable", EnvKey: "WK_BENCH_API_ENABLE", Kind: kindBool, Group: "bench", Label: "Bench API enabled"},
	{TOMLPath: "bench.api_max_batch_size", EnvKey: "WK_BENCH_API_MAX_BATCH_SIZE", Kind: kindInt, Group: "bench", Label: "Bench API max batch size"},
	{TOMLPath: "bench.api_max_payload_bytes", EnvKey: "WK_BENCH_API_MAX_PAYLOAD_BYTES", Kind: kindInt, Group: "bench", Label: "Bench API max payload bytes"},

	{TOMLPath: "observability.metrics_enable", EnvKey: "WK_METRICS_ENABLE", Kind: kindBool, Group: "observability", Label: "Metrics enabled"},
	{TOMLPath: "observability.debug_api_enable", EnvKey: "WK_DEBUG_API_ENABLE", Kind: kindBool, Group: "observability", Label: "Debug API enabled"},

	{TOMLPath: "prometheus.enable", EnvKey: "WK_PROMETHEUS_ENABLE", Kind: kindBool, Group: "prometheus", Label: "Prometheus enabled"},
	{TOMLPath: "prometheus.query_base_url", EnvKey: "WK_PROMETHEUS_QUERY_BASE_URL", Kind: kindString, Group: "prometheus", Label: "Prometheus query base URL", DiagnosticSensitive: true},
	{TOMLPath: "prometheus.binary_path", EnvKey: "WK_PROMETHEUS_BINARY_PATH", Kind: kindString, Group: "prometheus", Label: "Prometheus binary path"},
	{TOMLPath: "prometheus.listen_addr", EnvKey: "WK_PROMETHEUS_LISTEN_ADDR", Kind: kindString, Group: "prometheus", Label: "Prometheus listen address"},
	{TOMLPath: "prometheus.data_dir", EnvKey: "WK_PROMETHEUS_DATA_DIR", Kind: kindString, Group: "prometheus", Label: "Prometheus data directory"},
	{TOMLPath: "prometheus.retention_time", EnvKey: "WK_PROMETHEUS_RETENTION_TIME", Kind: kindDuration, Group: "prometheus", Label: "Prometheus retention time"},
	{TOMLPath: "prometheus.retention_size", EnvKey: "WK_PROMETHEUS_RETENTION_SIZE", Kind: kindString, Group: "prometheus", Label: "Prometheus retention size"},
	{TOMLPath: "prometheus.scrape_interval", EnvKey: "WK_PROMETHEUS_SCRAPE_INTERVAL", Kind: kindDuration, Group: "prometheus", Label: "Prometheus scrape interval"},
	{TOMLPath: "prometheus.scrape_targets", EnvKey: "WK_PROMETHEUS_SCRAPE_TARGETS", Kind: kindStringList, Group: "prometheus", Label: "Prometheus scrape targets"},

	{TOMLPath: "top.api_enable", EnvKey: "WK_TOP_API_ENABLE", Kind: kindBool, Group: "top", Label: "Top API enabled"},
	{TOMLPath: "top.collect_interval", EnvKey: "WK_TOP_COLLECT_INTERVAL", Kind: kindDuration, Group: "top", Label: "Top collect interval"},
	{TOMLPath: "top.history_window", EnvKey: "WK_TOP_HISTORY_WINDOW", Kind: kindDuration, Group: "top", Label: "Top history window"},

	{TOMLPath: "diagnostics.enable", EnvKey: "WK_DIAGNOSTICS_ENABLE", Kind: kindBool, Group: "diagnostics", Label: "Diagnostics enabled"},
	{TOMLPath: "diagnostics.buffer_size", EnvKey: "WK_DIAGNOSTICS_BUFFER_SIZE", Kind: kindInt, Group: "diagnostics", Label: "Diagnostics buffer size"},
	{TOMLPath: "diagnostics.sample_rate", EnvKey: "WK_DIAGNOSTICS_SAMPLE_RATE", Kind: kindFloat, Group: "diagnostics", Label: "Diagnostics sample rate"},
	{TOMLPath: "diagnostics.slow_threshold_ms", EnvKey: "WK_DIAGNOSTICS_SLOW_THRESHOLD_MS", Kind: kindInt, Group: "diagnostics", Label: "Diagnostics slow threshold milliseconds"},
	{TOMLPath: "diagnostics.error_sample_rate", EnvKey: "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE", Kind: kindFloat, Group: "diagnostics", Label: "Diagnostics error sample rate"},
	{TOMLPath: "diagnostics.deep_sample_rate", EnvKey: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE", Kind: kindFloat, Group: "diagnostics", Label: "Diagnostics deep sample rate"},
	{TOMLPath: "diagnostics.deep_slow_threshold_ms", EnvKey: "WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS", Kind: kindInt, Group: "diagnostics", Label: "Diagnostics deep slow threshold milliseconds"},
	{TOMLPath: "diagnostics.deep_max_items_per_batch", EnvKey: "WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH", Kind: kindInt, Group: "diagnostics", Label: "Diagnostics deep max items per batch"},
	{TOMLPath: "diagnostics.debug_matches", EnvKey: "WK_DIAGNOSTICS_DEBUG_MATCHES", Kind: kindObjectList, Group: "diagnostics", Label: "Diagnostics debug matches"},

	{TOMLPath: "gateway.gnet_multicore", EnvKey: "WK_GATEWAY_GNET_MULTICORE", Kind: kindBool, Group: "gateway", Label: "Gateway gnet multicore"},
	{TOMLPath: "gateway.gnet_num_event_loop", EnvKey: "WK_GATEWAY_GNET_NUM_EVENT_LOOP", Kind: kindInt, Group: "gateway", Label: "Gateway gnet event loops"},
	{TOMLPath: "gateway.runtime_async_send_workers", EnvKey: "WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS", Kind: kindInt, Group: "gateway", Label: "Gateway async send workers"},
	{TOMLPath: "gateway.runtime_async_send_queue_capacity", EnvKey: "WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY", Kind: kindInt, Group: "gateway", Label: "Gateway async send queue capacity"},
	{TOMLPath: "gateway.runtime_async_auth_workers", EnvKey: "WK_GATEWAY_RUNTIME_ASYNC_AUTH_WORKERS", Kind: kindInt, Group: "gateway", Label: "Gateway async auth workers"},
	{TOMLPath: "gateway.runtime_async_auth_queue_capacity", EnvKey: "WK_GATEWAY_RUNTIME_ASYNC_AUTH_QUEUE_CAPACITY", Kind: kindInt, Group: "gateway", Label: "Gateway async auth queue capacity"},
	{TOMLPath: "gateway.runtime_async_pool_release_timeout", EnvKey: "WK_GATEWAY_RUNTIME_ASYNC_POOL_RELEASE_TIMEOUT", Kind: kindDuration, Group: "gateway", Label: "Gateway async pool release timeout"},
	{TOMLPath: "gateway.default_session_async_send_batch_max_wait", EnvKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT", Kind: kindDuration, Group: "gateway", Label: "Gateway session async send batch max wait"},
	{TOMLPath: "gateway.default_session_async_send_batch_max_records", EnvKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS", Kind: kindInt, Group: "gateway", Label: "Gateway session async send batch max records"},
	{TOMLPath: "gateway.default_session_async_send_batch_max_bytes", EnvKey: "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES", Kind: kindInt, Group: "gateway", Label: "Gateway session async send batch max bytes"},
	{TOMLPath: "gateway.listeners", EnvKey: "WK_GATEWAY_LISTENERS", Kind: kindObjectList, Group: "gateway", Label: "Gateway listeners"},
	{TOMLPath: "gateway.send_timeout", EnvKey: "WK_GATEWAY_SEND_TIMEOUT", Kind: kindDuration, Group: "gateway", Label: "Gateway send timeout"},

	{TOMLPath: "message.person_whitelist_enabled", EnvKey: "WK_MESSAGE_PERSON_WHITELIST_ENABLED", Kind: kindBool, Group: "message", Label: "Person whitelist enabled"},
	{TOMLPath: "message.system_device_id", EnvKey: "WK_MESSAGE_SYSTEM_DEVICE_ID", Kind: kindString, Group: "message", Label: "System device ID"},
	{TOMLPath: "message.permission_cache_ttl", EnvKey: "WK_MESSAGE_PERMISSION_CACHE_TTL", Kind: kindDuration, Group: "message", Label: "Permission cache TTL"},

	{TOMLPath: "presence.activation_timeout", EnvKey: "WK_PRESENCE_ACTIVATION_TIMEOUT", Kind: kindDuration, Group: "presence", Label: "Presence activation timeout"},
	{TOMLPath: "presence.touch_flush_interval", EnvKey: "WK_PRESENCE_TOUCH_FLUSH_INTERVAL", Kind: kindDuration, Group: "presence", Label: "Presence touch flush interval"},
	{TOMLPath: "presence.touch_batch_size", EnvKey: "WK_PRESENCE_TOUCH_BATCH_SIZE", Kind: kindInt, Group: "presence", Label: "Presence touch batch size"},
	{TOMLPath: "presence.route_ttl", EnvKey: "WK_PRESENCE_ROUTE_TTL", Kind: kindDuration, Group: "presence", Label: "Presence route TTL"},

	{TOMLPath: "conversation.max_last_message_concurrency", EnvKey: "WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY", Kind: kindInt, Group: "conversation", Label: "Conversation max last message concurrency"},
	{TOMLPath: "conversation.authority_cache_max_rows_per_uid", EnvKey: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID", Kind: kindInt, Group: "conversation", Label: "Conversation authority cache max rows per UID"},
	{TOMLPath: "conversation.authority_cache_max_rows", EnvKey: "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS", Kind: kindInt, Group: "conversation", Label: "Conversation authority cache max rows"},
	{TOMLPath: "conversation.authority_list_db_window_max", EnvKey: "WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX", Kind: kindInt, Group: "conversation", Label: "Conversation authority list DB window max"},
	{TOMLPath: "conversation.authority_handoff_timeout", EnvKey: "WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT", Kind: kindDuration, Group: "conversation", Label: "Conversation authority handoff timeout"},
	{TOMLPath: "conversation.authority_active_cooldown", EnvKey: "WK_CONVERSATION_AUTHORITY_ACTIVE_COOLDOWN", Kind: kindDuration, Group: "conversation", Label: "Conversation authority active cooldown"},
	{TOMLPath: "conversation.authority_flush_interval", EnvKey: "WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL", Kind: kindDuration, Group: "conversation", Label: "Conversation authority flush interval"},
	{TOMLPath: "conversation.authority_flush_timeout", EnvKey: "WK_CONVERSATION_AUTHORITY_FLUSH_TIMEOUT", Kind: kindDuration, Group: "conversation", Label: "Conversation authority flush timeout"},
	{TOMLPath: "conversation.authority_flush_batch_rows", EnvKey: "WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS", Kind: kindInt, Group: "conversation", Label: "Conversation authority flush batch rows"},
	{TOMLPath: "conversation.authority_admit_batch_rows", EnvKey: "WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS", Kind: kindInt, Group: "conversation", Label: "Conversation authority admit batch rows"},
	{TOMLPath: "conversation.authority_admit_concurrency", EnvKey: "WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY", Kind: kindInt, Group: "conversation", Label: "Conversation authority admit concurrency"},

	{TOMLPath: "channel_append.shard_count", EnvKey: "WK_CHANNEL_APPEND_SHARD_COUNT", Kind: kindInt, Group: "channel_append", Label: "Channel append shard count"},
	{TOMLPath: "channel_append.advance_pool_size", EnvKey: "WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE", Kind: kindInt, Group: "channel_append", Label: "Channel append advance pool size"},
	{TOMLPath: "channel_append.effect_pool_size", EnvKey: "WK_CHANNEL_APPEND_EFFECT_POOL_SIZE", Kind: kindInt, Group: "channel_append", Label: "Channel append effect pool size"},
	{TOMLPath: "channel_append.recipient_authority_dispatch_concurrency", EnvKey: "WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY", Kind: kindInt, Group: "channel_append", Label: "Channel append recipient authority dispatch concurrency"},

	{TOMLPath: "delivery.enable", EnvKey: "WK_DELIVERY_ENABLE", Kind: kindBool, Group: "delivery", Label: "Delivery enabled"},
	{TOMLPath: "delivery.fanout_page_size", EnvKey: "WK_DELIVERY_FANOUT_PAGE_SIZE", Kind: kindInt, Group: "delivery", Label: "Delivery fanout page size"},
	{TOMLPath: "delivery.push_batch_size", EnvKey: "WK_DELIVERY_PUSH_BATCH_SIZE", Kind: kindInt, Group: "delivery", Label: "Delivery push batch size"},
	{TOMLPath: "delivery.pending_ack_ttl", EnvKey: "WK_DELIVERY_PENDING_ACK_TTL", Kind: kindDuration, Group: "delivery", Label: "Delivery pending ack TTL"},
	{TOMLPath: "delivery.pending_ack_max_per_session", EnvKey: "WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION", Kind: kindInt, Group: "delivery", Label: "Delivery pending ack max per session"},
	{TOMLPath: "delivery.event_queue_size", EnvKey: "WK_DELIVERY_EVENT_QUEUE_SIZE", Kind: kindInt, Group: "delivery", Label: "Delivery event queue size"},

	{TOMLPath: "webhook.http_addr", EnvKey: "WK_WEBHOOK_HTTP_ADDR", Kind: kindString, Group: "webhook", Label: "Webhook HTTP address", DiagnosticSensitive: true},
	{TOMLPath: "webhook.focus_events", EnvKey: "WK_WEBHOOK_FOCUS_EVENTS", Kind: kindStringList, Group: "webhook", Label: "Webhook focus events"},
	{TOMLPath: "webhook.queue_size", EnvKey: "WK_WEBHOOK_QUEUE_SIZE", Kind: kindInt, Group: "webhook", Label: "Webhook queue size"},
	{TOMLPath: "webhook.workers", EnvKey: "WK_WEBHOOK_WORKERS", Kind: kindInt, Group: "webhook", Label: "Webhook workers"},
	{TOMLPath: "webhook.msg_notify_batch_max_items", EnvKey: "WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS", Kind: kindInt, Group: "webhook", Label: "Webhook msg notify batch max items"},
	{TOMLPath: "webhook.msg_notify_batch_max_wait", EnvKey: "WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT", Kind: kindDuration, Group: "webhook", Label: "Webhook msg notify batch max wait"},
	{TOMLPath: "webhook.online_status_batch_max_items", EnvKey: "WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS", Kind: kindInt, Group: "webhook", Label: "Webhook online status batch max items"},
	{TOMLPath: "webhook.online_status_batch_max_wait", EnvKey: "WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT", Kind: kindDuration, Group: "webhook", Label: "Webhook online status batch max wait"},
	{TOMLPath: "webhook.offline_uid_batch_size", EnvKey: "WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE", Kind: kindInt, Group: "webhook", Label: "Webhook offline UID batch size"},
	{TOMLPath: "webhook.request_timeout", EnvKey: "WK_WEBHOOK_REQUEST_TIMEOUT", Kind: kindDuration, Group: "webhook", Label: "Webhook request timeout"},
	{TOMLPath: "webhook.retry_max_attempts", EnvKey: "WK_WEBHOOK_RETRY_MAX_ATTEMPTS", Kind: kindInt, Group: "webhook", Label: "Webhook retry max attempts"},

	{TOMLPath: "plugin.enable", EnvKey: "WK_PLUGIN_ENABLE", Kind: kindBool, Group: "plugin", Label: "Plugin enabled"},
	{TOMLPath: "plugin.dir", EnvKey: "WK_PLUGIN_DIR", Kind: kindString, Group: "plugin", Label: "Plugin directory"},
	{TOMLPath: "plugin.socket_path", EnvKey: "WK_PLUGIN_SOCKET_PATH", Kind: kindString, Group: "plugin", Label: "Plugin socket path"},
	{TOMLPath: "plugin.sandbox_dir", EnvKey: "WK_PLUGIN_SANDBOX_DIR", Kind: kindString, Group: "plugin", Label: "Plugin sandbox directory"},
	{TOMLPath: "plugin.state_dir", EnvKey: "WK_PLUGIN_STATE_DIR", Kind: kindString, Group: "plugin", Label: "Plugin state directory"},
	{TOMLPath: "plugin.timeout", EnvKey: "WK_PLUGIN_TIMEOUT", Kind: kindDuration, Group: "plugin", Label: "Plugin timeout"},
	{TOMLPath: "plugin.hot_reload", EnvKey: "WK_PLUGIN_HOT_RELOAD", Kind: kindBool, Group: "plugin", Label: "Plugin hot reload"},
	{TOMLPath: "plugin.fail_open", EnvKey: "WK_PLUGIN_FAIL_OPEN", Kind: kindBool, Group: "plugin", Label: "Plugin fail open"},
	{TOMLPath: "plugin.persist_after_queue_size", EnvKey: "WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE", Kind: kindInt, Group: "plugin", Label: "Plugin persist-after queue size"},
	{TOMLPath: "plugin.persist_after_workers", EnvKey: "WK_PLUGIN_PERSIST_AFTER_WORKERS", Kind: kindInt, Group: "plugin", Label: "Plugin persist-after workers"},

	{TOMLPath: "log.level", EnvKey: "WK_LOG_LEVEL", Kind: kindString, Group: "log", Label: "Log level"},
	{TOMLPath: "log.dir", EnvKey: "WK_LOG_DIR", Kind: kindString, Group: "log", Label: "Log directory"},
	{TOMLPath: "log.max_size", EnvKey: "WK_LOG_MAX_SIZE", Kind: kindInt, Group: "log", Label: "Log max size"},
	{TOMLPath: "log.max_age", EnvKey: "WK_LOG_MAX_AGE", Kind: kindInt, Group: "log", Label: "Log max age"},
	{TOMLPath: "log.max_backups", EnvKey: "WK_LOG_MAX_BACKUPS", Kind: kindInt, Group: "log", Label: "Log max backups"},
	{TOMLPath: "log.compress", EnvKey: "WK_LOG_COMPRESS", Kind: kindBool, Group: "log", Label: "Log compression"},
	{TOMLPath: "log.console", EnvKey: "WK_LOG_CONSOLE", Kind: kindBool, Group: "log", Label: "Log console output"},
	{TOMLPath: "log.format", EnvKey: "WK_LOG_FORMAT", Kind: kindString, Group: "log", Label: "Log format"},
}

func schemaByTOMLPath() map[string]fieldSpec {
	out := make(map[string]fieldSpec, len(schemaFields))
	for _, field := range schemaFields {
		out[field.TOMLPath] = field
	}
	return out
}

func schemaByEnvKey() map[string]fieldSpec {
	out := make(map[string]fieldSpec, len(schemaFields))
	for _, field := range schemaFields {
		out[field.EnvKey] = field
	}
	return out
}

func supportedConfigKeysForBuilder() []string {
	return []string{
		"WK_NODE_ID",
		"WK_NODE_DATA_DIR",
		"WK_CLUSTER_LISTEN_ADDR",
		"WK_CLUSTER_ID",
		"WK_CLUSTER_SEEDS",
		"WK_CLUSTER_ADVERTISE_ADDR",
		"WK_CLUSTER_JOIN_TOKEN",
		"WK_CLUSTER_NODES",
		"WK_CLUSTER_INITIAL_SLOT_COUNT",
		"WK_CLUSTER_HASH_SLOT_COUNT",
		"WK_CLUSTER_SLOT_REPLICA_N",
		"WK_CLUSTER_CHANNEL_REPLICA_N",
		"WK_CLUSTER_SLOT_TICK_INTERVAL",
		"WK_CLUSTER_SLOT_ELECTION_TICK",
		"WK_CLUSTER_SLOT_HEARTBEAT_TICK",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL",
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT",
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS",
		"WK_CLUSTER_CHANNEL_STORE_APPEND_BATCH_MAX_WAIT",
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS",
		"WK_CLUSTER_CHANNEL_RPC_WORKERS",
		"WK_CLUSTER_MAX_CHANNELS",
		"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS",
		"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT",
		"WK_CLUSTER_CHANNEL_APPEND_BATCH_ADAPTIVE_FLUSH",
		"WK_CLUSTER_CHANNEL_APPEND_BATCH_COLD_MAX_WAIT",
		"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL",
		"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL",
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL",
		"WK_CHANNEL_MIGRATION_ENABLE",
		"WK_CHANNEL_MIGRATION_SCAN_INTERVAL",
		"WK_CHANNEL_MIGRATION_SCAN_LIMIT",
		"WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK",
		"WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK",
		"WK_CHANNEL_MIGRATION_TASK_LIMIT",
		"WK_CHANNEL_MESSAGE_RETENTION_PHYSICAL_GC_ENABLE",
		"WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL",
		"WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE",
		"WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES",
		"WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_BYTES",
		"WK_CLUSTER_COMMIT_COORDINATOR_SYNC",
		"WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES",
		"WK_CLUSTER_COMMIT_COORDINATOR_SHARDS",
		"WK_API_LISTEN_ADDR",
		"WK_MANAGER_LISTEN_ADDR",
		"WK_MANAGER_AUTH_ON",
		"WK_MANAGER_JWT_SECRET",
		"WK_MANAGER_JWT_ISSUER",
		"WK_MANAGER_JWT_EXPIRE",
		"WK_MANAGER_USERS",
		"WK_BENCH_API_ENABLE",
		"WK_BENCH_API_MAX_BATCH_SIZE",
		"WK_BENCH_API_MAX_PAYLOAD_BYTES",
		"WK_METRICS_ENABLE",
		"WK_PROMETHEUS_ENABLE",
		"WK_PROMETHEUS_QUERY_BASE_URL",
		"WK_PROMETHEUS_BINARY_PATH",
		"WK_PROMETHEUS_LISTEN_ADDR",
		"WK_PROMETHEUS_DATA_DIR",
		"WK_PROMETHEUS_RETENTION_TIME",
		"WK_PROMETHEUS_RETENTION_SIZE",
		"WK_PROMETHEUS_SCRAPE_INTERVAL",
		"WK_PROMETHEUS_SCRAPE_TARGETS",
		"WK_DEBUG_API_ENABLE",
		"WK_TOP_API_ENABLE",
		"WK_TOP_COLLECT_INTERVAL",
		"WK_TOP_HISTORY_WINDOW",
		"WK_DIAGNOSTICS_ENABLE",
		"WK_DIAGNOSTICS_BUFFER_SIZE",
		"WK_DIAGNOSTICS_SAMPLE_RATE",
		"WK_DIAGNOSTICS_SLOW_THRESHOLD_MS",
		"WK_DIAGNOSTICS_ERROR_SAMPLE_RATE",
		"WK_DIAGNOSTICS_DEEP_SAMPLE_RATE",
		"WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS",
		"WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH",
		"WK_DIAGNOSTICS_DEBUG_MATCHES",
		"WK_EXTERNAL_TCPADDR",
		"WK_EXTERNAL_WSADDR",
		"WK_EXTERNAL_WSSADDR",
		"WK_GATEWAY_GNET_MULTICORE",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY",
		"WK_GATEWAY_RUNTIME_ASYNC_AUTH_WORKERS",
		"WK_GATEWAY_RUNTIME_ASYNC_AUTH_QUEUE_CAPACITY",
		"WK_GATEWAY_RUNTIME_ASYNC_POOL_RELEASE_TIMEOUT",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS",
		"WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES",
		"WK_GATEWAY_LISTENERS",
		"WK_GATEWAY_SEND_TIMEOUT",
		"WK_MESSAGE_PERSON_WHITELIST_ENABLED",
		"WK_MESSAGE_SYSTEM_DEVICE_ID",
		"WK_MESSAGE_PERMISSION_CACHE_TTL",
		"WK_PRESENCE_ACTIVATION_TIMEOUT",
		"WK_PRESENCE_TOUCH_FLUSH_INTERVAL",
		"WK_PRESENCE_TOUCH_BATCH_SIZE",
		"WK_PRESENCE_ROUTE_TTL",
		"WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY",
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID",
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS",
		"WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX",
		"WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT",
		"WK_CONVERSATION_AUTHORITY_ACTIVE_COOLDOWN",
		"WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL",
		"WK_CONVERSATION_AUTHORITY_FLUSH_TIMEOUT",
		"WK_CONVERSATION_AUTHORITY_FLUSH_BATCH_ROWS",
		"WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS",
		"WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY",
		"WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD",
		"WK_CHANNEL_APPEND_SHARD_COUNT",
		"WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE",
		"WK_CHANNEL_APPEND_EFFECT_POOL_SIZE",
		"WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY",
		"WK_DELIVERY_ENABLE",
		"WK_DELIVERY_FANOUT_PAGE_SIZE",
		"WK_DELIVERY_PUSH_BATCH_SIZE",
		"WK_DELIVERY_PENDING_ACK_TTL",
		"WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION",
		"WK_DELIVERY_EVENT_QUEUE_SIZE",
		"WK_WEBHOOK_HTTP_ADDR",
		"WK_WEBHOOK_FOCUS_EVENTS",
		"WK_WEBHOOK_QUEUE_SIZE",
		"WK_WEBHOOK_WORKERS",
		"WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS",
		"WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT",
		"WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS",
		"WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT",
		"WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE",
		"WK_WEBHOOK_REQUEST_TIMEOUT",
		"WK_WEBHOOK_RETRY_MAX_ATTEMPTS",
		"WK_PLUGIN_ENABLE",
		"WK_PLUGIN_DIR",
		"WK_PLUGIN_SOCKET_PATH",
		"WK_PLUGIN_SANDBOX_DIR",
		"WK_PLUGIN_STATE_DIR",
		"WK_PLUGIN_TIMEOUT",
		"WK_PLUGIN_HOT_RELOAD",
		"WK_PLUGIN_FAIL_OPEN",
		"WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE",
		"WK_PLUGIN_PERSIST_AFTER_WORKERS",
		"WK_LOG_LEVEL",
		"WK_LOG_DIR",
		"WK_LOG_MAX_SIZE",
		"WK_LOG_MAX_AGE",
		"WK_LOG_MAX_BACKUPS",
		"WK_LOG_COMPRESS",
		"WK_LOG_CONSOLE",
		"WK_LOG_FORMAT",
	}
}
