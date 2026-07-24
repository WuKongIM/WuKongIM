//go:build e2e

package suite

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
		require.Contains(t, string(cfg), `id = "wukongim-e2e-three"`+"\n")
		require.Contains(t, string(cfg), "nodes = [")
		require.Contains(t, string(cfg), "initial_slot_count = 3\n")
		require.Contains(t, string(cfg), "hash_slot_count = 16\n")
		require.Contains(t, string(cfg), "slot_replica_n = 3\n")
		require.NotContains(t, string(cfg), "[manager]")
		require.Empty(t, node.Spec.ManagerAddr)
		require.Contains(t, string(cfg), `dir = "`+node.Spec.LogDir+`"`)
		require.Contains(t, node.Spec.Env, "WK_NODE_ID="+nodeIDString(node.Spec.ID))
		require.Contains(t, node.Spec.Env, "WK_PLUGIN_ENABLE=false")
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
		require.Contains(t, string(cfg), "[manager]\n")
		require.Contains(t, string(cfg), `listen_addr = "`+node.Spec.ManagerAddr+`"`)
	}
}

func TestStartThreeNodeClusterWritesDynamicJoinTokenWhenConfigured(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", writeFakeNodeBinary(t))

	cluster := New(t).StartThreeNodeCluster(WithDynamicJoinToken("join-secret"))

	require.Len(t, cluster.Nodes, 3)
	for _, node := range cluster.Nodes {
		cfg, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(cfg), `join_token = "join-secret"`+"\n")
		require.Empty(t, cluster.options.nodeConfigOverrides[node.Spec.ID]["WK_CLUSTER_JOIN_TOKEN"])
	}
}

func TestWorkspacePluginSocketStaysOutsideLongArtifactTree(t *testing.T) {
	longArtifactParent := filepath.Join(t.TempDir(), strings.Repeat("long-artifact-parent-", 8))
	workspace := NewWorkspace(t, WithWorkspaceRootDir(longArtifactParent))
	nodeID := ^uint64(0)
	spec := buildNodeSpec(nodeID, PortSet{}, workspace, suiteOptions{})

	legacySocketPath := filepath.Join(spec.DataDir, "run", "plugin.sock")
	require.Greater(t, len([]byte(legacySocketPath)), 100, "test artifact path must reproduce the old length coupling")
	for _, artifactPath := range []string{
		spec.RootDir,
		spec.DataDir,
		spec.LogDir,
		spec.ConfigPath,
		spec.StdoutPath,
		spec.StderrPath,
	} {
		requirePathWithin(t, workspace.RootDir, artifactPath)
	}

	socketPath := spec.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"]
	require.NotEmpty(t, socketPath)
	requirePathOutside(t, workspace.RootDir, socketPath)
	require.LessOrEqual(t, len([]byte(socketPath)), 100)
	require.Equal(t, "n"+nodeIDString(nodeID)+".sock", filepath.Base(socketPath))
}

func TestWorkspacePluginSocketPreservesExplicitOverride(t *testing.T) {
	tests := map[string]string{
		"non-empty": "/explicit/plugin.sock",
		"empty":     "",
	}
	for name, override := range tests {
		t.Run(name, func(t *testing.T) {
			workspace := NewWorkspace(t)
			options := resolveSuiteOptions(WithNodeConfigOverrides(7, map[string]string{
				"WK_PLUGIN_SOCKET_PATH": override,
			}))

			spec := buildNodeSpec(7, PortSet{}, workspace, options)

			actual, ok := spec.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"]
			require.True(t, ok)
			require.Equal(t, override, actual)
		})
	}
}

func TestWorkspacePluginSocketPathsAreUnique(t *testing.T) {
	firstWorkspace := NewWorkspace(t)
	secondWorkspace := NewWorkspace(t)
	options := suiteOptions{}

	firstNode := buildNodeSpec(1, PortSet{}, firstWorkspace, options)
	secondNode := buildNodeSpec(2, PortSet{}, firstWorkspace, options)
	sameNodeOtherWorkspace := buildNodeSpec(1, PortSet{}, secondWorkspace, options)

	paths := []string{
		firstNode.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"],
		secondNode.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"],
		sameNodeOtherWorkspace.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"],
	}
	for _, path := range paths {
		require.NotEmpty(t, path)
	}
	require.Len(t, map[string]struct{}{
		paths[0]: {},
		paths[1]: {},
		paths[2]: {},
	}, len(paths))
}

