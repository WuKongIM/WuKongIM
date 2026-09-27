//go:build e2e

package startup_recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestStartupRecoveryPreservesSnapshotAndSuffixCredentials(t *testing.T) {
	for _, slots := range []int{1, 12} {
		t.Run(fmt.Sprintf("slots-%d", slots), func(t *testing.T) { checkStartupRecovery(t, slots) })
	}
}

func checkStartupRecovery(t *testing.T, physicalSlots int) {
	node := suite.New(t).StartSingleNodeCluster(suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": fmt.Sprint(physicalSlots), "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES": "1000000", "WK_LOG_FORMAT": "json", "WK_LOG_CONSOLE": "false",
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	token := "startup-recovery-synthetic-secret"
	register := func(uid string) {
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": token, "device_flag": int(frame.APP), "device_level": 1}, nil)
		require.NoError(t, err)
	}
	for i := 0; i < 256; i++ {
		register(fmt.Sprintf("startup-user-%03d", i))
	}
	var compact struct {
		Failed int `json:"failed"`
		Items  []struct {
			Success bool   `json:"success"`
			Index   uint64 `json:"after_snapshot_index"`
		} `json:"items"`
	}
	for slotID := 1; slotID <= physicalSlots; slotID++ {
		_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/nodes/1/slots/%d/compact", node.Spec.ManagerAddr, slotID), nil, &compact)
		require.NoError(t, err)
		require.Zero(t, compact.Failed)
		require.Len(t, compact.Items, 1)
		require.True(t, compact.Items[0].Success)
		require.NotZero(t, compact.Items[0].Index)
	}
	register("startup-after-snapshot")
	// Both normal restarts must preserve the suffix as well as snapshot rows.
	var reports []map[string]any
	for attempt := 0; attempt < 2; attempt++ {
		logPath := filepath.Join(node.Spec.LogDir, "app.log")
		before, _ := os.ReadFile(logPath)
		started := time.Now()
		require.NoError(t, node.Restart(node.Process.BinaryPath))
		_, err := node.Process.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
		require.NoError(t, err, node.DumpDiagnostics())
		require.NoError(t, node.Process.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
		for _, uid := range []string{"startup-user-000", "startup-user-127", "startup-user-255", "startup-after-snapshot"} {
			client, err := wkclient.New(wkclient.Config{Addr: node.Spec.GatewayAddr, OperationTimeout: 5 * time.Second})
			require.NoError(t, err)
			_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: uid, Token: token, DeviceID: "startup-check", DeviceFlag: frame.APP})
			require.NoError(t, err)
			require.NoError(t, client.Close())
		}
		after, err := os.ReadFile(logPath)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(after), len(before))
		lines := strings.Split(string(after[len(before):]), "\n")
		stages := map[string]bool{}
		var events []map[string]any
		for _, line := range lines {
			if !strings.Contains(line, "slot.recovery.progress") {
				continue
			}
			require.NotContains(t, line, token)
			var row map[string]any
			if json.Unmarshal([]byte(line), &row) == nil {
				if stage, ok := row["stage"].(string); ok {
					stages[stage] = true
					events = append(events, row)
				}
			}
		}
		for _, stage := range []string{"snapshot_verify", "checkpoint_reuse", "complete"} {
			require.True(t, stages[stage], "missing recovery stage %s: %v", stage, stages)
		}
		require.False(t, stages["snapshot_install"], "certified restart rewrote snapshot data")
		reports = append(reports, map[string]any{"restart": attempt + 1, "elapsed_ms": time.Since(started).Milliseconds(), "authenticated_users": 4, "events": events})
	}
	path := os.Getenv("WK_E2E_STARTUP_RECOVERY_REPORT")
	if path == "" {
		path = filepath.Join(node.Spec.RootDir, "startup-recovery-report.json")
	}
	if physicalSlots != 1 {
		path = strings.TrimSuffix(path, ".json") + fmt.Sprintf("-%d.json", physicalSlots)
	}
	data, err := json.MarshalIndent(map[string]any{"hash_slots": 256, "physical_slots": physicalSlots, "snapshot_index": compact.Items[0].Index, "restarts": reports}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	t.Logf("recovery artifact: %s", path)
}

