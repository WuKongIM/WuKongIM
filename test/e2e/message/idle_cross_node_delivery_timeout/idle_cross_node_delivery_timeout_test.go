//go:build e2e

package idle_cross_node_delivery_timeout

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	nodetransport "github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const managerPollInterval = 250 * time.Millisecond

const idleCrossNodeDeliveryScenarioName = "idle_cross_node_delivery_timeout"

// channelRuntimeMetaDetail mirrors the manager runtime-meta fields used by
// this black-box scenario for diagnostics.
type channelRuntimeMetaDetail struct {
	ChannelID    string `json:"channel_id"`
	ChannelType  int64  `json:"channel_type"`
	Leader       uint64 `json:"leader"`
	ChannelEpoch uint64 `json:"channel_epoch"`
	LeaderEpoch  uint64 `json:"leader_epoch"`
	LeaseUntilMS int64  `json:"lease_until_ms"`
}

type connectionExpectation struct {
	node suite.StartedNode
	uid  string
}

func idleCrossNodeDeliveryDebugRoots() (string, string) {
	baseDir := filepath.Join(os.TempDir(), "wukongim-e2e-debug", idleCrossNodeDeliveryScenarioName)
	return filepath.Join(baseDir, "artifacts"), filepath.Join(baseDir, "logs")
}

func startIdleCrossNodeDebugCluster(t *testing.T, s *suite.Suite) *suite.StartedCluster {
	t.Helper()

	workspaceRoot, logRoot := idleCrossNodeDeliveryDebugRoots()
	t.Logf("idle debug workspace root: %s", workspaceRoot)
	t.Logf("idle debug log root: %s", logRoot)

	return s.StartThreeNodeCluster(
		suite.WithWorkspaceRootDir(workspaceRoot),
		suite.WithNodeLogRootDir(logRoot),
		suite.WithNodeConfigOverrides(1, map[string]string{"WK_LOG_LEVEL": "debug"}),
		suite.WithNodeConfigOverrides(2, map[string]string{"WK_LOG_LEVEL": "debug"}),
		suite.WithNodeConfigOverrides(3, map[string]string{"WK_LOG_LEVEL": "debug"}),
	)
}

func TestIdleCrossNodeDeliveryDebugRootsUseStableTempLocation(t *testing.T) {
	workspaceRoot, logRoot := idleCrossNodeDeliveryDebugRoots()

	require.Equal(t, filepath.Join(os.TempDir(), "wukongim-e2e-debug", "idle_cross_node_delivery_timeout", "artifacts"), workspaceRoot)
	require.Equal(t, filepath.Join(os.TempDir(), "wukongim-e2e-debug", "idle_cross_node_delivery_timeout", "logs"), logRoot)
}

func TestCrossNodeMutualSendStillDeliversAfterLeaseExpiry(t *testing.T) {
	s := suite.New(t)
	cluster := startIdleCrossNodeDebugCluster(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	topology, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Len(t, topology.FollowerNodeIDs, 2)

	senderNode := cluster.MustNode(topology.FollowerNodeIDs[0])
	recipientNode := cluster.MustNode(topology.FollowerNodeIDs[1])
	observerNode := cluster.MustNode(topology.LeaderNodeID)

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	recipient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = recipient.Close() }()

	require.NoError(t, sender.Connect(senderNode.GatewayAddr(), "safe1", "safe1-device"))
	require.NoError(t, recipient.Connect(recipientNode.GatewayAddr(), "safe2", "safe2-device"))

	require.NoError(t, requireConnectionPresent(ctx, *senderNode, "safe1"), cluster.DumpDiagnostics())
	require.NoError(t, requireConnectionPresent(ctx, *recipientNode, "safe2"), cluster.DumpDiagnostics())

	presenceClient := newPresenceProbeClient(t, cluster)
	requirePresenceRouteEventually(t, ctx, presenceClient, "safe1", senderNode.Spec.ID, cluster, "")
	requirePresenceRouteEventually(t, ctx, presenceClient, "safe2", recipientNode.Spec.ID, cluster, "")

	senderSeq := uint64(1)
	recipientSeq := uint64(1)
	channelID := encodePersonChannel("safe1", "safe2")

	sendAndRequireRecvClosure(
		t,
		cluster,
		sender,
		recipient,
		"safe1",
		"safe2",
		"idle-bootstrap-safe1-to-safe2-1",
		senderSeq,
		[]byte("hello before idle window"),
		"",
	)
	senderSeq++

	channelMetaBody := waitForExpiredChannelRuntimeMeta(
		t,
		ctx,
		cluster,
		*observerNode,
		channelID,
		[]connectionExpectation{{node: *senderNode, uid: "safe1"}, {node: *recipientNode, uid: "safe2"}},
	)

	requirePresenceRouteEventually(t, ctx, presenceClient, "safe1", senderNode.Spec.ID, cluster, channelMetaBody)
	requirePresenceRouteEventually(t, ctx, presenceClient, "safe2", recipientNode.Spec.ID, cluster, channelMetaBody)

	for round := 0; round < 5; round++ {
		sendAndRequireRecvClosure(
			t,
			cluster,
			sender,
			recipient,
			"safe1",
			"safe2",
			fmt.Sprintf("idle-safe1-to-safe2-%d", round+1),
			senderSeq,
			[]byte(fmt.Sprintf("hello after idle from safe1 #%d", round+1)),
			channelMetaBody,
		)
		senderSeq++

		sendAndRequireRecvClosure(
			t,
			cluster,
			recipient,
			sender,
			"safe2",
			"safe1",
			fmt.Sprintf("idle-safe2-to-safe1-%d", round+1),
			recipientSeq,
			[]byte(fmt.Sprintf("hello after idle from safe2 #%d", round+1)),
			channelMetaBody,
		)
		recipientSeq++
	}
}

