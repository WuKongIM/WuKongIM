package config

import (
	"sort"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

const redactedValue = "******"

func buildStartupSnapshot(values sourceValues, nodeID uint64) managementusecase.NodeConfigSnapshot {
	groups := map[string][]managementusecase.NodeConfigItem{}
	for _, field := range schemaFields {
		raw, ok := values.values[field.EnvKey]
		if !ok {
			continue
		}
		value := formatSnapshotValue(field, raw)
		groups[field.Group] = append(groups[field.Group], managementusecase.NodeConfigItem{
			Key:       field.EnvKey,
			Label:     field.Label,
			Value:     value,
			Sensitive: field.Sensitive,
			Redacted:  field.Sensitive && strings.TrimSpace(raw) != "",
		})
	}
	return managementusecase.NodeConfigSnapshot{
		GeneratedAt:     time.Now().UTC(),
		NodeID:          nodeID,
		Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		RequiresRestart: true,
		Groups:          orderedSnapshotGroups(groups),
	}
}

func orderedSnapshotGroups(groups map[string][]managementusecase.NodeConfigItem) []managementusecase.NodeConfigGroup {
	order := []string{
		"node",
		"cluster",
		"backup",
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
		strings.Contains(path, "backup.staging_dir") ||
		strings.Contains(path, "plugin.socket_path")
}

func groupTitle(id string) string {
	titles := map[string]string{
		"node":              "Node",
		"cluster":           "Cluster",
		"backup":            "Backup",
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
