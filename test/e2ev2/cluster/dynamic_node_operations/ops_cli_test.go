//go:build e2e

package dynamic_node_operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestWKCLINodeOperationsLifecycleWithTraffic(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-stage11-ops-token"
	overrides := readinessOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
		suite.WithNodeConfigOverrides(4, overrides),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	eventuallyNodeHealthFresh(t, cluster, manager, 1, 120*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 2, 120*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 3, 120*time.Second)

	traffic := startTrafficWorker(t, cluster, cluster.MustNode(1), "stage11-ops")
	defer stopTrafficWorker(t, traffic)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	require.NotNil(t, node4)
	manager.EventuallyNodeJoinState(t, 4, "joining", 30*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 30*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 4, 120*time.Second)
	requireNodeNotSchedulable(t, cluster, manager, 4)

	contextDir := t.TempDir()
	runWKCLI(t, contextDir, "context", "add", "ops", "--server", "http://"+cluster.MustNode(1).ManagerAddr(), "--select")
	eventuallyWKCLIContains(t, contextDir, 30*time.Second,
		[]string{"node", "ls", "--context", "ops"},
		[]string{"node=4", "join_state=joining", "schedulable=false", "health=fresh/alive", "control_rev="},
		cluster.DumpDiagnostics,
	)

	eventuallyWKCLIContains(t, contextDir, 30*time.Second,
		[]string{"node", "activate", "4", "--context", "ops"},
		[]string{"node=4", "join_state=active"},
		cluster.DumpDiagnostics,
	)
	manager.EventuallyNodeJoinState(t, 4, "active", 30*time.Second)
	eventuallyNodeSchedulable(t, cluster, manager, 4, 120*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	onboardingOut := eventuallyWKCLIContains(t, contextDir, 60*time.Second,
		[]string{"node", "onboarding", "start", "4", "--context", "ops", "--max-slot-moves", "1", "--json"},
		[]string{`"created":`, `"target_node_id": 4`},
		cluster.DumpDiagnostics,
	)
	require.GreaterOrEqual(t, requireJSONUintField(t, onboardingOut, "created"), uint64(1), onboardingOut)
	require.Equal(t, uint64(4), requireJSONUintField(t, onboardingOut, "target_node_id"), onboardingOut)
	manager.EventuallyOnboardingSafe(t, 4, 240*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	eventuallyWKCLIContains(t, contextDir, 90*time.Second,
		[]string{"node", "scale-in", "start", "4", "--context", "ops"},
		[]string{"node=4", "join_state=leaving"},
		cluster.DumpDiagnostics,
	)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 30*time.Second)

	eventuallyWKCLIContains(t, contextDir, 30*time.Second,
		[]string{"node", "scale-in", "drain", "4", "--context", "ops", "--draining=true"},
		[]string{"draining=true", "gateway_sessions=", "active_online=", "total_online=", "pending_activations=", "unknown="},
		cluster.DumpDiagnostics,
	)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	eventuallyWKCLIContains(t, contextDir, 60*time.Second,
		[]string{"node", "scale-in", "plan", "4", "--context", "ops", "--max-slot-moves", "1", "--json"},
		[]string{`"node_id": 4`, `"blocked_by_status": false`, `"source_node_id": 4`, `"candidates": [`},
		cluster.DumpDiagnostics,
	)
	advanceOut := eventuallyWKCLIContains(t, contextDir, 60*time.Second,
		[]string{"node", "scale-in", "advance", "4", "--context", "ops", "--max-slot-moves", "1", "--json"},
		[]string{`"node_id": 4`, `"created":`, `"state_revision":`},
		cluster.DumpDiagnostics,
	)
	requireJSONFieldPresent(t, advanceOut, "created")
	require.Equal(t, uint64(4), requireJSONUintField(t, advanceOut, "node_id"), advanceOut)

	drained := eventuallyScaleInSlotsDrained(t, cluster, manager, 4, 240*time.Second)
	require.False(t, drained.BlockedBySlots, "status=%#v", drained)
	require.False(t, drained.BlockedByTasks, "status=%#v", drained)
	require.False(t, drained.BlockedByControlRevision, "status=%#v", drained)
	require.Zero(t, drained.SlotReplicaCount, "status=%#v", drained)
	safe := manager.EventuallyScaleInSafeToRemove(t, 4, 150*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v", safe)
	statusOut := eventuallyWKCLIContains(t, contextDir, 30*time.Second,
		[]string{"node", "scale-in", "status", "4", "--context", "ops"},
		[]string{"safe_to_remove=true", "control_rev=", "gateway_sessions=0", "blocked_reasons="},
		cluster.DumpDiagnostics,
	)
	require.Contains(t, statusOut, "health=")

	eventuallyWKCLIContains(t, contextDir, 60*time.Second,
		[]string{"node", "scale-in", "remove", "4", "--context", "ops"},
		[]string{"node=4", "join_state=removed"},
		cluster.DumpDiagnostics,
	)
	manager.EventuallyNodeJoinState(t, 4, "removed", 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)
}

func TestIsRetryableWKCLIResult(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		result wkcliResult
		want   bool
	}{
		{
			name: "context deadline exceeded",
			result: wkcliResult{
				stderr: `call manager POST http://127.0.0.1:56353/manager/nodes/4/scale-in/remove: Post "http://127.0.0.1:56353/manager/nodes/4/scale-in/remove": context deadline exceeded`,
				err:    errors.New("exit status 3"),
			},
			want: true,
		},
		{
			name: "client timeout awaiting headers",
			result: wkcliResult{
				stderr: `Client.Timeout exceeded while awaiting headers`,
				err:    errors.New("exit status 3"),
			},
			want: true,
		},
		{
			name: "connection refused",
			result: wkcliResult{
				stderr: `dial tcp 127.0.0.1:56353: connect: connection refused`,
				err:    errors.New("exit status 3"),
			},
			want: true,
		},
		{
			name: "io timeout",
			result: wkcliResult{
				stderr: `read tcp 127.0.0.1:56123->127.0.0.1:56353: i/o timeout`,
				err:    errors.New("exit status 3"),
			},
			want: true,
		},
		{
			name: "controller proposal invalid state",
			result: wkcliResult{
				stderr: `manager API status 500: {"error":"internal_error","message":"controllerv2/raft: proposal rejected at index 188: invalid_state"}`,
				err:    errors.New("exit status 2"),
			},
			want: true,
		},
		{
			name: "standalone invalid state",
			result: wkcliResult{
				stderr: `manager API status 500: {"error":"internal_error","message":"invalid_state"}`,
				err:    errors.New("exit status 2"),
			},
			want: false,
		},
		{
			name: "unrelated proposal rejected",
			result: wkcliResult{
				stderr: `manager API status 500: {"error":"internal_error","message":"message proposal rejected by channel runtime"}`,
				err:    errors.New("exit status 2"),
			},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isRetryableWKCLIResult(tc.result), "result=%#v", tc.result)
		})
	}
}

