//go:build e2e

package docs_quickstart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestMQTTJSQuickstart(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err, "Node.js is required; install example dependencies with npm ci")
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	example := filepath.Join(root, "docs-site/examples/mqtt-quickstart")
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			dir := strings.TrimSpace(os.Getenv("WK_E2E_MQTT_REPORT_DIR"))
			if dir == "" {
				dir = t.TempDir()
			}
			report := map[string]any{"scenario": "mqtt-js-docs-quickstart", "nodes": count,
				"hash_slots": 256, "passed": false, "invalid_token_checked": count == 1}
			// Registered first so the artifact records failures from joined product cleanup too.
			t.Cleanup(func() {
				report["passed"] = !t.Failed()
				body, err := json.MarshalIndent(report, "", "  ")
				require.NoError(t, err)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				path := filepath.Join(dir, fmt.Sprintf("mqtt-docs-quickstart-%d.json", count))
				require.NoError(t, os.WriteFile(path, append(body, '\n'), 0o600))
				t.Logf("result artifact: %s", path)
			})
			var options []suite.Option
			addrs := make([]string, count)
			for i := range count {
				addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
				options = append(options, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
					"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true",
					"WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i],
				}))
			}
			s := suite.New(t)
			var first *suite.StartedNode
			if count == 1 {
				first = s.StartSingleNodeCluster(options...)
			} else {
				cluster := s.StartThreeNodeCluster(append(options, suite.WithManagerHTTP())...)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
				_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
				cancel()
				require.NoError(t, err, cluster.DumpDiagnostics())
				first = cluster.MustNode(1)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			for _, uid := range []string{"alice", "bob"} {
				_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{
					"uid": uid, "token": uid + "-docs-fixture-token", "device_flag": 1, "device_level": 1,
				}, nil)
				require.NoError(t, err)
			}
			run := func(bobToken string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, node, "quickstart.mjs")
				cmd.Dir = example
				// Keep host credentials and unrelated MQTT settings out of the client process.
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "MQTT_URL=mqtt://" + addrs[0],
					"MQTT_BOB_URL=mqtt://" + addrs[count-1], "MQTT_ALICE_TOKEN=alice-docs-fixture-token",
					"MQTT_BOB_TOKEN=" + bobToken}
				return cmd.CombinedOutput()
			}
			if count == 1 {
				output, err := run("wrong-docs-fixture-token")
				require.Error(t, err, "wrong token must fail")
				require.NotContains(t, string(output), "fixture-token")
				require.Contains(t, string(output), "MQTT example failed")
				report["invalid_token_rejected"] = true
			}
			output, err := run("bob-docs-fixture-token")
			require.NoError(t, err, "example failed: %s", output)
			require.NotContains(t, string(output), "fixture-token")
			var receipt struct {
				Passed    bool   `json:"passed"`
				Version   string `json:"client_version"`
				Exchanges []struct {
					From string `json:"from_uid"`
					ID   string `json:"message_id"`
					Seq  string `json:"message_seq"`
				} `json:"exchanges"`
			}
			require.NoError(t, json.Unmarshal(output, &receipt))
			require.True(t, receipt.Passed)
			require.Equal(t, "5.16.0", receipt.Version)
			require.Len(t, receipt.Exchanges, 2)
			for i, uid := range []string{"alice", "bob"} {
				require.Equal(t, uid, receipt.Exchanges[i].From)
				require.Regexp(t, `^[1-9][0-9]*$`, receipt.Exchanges[i].ID)
				require.Regexp(t, `^[1-9][0-9]*$`, receipt.Exchanges[i].Seq)
			}
			require.NotEqual(t, receipt.Exchanges[0].ID, receipt.Exchanges[1].ID)
			hashes := map[string]string{}
			for _, name := range []string{"quickstart.mjs", "package-lock.json"} {
				body, err := os.ReadFile(filepath.Join(example, name))
				require.NoError(t, err)
				sum := sha256.Sum256(body)
				hashes[name] = hex.EncodeToString(sum[:])
			}
			report["client"] = receipt
			report["example_sha256"] = hashes
		})
	}
}
