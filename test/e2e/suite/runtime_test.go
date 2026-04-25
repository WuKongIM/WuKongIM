//go:build e2e

package suite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewWorkspaceCreatesNodeScopedPaths(t *testing.T) {
	workspace := NewWorkspace(t)

	require.DirExists(t, workspace.RootDir)
	require.Equal(t, filepath.Join(workspace.RootDir, "node-1"), workspace.NodeRootDir(1))
	require.Equal(t, filepath.Join(workspace.RootDir, "node-1", "data"), workspace.NodeDataDir(1))
	require.Equal(t, filepath.Join(workspace.RootDir, "node-1", "wukongim.conf"), workspace.NodeConfigPath(1))
	require.Equal(t, filepath.Join(workspace.RootDir, "node-1", "stdout.log"), workspace.NodeStdoutPath(1))
	require.Equal(t, filepath.Join(workspace.RootDir, "node-1", "stderr.log"), workspace.NodeStderrPath(1))
}

func TestNewWorkspaceCreatesNodeScopedLogDirPaths(t *testing.T) {
	workspace := NewWorkspace(t)

	require.Equal(t, filepath.Join(workspace.RootDir, "node-1", "logs"), workspace.NodeLogDir(1))
}

func TestSuiteConstructorPreservesWorkspaceLayout(t *testing.T) {
	fakeBinary := writeFakeNodeBinary(t)
	t.Setenv("WK_E2E_BINARY", fakeBinary)

	suite := New(t)

	require.Equal(t, fakeBinary, suite.binaryPath)
	require.DirExists(t, suite.workspace.RootDir)
	require.Equal(t, filepath.Join(suite.workspace.RootDir, "node-1"), suite.workspace.NodeRootDir(1))
	require.Equal(t, filepath.Join(suite.workspace.RootDir, "node-1", "data"), suite.workspace.NodeDataDir(1))
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

func TestStartedClusterDumpDiagnosticsIncludesReadyzAndSlotBodies(t *testing.T) {
	cluster := StartedCluster{
		Nodes: []StartedNode{
			{
				Spec: NodeSpec{
					ID:         1,
					ConfigPath: "/tmp/node-1/wukongim.conf",
					StdoutPath: "/tmp/node-1/stdout.log",
					StderrPath: "/tmp/node-1/stderr.log",
				},
			},
		},
		lastReadyz: map[uint64]HTTPObservation{
			1: {StatusCode: 503, Body: `{"status":"not_ready"}`},
		},
		lastSlotBodies: map[uint32]string{
			1: `{"runtime":{"leader_id":2}}`,
		},
	}

	dump := cluster.DumpDiagnostics()
	require.Contains(t, dump, `{"status":"not_ready"}`)
	require.Contains(t, dump, `leader_id`)
}

func TestStartThreeNodeClusterWritesThreeNodeScopedConfigs(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", writeFakeNodeBinary(t))

	suite := New(t)
	cluster := suite.StartThreeNodeCluster()

	require.Len(t, cluster.Nodes, 3)
	for _, node := range cluster.Nodes {
		require.FileExists(t, node.Spec.ConfigPath)

		cfg, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(cfg), "WK_CLUSTER_CONTROLLER_REPLICA_N=3")
		require.Contains(t, string(cfg), "WK_CLUSTER_SLOT_REPLICA_N=3")
		require.Contains(t, string(cfg), "WK_MANAGER_AUTH_ON=false")
		require.Contains(t, string(cfg), "WK_LOG_DIR="+node.Spec.LogDir)
	}
}

func TestStartThreeNodeClusterWritesCustomNodeLogDirsIntoConfigs(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", writeFakeNodeBinary(t))

	logRoot := filepath.Join(t.TempDir(), "cluster-logs")
	workspaceRoot := filepath.Join(t.TempDir(), "cluster-artifacts")
	suite := New(t)
	cluster := suite.StartThreeNodeCluster(
		WithWorkspaceRootDir(workspaceRoot),
		WithNodeLogRootDir(logRoot),
	)

	for _, node := range cluster.Nodes {
		require.Equal(t, workspaceRoot, filepath.Dir(filepath.Dir(node.Spec.RootDir)))
		require.Equal(t, filepath.Join(logRoot, filepath.Base(filepath.Dir(node.Spec.RootDir)), nodeDirName(node.Spec.ID)), node.Spec.LogDir)
		require.DirExists(t, node.Spec.LogDir)

		cfg, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(cfg), "WK_LOG_DIR="+node.Spec.LogDir)
	}
}

func TestStartThreeNodeClusterAppliesNodeConfigOverrides(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", writeFakeNodeBinary(t))

	suite := New(t)
	cluster := suite.StartThreeNodeCluster(
		WithNodeConfigOverrides(2, map[string]string{
			"WK_MANAGER_AUTH_ON": "true",
			"WK_LOG_LEVEL":       "debug",
		}),
	)

	node := cluster.MustNode(2)
	cfg, err := os.ReadFile(node.Spec.ConfigPath)
	require.NoError(t, err)
	require.Contains(t, string(cfg), "WK_MANAGER_AUTH_ON=true")
	require.Contains(t, string(cfg), "WK_LOG_LEVEL=debug")
}

func writeFakeNodeBinary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fake-wukongim.sh")
	script := "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}
