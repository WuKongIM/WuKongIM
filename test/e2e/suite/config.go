//go:build e2e

package suite

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// NodeSpec describes the external runtime inputs for one e2e node process.
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
}

// RenderSingleNodeConfig renders a real wukongim.conf file for one-node clusters.
func RenderSingleNodeConfig(spec NodeSpec) string {
	return RenderClusterConfig(spec, []NodeSpec{spec})
}

// RenderClusterConfig renders one real wukongim.conf file for a managed cluster node.
func RenderClusterConfig(local NodeSpec, nodes []NodeSpec) string {
	lines := []configLine{
		{key: "WK_NODE_ID", value: fmt.Sprintf("%d", local.ID)},
		{key: "WK_NODE_NAME", value: local.Name},
		{key: "WK_NODE_DATA_DIR", value: local.DataDir},
		{key: "WK_CLUSTER_LISTEN_ADDR", value: local.ClusterAddr},
		{key: "WK_CLUSTER_SLOT_COUNT", value: "1"},
		{key: "WK_CLUSTER_INITIAL_SLOT_COUNT", value: "1"},
		{key: "WK_CLUSTER_CONTROLLER_REPLICA_N", value: fmt.Sprintf("%d", len(nodes))},
		{key: "WK_CLUSTER_SLOT_REPLICA_N", value: fmt.Sprintf("%d", len(nodes))},
		{key: "WK_CLUSTER_NODES", value: marshalClusterNodes(nodes)},
		{key: "WK_GATEWAY_LISTENERS", value: fmt.Sprintf(`[{"name":"tcp-wkproto","network":"tcp","address":"%s","transport":"gnet","protocol":"wkproto"}]`, local.GatewayAddr)},
	}
	if local.APIAddr != "" {
		lines = append(lines, configLine{key: "WK_API_LISTEN_ADDR", value: local.APIAddr})
	}
	if local.ManagerAddr != "" {
		lines = append(lines,
			configLine{key: "WK_MANAGER_LISTEN_ADDR", value: local.ManagerAddr},
			configLine{key: "WK_MANAGER_AUTH_ON", value: "false"},
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

func marshalClusterNodes(nodes []NodeSpec) string {
	type clusterNode struct {
		ID   uint64 `json:"id"`
		Addr string `json:"addr"`
	}

	items := make([]clusterNode, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, clusterNode{
			ID:   node.ID,
			Addr: node.ClusterAddr,
		})
	}

	data, err := json.Marshal(items)
	if err != nil {
		panic(fmt.Sprintf("marshal cluster nodes: %v", err))
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