func TestWorkspacePluginSocketRootIsRemovedAfterCleanup(t *testing.T) {
	artifactParent := t.TempDir()
	var artifactRoot string
	var socketRoot string

	t.Run("workspace lifetime", func(t *testing.T) {
		workspace := NewWorkspace(t, WithWorkspaceRootDir(artifactParent))
		spec := buildNodeSpec(1, PortSet{}, workspace, suiteOptions{})
		artifactRoot = workspace.RootDir
		socketPath := spec.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"]
		require.NotEmpty(t, socketPath)
		socketRoot = filepath.Dir(socketPath)

		require.DirExists(t, artifactRoot)
		require.DirExists(t, socketRoot)
	})

	require.NoDirExists(t, socketRoot)
	require.DirExists(t, artifactParent)
	require.DirExists(t, artifactRoot)
}

func TestWorkspacePluginSocketPathIsRenderedIntoChildEnvironment(t *testing.T) {
	workspace := NewWorkspace(t)
	spec := buildNodeSpec(1, PortSet{}, workspace, suiteOptions{})
	socketPath := spec.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"]
	require.NotEmpty(t, socketPath)

	renderedConfig := RenderSingleNodeConfig(spec)
	require.Contains(t, renderedConfig, "socket_path = "+strconv.Quote(socketPath))

	childEnv := envFromConfig(renderedConfig)
	require.Contains(t, childEnv, "WK_PLUGIN_SOCKET_PATH="+socketPath)
}

func TestWorkspacePluginSocketPathRejectsZeroWorkspace(t *testing.T) {
	require.PanicsWithValue(t, "e2e workspace plugin socket root is empty", func() {
		_ = (Workspace{}).pluginSocketPath(1)
	})
}

