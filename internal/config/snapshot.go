package config

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	channelreactor "github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	channelworker "github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

const redactedValue = "******"

type effectiveSnapshotValue struct {
	value  string
	source string
}

// buildStartupSnapshot captures the bounded redacted effective configuration
// exposed by Manager after loader and runtime default normalization.
func buildStartupSnapshot(values sourceValues, cfg app.Config) managementusecase.NodeConfigSnapshot {
	groups := map[string][]managementusecase.NodeConfigItem{}
	effective := effectiveCriticalSnapshotValues(values, cfg)
	for _, field := range schemaFields {
		raw, configured := values.values[field.EnvKey]
		resolved, critical := effective[field.EnvKey]
		if !configured && !critical {
			continue
		}
		value := formatSnapshotValue(field, raw)
		source := configuredValueSource(values.sources[field.EnvKey])
		if critical {
			value = resolved.value
			source = resolved.source
		}
		groups[field.Group] = append(groups[field.Group], managementusecase.NodeConfigItem{
			Key:       field.EnvKey,
			Label:     field.Label,
			Value:     value,
			Source:    source,
			Sensitive: field.Sensitive,
			Redacted:  field.Sensitive && strings.TrimSpace(raw) != "",
		})
	}
	return managementusecase.NodeConfigSnapshot{
		GeneratedAt:     time.Now().UTC(),
		NodeID:          cfg.NodeID,
		Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		RequiresRestart: true,
		Groups:          orderedSnapshotGroups(groups),
	}
}

// effectiveCriticalSnapshotValues normalizes performance-critical startup
// values exactly as their owning runtimes do and records how each value arose.
func effectiveCriticalSnapshotValues(values sourceValues, cfg app.Config) map[string]effectiveSnapshotValue {
	clusterCfg := cfg.Cluster.WithDefaults()
	initialSlotCount := int(clusterCfg.Slots.InitialSlotCount)
	initialDerived := cfg.Cluster.Slots.InitialSlotCount == 0 && configuredAsZero(values, "WK_CLUSTER_INITIAL_SLOT_COUNT")
	hashSlotCount := int(clusterCfg.Slots.HashSlotCount)
	hashDerived := cfg.Cluster.Slots.HashSlotCount == 0 && configuredAsZero(values, "WK_CLUSTER_HASH_SLOT_COUNT")
	slotReplicaCount := int(clusterCfg.Slots.ReplicaCount)
	slotReplicaDerived := cfg.Cluster.Slots.ReplicaCount == 0
	channelReplicaCount := int(clusterCfg.Channel.ReplicaCount)
	channelReplicaDerived := cfg.Cluster.Channel.ReplicaCount == 0
	reactorCount := clusterCfg.Channel.ReactorCount
	reactorDerived := cfg.Cluster.Channel.ReactorCount == 0
	appendWorkers := cfg.Cluster.Channel.StoreAppendWorkers
	appendDerived := false
	if appendWorkers == 0 {
		appendWorkers = channelreactor.DefaultStoreAppendWorkerCount(reactorCount)
		appendDerived = true
	}
	applyWorkers := cfg.Cluster.Channel.StoreApplyWorkers
	applyDerived := false
	if applyWorkers == 0 {
		applyWorkers = channelreactor.DefaultStoreApplyWorkerCount(reactorCount)
		applyDerived = true
	}
	rpcWorkers := cfg.Cluster.Channel.RPCWorkers
	rpcDerived := false
	if rpcWorkers == 0 {
		rpcWorkers = channelreactor.DefaultRPCWorkerCount(reactorCount)
		rpcDerived = true
	}
	rpcBatchMaxItems := cfg.Cluster.Channel.RPCBatchMaxItems
	rpcBatchDerived := false
	if rpcBatchMaxItems == 0 {
		rpcBatchMaxItems = channelworker.DefaultRPCBatchMaxItems
		rpcBatchDerived = true
	}
	gatewayRuntime := gateway.NormalizeRuntimeOptions(cfg.Gateway.Runtime)
	eventLoopsDerived := !configuredPositive(values, "WK_GATEWAY_GNET_NUM_EVENT_LOOP")
	multicoreDerived := values.sources["WK_GATEWAY_GNET_MULTICORE"] == ""
	recipientWorkers := cfg.Delivery.RecipientWorkerConcurrency
	recipientDerived := false
	if recipientWorkers == 0 {
		recipientWorkers = app.DefaultDeliveryRecipientWorkerConcurrency
		recipientDerived = configuredAsZero(values, "WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY")
	}
	cacheRows := cfg.Conversation.AuthorityCacheMaxRows
	cacheRowsDerived := false
	if cacheRows == 0 {
		cacheRows = app.DefaultConversationAuthorityCacheMaxRows
		cacheRowsDerived = configuredAsZero(values, "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS")
	}

	return map[string]effectiveSnapshotValue{
		"WK_CLUSTER_INITIAL_SLOT_COUNT":                effectiveInteger(values, "WK_CLUSTER_INITIAL_SLOT_COUNT", initialSlotCount, initialDerived),
		"WK_CLUSTER_HASH_SLOT_COUNT":                   effectiveInteger(values, "WK_CLUSTER_HASH_SLOT_COUNT", hashSlotCount, hashDerived),
		"WK_CLUSTER_SLOT_REPLICA_N":                    effectiveInteger(values, "WK_CLUSTER_SLOT_REPLICA_N", slotReplicaCount, slotReplicaDerived),
		"WK_CLUSTER_CHANNEL_REPLICA_N":                 effectiveInteger(values, "WK_CLUSTER_CHANNEL_REPLICA_N", channelReplicaCount, channelReplicaDerived),
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT":             effectiveInteger(values, "WK_CLUSTER_CHANNEL_REACTOR_COUNT", reactorCount, reactorDerived),
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS":      effectiveInteger(values, "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS", appendWorkers, appendDerived),
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS":       effectiveInteger(values, "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS", applyWorkers, applyDerived),
		"WK_CLUSTER_CHANNEL_RPC_WORKERS":               effectiveInteger(values, "WK_CLUSTER_CHANNEL_RPC_WORKERS", rpcWorkers, rpcDerived),
		"WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS":       effectiveInteger(values, "WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS", rpcBatchMaxItems, rpcBatchDerived),
		"WK_GATEWAY_GNET_MULTICORE":                    effectiveBoolean(values, "WK_GATEWAY_GNET_MULTICORE", cfg.Gateway.Transport.Gnet.Multicore, multicoreDerived),
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP":               effectiveInteger(values, "WK_GATEWAY_GNET_NUM_EVENT_LOOP", cfg.Gateway.Transport.Gnet.NumEventLoop, eventLoopsDerived),
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS":        effectiveInteger(values, "WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS", gatewayRuntime.AsyncSendWorkers, cfg.Gateway.Runtime.AsyncSendWorkers != gatewayRuntime.AsyncSendWorkers),
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY": effectiveInteger(values, "WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY", gatewayRuntime.AsyncSendQueueCapacity, cfg.Gateway.Runtime.AsyncSendQueueCapacity != gatewayRuntime.AsyncSendQueueCapacity),
		"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY":     effectiveInteger(values, "WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY", recipientWorkers, recipientDerived),
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS":     effectiveInteger(values, "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS", cacheRows, cacheRowsDerived),
	}
}