func readinessOverrides() map[string]string {
	return map[string]string{
		"WK_BENCH_API_ENABLE":                    "true",
		"WK_METRICS_ENABLE":                      "true",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": "500ms",
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":      "30s",
		"WK_DELIVERY_ENABLE":                     "true",
	}
}

type trafficWorker struct {
	client *suite.WKProtoClient
	stop   chan struct{}
	done   chan struct{}
	sent   atomic.Uint64
	errs   atomic.Uint64

	mu      sync.Mutex
	lastErr error
}

func startTrafficWorker(t testing.TB, cluster *suite.StartedCluster, node *suite.StartedNode, prefix string) *trafficWorker {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	uid := prefix + "-sender"
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), node.DumpDiagnostics())

	worker := &trafficWorker{client: client, stop: make(chan struct{}), done: make(chan struct{})}
	channelID := prefix + "-recipient"
	go func() {
		defer close(worker.done)
		defer func() { _ = client.Close() }()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for seq := uint64(1); ; seq++ {
			select {
			case <-worker.stop:
				return
			case <-ticker.C:
			}
			msgNo := fmt.Sprintf("%s-%06d", prefix, seq)
			if err := client.SendFrame(&frame.SendPacket{
				ChannelID:   channelID,
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   seq,
				ClientMsgNo: msgNo,
				Payload:     []byte(msgNo),
			}); err != nil {
				worker.recordErr(err)
				continue
			}
			ack, err := client.ReadSendAck()
			if err != nil {
				worker.recordErr(err)
				continue
			}
			if ack.ReasonCode != frame.ReasonSuccess {
				worker.recordErr(fmt.Errorf("sendack reason=%v seq=%d msg_no=%s", ack.ReasonCode, seq, msgNo))
				continue
			}
			worker.sent.Add(1)
		}
	}()
	requireTrafficProgress(t, cluster, worker, 2, 10*time.Second)
	return worker
}

