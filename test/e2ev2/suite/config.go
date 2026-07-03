//go:build e2e

package suite

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const staticThreeNodeClusterID = "wukongim-e2ev2-three"

// NodeSpec describes the external runtime inputs for one e2ev2 node process.
type NodeSpec struct {
	ID          uint64
	Name        string
	RootDir     string
	DataDir     string
	ConfigPath  string
	StdoutPath  string
	StderrPath  string
	ClusterAddr string
	GatewayAddr string
	APIAddr     string
	ManagerAddr string
	LogDir      string
	// ConfigOverrides appends or replaces rendered WK_* config keys for one node.
	ConfigOverrides map[string]string
	// Env appends process environment variables for this node after rendered config values.
	Env []string
}

// RenderSingleNodeConfig renders a real wukongim.conf file for a v2 single-node cluster.
func RenderSingleNodeConfig(spec NodeSpec) string {
	return RenderClusterConfig(spec, []NodeSpec{spec})
}

// RenderClusterConfig renders one real wukongim.conf file for a static v2 cluster node.
func RenderClusterConfig(local NodeSpec, nodes []NodeSpec) string {
	if len(nodes) == 0 {
		nodes = []NodeSpec{local}
	}
	initialSlotCount := 1
	hashSlotCount := 4
	replicaN := len(nodes)
	if len(nodes) > 1 {
		initialSlotCount = len(nodes)
		hashSlotCount = 16
	}
	lines := []configLine{
		{key: "WK_NODE_ID", value: fmt.Sprintf("%d", local.ID)},
		{key: "WK_NODE_DATA_DIR", value: local.DataDir},
		{key: "WK_CLUSTER_LISTEN_ADDR", value: local.ClusterAddr},
		{key: "WK_CLUSTER_INITIAL_SLOT_COUNT", value: fmt.Sprintf("%d", initialSlotCount)},
		{key: "WK_CLUSTER_HASH_SLOT_COUNT", value: fmt.Sprintf("%d", hashSlotCount)},
		{key: "WK_CLUSTER_SLOT_REPLICA_N", value: fmt.Sprintf("%d", replicaN)},
		{key: "WK_API_LISTEN_ADDR", value: local.APIAddr},
		{key: "WK_METRICS_ENABLE", value: "true"},
		{key: "WK_GATEWAY_LISTENERS", value: renderGatewayListeners(local.GatewayAddr)},
		{key: "WK_GATEWAY_SEND_TIMEOUT", value: "5s"},
	}
	if local.ManagerAddr != "" {
		lines = append(lines, configLine{key: "WK_MANAGER_LISTEN_ADDR", value: local.ManagerAddr})
	}
	if len(nodes) > 1 {
		lines = append(lines,
			configLine{key: "WK_CLUSTER_ID", value: staticThreeNodeClusterID},
			configLine{key: "WK_CLUSTER_NODES", value: marshalClusterNodes(nodes)},
		)
	}
	if local.LogDir != "" {
		lines = append(lines, configLine{key: "WK_LOG_DIR", value: local.LogDir})
	}
	lines = applyConfigOverrides(lines, local.ConfigOverrides)

	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, line.key+"="+line.value)
	}
	return strings.Join(rendered, "\n") + "\n"
}

// RenderSeedJoinNodeConfig renders a seed-join node config without static cluster nodes.
func RenderSeedJoinNodeConfig(local NodeSpec, cfg SeedJoinNodeConfig) string {
	clusterID := strings.TrimSpace(cfg.ClusterID)
	if clusterID == "" {
		clusterID = staticThreeNodeClusterID
	}
	joinAddr := strings.TrimSpace(cfg.JoinAddr)
	if joinAddr == "" {
		joinAddr = local.ClusterAddr
	}

	lines := []configLine{
		{key: "WK_NODE_ID", value: fmt.Sprintf("%d", local.ID)},
		{key: "WK_NODE_DATA_DIR", value: local.DataDir},
		{key: "WK_CLUSTER_LISTEN_ADDR", value: local.ClusterAddr},
		{key: "WK_CLUSTER_ID", value: clusterID},
		{key: "WK_CLUSTER_SEEDS", value: marshalStringList(cfg.Seeds)},
		{key: "WK_CLUSTER_ADVERTISE_ADDR", value: joinAddr},
		{key: "WK_CLUSTER_JOIN_TOKEN", value: strings.TrimSpace(cfg.JoinToken)},
		{key: "WK_CLUSTER_INITIAL_SLOT_COUNT", value: "3"},
		{key: "WK_CLUSTER_HASH_SLOT_COUNT", value: "16"},
		{key: "WK_CLUSTER_SLOT_REPLICA_N", value: "3"},
		{key: "WK_API_LISTEN_ADDR", value: local.APIAddr},
		{key: "WK_METRICS_ENABLE", value: "true"},
		{key: "WK_GATEWAY_LISTENERS", value: renderGatewayListeners(local.GatewayAddr)},
		{key: "WK_GATEWAY_SEND_TIMEOUT", value: "5s"},
	}
	if local.ManagerAddr != "" {
		lines = append(lines, configLine{key: "WK_MANAGER_LISTEN_ADDR", value: local.ManagerAddr})
	}
	if local.LogDir != "" {
		lines = append(lines, configLine{key: "WK_LOG_DIR", value: local.LogDir})
	}
	lines = applyConfigOverrides(lines, local.ConfigOverrides)

	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, line.key+"="+line.value)
	}
	return strings.Join(rendered, "\n") + "\n"
}

func renderGatewayListeners(gatewayAddr string) string {
	return fmt.Sprintf(`[{"name":"tcp-wkproto","network":"tcp","address":"%s","transport":"gnet","protocol":"wkproto"}]`, gatewayAddr)
}

func marshalClusterNodes(nodes []NodeSpec) string {
	type clusterNode struct {
		ID   uint64 `json:"id"`
		Addr string `json:"addr"`
	}

	items := make([]clusterNode, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, clusterNode{ID: node.ID, Addr: node.ClusterAddr})
	}

	data, err := json.Marshal(items)
	if err != nil {
		panic(fmt.Sprintf("marshal cluster nodes: %v", err))
	}
	return string(data)
}

func marshalStringList(items []string) string {
	data, err := json.Marshal(items)
	if err != nil {
		panic(fmt.Sprintf("marshal string list: %v", err))
	}
	return string(data)
}

type configLine struct {
	key   string
	value string
}

func applyConfigOverrides(lines []configLine, overrides map[string]string) []configLine {
	if len(overrides) == 0 {
		return lines
	}

	extraKeys := make([]string, 0, len(overrides))
	for key, value := range overrides {
		replaced := false
		for i := range lines {
			if lines[i].key != key {
				continue
			}
			lines[i].value = value
			replaced = true
			break
		}
		if !replaced {
			extraKeys = append(extraKeys, key)
		}
	}

	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		lines = append(lines, configLine{key: key, value: overrides[key]})
	}
	return lines
}

func envFromConfig(config string) []string {
	var env []string
	for _, rawLine := range strings.Split(config, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !strings.HasPrefix(key, "WK_") {
			continue
		}
		env = append(env, key+"="+strings.TrimSpace(value))
	}
	return env
}
