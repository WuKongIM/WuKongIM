//go:build e2e

package wukongimv2_conversation_projection

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

func TestWukongIMV2GroupConversationProjectionDenseAndSparse(t *testing.T) {
	binaryPath := buildWukongIMV2Binary(t)
	workspace := suite.NewWorkspace(t)
	ports := suite.ReserveLoopbackPorts(t)
	spec := newSingleNodeClusterSpec(workspace, ports)

	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte(renderSingleNodeClusterConfig(spec)), 0o644))

	process := &suite.NodeProcess{
		Spec:       spec,
		BinaryPath: binaryPath,
	}
	require.NoError(t, process.Start())
	t.Cleanup(func() {
		require.NoError(t, process.Stop())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.WaitHTTPReady(ctx, spec.APIAddr, "/readyz"), process.DumpDiagnostics())
	require.NoError(t, suite.WaitWKProtoReady(ctx, spec.GatewayAddr), process.DumpDiagnostics())

	apiBaseURL := "http://" + spec.APIAddr
	const smallChannelID = "e2e-conversation-small-group"
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
		"payload":       base64.StdEncoding.EncodeToString([]byte("small group projection")),
	})
	require.Equal(t, uint8(frame.ReasonSuccess), smallSend.Reason)
	require.NotZero(t, smallSend.MessageID)
	require.NotZero(t, smallSend.MessageSeq)

	for _, uid := range []string{"conv-small-sender", "conv-small-a", "conv-small-b"} {
		page := requireConversationEventually(t, process, spec.APIAddr, uid, smallChannelID, func(item conversationListItem) error {
			if item.SparseActive {
				return fmt.Errorf("sparse_active = true, want dense small-group row")
			}
			if item.LastMessage == nil {
				return fmt.Errorf("last_message is nil")
			}
			if item.LastMessage.MessageID != uint64(smallSend.MessageID) || item.LastMessage.MessageSeq != smallSend.MessageSeq ||
				item.LastMessage.FromUID != "conv-small-sender" || item.LastMessage.ClientMsgNo != "conv-small-1" ||
				string(item.LastMessage.Payload) != "small group projection" {
				return fmt.Errorf("last_message = %#v, want small-group message", item.LastMessage)
			}
			return nil
		})
		require.Equal(t, 0, page.More)
	}

	const largeChannelID = "e2e-conversation-large-group"
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
		"payload":       base64.StdEncoding.EncodeToString([]byte("large group sparse projection")),
	})
	require.Equal(t, uint8(frame.ReasonSuccess), largeSend.Reason)
	require.NotZero(t, largeSend.MessageID)
	require.NotZero(t, largeSend.MessageSeq)

	senderPage := requireConversationEventually(t, process, spec.APIAddr, "conv-large-sender", largeChannelID, func(item conversationListItem) error {
		if !item.SparseActive {
			return fmt.Errorf("sparse_active = false, want sparse large-group sender row")
		}
		if item.LastMessage == nil {
			return fmt.Errorf("last_message is nil")
		}
		if item.LastMessage.MessageID != uint64(largeSend.MessageID) || item.LastMessage.MessageSeq != largeSend.MessageSeq ||
			item.LastMessage.ClientMsgNo != "conv-large-1" || string(item.LastMessage.Payload) != "large group sparse projection" {
			return fmt.Errorf("last_message = %#v, want large-group message", item.LastMessage)
		}
		return nil
	})
	require.Equal(t, 0, senderPage.More)

	for _, uid := range []string{"conv-large-a", "conv-large-b", "conv-large-c"} {
		page := fetchConversationList(t, ctx, spec.APIAddr, uid, 10)
		if item, ok := findConversation(page, largeChannelID); ok {
			t.Fatalf("uid %s unexpectedly has large-group conversation: %#v\n%s", uid, item, process.DumpDiagnostics())
		}
	}
}