func stopTrafficWorker(t testing.TB, worker *trafficWorker) {
	t.Helper()
	if worker == nil {
		return
	}
	close(worker.stop)
	select {
	case <-worker.done:
	case <-time.After(5 * time.Second):
		t.Fatal("traffic worker did not stop")
	}
}

func requireTrafficProgress(t testing.TB, cluster *suite.StartedCluster, worker *trafficWorker, additional uint64, timeout time.Duration) {
	t.Helper()
	start := worker.sent.Load()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if worker.errs.Load() != 0 {
			t.Fatalf("traffic worker recorded %d errors: %v\n%s", worker.errs.Load(), worker.lastError(), cluster.DumpDiagnostics())
		}
		if worker.sent.Load() >= start+additional {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("traffic worker sent=%d, want at least %d more\n%s", worker.sent.Load(), additional, cluster.DumpDiagnostics())
}

func (w *trafficWorker) recordErr(err error) {
	w.errs.Add(1)
	w.mu.Lock()
	w.lastErr = err
	w.mu.Unlock()
}

func (w *trafficWorker) lastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

func eventuallyNodeHealthFresh(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		last    suite.NodeDTO
		lastErr error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		nodes, err := manager.ListNodes(reqCtx)
		cancelReq()
		if err == nil {
			for _, node := range nodes.Items {
				if node.NodeID != nodeID {
					continue
				}
				last = node
				if node.Health.Fresh &&
					node.Health.Freshness == "fresh" &&
					node.Health.Status == "alive" &&
					node.Health.RuntimeReady {
					return node
				}
				lastErr = fmt.Errorf("health=%#v membership=%#v", node.Health, node.Membership)
			}
			if last.NodeID == 0 {
				lastErr = fmt.Errorf("node %d missing from manager inventory", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d health did not become fresh: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyNodeSchedulable(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		last    suite.NodeDTO
		lastErr error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		nodes, err := manager.ListNodes(reqCtx)
		cancelReq()
		if err == nil {
			for _, node := range nodes.Items {
				if node.NodeID != nodeID {
					continue
				}
				last = node
				if node.Membership.JoinState == "active" &&
					node.Membership.Schedulable &&
					node.Health.Fresh &&
					node.Health.Status == "alive" &&
					node.Health.RuntimeReady {
					return node
				}
				lastErr = fmt.Errorf("membership=%#v health=%#v", node.Membership, node.Health)
			}
			if last.NodeID == 0 {
				lastErr = fmt.Errorf("node %d missing from manager inventory", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d did not become schedulable: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyScaleInSlotsDrained(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		last    suite.NodeScaleInStatusDTO
		lastErr error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		status, err := manager.NodeScaleInStatus(reqCtx, nodeID)
		cancelReq()
		if err == nil {
			last = status
			if !status.BlockedBySlots &&
				!status.BlockedBySlotLeadership &&
				!status.BlockedBySlotRuntime &&
				!status.BlockedByControlRevision &&
				!status.BlockedByHealth &&
				!status.BlockedByStaleRevision &&
				!status.BlockedByTasks &&
				status.SlotReplicaCount == 0 &&
				status.SlotLeaderCount == 0 &&
				status.ActiveTaskCount == 0 &&
				status.FailedTaskCount == 0 {
				return status
			}
			lastErr = fmt.Errorf("blocked_by_slots=%t blocked_by_slot_leadership=%t blocked_by_slot_runtime=%t blocked_by_control_revision=%t blocked_by_health=%t blocked_by_stale_revision=%t blocked_by_tasks=%t slot_replica_count=%d slot_leader_count=%d active_task_count=%d failed_task_count=%d observed_control_revision=%d required_control_revision=%d blocked_reasons=%v",
				status.BlockedBySlots,
				status.BlockedBySlotLeadership,
				status.BlockedBySlotRuntime,
				status.BlockedByControlRevision,
				status.BlockedByHealth,
				status.BlockedByStaleRevision,
				status.BlockedByTasks,
				status.SlotReplicaCount,
				status.SlotLeaderCount,
				status.ActiveTaskCount,
				status.FailedTaskCount,
				status.ObservedControlRevision,
				status.RequiredControlRevision,
				status.BlockedReasons,
			)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in Slots did not drain: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireNodeNotSchedulable(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodes, err := manager.ListNodes(ctx)
	require.NoError(t, err, cluster.DumpDiagnostics())
	for _, node := range nodes.Items {
		if node.NodeID != nodeID {
			continue
		}
		require.False(t, node.Membership.Schedulable, "joining node must not be schedulable before activation: %#v", node)
		return
	}
	t.Fatalf("node %d missing from manager inventory\n%s", nodeID, cluster.DumpDiagnostics())
}

type wkcliResult struct {
	stdout string
	stderr string
	err    error
}

func runWKCLI(t testing.TB, contextDir string, args ...string) string {
	t.Helper()
	result := runWKCLIResult(t, contextDir, args...)
	if result.err != nil {
		t.Fatalf("wkcli %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), result.err, result.stdout, result.stderr)
	}
	return result.stdout
}

func runWKCLIResult(t testing.TB, contextDir string, args ...string) wkcliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cliArgs := append([]string{"run", "./cmd/wkcli", "--context-dir", contextDir}, args...)
	cmd := exec.CommandContext(ctx, "go", cliArgs...)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("wkcli timed out after 30s: %w", ctx.Err())
	}
	return wkcliResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func eventuallyWKCLIContains(t testing.TB, contextDir string, timeout time.Duration, args []string, wants []string, diagnostics ...func() string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastResult  wkcliResult
		lastMissing []string
	)
	for {
		result := runWKCLIResult(t, contextDir, args...)
		if result.err == nil {
			missing := missingSubstrings(result.stdout, wants)
			if len(missing) == 0 {
				return result.stdout
			}
			lastResult = result
			lastMissing = missing
		} else {
			lastResult = result
			if !isRetryableWKCLIResult(result) {
				t.Fatalf("wkcli %s failed without retryable evidence: %v\nstdout:\n%s\nstderr:\n%s%s",
					strings.Join(args, " "), result.err, result.stdout, result.stderr, formatDiagnostics(diagnostics))
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("wkcli %s did not reach expected output before timeout: missing=%v err=%v\nstdout:\n%s\nstderr:\n%s%s",
				strings.Join(args, " "), lastMissing, lastResult.err, lastResult.stdout, lastResult.stderr, formatDiagnostics(diagnostics))
		case <-ticker.C:
		}
	}
}

func formatDiagnostics(diagnostics []func() string) string {
	for _, dump := range diagnostics {
		if dump == nil {
			continue
		}
		if text := strings.TrimSpace(dump()); text != "" {
			return "\ndiagnostics:\n" + text + "\n"
		}
	}
	return ""
}

func missingSubstrings(haystack string, wants []string) []string {
	var missing []string
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			missing = append(missing, want)
		}
	}
	return missing
}

func isRetryableWKCLIResult(result wkcliResult) bool {
	text := strings.ToLower(result.stdout + "\n" + result.stderr + "\n" + fmt.Sprint(result.err))
	if isRetryableControllerProposalState(text) {
		return true
	}
	for _, token := range []string{
		"409",
		"503",
		"conflict",
		"service_unavailable",
		"blocked",
		"context deadline exceeded",
		"client.timeout exceeded",
		"awaiting headers",
		"connection refused",
		"i/o timeout",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func isRetryableControllerProposalState(text string) bool {
	for _, token := range []string{
		"manager api status 500",
		"internal_error",
		"controllerv2/raft: proposal rejected",
		"invalid_state",
	} {
		if !strings.Contains(text, token) {
			return false
		}
	}
	return true
}

func repoRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for {
		if fileExists(filepath.Join(wd, "go.mod")) && fileExists(filepath.Join(wd, "cmd", "wkcli")) {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("repo root not found from %s", wd)
		}
		wd = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func requireJSONUintField(t testing.TB, raw string, field string) uint64 {
	t.Helper()
	value, ok := parseJSONObject(t, raw)[field]
	require.True(t, ok, "json field %q missing in %s", field, raw)
	switch typed := value.(type) {
	case json.Number:
		out, err := typed.Int64()
		require.NoError(t, err, "json field %q is not an integer in %s", field, raw)
		require.GreaterOrEqual(t, out, int64(0), "json field %q is negative in %s", field, raw)
		return uint64(out)
	case float64:
		require.GreaterOrEqual(t, typed, float64(0), "json field %q is negative in %s", field, raw)
		return uint64(typed)
	default:
		t.Fatalf("json field %q has type %T, want unsigned integer in %s", field, value, raw)
		return 0
	}
}

func requireJSONFieldPresent(t testing.TB, raw string, field string) {
	t.Helper()
	_, ok := parseJSONObject(t, raw)[field]
	require.True(t, ok, "json field %q missing in %s", field, raw)
}

func parseJSONObject(t testing.TB, raw string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	require.NoError(t, decoder.Decode(&out), "decode json: %s", raw)
	return out
}
