//go:build e2e

package suite

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartThreeNodeClusterWritesWukongIMStaticConfigs(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", writeFakeNodeBinary(t))

	cluster := New(t).StartThreeNodeCluster()

	require.Len(t, cluster.Nodes, 3)
	for _, node := range cluster.Nodes {
		require.FileExists(t, node.Spec.ConfigPath)

		cfg, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(cfg), "WK_CLUSTER_ID=wukongim-e2ev2-three\n")
		require.Contains(t, string(cfg), "WK_CLUSTER_NODES=")
		require.Contains(t, string(cfg), "WK_CLUSTER_INITIAL_SLOT_COUNT=3\n")
		require.Contains(t, string(cfg), "WK_CLUSTER_HASH_SLOT_COUNT=16\n")
		require.Contains(t, string(cfg), "WK_CLUSTER_SLOT_REPLICA_N=3\n")
		require.NotContains(t, string(cfg), "WK_MANAGER_LISTEN_ADDR")
		require.Empty(t, node.Spec.ManagerAddr)
		require.Contains(t, string(cfg), "WK_LOG_DIR="+node.Spec.LogDir)
		require.Contains(t, node.Spec.Env, "WK_NODE_ID="+nodeIDString(node.Spec.ID))
	}
}

func TestStartThreeNodeClusterWritesManagerConfigWhenEnabled(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", writeFakeNodeBinary(t))

	cluster := New(t).StartThreeNodeCluster(WithManagerHTTP())

	require.Len(t, cluster.Nodes, 3)
	for _, node := range cluster.Nodes {
		require.NotEmpty(t, node.Spec.ManagerAddr)
		cfg, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(cfg), "WK_MANAGER_LISTEN_ADDR="+node.Spec.ManagerAddr)
	}
}

func TestStartThreeNodeClusterWritesDynamicJoinTokenWhenConfigured(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", writeFakeNodeBinary(t))

	cluster := New(t).StartThreeNodeCluster(WithDynamicJoinToken("join-secret"))

	require.Len(t, cluster.Nodes, 3)
	for _, node := range cluster.Nodes {
		cfg, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(cfg), "WK_CLUSTER_JOIN_TOKEN=join-secret\n")
		require.Empty(t, cluster.options.nodeConfigOverrides[node.Spec.ID]["WK_CLUSTER_JOIN_TOKEN"])
	}
}

func TestRenderSeedJoinNodeConfigOmitsStaticClusterNodes(t *testing.T) {
	spec := NodeSpec{
		ID:          4,
		DataDir:     t.TempDir(),
		ClusterAddr: "127.0.0.1:7014",
		APIAddr:     "127.0.0.1:7024",
		GatewayAddr: "127.0.0.1:7034",
		LogDir:      t.TempDir(),
	}

	cfg := RenderSeedJoinNodeConfig(spec, SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     []string{"127.0.0.1:7011", "127.0.0.1:7012"},
		JoinToken: "join-secret",
	})

	require.Contains(t, cfg, "WK_CLUSTER_ID=wukongim-e2ev2-three\n")
	require.Contains(t, cfg, `WK_CLUSTER_SEEDS=["127.0.0.1:7011","127.0.0.1:7012"]`+"\n")
	require.Contains(t, cfg, "WK_CLUSTER_ADVERTISE_ADDR=127.0.0.1:7014\n")
	require.Contains(t, cfg, "WK_CLUSTER_JOIN_TOKEN=join-secret\n")
	require.Contains(t, cfg, "WK_CLUSTER_SLOT_REPLICA_N=3\n")
	require.NotContains(t, cfg, "WK_CLUSTER_NODES=")
}

func TestStartedClusterSeedAddrsAreSortedByNodeID(t *testing.T) {
	cluster := StartedCluster{
		Nodes: []StartedNode{
			{Spec: NodeSpec{ID: 3, ClusterAddr: "node-3"}},
			{Spec: NodeSpec{ID: 1, ClusterAddr: "node-1"}},
			{Spec: NodeSpec{ID: 2, ClusterAddr: "node-2"}},
		},
	}

	require.Equal(t, []string{"node-1", "node-2", "node-3"}, cluster.SeedAddrs())
}

