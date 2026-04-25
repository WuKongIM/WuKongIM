//go:build e2e

package suite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSingleNodeConfigUsesOfficialWKKeys(t *testing.T) {
	spec := NodeSpec{
		ID:          1,
		Name:        "node-1",
		DataDir:     "/tmp/node-1/data",
		ConfigPath:  "/tmp/node-1/wukongim.conf",
		ClusterAddr: "127.0.0.1:17000",
		GatewayAddr: "127.0.0.1:15100",
		APIAddr:     "127.0.0.1:18080",
	}

	cfg := RenderSingleNodeConfig(spec)
	require.Contains(t, cfg, "WK_NODE_ID=1")
	require.Contains(t, cfg, "WK_NODE_NAME=node-1")
	require.Contains(t, cfg, "WK_NODE_DATA_DIR=/tmp/node-1/data")
	require.Contains(t, cfg, "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:17000")
	require.Contains(t, cfg, "WK_CLUSTER_SLOT_COUNT=1")
	require.Contains(t, cfg, `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:17000"}]`)
	require.Contains(t, cfg, `"address":"127.0.0.1:15100"`)
	require.Contains(t, cfg, `"transport":"gnet"`)
	require.Contains(t, cfg, "WK_API_LISTEN_ADDR=127.0.0.1:18080")
}

func TestRenderSingleNodeConfigIncludesManagerLoopbackAndLogDir(t *testing.T) {
	spec := NodeSpec{
		ID:          1,
		Name:        "node-1",
		DataDir:     "/tmp/node-1/data",
		ConfigPath:  "/tmp/node-1/wukongim.conf",
		ClusterAddr: "127.0.0.1:17001",
		GatewayAddr: "127.0.0.1:15101",
		APIAddr:     "127.0.0.1:18081",
		ManagerAddr: "127.0.0.1:19081",
		LogDir:      "/tmp/node-1/logs",
	}

	cfg := RenderSingleNodeConfig(spec)
	require.Contains(t, cfg, "WK_MANAGER_LISTEN_ADDR=127.0.0.1:19081")
	require.Contains(t, cfg, "WK_MANAGER_AUTH_ON=false")
	require.Contains(t, cfg, "WK_LOG_DIR=/tmp/node-1/logs")
}

func TestRenderThreeNodeClusterConfigIncludesAllNodesAndReplicaCounts(t *testing.T) {
	specs := []NodeSpec{
		{
			ID:          1,
			Name:        "node-1",
			DataDir:     "/tmp/node-1/data",
			ClusterAddr: "127.0.0.1:17001",
			GatewayAddr: "127.0.0.1:15101",
			APIAddr:     "127.0.0.1:18081",
			ManagerAddr: "127.0.0.1:19081",
			LogDir:      "/tmp/node-1/logs",
		},
		{
			ID:          2,
			Name:        "node-2",
			DataDir:     "/tmp/node-2/data",
			ClusterAddr: "127.0.0.1:17002",
			GatewayAddr: "127.0.0.1:15102",
			APIAddr:     "127.0.0.1:18082",
			ManagerAddr: "127.0.0.1:19082",
			LogDir:      "/tmp/node-2/logs",
		},
		{
			ID:          3,
			Name:        "node-3",
			DataDir:     "/tmp/node-3/data",
			ClusterAddr: "127.0.0.1:17003",
			GatewayAddr: "127.0.0.1:15103",
			APIAddr:     "127.0.0.1:18083",
			ManagerAddr: "127.0.0.1:19083",
			LogDir:      "/tmp/node-3/logs",
		},
	}

	cfg := RenderClusterConfig(specs[0], specs)
	require.Contains(t, cfg, "WK_CLUSTER_CONTROLLER_REPLICA_N=3")
	require.Contains(t, cfg, "WK_CLUSTER_SLOT_REPLICA_N=3")
	require.Contains(t, cfg, "WK_CLUSTER_INITIAL_SLOT_COUNT=1")
	require.Contains(t, cfg, "WK_MANAGER_LISTEN_ADDR=127.0.0.1:19081")
	require.Contains(t, cfg, "WK_MANAGER_AUTH_ON=false")
	require.Contains(t, cfg, "WK_LOG_DIR=/tmp/node-1/logs")
	require.Contains(t, cfg, `{"id":2,"addr":"127.0.0.1:17002"}`)
	require.Contains(t, cfg, `{"id":3,"addr":"127.0.0.1:17003"}`)
}

func TestRenderClusterConfigAppliesConfigOverrides(t *testing.T) {
	spec := NodeSpec{
		ID:          1,
		Name:        "node-1",
		DataDir:     "/tmp/node-1/data",
		ClusterAddr: "127.0.0.1:17001",
		GatewayAddr: "127.0.0.1:15101",
		ManagerAddr: "127.0.0.1:19081",
		ConfigOverrides: map[string]string{
			"WK_MANAGER_AUTH_ON": "true",
			"WK_LOG_LEVEL":       "debug",
		},
	}

	cfg := RenderClusterConfig(spec, []NodeSpec{spec})
	require.Contains(t, cfg, "WK_MANAGER_AUTH_ON=true")
	require.NotContains(t, cfg, "WK_MANAGER_AUTH_ON=false")
	require.Contains(t, cfg, "WK_LOG_LEVEL=debug")
}