func TestCheckpointThreeNodeClusterFullRestart(t *testing.T) {
	overrides := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "1", "WK_CLUSTER_SLOT_REPLICA_N": "3", "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES": "1000000", "WK_LOG_FORMAT": "json", "WK_LOG_CONSOLE": "false"}
	cluster := suite.New(t).StartThreeNodeCluster(suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, overrides), suite.WithNodeConfigOverrides(2, overrides), suite.WithNodeConfigOverrides(3, overrides))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	token := "checkpoint-three-node-synthetic-token"
	register := func(uid string) {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": token, "device_flag": int(frame.APP), "device_level": 1}, nil)
		require.NoError(t, err)
	}
	register("checkpoint-before-snapshot")
	for id := uint64(1); id <= 3; id++ {
		var result struct {
			Failed int `json:"failed"`
			Items  []struct {
				Success bool `json:"success"`
			} `json:"items"`
		}
		_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/nodes/%d/slots/1/compact", cluster.MustNode(id).ManagerAddr(), id), nil, &result)
		require.NoError(t, err)
		require.Zero(t, result.Failed)
		require.Len(t, result.Items, 1)
		require.True(t, result.Items[0].Success)
	}
	register("checkpoint-after-snapshot")
	offsets := make(map[uint64]int)
	for id := uint64(1); id <= 3; id++ {
		require.NoError(t, cluster.MustNode(id).Stop())
	}
	for id := uint64(1); id <= 3; id++ {
		data, err := os.ReadFile(filepath.Join(cluster.MustNode(id).Spec.LogDir, "app.log"))
		require.NoError(t, err)
		offsets[id] = len(data)
		require.NoError(t, cluster.StartStoppedNode(id))
	}
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	var reports []map[string]any
	for id := uint64(1); id <= 3; id++ {
		node := cluster.MustNode(id)
		for _, uid := range []string{"checkpoint-before-snapshot", "checkpoint-after-snapshot"} {
			client, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), OperationTimeout: 5 * time.Second})
			require.NoError(t, err)
			_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: uid, Token: token, DeviceID: "three-node-check", DeviceFlag: frame.APP})
			require.NoError(t, err)
			require.NoError(t, client.Close())
		}
		data, err := os.ReadFile(filepath.Join(node.Spec.LogDir, "app.log"))
		require.NoError(t, err)
		lines := string(data[offsets[id]:])
		require.Contains(t, lines, `"stage":"checkpoint_reuse"`)
		require.NotContains(t, lines, `"stage":"snapshot_install"`)
		require.NotContains(t, lines, token)
		reports = append(reports, map[string]any{"node_id": id, "checkpoint_reused": true, "authenticated_users": 2})
	}
	path := os.Getenv("WK_E2E_STARTUP_RECOVERY_REPORT")
	if path == "" {
		path = filepath.Join(cluster.MustNode(1).Spec.RootDir, "checkpoint-three-node.json")
	} else {
		path = strings.TrimSuffix(path, ".json") + "-three-node.json"
	}
	data, err := json.MarshalIndent(map[string]any{"hash_slots": 256, "nodes": reports}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	t.Logf("three-node checkpoint artifact: %s", path)
}

func TestCheckpointFallsBackAfterOlderBinaryWrites(t *testing.T) {
	older := os.Getenv("WK_E2E_STARTUP_OLDER_BINARY")
	if older == "" {
		t.Skip("requires a frozen pre-checkpoint product binary")
	}
	node := suite.New(t).StartSingleNodeCluster(suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "1", "WK_LOG_FORMAT": "json", "WK_LOG_CONSOLE": "false"}))
	current := node.Process.BinaryPath
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	register := func(token string) {
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/token", map[string]any{"uid": "older-writer-user", "token": token, "device_flag": int(frame.APP), "device_level": 1}, nil)
		require.NoError(t, err)
	}
	register("checkpoint-before-older-writer")
	var compact struct {
		Failed int `json:"failed"`
		Items  []struct {
			Success bool `json:"success"`
		} `json:"items"`
	}
	_, err := suite.PostJSON(ctx, "http://"+node.ManagerAddr()+"/manager/nodes/1/slots/1/compact", nil, &compact)
	require.NoError(t, err)
	require.Zero(t, compact.Failed)
	require.Len(t, compact.Items, 1)
	require.True(t, compact.Items[0].Success)
	require.NoError(t, node.Restart(older))
	_, err = node.Process.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
	require.NoError(t, err, node.DumpDiagnostics())
	require.NoError(t, node.Process.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
	register("checkpoint-after-older-writer")
	require.NoError(t, node.Stop())
	logPath := filepath.Join(node.Spec.LogDir, "app.log")
	before, err := os.ReadFile(logPath)
	require.NoError(t, err)
	node.Process = &suite.NodeProcess{Spec: node.Spec, BinaryPath: current}
	require.NoError(t, node.Process.Start())
	_, err = node.Process.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
	require.NoError(t, err, node.DumpDiagnostics())
	require.NoError(t, node.Process.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
	client, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), OperationTimeout: 5 * time.Second})
	require.NoError(t, err)
	_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: "older-writer-user", Token: "checkpoint-after-older-writer", DeviceID: "older-writer-check", DeviceFlag: frame.APP})
	require.NoError(t, err)
	require.NoError(t, client.Close())
	after, err := os.ReadFile(logPath)
	require.NoError(t, err)
	lines := string(after[len(before):])
	require.Contains(t, lines, `"stage":"checkpoint_fallback"`)
	require.Contains(t, lines, `"stage":"snapshot_install"`)
	require.NotContains(t, lines, `"stage":"checkpoint_reuse"`)
	path := os.Getenv("WK_E2E_STARTUP_RECOVERY_REPORT")
	if path == "" {
		path = filepath.Join(node.Spec.RootDir, "older-writer.json")
	} else {
		path = strings.TrimSuffix(path, ".json") + "-older-writer.json"
	}
	require.NoError(t, os.WriteFile(path, []byte("{\"older_writer_fallback\":true,\"rotated_credential_authenticated\":true}\n"), 0600))
	t.Logf("older-writer artifact: %s", path)
}