func TestSameSlotAssignmentsIgnoresOrdering(t *testing.T) {
	a := []SlotDTO{
		{SlotID: 2, Assignment: SlotAssignmentDTO{DesiredPeers: []uint64{1, 2, 3}, PreferredLeaderID: 2, ConfigEpoch: 9}},
		{SlotID: 1, Assignment: SlotAssignmentDTO{DesiredPeers: []uint64{1, 2, 3}, PreferredLeaderID: 1, ConfigEpoch: 8}},
	}
	b := []SlotDTO{
		{SlotID: 1, Assignment: SlotAssignmentDTO{DesiredPeers: []uint64{1, 2, 3}, PreferredLeaderID: 1, ConfigEpoch: 8}},
		{SlotID: 2, Assignment: SlotAssignmentDTO{DesiredPeers: []uint64{1, 2, 3}, PreferredLeaderID: 2, ConfigEpoch: 9}},
	}

	require.True(t, SameSlotAssignments(a, b))

	b[1].Assignment.DesiredPeers = []uint64{1, 2, 4}
	require.False(t, SameSlotAssignments(a, b))
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

func TestStartedClusterWaitHTTPReadyRecordsReadyzObservations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/readyz", r.URL.Path)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	cluster := StartedCluster{
		Nodes: []StartedNode{
			{Spec: NodeSpec{ID: 1, APIAddr: strings.TrimPrefix(server.URL, "http://")}},
			{Spec: NodeSpec{ID: 2, APIAddr: strings.TrimPrefix(server.URL, "http://")}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx))

	require.Equal(t, HTTPObservation{StatusCode: http.StatusOK, Body: `{"status":"ok"}`}, cluster.lastReadyz[1])
	require.Equal(t, HTTPObservation{StatusCode: http.StatusOK, Body: `{"status":"ok"}`}, cluster.lastReadyz[2])
}

func TestStartedNodeCleanupStopsCurrentProcessAfterRestart(t *testing.T) {
	binaryPath := writeFakeNodeBinary(t)
	first := startFakeNodeProcess(t, binaryPath, "first")
	defer func() { _ = first.Stop() }()
	second := startFakeNodeProcess(t, binaryPath, "second")
	defer func() { _ = second.Stop() }()

	node := &StartedNode{Spec: first.Spec, Process: first}
	cleanupTB := &recordedCleanupTB{}
	registerStartedNodeCleanup(cleanupTB, node)

	node.Process = second
	for _, cleanup := range cleanupTB.cleanups {
		cleanup()
	}

	require.Empty(t, cleanupTB.errors)
	require.NotNil(t, second.Cmd.ProcessState)
}

func TestStartedClusterStartStoppedNodeStartsDetachedProcess(t *testing.T) {
	binaryPath := writeFakeNodeBinary(t)
	rootDir := filepath.Join(t.TempDir(), "node-1")
	spec := NodeSpec{
		ID:         1,
		RootDir:    rootDir,
		ConfigPath: filepath.Join(rootDir, "wukongim.conf"),
		StdoutPath: filepath.Join(rootDir, "stdout.log"),
		StderrPath: filepath.Join(rootDir, "stderr.log"),
	}
	require.NoError(t, os.MkdirAll(rootDir, 0o755))
	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte("WK_NODE_ID=1\n"), 0o644))

	cluster := &StartedCluster{
		Nodes:      []StartedNode{{Spec: spec}},
		binaryPath: binaryPath,
	}

	require.NoError(t, cluster.StartStoppedNode(1))
	defer func() { _ = cluster.MustNode(1).Stop() }()

	node := cluster.MustNode(1)
	require.NotNil(t, node.Process)
	require.NotNil(t, node.Process.Cmd)
	require.NotNil(t, node.Process.Cmd.Process)
	require.Nil(t, node.Process.Cmd.ProcessState)
	require.Equal(t, spec.ConfigPath, node.Process.Spec.ConfigPath)
}

func startFakeNodeProcess(t *testing.T, binaryPath string, name string) *NodeProcess {
	t.Helper()

	rootDir := filepath.Join(t.TempDir(), name)
	process := &NodeProcess{
		Spec: NodeSpec{
			ID:         1,
			RootDir:    rootDir,
			ConfigPath: filepath.Join(rootDir, "wukongim.conf"),
			StdoutPath: filepath.Join(rootDir, "stdout.log"),
			StderrPath: filepath.Join(rootDir, "stderr.log"),
		},
		BinaryPath: binaryPath,
	}
	require.NoError(t, os.MkdirAll(rootDir, 0o755))
	require.NoError(t, os.WriteFile(process.Spec.ConfigPath, []byte("WK_NODE_ID=1\n"), 0o644))
	require.NoError(t, process.Start())
	return process
}

type recordedCleanupTB struct {
	cleanups []func()
	errors   []string
	failed   bool
}

func (t *recordedCleanupTB) Helper() {}

func (t *recordedCleanupTB) Cleanup(cleanup func()) {
	t.cleanups = append(t.cleanups, cleanup)
}

func (t *recordedCleanupTB) Errorf(format string, args ...any) {
	t.errors = append(t.errors, fmt.Sprintf(format, args...))
}

func (t *recordedCleanupTB) FailNow() {
	t.failed = true
}

func writeFakeNodeBinary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fake-wukongim.sh")
	script := "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}
