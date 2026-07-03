//go:build e2e

package suite

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSingleNodeConfigUsesWukongIMKeys(t *testing.T) {
	spec := NodeSpec{
		ID:          1,
		Name:        "node-1",
		DataDir:     "/tmp/wukongim/node-1/data",
		ClusterAddr: "127.0.0.1:11001",
		GatewayAddr: "127.0.0.1:12001",
		APIAddr:     "127.0.0.1:13001",
		LogDir:      "/tmp/wukongim/node-1/logs",
	}

	cfg := RenderSingleNodeConfig(spec)

	require.Contains(t, cfg, "WK_NODE_ID=1\n")
	require.Contains(t, cfg, "WK_NODE_DATA_DIR=/tmp/wukongim/node-1/data\n")
	require.Contains(t, cfg, "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:11001\n")
	require.Contains(t, cfg, "WK_CLUSTER_INITIAL_SLOT_COUNT=1\n")
	require.Contains(t, cfg, "WK_CLUSTER_HASH_SLOT_COUNT=4\n")
	require.Contains(t, cfg, "WK_CLUSTER_SLOT_REPLICA_N=1\n")
	require.Contains(t, cfg, "WK_API_LISTEN_ADDR=127.0.0.1:13001\n")
	require.Contains(t, cfg, "WK_METRICS_ENABLE=true\n")
	require.Contains(t, cfg, `WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:12001","transport":"gnet","protocol":"wkproto"}]`)
	require.NotContains(t, cfg, "WK_CLUSTER_SLOT_COUNT")
	require.NotContains(t, cfg, "WK_MANAGER_")
}

func TestRenderThreeNodeConfigUsesStaticClusterMembership(t *testing.T) {
	nodes := []NodeSpec{
		{ID: 1, DataDir: "/tmp/node-1/data", ClusterAddr: "127.0.0.1:11001", GatewayAddr: "127.0.0.1:12001", APIAddr: "127.0.0.1:13001", ManagerAddr: "127.0.0.1:14001"},
		{ID: 2, DataDir: "/tmp/node-2/data", ClusterAddr: "127.0.0.1:11002", GatewayAddr: "127.0.0.1:12002", APIAddr: "127.0.0.1:13002", ManagerAddr: "127.0.0.1:14002"},
		{ID: 3, DataDir: "/tmp/node-3/data", ClusterAddr: "127.0.0.1:11003", GatewayAddr: "127.0.0.1:12003", APIAddr: "127.0.0.1:13003", ManagerAddr: "127.0.0.1:14003"},
	}

	cfg := RenderClusterConfig(nodes[1], nodes)

	require.Contains(t, cfg, "WK_NODE_ID=2\n")
	require.Contains(t, cfg, "WK_CLUSTER_ID=wukongim-e2ev2-three\n")
	require.Contains(t, cfg, `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:11001"},{"id":2,"addr":"127.0.0.1:11002"},{"id":3,"addr":"127.0.0.1:11003"}]`)
	require.Contains(t, cfg, "WK_CLUSTER_INITIAL_SLOT_COUNT=3\n")
	require.Contains(t, cfg, "WK_CLUSTER_HASH_SLOT_COUNT=16\n")
	require.Contains(t, cfg, "WK_CLUSTER_SLOT_REPLICA_N=3\n")
	require.Contains(t, cfg, "WK_MANAGER_LISTEN_ADDR=127.0.0.1:14002\n")
}

func TestRenderClusterConfigAppliesOverridesDeterministically(t *testing.T) {
	spec := NodeSpec{
		ID:          1,
		Name:        "node-1",
		DataDir:     "/tmp/wukongim/node-1/data",
		ClusterAddr: "127.0.0.1:11001",
		GatewayAddr: "127.0.0.1:12001",
		APIAddr:     "127.0.0.1:13001",
		ConfigOverrides: map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "8",
			"WK_GATEWAY_SEND_TIMEOUT":    "7s",
		},
	}

	cfg := RenderSingleNodeConfig(spec)

	require.Contains(t, cfg, "WK_CLUSTER_HASH_SLOT_COUNT=8\n")
	require.Contains(t, cfg, "WK_GATEWAY_SEND_TIMEOUT=7s\n")
	require.Less(t, strings.Index(cfg, "WK_CLUSTER_HASH_SLOT_COUNT=8"), strings.Index(cfg, "WK_GATEWAY_SEND_TIMEOUT=7s"))
}

func TestEnvFromConfigPinsRenderedWKValues(t *testing.T) {
	env := envFromConfig("WK_NODE_ID=1\n# comment\n\nWK_GATEWAY_SEND_TIMEOUT=5s\n")

	require.Equal(t, []string{"WK_NODE_ID=1", "WK_GATEWAY_SEND_TIMEOUT=5s"}, env)
}
