//go:build e2e

package wukongimv2_single_node_send

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMV2SingleNodeClusterSendReturnsSendack(t *testing.T) {
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
WK_GATEWAY_LISTENERS=%s
WK_GATEWAY_SEND_TIMEOUT=5s
`, spec.ID, spec.DataDir, spec.ClusterAddr, renderGatewayListeners(spec.GatewayAddr))
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