func TestCheckpointMembershipChangeRestart(t *testing.T) {
	const joinToken = "checkpoint-membership-join"
	overrides := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "1", "WK_CLUSTER_SLOT_REPLICA_N": "3", "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES": "1000000", "WK_LOG_FORMAT": "json", "WK_LOG_CONSOLE": "false"}
	cluster := suite.New(t).StartThreeNodeCluster(suite.WithManagerHTTP(), suite.WithDynamicJoinToken(joinToken), suite.WithNodeConfigOverrides(1, overrides), suite.WithNodeConfigOverrides(2, overrides), suite.WithNodeConfigOverrides(3, overrides), suite.WithNodeConfigOverrides(4, overrides))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	const token = "checkpoint-membership-token"
	_, err = suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": "checkpoint-membership-user", "token": token, "device_flag": int(frame.APP), "device_level": 1}, nil)
	require.NoError(t, err)
	for id := uint64(1); id <= 3; id++ {
		var result struct {
			Failed int `json:"failed"`
			Items  []struct {
				Success bool `json:"success"`
			} `json:"items"`
		}
		_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/nodes/%d/slots/1/compact", cluster.MustNode(id).ManagerAddr(), id), nil, &result)
		require.NoError(t, err)
		require.Zero(t, result.Failed)
		require.Len(t, result.Items, 1)
		require.True(t, result.Items[0].Success)
	}
	manager := cluster.ManagerClient(t, 1)
	cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{NodeID: 4, Seeds: cluster.SeedAddrs(), JoinToken: joinToken})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)
	require.Eventually(t, func() bool {
		nodes, err := manager.ListNodes(ctx)
		if err != nil {
			return false
		}
		for _, node := range nodes.Items {
			if node.NodeID == 4 {
				return node.Membership.Schedulable && node.Health.Fresh && node.Health.Status == "alive" && node.Health.RuntimeReady
			}
		}
		return false
	}, 45*time.Second, 100*time.Millisecond)
	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1)
	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created)
	manager.EventuallyOnboardingSafe(t, 4, 45*time.Second)
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	slots := manager.MustSlots(t)
	require.Len(t, slots, 1)
	peers := append([]uint64(nil), slots[0].Assignment.DesiredPeers...)
	require.Contains(t, peers, uint64(4))
	offsets := map[uint64]int{}
	for id := uint64(1); id <= 4; id++ {
		require.NoError(t, cluster.MustNode(id).Stop())
	}
	for id := uint64(1); id <= 4; id++ {
		if id == 4 {
			// Start the static seed cluster before its dynamically joined node.
			for seed := uint64(1); seed <= 3; seed++ {
				node := cluster.MustNode(seed)
				_, err := node.Process.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
				require.NoError(t, err, node.DumpDiagnostics())
				require.NoError(t, node.Process.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
			}
		}
		data, err := os.ReadFile(filepath.Join(cluster.MustNode(id).Spec.LogDir, "app.log"))
		require.NoError(t, err)
		offsets[id] = len(data)
		require.NoError(t, cluster.StartStoppedNode(id))
	}
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	for _, id := range peers {
		node := cluster.MustNode(id)
		client, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), OperationTimeout: 5 * time.Second})
		require.NoError(t, err)
		_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: "checkpoint-membership-user", Token: token, DeviceID: "membership-check", DeviceFlag: frame.APP})
		require.NoError(t, err)
		require.NoError(t, client.Close())
		data, err := os.ReadFile(filepath.Join(node.Spec.LogDir, "app.log"))
		require.NoError(t, err)
		lines := string(data[offsets[id]:])
		require.Contains(t, lines, `"stage":"checkpoint_reuse"`)
		require.NotContains(t, lines, `"stage":"snapshot_install"`)
	}
	path := os.Getenv("WK_E2E_STARTUP_RECOVERY_REPORT")
	if path == "" {
		path = filepath.Join(cluster.MustNode(1).Spec.RootDir, "checkpoint-membership.json")
	} else {
		path = strings.TrimSuffix(path, ".json") + "-membership.json"
	}
	data, err := json.MarshalIndent(map[string]any{"hash_slots": 256, "restarted_nodes": 4, "current_replicas": peers, "authenticated_replicas": len(peers), "checkpoint_reused": true}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	t.Logf("membership checkpoint artifact: %s", path)
}

