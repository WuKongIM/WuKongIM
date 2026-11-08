//go:build e2e

package startup_recovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// controllerCheckpointConfig is a 256-hash-slot single-node cluster with token
// authentication, the startup shape that exceeded its budget in issue #1098.
var controllerCheckpointConfig = map[string]string{
	"WK_CLUSTER_HASH_SLOT_COUNT": "256",
	"WK_GATEWAY_TOKEN_AUTH_ON":   "true",
}

type controllerSlotView struct {
	SlotID     uint32                  `json:"slot_id"`
	HashSlots  *suite.SlotHashSlotsDTO `json:"hash_slots,omitempty"`
	Assignment suite.SlotAssignmentDTO `json:"assignment"`
}

// controllerSlotViews reads the Controller-owned Slot assignment through Manager.
func controllerSlotViews(t *testing.T, ctx context.Context, node *suite.StartedNode) []controllerSlotView {
	t.Helper()
	var resp struct {
		Items []controllerSlotView `json:"items"`
	}
	require.Eventually(t, func() bool {
		_, err := suite.GetJSON(ctx, "http://"+node.ManagerAddr()+"/manager/slots", &resp)
		return err == nil && len(resp.Items) > 0
	}, 30*time.Second, 100*time.Millisecond, node.DumpDiagnostics())
	return resp.Items
}

func authenticateToken(t *testing.T, ctx context.Context, node *suite.StartedNode, uid, token string) {
	t.Helper()
	client, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), OperationTimeout: 5 * time.Second})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: uid, Token: token, DeviceID: uid + "-check", DeviceFlag: frame.APP})
	require.NoError(t, err, node.DumpDiagnostics())
}

// killNode ends the product process with SIGKILL, as a crash would: no
// shutdown checkpoint is written, so the Controller state file lags its WAL.
func killNode(t *testing.T, node *suite.StartedNode) {
	t.Helper()
	require.NotNil(t, node.Process.Cmd)
	require.NoError(t, node.Process.Cmd.Process.Signal(syscall.SIGKILL))
	select {
	case <-node.Process.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("node did not exit after SIGKILL\n%s", node.DumpDiagnostics())
	}
}

func startNodeBinary(t *testing.T, ctx context.Context, node *suite.StartedNode, binary string) time.Duration {
	t.Helper()
	node.Process = &suite.NodeProcess{Spec: node.Spec, BinaryPath: binary}
	started := time.Now()
	require.NoError(t, node.Process.Start())
	_, err := node.Process.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
	require.NoError(t, err, node.DumpDiagnostics())
	require.NoError(t, node.Process.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
	return time.Since(started)
}

func writeControllerCheckpointReport(t *testing.T, node *suite.StartedNode, name string, report map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	path := os.Getenv("WK_E2E_CONTROLLER_CHECKPOINT_REPORT")
	if path == "" {
		path = filepath.Join(node.Spec.RootDir, name+".json")
	} else {
		path = path[:len(path)-len(filepath.Ext(path))] + "-" + name + ".json"
	}
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o600))
	t.Logf("controller checkpoint artifact %s:\n%s", path, data)
}

// F7 (issue #1098): presence startup claims one Controller generation per hash
// slot; with one WAL fsync per entry the single-node cluster meets the
// harness's 10-second HTTP+WKProto budget on an ordinary disk.
func TestControllerStartupMeetsSingleNodeReadinessBudget(t *testing.T) {
	s := suite.New(t) // may build the product binary; not part of startup
	started := time.Now()
	node := s.StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, controllerCheckpointConfig))
	elapsed := time.Since(started)
	writeControllerCheckpointReport(t, node, "startup-budget", map[string]any{
		"hash_slots":      256,
		"token_auth":      true,
		"ready_ms":        elapsed.Milliseconds(),
		"ready_budget_ms": (10 * time.Second).Milliseconds(),
	})
}

// F5: a crash leaves the Controller state checkpoint behind its WAL. Restart
// must replay committed history to the same Controller state.
func TestControllerKillRestartRecoversFromLaggingCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	node := suite.New(t).StartSingleNodeCluster(suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, controllerCheckpointConfig))
	binary := node.Process.BinaryPath
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/token", map[string]any{"uid": "checkpoint-crash-user", "token": "before-crash", "device_flag": int(frame.APP), "device_level": 1}, nil)
	require.NoError(t, err)
	before := controllerSlotViews(t, ctx, node)

	killNode(t, node)
	readyAfterCrash := startNodeBinary(t, ctx, node, binary)

	require.Equal(t, before, controllerSlotViews(t, ctx, node), "Controller Slot assignment changed across crash recovery")
	authenticateToken(t, ctx, node, "checkpoint-crash-user", "before-crash")
	writeControllerCheckpointReport(t, node, "kill-restart", map[string]any{
		"slots":                    len(before),
		"ready_after_crash_ms":     readyAfterCrash.Milliseconds(),
		"assignment_preserved":     true,
		"credential_authenticated": true,
	})
}

// Rollback: a pre-#1098 binary must start on data whose Controller checkpoint
// lags the WAL, because every v3 recovery replays from the state file index.
func TestControllerLaggingCheckpointRollsBackToOlderBinary(t *testing.T) {
	older := os.Getenv("WK_E2E_STARTUP_OLDER_BINARY")
	if older == "" {
		t.Skip("requires a frozen pre-#1098 product binary in WK_E2E_STARTUP_OLDER_BINARY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	node := suite.New(t).StartSingleNodeCluster(suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, controllerCheckpointConfig))
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/token", map[string]any{"uid": "checkpoint-rollback-user", "token": "before-rollback", "device_flag": int(frame.APP), "device_level": 1}, nil)
	require.NoError(t, err)
	before := controllerSlotViews(t, ctx, node)

	killNode(t, node)
	readyOnOlder := startNodeBinary(t, ctx, node, older)

	require.Equal(t, before, controllerSlotViews(t, ctx, node), "older binary recovered a different Controller Slot assignment")
	authenticateToken(t, ctx, node, "checkpoint-rollback-user", "before-rollback")
	writeControllerCheckpointReport(t, node, "older-rollback", map[string]any{
		"slots":                    len(before),
		"ready_on_older_ms":        readyOnOlder.Milliseconds(),
		"assignment_preserved":     true,
		"credential_authenticated": true,
	})
}
