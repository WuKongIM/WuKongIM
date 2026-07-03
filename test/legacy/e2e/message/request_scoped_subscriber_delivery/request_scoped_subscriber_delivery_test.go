//go:build e2e && legacy_e2e

package request_scoped_subscriber_delivery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/legacy/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestRequestScopedSubscriberDeliveryAcrossNodes(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	node1 := cluster.MustNode(1)
	node2 := cluster.MustNode(2)
	node3 := cluster.MustNode(3)

	subscriberA := connectClient(t, node2, "scoped-subscriber-a", "scoped-subscriber-a-device", cluster)
	defer func() { _ = subscriberA.Close() }()
	subscriberB := connectClient(t, node3, "scoped-subscriber-b", "scoped-subscriber-b-device", cluster)
	defer func() { _ = subscriberB.Close() }()
	outsider := connectClient(t, node1, "scoped-outsider", "scoped-outsider-device", cluster)
	defer func() { _ = outsider.Close() }()

	requireConnection(t, ctx, node2, "scoped-subscriber-a", cluster)
	requireConnection(t, ctx, node3, "scoped-subscriber-b", cluster)
	requireConnection(t, ctx, node1, "scoped-outsider", cluster)

	apiBaseURL := "http://" + node1.Spec.APIAddr
	durable := postRequestScopedSend(t, ctx, apiBaseURL, requestScopedSendRequest{
		FromUID:     "system-scoped",
		ClientMsgNo: "request-scoped-durable-1",
		Payload:     []byte("request scoped durable payload"),
		Subscribers: []string{"scoped-subscriber-a", "scoped-subscriber-b", "scoped-subscriber-a"},
		SyncOnce:    true,
	})
	require.NotZero(t, durable.MessageID)
	require.NotZero(t, durable.MessageSeq)

	requireRequestScopedRecv(t, cluster, subscriberA, durable, requestScopedRecvExpectation{
		FromUID:   "system-scoped",
		Payload:   []byte("request scoped durable payload"),
		SyncOnce:  true,
		NoPersist: false,
	})
	requireRequestScopedRecv(t, cluster, subscriberB, durable, requestScopedRecvExpectation{
		FromUID:   "system-scoped",
		Payload:   []byte("request scoped durable payload"),
		SyncOnce:  true,
		NoPersist: false,
	})
	requireNoRecv(t, outsider, "scoped-outsider", cluster)

	nonDurable := postRequestScopedSend(t, ctx, apiBaseURL, requestScopedSendRequest{
		FromUID:     "system-scoped",
		ClientMsgNo: "request-scoped-non-durable-1",
		Payload:     []byte("request scoped non durable payload"),
		Subscribers: []string{"scoped-subscriber-a", "scoped-subscriber-b"},
		SyncOnce:    true,
		NoPersist:   true,
	})
	require.NotZero(t, nonDurable.MessageID)
	require.Zero(t, nonDurable.MessageSeq)

	requireRequestScopedRecv(t, cluster, subscriberA, nonDurable, requestScopedRecvExpectation{
		FromUID:   "system-scoped",
		Payload:   []byte("request scoped non durable payload"),
		SyncOnce:  true,
		NoPersist: true,
	})
	requireRequestScopedRecv(t, cluster, subscriberB, nonDurable, requestScopedRecvExpectation{
		FromUID:   "system-scoped",
		Payload:   []byte("request scoped non durable payload"),
		SyncOnce:  true,
		NoPersist: true,
	})
	requireNoRecv(t, outsider, "scoped-outsider", cluster)
}

type requestScopedSendRequest struct {
	FromUID     string
	ClientMsgNo string
	Payload     []byte
	Subscribers []string
	SyncOnce    bool
	NoPersist   bool
}

type requestScopedSendResponse struct {
	MessageID  int64  `json:"message_id"`
	MessageSeq uint64 `json:"message_seq"`
	Reason     uint8  `json:"reason"`
}

type requestScopedRecvExpectation struct {
	FromUID   string
	Payload   []byte
	SyncOnce  bool
	NoPersist bool
}

func connectClient(t *testing.T, node *suite.StartedNode, uid, deviceID string, cluster *suite.StartedCluster) *suite.WKProtoClient {
	t.Helper()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, deviceID), cluster.DumpDiagnostics())
	return client
}

func requireConnection(t *testing.T, ctx context.Context, node *suite.StartedNode, uid string, cluster *suite.StartedCluster) {
	t.Helper()

	ok, err := suite.ConnectionsContainUID(ctx, *node, uid)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Truef(t, ok, "uid %s was not visible on node %d\n%s", uid, node.Spec.ID, cluster.DumpDiagnostics())
}

func postRequestScopedSend(t *testing.T, ctx context.Context, apiBaseURL string, req requestScopedSendRequest) requestScopedSendResponse {
	t.Helper()

	body := map[string]any{
		"sender_uid":    req.FromUID,
		"client_msg_no": req.ClientMsgNo,
		"payload":       base64.StdEncoding.EncodeToString(req.Payload),
		"subscribers":   req.Subscribers,
		"header": map[string]int{
			"sync_once":  boolToInt(req.SyncOnce),
			"no_persist": boolToInt(req.NoPersist),
		},
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/message/send", bytes.NewReader(data))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := http.DefaultClient.Do(httpReq)
	require.NoError(t, err)
	defer func() { _ = httpResp.Body.Close() }()

	respBody, err := io.ReadAll(httpResp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, httpResp.StatusCode, strings.TrimSpace(string(respBody)))

	var resp requestScopedSendResponse
	require.NoError(t, json.Unmarshal(respBody, &resp), strings.TrimSpace(string(respBody)))
	require.Equal(t, uint8(frame.ReasonSuccess), resp.Reason)
	return resp
}

func requireRequestScopedRecv(t *testing.T, cluster *suite.StartedCluster, recipient *suite.WKProtoClient, sent requestScopedSendResponse, expected requestScopedRecvExpectation) {
	t.Helper()

	recv, err := recipient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, expected.FromUID, recv.FromUID)
	require.Equal(t, frame.ChannelTypeTemp, recv.ChannelType)
	require.NotEmpty(t, recv.ChannelID)
	require.NotContains(t, recv.ChannelID, "____cmd")
	require.Equal(t, expected.Payload, recv.Payload)
	require.Equal(t, sent.MessageID, recv.MessageID)
	require.Equal(t, sent.MessageSeq, recv.MessageSeq)
	require.Equal(t, expected.SyncOnce, recv.Framer.SyncOnce)
	require.Equal(t, expected.NoPersist, recv.Framer.NoPersist)
	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq))
}

func requireNoRecv(t *testing.T, recipient *suite.WKProtoClient, uid string, cluster *suite.StartedCluster) {
	t.Helper()

	recv, err := recipient.ReadRecv()
	if err == nil {
		require.Failf(t, "unexpected request-scoped message received", "uid=%s message_id=%d seq=%d payload=%q\n%s", uid, recv.MessageID, recv.MessageSeq, string(recv.Payload), cluster.DumpDiagnostics())
	}
	require.Truef(t, isTimeoutError(err), "uid=%s unexpected read error: %v\n%s", uid, err, cluster.DumpDiagnostics())
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "i/o timeout") || strings.Contains(err.Error(), "deadline exceeded")
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
