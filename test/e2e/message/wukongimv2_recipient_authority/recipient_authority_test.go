//go:build e2e

package wukongimv2_recipient_authority

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMV2GroupRecipientAuthorityUpdatesSubscribers(t *testing.T) {
	binaryPath := buildWukongIMV2Binary(t)
	workspace := suite.NewWorkspace(t)
	ports := suite.ReserveLoopbackPorts(t)
	spec := newSingleNodeClusterSpec(workspace, ports)

	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte(renderSingleNodeClusterConfig(spec)), 0o644))

	process := &suite.NodeProcess{Spec: spec, BinaryPath: binaryPath}
	require.NoError(t, process.Start())
	t.Cleanup(func() { require.NoError(t, process.Stop()) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.WaitHTTPReady(ctx, spec.APIAddr, "/readyz"), process.DumpDiagnostics())
	require.NoError(t, suite.WaitWKProtoReady(ctx, spec.GatewayAddr), process.DumpDiagnostics())

	apiBaseURL := "http://" + spec.APIAddr
	const smallChannelID = "e2e-recipient-authority-small-group"
	postLegacyJSON(t, ctx, apiBaseURL+"/channel", map[string]any{
		"channel_id":   smallChannelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"conv-small-a", "conv-small-b"},
	})
	smallSend := postMessageSend(t, ctx, apiBaseURL, map[string]any{
		"from_uid":      "conv-small-sender",
		"channel_id":    smallChannelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": "conv-small-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("small group recipient authority")),
	})
	require.Equal(t, uint8(frame.ReasonSuccess), smallSend.Reason)
	require.NotZero(t, smallSend.MessageID)
	require.NotZero(t, smallSend.MessageSeq)

	for _, uid := range []string{"conv-small-a", "conv-small-b"} {
		page := requireConversationEventually(t, process, spec.APIAddr, uid, smallChannelID, func(item conversationListItem) error {
			return checkRecipientConversation(item, smallSend, "conv-small-sender", "conv-small-1", "small group recipient authority")
		})
		require.Equal(t, 0, page.More)
	}
	requireConversationAbsent(t, ctx, process, spec.APIAddr, "conv-small-sender", smallChannelID)

	const largeChannelID = "e2e-recipient-authority-large-group"
	postLegacyJSON(t, ctx, apiBaseURL+"/channel", map[string]any{
		"channel_id":   largeChannelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"conv-large-a", "conv-large-b", "conv-large-c"},
	})
	largeSend := postMessageSend(t, ctx, apiBaseURL, map[string]any{
		"from_uid":      "conv-large-sender",
		"channel_id":    largeChannelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": "conv-large-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("large group recipient authority")),
	})
	require.Equal(t, uint8(frame.ReasonSuccess), largeSend.Reason)
	require.NotZero(t, largeSend.MessageID)
	require.NotZero(t, largeSend.MessageSeq)

	for _, uid := range []string{"conv-large-a", "conv-large-b", "conv-large-c"} {
		page := requireConversationEventually(t, process, spec.APIAddr, uid, largeChannelID, func(item conversationListItem) error {
			return checkRecipientConversation(item, largeSend, "conv-large-sender", "conv-large-1", "large group recipient authority")
		})
		require.Equal(t, 0, page.More)
	}
	requireConversationAbsent(t, ctx, process, spec.APIAddr, "conv-large-sender", largeChannelID)

	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_authority_sender_route_total`, map[string]string{"result": "local"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_authority_recipient_queue_total`, map[string]string{"result": "accepted"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_authority_recipient_dispatch_total`, map[string]string{"phase": "conversation", "result": "ok"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_conversation_authority_admit_total`, map[string]string{"result": "ok"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_conversation_authority_list_total`, map[string]string{"result": "ok"}, 1)
}

func TestWukongIMV2HundredKGroupRecipientAuthorityUpdatesSubscribers(t *testing.T) {
	if os.Getenv("WK_E2E_100K_CONVERSATION") != "1" {
		t.Skip("set WK_E2E_100K_CONVERSATION=1 to run the 100k recipient-authority stress test")
	}

	binaryPath := buildWukongIMV2Binary(t)
	workspace := suite.NewWorkspace(t)
	ports := suite.ReserveLoopbackPorts(t)
	spec := newSingleNodeClusterSpec(workspace, ports)

	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte(renderSingleNodeClusterConfig(spec)), 0o644))

	process := &suite.NodeProcess{Spec: spec, BinaryPath: binaryPath}
	require.NoError(t, process.Start())
	t.Cleanup(func() { require.NoError(t, process.Stop()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	require.NoError(t, suite.WaitHTTPReady(ctx, spec.APIAddr, "/readyz"), process.DumpDiagnostics())
	require.NoError(t, suite.WaitWKProtoReady(ctx, spec.GatewayAddr), process.DumpDiagnostics())

	apiBaseURL := "http://" + spec.APIAddr
	const channelID = "e2e-recipient-authority-100k-group"
	postLegacyJSON(t, ctx, apiBaseURL+"/channel", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  hundredKConversationSubscribers(),
	})
	sendResp := postMessageSend(t, ctx, apiBaseURL, map[string]any{
		"from_uid":      "conv-100k-sender",
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": "conv-100k-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("100k recipient authority")),
	})
	require.Equal(t, uint8(frame.ReasonSuccess), sendResp.Reason)

	for _, uid := range []string{hundredKConversationSubscriberUID(0), hundredKConversationSubscriberUID(50000), hundredKConversationSubscriberUID(99999)} {
		requireConversationEventuallyWithin(t, process, spec.APIAddr, uid, channelID, 5*time.Minute, func(item conversationListItem) error {
			return checkRecipientConversation(item, sendResp, "conv-100k-sender", "conv-100k-1", "100k recipient authority")
		})
	}
	requireConversationAbsent(t, ctx, process, spec.APIAddr, "conv-100k-sender", channelID)

	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_authority_recipient_queue_total`, map[string]string{"result": "accepted"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_authority_recipient_dispatch_total`, map[string]string{"phase": "conversation", "result": "ok"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_conversation_authority_admit_total`, map[string]string{"result": "ok"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_conversation_authority_list_total`, map[string]string{"result": "ok"}, 1)
}

func checkRecipientConversation(item conversationListItem, send messageSendResponse, fromUID, clientMsgNo, payload string) error {
	if item.SparseActive {
		return fmt.Errorf("sparse_active = true, want recipient-scoped row")
	}
	if item.LastMessage == nil {
		return fmt.Errorf("last_message is nil")
	}
	if item.LastMessage.MessageID != uint64(send.MessageID) ||
		item.LastMessage.MessageSeq != send.MessageSeq ||
		item.LastMessage.FromUID != fromUID ||
		item.LastMessage.ClientMsgNo != clientMsgNo ||
		string(item.LastMessage.Payload) != payload {
		return fmt.Errorf("last_message = %#v, want recipient-authority message", item.LastMessage)
	}
	return nil
}

func requireConversationAbsent(t *testing.T, ctx context.Context, process *suite.NodeProcess, apiAddr, uid, channelID string) {
	t.Helper()
	page := fetchConversationList(t, ctx, apiAddr, uid, 10)
	if item, ok := findConversation(page, channelID); ok {
		t.Fatalf("uid %s unexpectedly has non-subscriber conversation: %#v\n%s", uid, item, process.DumpDiagnostics())
	}
}

func requireConversationEventually(t *testing.T, process *suite.NodeProcess, apiAddr, uid, channelID string, check func(conversationListItem) error) conversationListPage {
	t.Helper()
	return requireConversationEventuallyWithin(t, process, apiAddr, uid, channelID, 5*time.Second, check)
}

func requireConversationEventuallyWithin(t *testing.T, process *suite.NodeProcess, apiAddr, uid, channelID string, timeout time.Duration, check func(conversationListItem) error) conversationListPage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage conversationListPage
	var lastErr error
	for {
		page, err := postConversationList(ctx, apiAddr, uid, 10)
		if err == nil {
			lastPage = page
			if item, ok := findConversation(page, channelID); ok {
				if checkErr := check(item); checkErr == nil {
					return page
				} else {
					lastErr = checkErr
				}
			} else {
				lastErr = fmt.Errorf("conversation %s not found in %#v", channelID, page.Conversations)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("conversation %s for uid %s timed out: lastPage=%#v lastErr=%v\n%s", channelID, uid, lastPage, lastErr, process.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func fetchConversationList(t *testing.T, ctx context.Context, apiAddr, uid string, limit int) conversationListPage {
	t.Helper()
	page, err := postConversationList(ctx, apiAddr, uid, limit)
	require.NoError(t, err)
	return page
}

func postConversationList(ctx context.Context, apiAddr, uid string, limit int) (conversationListPage, error) {
	var page conversationListPage
	data, err := json.Marshal(map[string]any{"uid": uid, "limit": limit})
	if err != nil {
		return page, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+apiAddr+"/conversation/list", bytes.NewReader(data))
	if err != nil {
		return page, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return page, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return page, err
	}
	if resp.StatusCode/100 != 2 {
		return page, fmt.Errorf("conversation/list status=%d body=%s", resp.StatusCode, respBody)
	}
	if err := json.Unmarshal(respBody, &page); err != nil {
		return page, err
	}
	return page, nil
}

func findConversation(page conversationListPage, channelID string) (conversationListItem, bool) {
	for _, item := range page.Conversations {
		if item.ChannelID == channelID {
			return item, true
		}
	}
	return conversationListItem{}, false
}

func postLegacyJSON(t *testing.T, ctx context.Context, url string, body map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Truef(t, resp.StatusCode/100 == 2, "status=%d body=%s", resp.StatusCode, respBody)
}

func postMessageSend(t *testing.T, ctx context.Context, apiBaseURL string, body map[string]any) messageSendResponse {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/message/send", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Truef(t, resp.StatusCode/100 == 2, "status=%d body=%s", resp.StatusCode, respBody)
	var out messageSendResponse
	require.NoError(t, json.Unmarshal(respBody, &out), "body=%s", respBody)
	return out
}

func requireMetricAtLeastEventually(t *testing.T, process *suite.NodeProcess, apiAddr, name string, labels map[string]string, want float64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last float64
	var lastErr error
	for {
		last, lastErr = fetchMetricValue(ctx, apiAddr, name, labels)
		if lastErr == nil && last >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metric %s%v = %v err=%v, want >= %v\n%s", name, labels, last, lastErr, want, process.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func fetchMetricValue(ctx context.Context, apiAddr, name string, labels map[string]string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiAddr+"/metrics", nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		metricName, metricLabels, value, ok := parseMetricSample(line)
		if !ok || metricName != name || !metricLabelsMatch(metricLabels, labels) {
			continue
		}
		return value, nil
	}
	return 0, fmt.Errorf("metric sample not found")
}

func parseMetricSample(line string) (string, map[string]string, float64, bool) {
	parts := strings.Fields(line)
	if len(parts) != 2 {
		return "", nil, 0, false
	}
	value, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return "", nil, 0, false
	}
	nameAndLabels := parts[0]
	labels := map[string]string{}
	if idx := strings.IndexByte(nameAndLabels, '{'); idx >= 0 {
		if !strings.HasSuffix(nameAndLabels, "}") {
			return "", nil, 0, false
		}
		name := nameAndLabels[:idx]
		labelBody := strings.TrimSuffix(nameAndLabels[idx+1:], "}")
		for _, raw := range strings.Split(labelBody, ",") {
			if raw == "" {
				continue
			}
			kv := strings.SplitN(raw, "=", 2)
			if len(kv) != 2 {
				return "", nil, 0, false
			}
			labels[kv[0]] = strings.Trim(kv[1], `"`)
		}
		return name, labels, value, true
	}
	return nameAndLabels, labels, value, true
}

func metricLabelsMatch(got, want map[string]string) bool {
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

type messageSendResponse struct {
	MessageID  int64  `json:"message_id"`
	MessageSeq uint64 `json:"message_seq"`
	Reason     uint8  `json:"reason"`
}

type conversationListPage struct {
	Conversations []conversationListItem `json:"conversations"`
	More          int                    `json:"more"`
}

type conversationListItem struct {
	ChannelID    string                   `json:"channel_id"`
	ChannelType  int64                    `json:"channel_type"`
	ActiveAt     int64                    `json:"active_at"`
	SparseActive bool                     `json:"sparse_active"`
	Unread       uint64                   `json:"unread"`
	LastMessage  *conversationLastMessage `json:"last_message"`
}

type conversationLastMessage struct {
	MessageID   uint64 `json:"message_id"`
	MessageSeq  uint64 `json:"message_seq"`
	FromUID     string `json:"from_uid"`
	ClientMsgNo string `json:"client_msg_no"`
	Payload     []byte `json:"payload"`
}

func hundredKConversationSubscribers() []string {
	subscribers := make([]string, 100000)
	for i := range subscribers {
		subscribers[i] = hundredKConversationSubscriberUID(i)
	}
	return subscribers
}

func hundredKConversationSubscriberUID(index int) string {
	return fmt.Sprintf("conv-100k-member-%06d", index)
}

func buildWukongIMV2Binary(t *testing.T) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "wukongimv2")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/wukongimv2")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build ./cmd/wukongimv2:\n%s", output)
	return binaryPath
}