// An older multi-Slot database must recover once and retain every independent
// checkpoint, including when a neighboring legacy Slot has no snapshot anchor.
func TestCheckpointMultiSlotOlderUpgrade(t *testing.T) {
	older := os.Getenv("WK_E2E_STARTUP_OLDER_BINARY")
	if older == "" {
		t.Skip("requires a frozen pre-checkpoint product binary")
	}
	for _, snapshotSlots := range []int{12, 11} {
		t.Run(fmt.Sprintf("snapshots-%d", snapshotSlots), func(t *testing.T) {
			node := suite.New(t).StartSingleNodeCluster(suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, map[string]string{
				"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES": "1000000", "WK_LOG_FORMAT": "json", "WK_LOG_CONSOLE": "false",
			}))
			candidate := node.Process.BinaryPath
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			require.NoError(t, node.Restart(older))
			_, err := node.Process.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
			require.NoError(t, err, node.DumpDiagnostics())
			require.NoError(t, node.Process.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
			token := "multi-slot-older-upgrade-synthetic-token"
			registerAll := func() {
				for i := 0; i < 256; i++ {
					_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/token", map[string]any{"uid": fmt.Sprintf("upgrade-user-%03d", i), "token": token, "device_flag": int(frame.APP), "device_level": 1}, nil)
					require.NoError(t, err)
				}
			}
			registerAll()
			for id := 1; id <= snapshotSlots; id++ {
				var result struct {
					Failed int `json:"failed"`
					Items  []struct {
						Success bool `json:"success"`
					} `json:"items"`
				}
				_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/nodes/1/slots/%d/compact", node.ManagerAddr(), id), nil, &result)
				require.NoError(t, err)
				require.Zero(t, result.Failed)
				require.Len(t, result.Items, 1)
				require.True(t, result.Items[0].Success)
			}
			token += "-suffix"
			registerAll()
			var reports []map[string]any
			for attempt := 0; attempt < 3; attempt++ {
				logPath := filepath.Join(node.Spec.LogDir, "app.log")
				before, err := os.ReadFile(logPath)
				require.NoError(t, err)
				require.NoError(t, node.Restart(candidate))
				_, err = node.Process.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
				require.NoError(t, err, node.DumpDiagnostics())
				require.NoError(t, node.Process.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
				for i := 0; i < 256; i++ {
					client, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), OperationTimeout: 5 * time.Second})
					require.NoError(t, err)
					_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: fmt.Sprintf("upgrade-user-%03d", i), Token: token, DeviceID: "upgrade-check", DeviceFlag: frame.APP})
					require.NoError(t, err, node.DumpDiagnostics())
					require.NoError(t, client.Close())
				}
				after, err := os.ReadFile(logPath)
				require.NoError(t, err)
				lines := string(after[len(before):])
				reuse := strings.Count(lines, `"stage":"checkpoint_reuse"`)
				installs := strings.Count(lines, `"stage":"snapshot_installed"`)
				if attempt == 0 {
					require.Equal(t, snapshotSlots, installs)
				} else {
					require.Equal(t, snapshotSlots, reuse, "neighboring Slot invalidated a proven checkpoint")
					require.Zero(t, installs)
				}
				reports = append(reports, map[string]any{"restart": attempt, "reused": reuse, "installed": installs, "authenticated_users": 256})
				token += "-rotated"
				registerAll()
			}
			path := os.Getenv("WK_E2E_STARTUP_RECOVERY_REPORT")
			if path == "" {
				path = filepath.Join(node.Spec.RootDir, "startup-recovery.json")
			}
			path = strings.TrimSuffix(path, ".json") + fmt.Sprintf("-upgrade-%d.json", snapshotSlots)
			data, err := json.MarshalIndent(map[string]any{"hash_slots": 256, "physical_slots": 12, "snapshot_slots": snapshotSlots, "restarts": reports}, "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, data, 0600))
			t.Logf("multi-Slot upgrade artifact: %s", path)
		})
	}
}
