//go:build e2e

package interop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestOfflineJSONLExportRefusesPersistentMQTTState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	report := map[string]any{"scenario": "mqtt-jsonl-export-refusal", "nodes": 1, "hash_slots": 256}
	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir == "" {
		reportDir = t.TempDir()
	}
	t.Cleanup(func() {
		report["passed"] = !t.Failed()
		raw, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(reportDir, 0700))
		path := filepath.Join(reportDir, "mqtt-jsonl-export-refusal.json")
		require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	cli := suite.BuildMigrationCLI(t)
	mqttAddr := suite.ReserveLoopbackPorts(t).GatewayAddr
	cluster := suite.New(t).StartStaticCluster(1, suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true",
		"WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": mqttAddr,
	}))
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	node := cluster.MustNode(1)
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/token", map[string]any{
			"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1,
		}, nil)
		require.NoError(t, err)
	}
	require.NoError(t, suite.WaitTCPReady(ctx, mqttAddr), cluster.DumpDiagnostics())
	client, err := suite.ConnectMQTT(ctx, mqttAddr, "alice", "alice-fixture-token", "export-bound-client", true, 86400)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.NoError(t, client.Close())
	require.NoError(t, node.Stop())
	out := t.TempDir()
	sentinel := filepath.Join(out, "existing-bundle")
	require.NoError(t, os.WriteFile(sentinel, []byte("preserve"), 0600))
	cmd := exec.CommandContext(ctx, cli, "db", "--data-dir", node.Spec.DataDir, "--hash-slot-count", "256", "export", "--output", out, "--overwrite")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.True(t, errors.As(err, &exit), "export must refuse with an exit status: %v, %s", err, output)
	require.NoError(t, ctx.Err(), "timeout is not an export refusal")
	report["export_exit_code"] = exit.ExitCode()
	require.Contains(t, string(output), "MQTT state")
	contents, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(contents))
	_, err = os.Stat(filepath.Join(out, "manifest.json"))
	require.True(t, os.IsNotExist(err))
	report["output_preserved"] = true
	require.NoError(t, cluster.StartStoppedNode(1))
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	require.NoError(t, suite.WaitTCPReady(ctx, mqttAddr), cluster.DumpDiagnostics())
	wrong, err := suite.ConnectMQTT(ctx, mqttAddr, "bob", "bob-fixture-token", "export-bound-client", false, 86400)
	if wrong != nil {
		_ = wrong.Close()
	}
	require.Error(t, err, "refused export must preserve the original ClientID/UID binding")
	report["other_uid_rejected"] = true
	resumed, err := suite.ConnectMQTT(ctx, mqttAddr, "alice", "alice-fixture-token", "export-bound-client", false, 86400)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, resumed.Connack.SessionPresent)
	require.NoError(t, resumed.Close())
	report["original_session_resumed"] = true
}
