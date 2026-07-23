//go:build e2e

package suite

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSingleNodeConfigUsesWukongIMTOML(t *testing.T) {
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

	require.Contains(t, cfg, "[node]\n")
	require.Contains(t, cfg, "id = 1\n")
	require.Contains(t, cfg, `data_dir = "/tmp/wukongim/node-1/data"`+"\n")
	require.Contains(t, cfg, "[cluster]\n")
	require.Contains(t, cfg, `listen_addr = "127.0.0.1:11001"`+"\n")
	require.Contains(t, cfg, "initial_slot_count = 1\n")
	require.Contains(t, cfg, "hash_slot_count = 4\n")
	require.Contains(t, cfg, "slot_replica_n = 1\n")
	require.Contains(t, cfg, "[api]\n")
	require.Contains(t, cfg, `listen_addr = "127.0.0.1:13001"`+"\n")
	require.Contains(t, cfg, "metrics_enable = true\n")
	require.Contains(t, cfg, `listeners = [{ address = "127.0.0.1:12001", name = "tcp-wkproto", network = "tcp", protocol = "wkproto", transport = "gnet" }]`)
	require.Contains(t, cfg, "[plugin]\n")
	require.Contains(t, cfg, "enable = false\n")
	require.NotContains(t, cfg, "WK_CLUSTER_SLOT_COUNT")
	require.NotContains(t, cfg, "[manager]")
}

func TestRenderThreeNodeConfigUsesStaticClusterMembership(t *testing.T) {
	nodes := []NodeSpec{
		{ID: 1, DataDir: "/tmp/node-1/data", ClusterAddr: "127.0.0.1:11001", GatewayAddr: "127.0.0.1:12001", APIAddr: "127.0.0.1:13001", ManagerAddr: "127.0.0.1:14001"},
		{ID: 2, DataDir: "/tmp/node-2/data", ClusterAddr: "127.0.0.1:11002", GatewayAddr: "127.0.0.1:12002", APIAddr: "127.0.0.1:13002", ManagerAddr: "127.0.0.1:14002"},
		{ID: 3, DataDir: "/tmp/node-3/data", ClusterAddr: "127.0.0.1:11003", GatewayAddr: "127.0.0.1:12003", APIAddr: "127.0.0.1:13003", ManagerAddr: "127.0.0.1:14003"},
	}

	cfg := RenderClusterConfig(nodes[1], nodes)

	require.Contains(t, cfg, "id = 2\n")
	require.Contains(t, cfg, `id = "wukongim-e2e-three"`+"\n")
	require.Contains(t, cfg, `nodes = [{ addr = "127.0.0.1:11001", id = 1 }, { addr = "127.0.0.1:11002", id = 2 }, { addr = "127.0.0.1:11003", id = 3 }]`)
	require.Contains(t, cfg, "initial_slot_count = 3\n")
	require.Contains(t, cfg, "hash_slot_count = 16\n")
	require.Contains(t, cfg, "slot_replica_n = 3\n")
	require.Contains(t, cfg, `listen_addr = "127.0.0.1:14002"`+"\n")
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
			"WK_PLUGIN_ENABLE":           "true",
		},
	}

	cfg := RenderSingleNodeConfig(spec)

	require.Contains(t, cfg, "hash_slot_count = 8\n")
	require.Contains(t, cfg, `send_timeout = "7s"`+"\n")
	require.Contains(t, cfg, "[plugin]\n")
	require.Contains(t, cfg, "enable = true\n")
	require.Less(t, strings.Index(cfg, "hash_slot_count = 8"), strings.Index(cfg, `send_timeout = "7s"`))
}

func TestRenderSeedJoinNodeConfigDisablesPluginByDefault(t *testing.T) {
	spec := NodeSpec{
		ID:          4,
		DataDir:     "/tmp/node-4/data",
		ClusterAddr: "127.0.0.1:11004",
		GatewayAddr: "127.0.0.1:12004",
		APIAddr:     "127.0.0.1:13004",
	}

	cfg := RenderSeedJoinNodeConfig(spec, SeedJoinNodeConfig{
		Seeds:     []string{"127.0.0.1:11001"},
		JoinToken: "join-secret",
	})

	require.Contains(t, cfg, "[plugin]\n")
	require.Contains(t, cfg, "enable = false\n")
	require.Contains(t, envFromConfig(cfg), "WK_PLUGIN_ENABLE=false")
}

func TestEnvFromConfigPinsRenderedWKValues(t *testing.T) {
	env := envFromConfig("WK_NODE_ID=1\n# comment\n\nWK_GATEWAY_SEND_TIMEOUT=5s\n")

	require.Equal(t, []string{"WK_NODE_ID=1", "WK_GATEWAY_SEND_TIMEOUT=5s"}, env)
}
