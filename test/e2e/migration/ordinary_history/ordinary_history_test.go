//go:build e2e

package ordinary_history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// The original public send requests and acknowledgements are independent of
// migration conversion. The original reader's skipped header column is not
// used to override the durable RedDot requested by the original sender.
type sourceObservation struct {
	Path    string `json:"path"`
	Request struct {
		ClientMsgNo string `json:"client_msg_no"`
		Payload     []byte `json:"payload"`
	} `json:"request"`
	Response json.RawMessage `json:"response"`
}
type messageRow struct {
	MessageID   string `json:"message_idstr"`
	Seq         uint64 `json:"message_seq"`
	ClientMsgNo string `json:"client_msg_no"`
	FromUID     string `json:"from_uid"`
	Payload     []byte `json:"payload"`
	Header      struct {
		RedDot int `json:"red_dot"`
	} `json:"header"`
}

func TestOriginalOrdinaryHistoryTopology(t *testing.T) {
	for _, tc := range []struct{ sources, targets int }{{1, 1}, {1, 3}, {3, 1}, {3, 3}, {3, 5}} {
		t.Run(fmt.Sprintf("%d_source_nodes_to_%d_target_nodes", tc.sources, tc.targets), func(t *testing.T) { runHistory(t, tc.sources, tc.targets) })
	}
}