func TestWorkspacePluginSocketExplicitOverrideAllowsZeroWorkspace(t *testing.T) {
	const explicitSocketPath = "/explicit/plugin.sock"
	options := resolveSuiteOptions(WithNodeConfigOverrides(1, map[string]string{
		"WK_PLUGIN_SOCKET_PATH": explicitSocketPath,
	}))

	spec := buildNodeSpec(1, PortSet{}, Workspace{}, options)

	require.Equal(t, explicitSocketPath, spec.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"])
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

	require.Contains(t, cfg, `id = "wukongim-e2e-three"`+"\n")
	require.Contains(t, cfg, `seeds = ["127.0.0.1:7011", "127.0.0.1:7012"]`+"\n")
	require.Contains(t, cfg, `advertise_addr = "127.0.0.1:7014"`+"\n")
	require.Contains(t, cfg, `join_token = "join-secret"`+"\n")
	require.Contains(t, cfg, "slot_replica_n = 3\n")
	require.NotContains(t, cfg, "nodes = ")
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
	require.False(t, second.Running())
	_, exited := second.ExitResult()
	require.True(t, exited)
}

func TestStartedNodeRestartReplacesSingleUseProcess(t *testing.T) {
	binaryPath := writeFakeNodeBinary(t)
	first := startFakeNodeProcess(t, binaryPath, "restart-first")
	node := &StartedNode{Spec: first.Spec, Process: first}
	t.Cleanup(func() {
		require.NoError(t, node.Stop())
	})

	require.NoError(t, node.Restart(binaryPath))
	require.NotSame(t, first, node.Process)
	require.False(t, first.Running())
	require.True(t, node.Process.Running())
}

func TestStartedClusterCleanupStopsNodesAppendedAfterRegistration(t *testing.T) {
	binaryPath := writeFakeNodeBinary(t)
	cleanupTB := &recordedCleanupTB{}
	cluster := &StartedCluster{}
	registerStartedClusterCleanup(cleanupTB, cluster)
	require.Len(t, cleanupTB.cleanups, 1)

	first := startFakeNodeProcess(t, binaryPath, "first-cluster-node")
	t.Cleanup(func() {
		if first.Running() {
			_ = first.Stop()
		}
	})
	cluster.Nodes = append(cluster.Nodes, StartedNode{Spec: first.Spec, Process: first})

	second := startFakeNodeProcess(t, binaryPath, "second-cluster-node")
	t.Cleanup(func() {
		if second.Running() {
			_ = second.Stop()
		}
	})
	cluster.Nodes = append(cluster.Nodes, StartedNode{Spec: second.Spec, Process: second})

	cleanupTB.cleanups[0]()

	require.Empty(t, cleanupTB.errors)
	require.False(t, first.Running())
	require.False(t, second.Running())
	_, firstExited := first.ExitResult()
	_, secondExited := second.ExitResult()
	require.True(t, firstExited)
	require.True(t, secondExited)
}

func TestStartedClusterCleanupStopsNodesConcurrently(t *testing.T) {
	cleanupTB := &recordedCleanupTB{}
	cluster := &StartedCluster{}
	registerStartedClusterCleanup(cleanupTB, cluster)
	releasePath := filepath.Join(t.TempDir(), "release")
	termPaths := make([]string, 0, 3)

	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		readyPath := filepath.Join(t.TempDir(), "ready")
		termPath := filepath.Join(t.TempDir(), "term")
		termPaths = append(termPaths, termPath)
		command := exec.Command(os.Args[0], "-test.run=TestStartedClusterCleanupSignalHelper")
		command.Env = append(
			os.Environ(),
			"GO_WANT_CLUSTER_CLEANUP_SIGNAL_HELPER=1",
			"READY_FILE="+readyPath,
			"TERM_FILE="+termPath,
			"RELEASE_FILE="+releasePath,
		)
		process := newCommandNodeProcess(t, command)
		process.Spec.ID = nodeID
		process.StopTimeout = 5 * time.Second
		require.NoError(t, process.Start())
		require.Eventually(t, func() bool {
			_, err := os.Stat(readyPath)
			return err == nil
		}, time.Second, 10*time.Millisecond)
		t.Cleanup(func() {
			if process.Running() {
				_ = process.Stop()
			}
		})
		cluster.Nodes = append(cluster.Nodes, StartedNode{Spec: process.Spec, Process: process})
	}

	cleanupDone := make(chan struct{})
	go func() {
		cleanupTB.cleanups[0]()
		close(cleanupDone)
	}()
	allTerminated := waitForPaths(termPaths, 3*time.Second)
	require.NoError(t, os.WriteFile(releasePath, []byte("release"), 0o600))
	select {
	case <-cleanupDone:
	case <-time.After(3 * time.Second):
		t.Fatal("cluster cleanup did not finish after release")
	}

	require.Empty(t, cleanupTB.errors)
	require.True(t, allTerminated, "cluster cleanup did not signal every node before release")
}

func TestStartedClusterCleanupSignalHelper(t *testing.T) {
	if os.Getenv("GO_WANT_CLUSTER_CLEANUP_SIGNAL_HELPER") != "1" {
		return
	}

	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)
	require.NoError(t, os.WriteFile(os.Getenv("READY_FILE"), nil, 0o600))
	<-term
	require.NoError(t, os.WriteFile(os.Getenv("TERM_FILE"), nil, 0o600))
	for {
		if _, err := os.Stat(os.Getenv("RELEASE_FILE")); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPaths(paths []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allExist := true
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				allExist = false
				break
			}
		}
		if allExist {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestStartedClusterStartStoppedNodeStartsDetachedProcess(t *testing.T) {
	binaryPath := writeFakeNodeBinary(t)
	rootDir := filepath.Join(t.TempDir(), "node-1")
	spec := NodeSpec{
		ID:         1,
		RootDir:    rootDir,
		ConfigPath: filepath.Join(rootDir, "wukongim.toml"),
		StdoutPath: filepath.Join(rootDir, "stdout.log"),
		StderrPath: filepath.Join(rootDir, "stderr.log"),
	}
	require.NoError(t, os.MkdirAll(rootDir, 0o755))
	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte("[node]\nid = 1\n"), 0o644))

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
	require.True(t, node.Process.Running())
	require.Equal(t, spec.ConfigPath, node.Process.Spec.ConfigPath)
}