func sendAndRequireRecvClosure(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID, clientMsgNo string,
	clientSeq uint64,
	payload []byte,
	channelMetaBody string,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}), clusterDiagnostics(cluster, channelMetaBody))

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, clusterDiagnostics(cluster, channelMetaBody))
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	recv, err := recipient.ReadRecv()
	require.NoError(t, err, clusterDiagnostics(cluster, channelMetaBody))
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, senderUID, recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, payload, recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), clusterDiagnostics(cluster, channelMetaBody))
}

func waitForExpiredChannelRuntimeMeta(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	observerNode suite.StartedNode,
	channelID string,
	expectations []connectionExpectation,
) string {
	t.Helper()

	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var (
		lastBody string
		lastErr  error
	)

	for {
		connectionsHealthy := true
		for _, expectation := range expectations {
			items, body, err := suite.FetchConnections(ctx, expectation.node)
			if err != nil {
				lastErr = err
				connectionsHealthy = false
				continue
			}
			if !connectionsContainUID(items, expectation.uid) {
				lastErr = fmt.Errorf("uid %s missing from node %d connections: %s", expectation.uid, expectation.node.Spec.ID, string(body))
				connectionsHealthy = false
			}
		}

		detail, body, err := fetchChannelRuntimeMetaDetail(ctx, observerNode, channelID, frame.ChannelTypePerson)
		if err == nil {
			lastBody = body
			if connectionsHealthy && detail.LeaseUntilMS < time.Now().UnixMilli() {
				return body
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			require.NoError(t, lastErr, clusterDiagnostics(cluster, lastBody))
			t.Fatalf("timed out waiting for expired channel runtime metadata with live connections: %s", clusterDiagnostics(cluster, lastBody))
		case <-ticker.C:
		}
	}
}

func requireConnectionPresent(ctx context.Context, node suite.StartedNode, uid string) error {
	items, body, err := suite.FetchConnections(ctx, node)
	if err != nil {
		return err
	}
	if !connectionsContainUID(items, uid) {
		return fmt.Errorf("uid %s missing from node %d connections: %s", uid, node.Spec.ID, string(body))
	}
	return nil
}

func connectionsContainUID(items []suite.ManagerConnection, uid string) bool {
	for _, item := range items {
		if item.UID == uid {
			return true
		}
	}
	return false
}

func fetchChannelRuntimeMetaDetail(
	ctx context.Context,
	node suite.StartedNode,
	channelID string,
	channelType uint8,
) (channelRuntimeMetaDetail, string, error) {
	path := fmt.Sprintf(
		"http://%s/manager/channel-runtime-meta/%d/%s",
		node.Spec.ManagerAddr,
		channelType,
		url.PathEscape(channelID),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return channelRuntimeMetaDetail{}, string(body), fmt.Errorf("manager channel runtime meta returned %d: %s", resp.StatusCode, string(body))
	}

	var detail channelRuntimeMetaDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return channelRuntimeMetaDetail{}, string(body), err
	}
	return detail, string(body), nil
}

func encodePersonChannel(leftUID, rightUID string) string {
	leftHash := crc32.ChecksumIEEE([]byte(leftUID))
	rightHash := crc32.ChecksumIEEE([]byte(rightUID))
	if leftHash > rightHash {
		return leftUID + "@" + rightUID
	}
	if leftHash == rightHash && leftUID > rightUID {
		return leftUID + "@" + rightUID
	}
	return rightUID + "@" + leftUID
}

