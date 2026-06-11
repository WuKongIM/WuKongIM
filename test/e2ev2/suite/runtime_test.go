//go:build e2e

package suite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartThreeNodeClusterWritesWukongIMV2StaticConfigs(t *testing.T) {
	t.Setenv("WK_E2EV2_BINARY", writeFakeNodeBinary(t))

	cluster := New(t).StartThreeNodeCluster()

	require.Len(t, cluster.Nodes, 3)
	for _, node := range cluster.Nodes {
		require.FileExists(t, node.Spec.ConfigPath)

		cfg, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(cfg), "WK_CLUSTER_ID=wukongimv2-e2ev2-three\n")
		require.Contains(t, string(cfg), "WK_CLUSTER_NODES=")
		require.Contains(t, string(cfg), "WK_CLUSTER_INITIAL_SLOT_COUNT=3\n")
		require.Contains(t, string(cfg), "WK_CLUSTER_HASH_SLOT_COUNT=16\n")
		require.Contains(t, string(cfg), "WK_CLUSTER_SLOT_REPLICA_N=3\n")
		require.Contains(t, string(cfg), "WK_LOG_DIR="+node.Spec.LogDir)
		require.Contains(t, node.Spec.Env, "WK_NODE_ID="+nodeIDString(node.Spec.ID))
	}
}

func TestStartedClusterNodeLookupByID(t *testing.T) {
	cluster := StartedCluster{
		Nodes: []StartedNode{
			{Spec: NodeSpec{ID: 1}},
			{Spec: NodeSpec{ID: 2}},
		},
	}

	node, ok := cluster.Node(2)
	require.True(t, ok)
	require.Equal(t, uint64(2), node.Spec.ID)
	require.Equal(t, uint64(1), cluster.MustNode(1).Spec.ID)
}

func writeFakeNodeBinary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fake-wukongimv2.sh")
	script := "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}
