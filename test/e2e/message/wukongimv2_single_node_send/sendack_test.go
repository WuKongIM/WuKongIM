//go:build e2e

package wukongimv2_single_node_send

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMV2SingleNodeClusterSendProjectsConversationList(t *testing.T) {
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

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.Connect(spec.GatewayAddr, "v2-sender", "v2-sender-device"), process.DumpDiagnostics())

	const (
		clientSeq   uint64 = 1
		clientMsgNo        = "wukongimv2-sendack-e2e-1"
	)
	require.NoError(t, client.SendFrame(&frame.SendPacket{
		ChannelID:   "v2-recipient",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     []byte("hello from wukongimv2 e2e"),
	}), process.DumpDiagnostics())

	sendack, err := client.ReadSendAck()
	require.NoError(t, err, process.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, process.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	senderPage := requireConversationListEventually(t, process, spec.APIAddr, "v2-sender", func(page conversationListPage) error {
		if len(page.Conversations) != 1 {
			return fmt.Errorf("conversation count = %d, want one", len(page.Conversations))
		}
		item := page.Conversations[0]
		if item.ChannelID != "v2-recipient" || item.ChannelType != int64(frame.ChannelTypePerson) {
			return fmt.Errorf("conversation key = %s/%d, want peer person channel", item.ChannelID, item.ChannelType)
		}
		if item.SparseActive {
			return fmt.Errorf("sparse_active = true, want dense person conversation")
		}
		if item.LastMessage == nil {
			return fmt.Errorf("last_message is nil")
		}
		if item.LastMessage.MessageID != uint64(sendack.MessageID) || item.LastMessage.MessageSeq != sendack.MessageSeq {
			return fmt.Errorf("last message id/seq = %d/%d, want %d/%d", item.LastMessage.MessageID, item.LastMessage.MessageSeq, sendack.MessageID, sendack.MessageSeq)
		}
		if item.LastMessage.FromUID != "v2-sender" || item.LastMessage.ClientMsgNo != clientMsgNo || string(item.LastMessage.Payload) != "hello from wukongimv2 e2e" {
			return fmt.Errorf("last message = %#v, want original committed message", item.LastMessage)
		}
		return nil
	})
	require.Equal(t, 0, senderPage.More)

	receiverPage := requireConversationListEventually(t, process, spec.APIAddr, "v2-recipient", func(page conversationListPage) error {
		if len(page.Conversations) != 1 {
			return fmt.Errorf("conversation count = %d, want one", len(page.Conversations))
		}
		item := page.Conversations[0]
		if item.ChannelID != "v2-sender" || item.ChannelType != int64(frame.ChannelTypePerson) {
			return fmt.Errorf("conversation key = %s/%d, want sender peer person channel", item.ChannelID, item.ChannelType)
		}
		if item.LastMessage == nil || item.LastMessage.ClientMsgNo != clientMsgNo || string(item.LastMessage.Payload) != "hello from wukongimv2 e2e" {
			return fmt.Errorf("conversation = %#v, want projected last message", item)
		}
		return nil
	})
	require.Equal(t, 0, receiverPage.More)
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

func requireConversationListEventually(t *testing.T, process *suite.NodeProcess, apiAddr, uid string, check func(conversationListPage) error) conversationListPage {
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
			if checkErr := check(page); checkErr == nil {
				return page
			} else {
				lastErr = checkErr
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