func TestWukongIMV2HundredKGroupConversationProjectionStaysSparse(t *testing.T) {
	if os.Getenv("WK_E2E_100K_CONVERSATION") != "1" {
		t.Skip("set WK_E2E_100K_CONVERSATION=1 to run the 100k conversation projection stress test")
	}

	binaryPath := buildWukongIMV2Binary(t)
	workspace := suite.NewWorkspace(t)
	ports := suite.ReserveLoopbackPorts(t)
	spec := newSingleNodeClusterSpec(workspace, ports)

	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte(renderSingleNodeClusterConfig(spec)), 0o644))

	process := &suite.NodeProcess{
		Spec:       spec,
		BinaryPath: binaryPath,
	}
	require.NoError(t, process.Start())
	t.Cleanup(func() {
		require.NoError(t, process.Stop())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	require.NoError(t, suite.WaitHTTPReady(ctx, spec.APIAddr, "/readyz"), process.DumpDiagnostics())
	require.NoError(t, suite.WaitWKProtoReady(ctx, spec.GatewayAddr), process.DumpDiagnostics())

	apiBaseURL := "http://" + spec.APIAddr
	const channelID = "e2e-conversation-100k-group"
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
		"payload":       base64.StdEncoding.EncodeToString([]byte("100k sparse projection")),
	})
	require.Equal(t, uint8(frame.ReasonSuccess), sendResp.Reason)

	requireConversationEventually(t, process, spec.APIAddr, "conv-100k-sender", channelID, func(item conversationListItem) error {
		if !item.SparseActive {
			return fmt.Errorf("sparse_active = false, want sparse sender row")
		}
		if item.LastMessage == nil || item.LastMessage.MessageID != uint64(sendResp.MessageID) ||
			item.LastMessage.MessageSeq != sendResp.MessageSeq || item.LastMessage.ClientMsgNo != "conv-100k-1" {
			return fmt.Errorf("last_message = %#v, want 100k committed message", item.LastMessage)
		}
		return nil
	})

	for _, uid := range []string{hundredKConversationSubscriberUID(0), hundredKConversationSubscriberUID(50000), hundredKConversationSubscriberUID(99999)} {
		page := fetchConversationList(t, ctx, spec.APIAddr, uid, 10)
		if item, ok := findConversation(page, channelID); ok {
			t.Fatalf("uid %s unexpectedly has 100k sparse conversation: %#v\n%s", uid, item, process.DumpDiagnostics())
		}
	}

	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_conversation_authority_admit_total`, map[string]string{"result": "ok"}, 1)
	requireMetricAtLeastEventually(t, process, spec.APIAddr, `wukongim_conversation_authority_list_total`, map[string]string{"result": "ok"}, 1)
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

func requireConversationEventually(t *testing.T, process *suite.NodeProcess, apiAddr, uid, channelID string, check func(conversationListItem) error) conversationListPage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
			require.NoError(t, ctx.Err(), "last conversation/list error: %v page:%#v\n%s", lastErr, lastPage, process.DumpDiagnostics())
			return conversationListPage{}
		case <-ticker.C:
		}
	}
}

func postLegacyJSON(t *testing.T, ctx context.Context, url string, payload any) {
	t.Helper()

	body := postJSON(t, ctx, url, payload)
	require.JSONEq(t, `{"status":200}`, string(body))
}

func postMessageSend(t *testing.T, ctx context.Context, apiBaseURL string, payload any) messageSendResponse {
	t.Helper()

	body := postJSON(t, ctx, apiBaseURL+"/message/send", payload)
	var out messageSendResponse
	require.NoError(t, json.Unmarshal(body, &out), "body=%s", string(body))
	return out
}

func postJSON(t *testing.T, ctx context.Context, url string, payload any) []byte {
	t.Helper()

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, strings.TrimSpace(string(body)))
	return body
}

func fetchConversationList(t *testing.T, ctx context.Context, apiAddr, uid string, limit int) conversationListPage {
	t.Helper()

	page, err := postConversationList(ctx, apiAddr, uid, limit)
	require.NoError(t, err)
	return page
}

func postConversationList(ctx context.Context, apiAddr, uid string, limit int) (conversationListPage, error) {
	body, err := json.Marshal(map[string]any{
		"uid":   uid,
		"limit": limit,
	})
	if err != nil {
		return conversationListPage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+apiAddr+"/conversation/list", bytes.NewReader(body))
	if err != nil {
		return conversationListPage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return conversationListPage{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return conversationListPage{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return conversationListPage{}, fmt.Errorf("conversation/list returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var page conversationListPage
	if err := json.Unmarshal(respBody, &page); err != nil {
		return conversationListPage{}, fmt.Errorf("decode conversation/list: %w body=%s", err, strings.TrimSpace(string(respBody)))
	}
	return page, nil
}

func findConversation(page conversationListPage, channelID string) (conversationListItem, bool) {
	for _, item := range page.Conversations {
		if item.ChannelID == channelID && item.ChannelType == int64(frame.ChannelTypeGroup) {
			return item, true
		}
	}
	return conversationListItem{}, false
}

func requireMetricValueEventually(t *testing.T, process *suite.NodeProcess, apiAddr, metricName string, labels map[string]string, want float64) {
	t.Helper()
	requireMetricEventually(t, process, apiAddr, metricName, labels, func(got float64) bool {
		return got == want
	}, fmt.Sprintf("want %v", want))
}

func requireMetricAtLeastEventually(t *testing.T, process *suite.NodeProcess, apiAddr, metricName string, labels map[string]string, min float64) {
	t.Helper()
	requireMetricEventually(t, process, apiAddr, metricName, labels, func(got float64) bool {
		return got >= min
	}, fmt.Sprintf("want >= %v", min))
}

func requireMetricEventually(t *testing.T, process *suite.NodeProcess, apiAddr, metricName string, labels map[string]string, match func(float64) bool, want string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastBody string
	var lastValue float64
	var found bool
	for {
		body, err := fetchMetrics(ctx, apiAddr)
		if err == nil {
			lastBody = body
			if value, ok := findPrometheusSample(body, metricName, labels); ok {
				lastValue = value
				found = true
				if match(value) {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metric %s labels=%v value=%v found=%v %s\nmetrics:\n%s\n%s", metricName, labels, lastValue, found, want, lastBody, process.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func fetchMetrics(ctx context.Context, apiAddr string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiAddr+"/metrics", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metrics returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func findPrometheusSample(body, metricName string, labels map[string]string) (float64, bool) {
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, metricName) {
			continue
		}
		nameAndLabels, rawValue, ok := strings.Cut(line, " ")
		if !ok || !prometheusSampleHasLabels(nameAndLabels, labels) {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(rawValue), 64)
		if err != nil {
			continue
		}
		return value, true
	}
	return 0, false
}

func prometheusSampleHasLabels(nameAndLabels string, labels map[string]string) bool {
	for key, value := range labels {
		if !strings.Contains(nameAndLabels, fmt.Sprintf(`%s=%q`, key, value)) {
			return false
		}
	}
	return true
}

func hundredKConversationSubscribers() []string {
	const count = 100000
	subscribers := make([]string, count)
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
WK_CONVERSATION_SMALL_GROUP_FANOUT_LIMIT=2
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