func runHistory(t *testing.T, sourceCount, targetCount int) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	s := suite.New(t)
	var sources, targets []any
	var specs []suite.NodeSpec
	for n := 1; n <= sourceCount; n++ {
		fixture := "original-v2-ordinary.tar.gz"
		if sourceCount == 3 {
			fixture = fmt.Sprintf("original-v2-three-%d.tar.gz", n)
		}
		sources = append(sources, map[string]any{"node_id": n, "data_dir": suite.UnpackMigrationFixture(t, fixture), "shard_count": 2})
	}
	replicas := min(targetCount, 3)
	for n := 1; n <= targetCount; n++ {
		ports := suite.ReserveLoopbackPorts(t)
		nodeRoot := filepath.Join(root, fmt.Sprint(n))
		require.NoError(t, os.Mkdir(nodeRoot, 0700))
		spec := suite.NodeSpec{ID: uint64(n), Name: fmt.Sprintf("migrated-%d", n), RootDir: nodeRoot, DataDir: filepath.Join(nodeRoot, "data"), ConfigPath: filepath.Join(nodeRoot, "wukongim.toml"), StdoutPath: filepath.Join(nodeRoot, "stdout.log"), StderrPath: filepath.Join(nodeRoot, "stderr.log"), ClusterAddr: ports.ClusterAddr, GatewayAddr: ports.GatewayAddr, APIAddr: ports.APIAddr, ManagerAddr: ports.ManagerAddr}
		spec.ConfigOverrides = map[string]string{"WK_CLUSTER_ID": "migration-ordinary-history", "WK_CLUSTER_INITIAL_SLOT_COUNT": "4", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_SLOT_REPLICA_N": fmt.Sprint(replicas), "WK_CLUSTER_CHANNEL_REPLICA_N": fmt.Sprint(replicas), "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": "500ms", "WK_CLUSTER_NODE_HEALTH_REPORT_TTL": "5s"}
		// Use the same bounded failure-detection and recovery budget as the native
		// Channel failover E2E suite; storage and routing semantics stay unchanged.
		for key, value := range map[string]string{
			"WK_CHANNEL_MIGRATION_ENABLE":             "true",
			"WK_CHANNEL_MIGRATION_SCAN_INTERVAL":      "100ms",
			"WK_CHANNEL_MIGRATION_SCAN_LIMIT":         "16",
			"WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK": "2",
			"WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK": "2",
			"WK_CHANNEL_MIGRATION_TASK_LIMIT":         "2",
		} {
			spec.ConfigOverrides[key] = value
		}
		if targetCount == 1 {
			spec.ConfigOverrides["WK_CLUSTER_NODES"] = fmt.Sprintf(`[{"id":1,"addr":%q}]`, ports.ClusterAddr)
		}
		specs = append(specs, spec)
		targets = append(targets, map[string]any{"node_id": n, "addr": ports.ClusterAddr, "data_dir": spec.DataDir})
	}
	plan := map[string]any{"version": 1, "source_commit": "a888f89533d0e7d1b2030e06504ca97f1ad891d4", "sources": sources, "metadata": map[string]any{"archive_empty_channels": true, "device_lookup": "v2_cold_start", "conversation_lookup": "v2_active_slot", "conversation_list_limit": 1000, "archive_user_timestamps": true}, "messages": map[string]any{"keep_latest_duplicates": true, "exclude_cmd": true, "exclude_streams": true, "compact_sequences": true}, "target": map[string]any{"cluster_id": "migration-ordinary-history", "created_at": "2026-09-08T05:00:00Z", "slot_count": 4, "hash_slot_count": 256, "replicas": replicas, "channel_replicas": replicas, "nodes": targets}}
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	planPath := filepath.Join(root, "plan.json")
	require.NoError(t, os.WriteFile(planPath, raw, 0600))
	cli := suite.BuildMigrationCLI(t)
	archive := filepath.Join(root, "archive")
	for _, phase := range []string{"prepare", "export", "import", "import", "verify"} {
		workspace := filepath.Join(root, "work")
		if phase == "verify" {
			workspace = filepath.Join(root, "independent-verify")
		}
		suite.RunMigrationCLI(t, ctx, cli, phase, "--plan", planPath, "--workspace", workspace, "--archive", archive)
	}
	cluster := s.StartPreparedCluster(specs)
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	provenance := "original-v2-ordinary-api.json"
	if sourceCount == 3 {
		provenance = "original-v2-three-api.json"
	}
	raw, err = os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "infra", "migrationv2", "testdata", provenance))
	require.NoError(t, err)
	var observations []sourceObservation
	require.NoError(t, json.Unmarshal(raw, &observations))
	var expected []messageRow
	for _, o := range observations {
		if o.Path == "/message/send" {
			var ack struct {
				Data struct {
					MessageID uint64 `json:"message_id"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(o.Response, &ack))
			row := messageRow{MessageID: strconv.FormatUint(ack.Data.MessageID, 10), Seq: uint64(len(expected) + 1), ClientMsgNo: o.Request.ClientMsgNo, FromUID: "migrationalice", Payload: o.Request.Payload}
			row.Header.RedDot = 1
			expected = append(expected, row)
		}
	}
	require.Len(t, expected, 3)
	check := func(node *suite.StartedNode, phase string) {
		t.Helper()
		var response struct {
			Messages []messageRow `json:"messages"`
		}
		readCtx, readCancel := context.WithTimeout(ctx, 20*time.Second)
		defer readCancel()
		temporaryRetries := 0
		// HTTP readiness precedes per-Channel recovery. Retry only the explicit
		// public unavailable envelope; a successful but incorrect page fails below.
		for {
			_, err := suite.PostJSON(readCtx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": "migrationbob", "channel_id": "migrationgroup", "channel_type": 2, "start_message_seq": 0, "end_message_seq": 0, "pull_mode": 1, "limit": 100}, &response)
			if err == nil {
				break
			}
			var statusErr *suite.HTTPStatusError
			var body map[string]json.RawMessage
			var code string
			var status int
			unavailable := errors.As(err, &statusErr) &&
				statusErr.StatusCode == http.StatusServiceUnavailable &&
				json.Unmarshal([]byte(statusErr.Body), &body) == nil &&
				json.Unmarshal(body["code"], &code) == nil &&
				json.Unmarshal(body["status"], &status) == nil &&
				code == "unavailable" && status == http.StatusServiceUnavailable
			if !unavailable {
				require.NoError(t, err, "history phase=%s node=%d temporary_retries=%d\n%s", phase, node.Spec.ID, temporaryRetries, cluster.DumpDiagnostics())
			}
			temporaryRetries++
			select {
			case <-readCtx.Done():
				require.NoError(t, readCtx.Err(), "history recovery phase=%s node=%d temporary_retries=%d: %v\n%s", phase, node.Spec.ID, temporaryRetries, err, cluster.DumpDiagnostics())
			case <-time.After(100 * time.Millisecond):
			}
		}
		require.Equal(t, expected, response.Messages, "retained original history or appended history differs: phase=%s node=%d", phase, node.Spec.ID)
		t.Logf("history verified: phase=%s node=%d rows=%d temporary_retries=%d", phase, node.Spec.ID, len(response.Messages), temporaryRetries)
	}
	for i := range cluster.Nodes {
		check(&cluster.Nodes[i], "after-import")
	}
	// Authenticate through the real protocol using the token retained from v2.
	client, err := wkclient.New(wkclient.Config{Addr: cluster.MustNode(1).GatewayAddr(), OperationTimeout: 5 * time.Second})
	require.NoError(t, err)
	_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: "migrationalice", Token: "synthetic-migrationalice", DeviceID: "migration-history-check", DeviceFlag: frame.DeviceFlag(1)})
	require.NoError(t, err)
	ack, err := client.Send(ctx, wkclient.Message{ClientSeq: 1, ClientMsgNo: "after-original-migration", ChannelID: "migrationgroup", ChannelType: 2, Payload: []byte("protocol append")})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	// Startup recovery can consume positions before the first business append.
	require.Greater(t, ack.MessageSeq, expected[len(expected)-1].Seq, cluster.DumpDiagnostics())
	require.NoError(t, client.Close())
	row := messageRow{MessageID: strconv.FormatUint(uint64(ack.MessageID), 10), Seq: ack.MessageSeq, ClientMsgNo: "after-original-migration", FromUID: "migrationalice", Payload: []byte("protocol append")}
	expected = append(expected, row)
	for i := range cluster.Nodes {
		check(&cluster.Nodes[i], "after-protocol-append")
	}
	appendHTTP := func(node *suite.StartedNode, key string) {
		t.Helper()
		body := map[string]any{"header": map[string]int{"red_dot": 1}, "from_uid": "migrationalice", "channel_id": "migrationgroup", "channel_type": 2, "client_msg_no": key, "payload": []byte(key)}
		sent, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), body)
		require.NoError(t, err, cluster.DumpDiagnostics())
		// Native recovery barriers may consume log sequences. Business messages
		// must remain strictly increasing without requiring gap-free allocation.
		require.Greater(t, sent.MessageSeq, expected[len(expected)-1].Seq)
		again, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), body)
		require.NoError(t, err)
		require.Equal(t, sent.MessageID, again.MessageID)
		require.Equal(t, sent.MessageSeq, again.MessageSeq)
		row := messageRow{MessageID: strconv.FormatInt(sent.MessageID, 10), Seq: sent.MessageSeq, ClientMsgNo: key, FromUID: "migrationalice", Payload: []byte(key)}
		row.Header.RedDot = 1
		expected = append(expected, row)
	}
	// Whole-cluster restart must expose old history before another append warms it.
	for i := range cluster.Nodes {
		require.NoError(t, cluster.Nodes[i].Stop())
	}
	for i := range cluster.Nodes {
		require.NoError(t, cluster.StartStoppedNode(cluster.Nodes[i].Spec.ID))
	}
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	for i := range cluster.Nodes {
		check(&cluster.Nodes[i], "after-full-restart")
	}
	appendHTTP(cluster.MustNode(uint64(targetCount)), "after-full-restart")
	if targetCount > 1 {
		meta := suite.RequireChannelRuntimeMetaEventually(t, cluster, cluster.MustNode(1), "migrationgroup", 2, 10*time.Second)
		leader := meta.Leader
		survivor := cluster.MustNode(1)
		if leader == 1 {
			survivor = cluster.MustNode(2)
		}
		require.NoError(t, cluster.MustNode(leader).Stop())
		appendHTTP(survivor, "after-leader-stop")
		check(survivor, "after-leader-stop")
		require.NoError(t, cluster.StartStoppedNode(leader))
		require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	}
	for i := range cluster.Nodes {
		check(&cluster.Nodes[i], "final")
	}
	t.Logf("original %d-node source -> %d-node cluster: history, credentials, appends, idempotency, full restart and applicable leader failover passed", sourceCount, targetCount)
}