func effectiveInteger(values sourceValues, key string, value int, derived bool) effectiveSnapshotValue {
	return effectiveSnapshotValue{value: strconv.Itoa(value), source: effectiveValueSource(values, key, derived)}
}

func effectiveBoolean(values sourceValues, key string, value bool, derived bool) effectiveSnapshotValue {
	return effectiveSnapshotValue{value: strconv.FormatBool(value), source: effectiveValueSource(values, key, derived)}
}

func effectiveValueSource(values sourceValues, key string, derived bool) string {
	if derived {
		return managementusecase.NodeConfigValueSourceDerived
	}
	if source := configuredValueSource(values.sources[key]); source != "" {
		return source
	}
	return managementusecase.NodeConfigValueSourceDefault
}

func configuredValueSource(source string) string {
	switch source {
	case managementusecase.NodeConfigValueSourceTOML:
		return managementusecase.NodeConfigValueSourceTOML
	case managementusecase.NodeConfigValueSourceEnvironment:
		return managementusecase.NodeConfigValueSourceEnvironment
	default:
		return ""
	}
}

func configuredAsZero(values sourceValues, key string) bool {
	raw, ok := values.values[key]
	return ok && strings.TrimSpace(raw) == "0"
}

func configuredPositive(values sourceValues, key string) bool {
	raw, ok := values.values[key]
	if !ok {
		return false
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	return err == nil && value > 0
}

func orderedSnapshotGroups(groups map[string][]managementusecase.NodeConfigItem) []managementusecase.NodeConfigGroup {
	order := []string{
		"node",
		"cluster",
		"api",
		"manager",
		"gateway",
		"message",
		"channel",
		"channel_append",
		"delivery",
		"webhook",
		"plugin",
		"log",
		"observability",
		"bench",
		"prometheus",
		"diagnostics",
		"top",
		"presence",
		"conversation",
		"channel_migration",
	}
	seen := map[string]bool{}
	out := make([]managementusecase.NodeConfigGroup, 0, len(groups))
	for _, id := range order {
		items, ok := groups[id]
		if !ok {
			continue
		}
		out = append(out, managementusecase.NodeConfigGroup{ID: id, Title: groupTitle(id), Items: items})
		seen[id] = true
	}
	extra := make([]string, 0)
	for id := range groups {
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		out = append(out, managementusecase.NodeConfigGroup{ID: id, Title: groupTitle(id), Items: groups[id]})
	}
	return out
}

func formatSnapshotValue(field fieldSpec, raw string) string {
	if field.Sensitive && strings.TrimSpace(raw) != "" {
		return redactedValue
	}
	if isPathLikeField(field.TOMLPath) {
		if strings.TrimSpace(raw) == "" {
			return ""
		}
		return "configured"
	}
	if field.Kind == kindObjectList {
		if strings.TrimSpace(raw) == "" {
			return ""
		}
		return "configured"
	}
	return raw
}

func isPathLikeField(tomlPath string) bool {
	path := strings.ToLower(tomlPath)
	return strings.Contains(path, "data_dir") ||
		strings.Contains(path, "log.dir") ||
		strings.Contains(path, "binary_path") ||
		strings.Contains(path, "plugin.dir") ||
		strings.Contains(path, "plugin.sandbox_dir") ||
		strings.Contains(path, "plugin.state_dir") ||
		strings.Contains(path, "plugin.socket_path")
}

func groupTitle(id string) string {
	titles := map[string]string{
		"node":              "Node",
		"cluster":           "Cluster",
		"api":               "API",
		"manager":           "Manager",
		"gateway":           "Gateway",
		"message":           "Message",
		"channel":           "Channel",
		"channel_append":    "Channel Append",
		"delivery":          "Delivery",
		"webhook":           "Webhook",
		"plugin":            "Plugin",
		"log":               "Log",
		"observability":     "Observability",
		"bench":             "Bench",
		"prometheus":        "Prometheus",
		"diagnostics":       "Diagnostics",
		"top":               "Top",
		"presence":          "Presence",
		"conversation":      "Conversation",
		"channel_migration": "Channel Migration",
	}
	if title, ok := titles[id]; ok {
		return title
	}
	return id
}