func requirePresenceRouteEventually(
	t *testing.T,
	ctx context.Context,
	client *accessnode.Client,
	uid string,
	nodeID uint64,
	cluster *suite.StartedCluster,
	channelMetaBody string,
) {
	t.Helper()

	require.Eventually(t, func() bool {
		routes, err := client.EndpointsByUIDs(ctx, []string{uid})
		if err != nil {
			return false
		}
		for _, route := range routes[uid] {
			if route.NodeID == nodeID && route.SessionID != 0 {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, clusterDiagnostics(cluster, channelMetaBody))
}

func newPresenceProbeClient(t *testing.T, cluster *suite.StartedCluster) *accessnode.Client {
	t.Helper()

	discovery := e2eStaticDiscovery{addrs: make(map[uint64]string, len(cluster.Nodes))}
	peers := make([]multiraft.NodeID, 0, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		discovery.addrs[node.Spec.ID] = node.Spec.ClusterAddr
		peers = append(peers, multiraft.NodeID(node.Spec.ID))
	}
	pool := nodetransport.NewPool(nodetransport.PoolConfig{
		Discovery:   discovery,
		Size:        1,
		DialTimeout: 3 * time.Second,
	})
	t.Cleanup(pool.Close)

	client := nodetransport.NewClient(pool)
	t.Cleanup(client.Stop)
	return accessnode.NewClient(e2eNodeRPCCluster{
		peers: peers,
		rpc:   client,
	})
}

type e2eStaticDiscovery struct {
	addrs map[uint64]string
}

func (d e2eStaticDiscovery) Resolve(nodeID uint64) (string, error) {
	addr, ok := d.addrs[nodeID]
	if !ok {
		return "", fmt.Errorf("missing cluster addr for node %d", nodeID)
	}
	return addr, nil
}

type e2eNodeRPCCluster struct {
	peers []multiraft.NodeID
	rpc   *nodetransport.Client
}

func (c e2eNodeRPCCluster) RPCMux() *nodetransport.RPCMux { return nil }

func (c e2eNodeRPCCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	if len(c.peers) == 0 {
		return 0, raftcluster.ErrNoLeader
	}
	return c.peers[0], nil
}

func (c e2eNodeRPCCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c e2eNodeRPCCluster) SlotForKey(string) multiraft.SlotID { return 1 }

func (c e2eNodeRPCCluster) RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	return c.rpc.RPCService(ctx, uint64(nodeID), uint64(slotID), serviceID, payload)
}

func (c e2eNodeRPCCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return append([]multiraft.NodeID(nil), c.peers...)
}

func clusterDiagnostics(cluster *suite.StartedCluster, channelMetaBody string) string {
	var b strings.Builder
	b.WriteString(cluster.DumpDiagnostics())
	if channelMetaBody != "" {
		b.WriteString("\nchannel runtime meta body: ")
		b.WriteString(channelMetaBody)
	}
	if matches := matchingNodeLogLines(cluster, []string{
		"message.send.refresh.failed",
		"access.gateway.frame.send_failed",
		"channel: no safe leader candidate",
		"register authoritative route failed",
		"delivery.diag.committed_route",
		"delivery.diag.resolve_page",
		"delivery.diag.local_push",
		"delivery.diag.remote_push",
		"delivery.diag.submit_rpc",
		"delivery.diag.push_rpc",
		"lane poll response dropped, no lane manager",
		"repl.diag.lane_resp_no_manager",
		"repl.diag.lane_membership_removed",
		"repl.diag.lane_manager_evict",
	}); matches != "" {
		b.WriteString("\nmatching log lines:\n")
		b.WriteString(matches)
	}
	return b.String()
}

func matchingNodeLogLines(cluster *suite.StartedCluster, patterns []string) string {
	if cluster == nil {
		return ""
	}

	var b strings.Builder
	for _, node := range cluster.Nodes {
		for _, path := range []string{
			node.Spec.StdoutPath,
			node.Spec.StderrPath,
			node.Spec.LogDir + "/app.log",
			node.Spec.LogDir + "/error.log",
		} {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			lines := strings.Split(string(data), "\n")
			wroteHeader := false
			for _, line := range lines {
				for _, pattern := range patterns {
					if !strings.Contains(line, pattern) {
						continue
					}
					if !wroteHeader {
						fmt.Fprintf(&b, "node %d %s:\n", node.Spec.ID, path)
						wroteHeader = true
					}
					b.WriteString(line)
					b.WriteByte('\n')
					break
				}
			}
		}
	}
	return b.String()
}