func newSingleNodeClusterSpec(workspace suite.Workspace, ports suite.PortSet) suite.NodeSpec {
	const nodeID uint64 = 1
	return suite.NodeSpec{
		ID:          nodeID,
		RootDir:     workspace.NodeRootDir(nodeID),
		DataDir:     workspace.NodeDataDir(nodeID),
		ConfigPath:  workspace.NodeConfigPath(nodeID),
		StdoutPath:  workspace.NodeStdoutPath(nodeID),
		StderrPath:  workspace.NodeStderrPath(nodeID),
		ClusterAddr: ports.ClusterAddr,
		GatewayAddr: ports.GatewayAddr,
		APIAddr:     ports.APIAddr,
		Env:         renderSingleNodeClusterEnv(nodeID, workspace.NodeDataDir(nodeID), ports.ClusterAddr, ports.GatewayAddr),
	}
}

func renderSingleNodeClusterConfig(spec suite.NodeSpec) string {
	return fmt.Sprintf(`WK_NODE_ID=%d
WK_NODE_DATA_DIR=%s
WK_CLUSTER_LISTEN_ADDR=%s
WK_CLUSTER_INITIAL_SLOT_COUNT=1
WK_CLUSTER_HASH_SLOT_COUNT=4
WK_CLUSTER_SLOT_REPLICA_N=1
WK_API_LISTEN_ADDR=%s
WK_METRICS_ENABLE=true
WK_GATEWAY_LISTENERS=%s
WK_GATEWAY_SEND_TIMEOUT=5s
`, spec.ID, spec.DataDir, spec.ClusterAddr, spec.APIAddr, renderGatewayListeners(spec.GatewayAddr))
}

func renderSingleNodeClusterEnv(nodeID uint64, dataDir, clusterAddr, gatewayAddr string) []string {
	return []string{
		fmt.Sprintf("WK_NODE_ID=%d", nodeID),
		"WK_NODE_DATA_DIR=" + dataDir,
		"WK_CLUSTER_LISTEN_ADDR=" + clusterAddr,
		"WK_CLUSTER_INITIAL_SLOT_COUNT=1",
		"WK_CLUSTER_HASH_SLOT_COUNT=4",
		"WK_CLUSTER_SLOT_REPLICA_N=1",
		"WK_GATEWAY_LISTENERS=" + renderGatewayListeners(gatewayAddr),
		"WK_GATEWAY_SEND_TIMEOUT=5s",
	}
}

func renderGatewayListeners(gatewayAddr string) string {
	return fmt.Sprintf(`[{"name":"tcp-wkproto","network":"tcp","address":"%s","transport":"gnet","protocol":"wkproto"}]`, gatewayAddr)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve current test file")
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
