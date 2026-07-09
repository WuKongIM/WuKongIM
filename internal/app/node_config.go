package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

const nodeConfigRedactedValue = "******"

// NodeConfigSnapshot returns this process's allowlisted effective startup configuration.
func (a *App) NodeConfigSnapshot(_ context.Context, requestedNodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	if a == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	cfg := a.cfg
	clusterCfg := defaultClusterConfig(cfg)
	nodeID := cfg.NodeID
	if nodeID == 0 {
		nodeID = clusterCfg.NodeID
	}
	if requestedNodeID != nodeID {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	if len(cfg.StartupConfigSnapshot.Groups) > 0 {
		snapshot := cfg.StartupConfigSnapshot
		snapshot.GeneratedAt = time.Now().UTC()
		snapshot.NodeID = nodeID
		if snapshot.Source == "" {
			snapshot.Source = managementusecase.NodeConfigSnapshotSourceEffectiveStartup
		}
		snapshot.RequiresRestart = true
		return snapshot, nil
	}
	return managementusecase.NodeConfigSnapshot{
		GeneratedAt:     time.Now().UTC(),
		NodeID:          nodeID,
		Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		RequiresRestart: true,
		Groups: []managementusecase.NodeConfigGroup{
			nodeConfigGroup("node", "Node", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_NODE_ID", "Node ID", fmt.Sprintf("%d", nodeID)),
				nodeConfigItem("WK_NODE_DATA_DIR", "Data directory", endpointPresenceValue(cfg.DataDir)),
			}),
			nodeConfigGroup("cluster", "Cluster", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_CLUSTER_ID", "Cluster ID", clusterCfg.Control.ClusterID),
				redactedNodeConfigItem("WK_CLUSTER_JOIN_TOKEN", "Join token", clusterCfg.Join.Token),
				nodeConfigItem("WK_CLUSTER_LISTEN_ADDR", "Cluster listen address", clusterCfg.ListenAddr),
				nodeConfigItem("WK_CLUSTER_ADVERTISE_ADDR", "Cluster advertise address", clusterCfg.Join.AdvertiseAddr),
				nodeConfigItem("WK_CLUSTER_HASH_SLOT_COUNT", "Hash slot count", fmt.Sprintf("%d", clusterCfg.Slots.HashSlotCount)),
				nodeConfigItem("WK_CLUSTER_INITIAL_SLOT_COUNT", "Initial slot count", fmt.Sprintf("%d", clusterCfg.Slots.InitialSlotCount)),
				nodeConfigItem("WK_CLUSTER_SLOT_REPLICA_N", "Slot replica count", fmt.Sprintf("%d", clusterCfg.Slots.ReplicaCount)),
				nodeConfigItem("WK_CLUSTER_CHANNEL_REPLICA_N", "Channel replica count", fmt.Sprintf("%d", clusterCfg.Channel.ReplicaCount)),
				nodeConfigItem("WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL", "Node health report interval", durationConfigValue(clusterCfg.HealthReport.Interval)),
				nodeConfigItem("WK_CLUSTER_NODE_HEALTH_REPORT_TTL", "Node health report TTL", durationConfigValue(clusterCfg.HealthReport.TTL)),
			}),
			nodeConfigGroup("gateway", "Gateway", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_EXTERNAL_TCPADDR", "External TCP address", cfg.API.ExternalTCPAddr),
				nodeConfigItem("WK_EXTERNAL_WSADDR", "External WS address", cfg.API.ExternalWSAddr),
				nodeConfigItem("WK_EXTERNAL_WSSADDR", "External WSS address", cfg.API.ExternalWSSAddr),
				nodeConfigItem("WK_GATEWAY_LISTENERS", "Gateway listeners", fmt.Sprintf("%d configured listeners", len(cfg.Gateway.Listeners))),
				nodeConfigItem("WK_GATEWAY_SEND_TIMEOUT", "Gateway send timeout", durationConfigValue(cfg.Gateway.SendTimeout)),
			}),
			nodeConfigGroup("message", "Message / Channel", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_MESSAGE_PERSON_WHITELIST_ENABLED", "Person whitelist", boolConfigValue(cfg.Message.PersonWhitelistEnabled)),
				nodeConfigItem("WK_MESSAGE_PERMISSION_CACHE_TTL", "Permission cache TTL", durationConfigValue(cfg.Message.PermissionCacheTTL)),
				nodeConfigItem("WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD", "Large group subscriber threshold", fmt.Sprintf("%d", cfg.Channel.LargeGroupSubscriberThreshold)),
				nodeConfigItem("WK_CHANNEL_APPEND_SHARD_COUNT", "Channel append shard count", fmt.Sprintf("%d", cfg.ChannelAppend.AuthorityShardCount)),
				nodeConfigItem("WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL", "Conversation flush interval", durationConfigValue(cfg.Conversation.AuthorityFlushInterval)),
				nodeConfigItem("WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY", "Conversation admit concurrency", fmt.Sprintf("%d", cfg.Conversation.AuthorityAdmitConcurrency)),
			}),
			nodeConfigGroup("delivery", "Delivery", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_DELIVERY_ENABLE", "Delivery enabled", boolConfigValue(cfg.Delivery.Enabled)),
				nodeConfigItem("WK_DELIVERY_FANOUT_PAGE_SIZE", "Fanout page size", fmt.Sprintf("%d", cfg.Delivery.FanoutPageSize)),
				nodeConfigItem("WK_DELIVERY_PUSH_BATCH_SIZE", "Push batch size", fmt.Sprintf("%d", cfg.Delivery.PushBatchSize)),
				nodeConfigItem("WK_DELIVERY_PENDING_ACK_TTL", "Pending ack TTL", durationConfigValue(cfg.Delivery.PendingAckTTL)),
				nodeConfigItem("WK_DELIVERY_EVENT_QUEUE_SIZE", "Event queue size", fmt.Sprintf("%d", cfg.Delivery.EventQueueSize)),
			}),
			nodeConfigGroup("webhook", "Webhook", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_WEBHOOK_ENABLE", "Webhook enabled", boolConfigValue(cfg.Webhook.Enabled)),
				nodeConfigItem("WK_WEBHOOK_HTTP_ADDR", "Webhook endpoint", endpointPresenceValue(cfg.Webhook.HTTPAddr)),
				nodeConfigItem("WK_WEBHOOK_FOCUS_EVENTS", "Focus events", stringListSummary(cfg.Webhook.FocusEvents, "all supported events")),
				nodeConfigItem("WK_WEBHOOK_QUEUE_SIZE", "Queue size", fmt.Sprintf("%d", cfg.Webhook.QueueSize)),
				nodeConfigItem("WK_WEBHOOK_WORKERS", "Workers", fmt.Sprintf("%d", cfg.Webhook.Workers)),
				nodeConfigItem("WK_WEBHOOK_REQUEST_TIMEOUT", "Request timeout", durationConfigValue(cfg.Webhook.RequestTimeout)),
				nodeConfigItem("WK_WEBHOOK_RETRY_MAX_ATTEMPTS", "Retry max attempts", fmt.Sprintf("%d", cfg.Webhook.RetryMaxAttempts)),
			}),
			nodeConfigGroup("plugin", "Plugin", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_PLUGIN_ENABLE", "Plugin enabled", boolConfigValue(cfg.Plugin.Enable)),
				nodeConfigItem("WK_PLUGIN_HOT_RELOAD", "Plugin hot reload", boolConfigValue(cfg.Plugin.HotReload)),
				nodeConfigItem("WK_PLUGIN_FAIL_OPEN", "Plugin fail open", boolConfigValue(cfg.Plugin.FailOpen)),
				nodeConfigItem("WK_PLUGIN_TIMEOUT", "Plugin timeout", durationConfigValue(cfg.Plugin.Timeout)),
				nodeConfigItem("WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE", "PersistAfter queue size", fmt.Sprintf("%d", cfg.Plugin.PersistAfterQueueSize)),
				nodeConfigItem("WK_PLUGIN_PERSIST_AFTER_WORKERS", "PersistAfter workers", fmt.Sprintf("%d", cfg.Plugin.PersistAfterWorkers)),
			}),
			nodeConfigGroup("log", "Log", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_LOG_LEVEL", "Log level", cfg.Log.Level),
				nodeConfigItem("WK_LOG_DIR", "Log directory", endpointPresenceValue(cfg.Log.Dir)),
				nodeConfigItem("WK_LOG_MAX_SIZE", "Log max size MB", fmt.Sprintf("%d", cfg.Log.MaxSize)),
				nodeConfigItem("WK_LOG_MAX_AGE", "Log max age days", fmt.Sprintf("%d", cfg.Log.MaxAge)),
				nodeConfigItem("WK_LOG_MAX_BACKUPS", "Log max backups", fmt.Sprintf("%d", cfg.Log.MaxBackups)),
				nodeConfigItem("WK_LOG_COMPRESS", "Log compression", boolConfigValue(cfg.Log.Compress)),
				nodeConfigItem("WK_LOG_CONSOLE", "Console logging", boolConfigValue(cfg.Log.Console)),
				nodeConfigItem("WK_LOG_FORMAT", "Log format", cfg.Log.Format),
			}),
			nodeConfigGroup("observability", "Observability", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_API_LISTEN_ADDR", "API listen address", cfg.API.ListenAddr),
				nodeConfigItem("WK_MANAGER_LISTEN_ADDR", "Manager listen address", cfg.Manager.ListenAddr),
				nodeConfigItem("WK_MANAGER_AUTH_ON", "Manager auth enabled", boolConfigValue(cfg.Manager.AuthOn)),
				redactedNodeConfigItem("WK_MANAGER_JWT_SECRET", "Manager JWT secret", cfg.Manager.JWTSecret),
				nodeConfigItem("WK_MANAGER_JWT_ISSUER", "Manager JWT issuer", cfg.Manager.JWTIssuer),
				nodeConfigItem("WK_MANAGER_JWT_EXPIRE", "Manager JWT expiry", durationConfigValue(cfg.Manager.JWTExpire)),
				redactedNodeConfigItem("WK_MANAGER_USERS", "Manager users", fmt.Sprintf("%d configured users", len(cfg.Manager.Users))),
				nodeConfigItem("WK_METRICS_ENABLE", "Metrics enabled", boolConfigValue(cfg.Observability.MetricsEnabled)),
				nodeConfigItem("WK_PROMETHEUS_ENABLE", "Prometheus enabled", boolConfigValue(cfg.Observability.Prometheus.Enabled)),
				nodeConfigItem("WK_TOP_API_ENABLE", "Top API enabled", boolConfigValue(cfg.Top.APIEnabled)),
				nodeConfigItem("WK_DEBUG_API_ENABLE", "Debug API enabled", boolConfigValue(cfg.Observability.DebugAPIEnabled)),
				nodeConfigItem("WK_DIAGNOSTICS_ENABLE", "Diagnostics enabled", boolConfigValue(cfg.Observability.Diagnostics.Enabled)),
			}),
		},
	}, nil
}

func nodeConfigGroup(id, title string, items []managementusecase.NodeConfigItem) managementusecase.NodeConfigGroup {
	filtered := make([]managementusecase.NodeConfigItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		filtered = append(filtered, item)
	}
	return managementusecase.NodeConfigGroup{ID: id, Title: title, Items: filtered}
}

func nodeConfigItem(key, label, value string) managementusecase.NodeConfigItem {
	return managementusecase.NodeConfigItem{Key: key, Label: label, Value: value}
}

func redactedNodeConfigItem(key, label, value string) managementusecase.NodeConfigItem {
	item := managementusecase.NodeConfigItem{Key: key, Label: label, Sensitive: true}
	if strings.TrimSpace(value) != "" {
		item.Value = nodeConfigRedactedValue
		item.Redacted = true
	}
	return item
}

func boolConfigValue(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func durationConfigValue(value time.Duration) string {
	if value == 0 {
		return "0s"
	}
	return value.String()
}

func endpointPresenceValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "configured"
}

func stringListSummary(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	return strings.Join(values, ", ")
}