func TestStartedClusterReconfigureStoppedNodesReplacesConfigEnvironmentAndKeepsExternalEnvironment(t *testing.T) {
	rootDir := t.TempDir()
	specs := make([]NodeSpec, 3)
	for index := range specs {
		nodeID := uint64(index + 1)
		nodeRoot := filepath.Join(rootDir, "node-"+strconv.FormatUint(nodeID, 10))
		require.NoError(t, os.MkdirAll(nodeRoot, 0o755))
		specs[index] = NodeSpec{
			ID:          nodeID,
			RootDir:     nodeRoot,
			DataDir:     filepath.Join(nodeRoot, "data"),
			ConfigPath:  filepath.Join(nodeRoot, "wukongim.toml"),
			ClusterAddr: fmt.Sprintf("127.0.0.1:%d", 7100+index),
			GatewayAddr: fmt.Sprintf("127.0.0.1:%d", 7200+index),
			APIAddr:     fmt.Sprintf("127.0.0.1:%d", 7300+index),
			ManagerAddr: fmt.Sprintf("127.0.0.1:%d", 7400+index),
			ConfigOverrides: map[string]string{
				"WK_BACKUP_RESTORE_MODE": "true",
			},
		}
	}
	for index := range specs {
		rendered := RenderClusterConfig(specs[index], specs)
		require.NoError(t, os.WriteFile(specs[index].ConfigPath, []byte(rendered), 0o644))
		specs[index].Env = append(envFromConfig(rendered), "WUKONGIM_BACKUP_E2E_FILE_ROOT=/repository", "AWS_ACCESS_KEY_ID=e2e")
	}
	cluster := &StartedCluster{Nodes: []StartedNode{{Spec: specs[0]}, {Spec: specs[1]}, {Spec: specs[2]}}}

	require.NoError(t, cluster.ReconfigureStoppedNodes(map[uint64]map[string]string{
		1: {"WK_BACKUP_RESTORE_MODE": "false"},
		2: {"WK_BACKUP_RESTORE_MODE": "false"},
		3: {"WK_BACKUP_RESTORE_MODE": "false"},
	}))

	for _, node := range cluster.Nodes {
		require.Contains(t, node.Spec.Env, "WK_BACKUP_RESTORE_MODE=false")
		require.NotContains(t, node.Spec.Env, "WK_BACKUP_RESTORE_MODE=true")
		require.Contains(t, node.Spec.Env, "WUKONGIM_BACKUP_E2E_FILE_ROOT=/repository")
		require.Contains(t, node.Spec.Env, "AWS_ACCESS_KEY_ID=e2e")
		body, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(body), "restore_mode = false")
	}
}

func startFakeNodeProcess(t *testing.T, binaryPath string, name string) *NodeProcess {
	t.Helper()

	rootDir := filepath.Join(t.TempDir(), name)
	process := &NodeProcess{
		Spec: NodeSpec{
			ID:         1,
			RootDir:    rootDir,
			ConfigPath: filepath.Join(rootDir, "wukongim.toml"),
			StdoutPath: filepath.Join(rootDir, "stdout.log"),
			StderrPath: filepath.Join(rootDir, "stderr.log"),
		},
		BinaryPath: binaryPath,
	}
	require.NoError(t, os.MkdirAll(rootDir, 0o755))
	require.NoError(t, os.WriteFile(process.Spec.ConfigPath, []byte("[node]\nid = 1\n"), 0o644))
	require.NoError(t, process.Start())
	return process
}

func requirePathWithin(t *testing.T, parent, path string) {
	t.Helper()

	rel, err := filepath.Rel(parent, path)
	require.NoError(t, err)
	require.NotEqual(t, "..", rel)
	require.False(t, strings.HasPrefix(rel, ".."+string(os.PathSeparator)), "path %q must stay within %q", path, parent)
}

func requirePathOutside(t *testing.T, parent, path string) {
	t.Helper()

	rel, err := filepath.Rel(parent, path)
	require.NoError(t, err)
	require.True(t, rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)), "path %q must stay outside %q", path, parent)
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
