//go:build e2e && legacy_e2e

package delivery_tag_group_delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/legacy/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestDeliveryTagGroupDeliveryRefreshesAfterSubscriberMutation(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	node1 := cluster.MustNode(1)
	node2 := cluster.MustNode(2)
	node3 := cluster.MustNode(3)

	sender := connectClient(t, node1, "tag-sender", "tag-sender-device", cluster)
	defer func() { _ = sender.Close() }()
	recipientA := connectClient(t, node2, "tag-recipient-a", "tag-recipient-a-device", cluster)
	defer func() { _ = recipientA.Close() }()
	recipientB := connectClient(t, node3, "tag-recipient-b", "tag-recipient-b-device", cluster)
	defer func() { _ = recipientB.Close() }()
	recipientC := connectClient(t, node1, "tag-recipient-c", "tag-recipient-c-device", cluster)
	defer func() { _ = recipientC.Close() }()

	requireConnection(t, ctx, node1, "tag-sender", cluster)
	requireConnection(t, ctx, node2, "tag-recipient-a", cluster)
	requireConnection(t, ctx, node3, "tag-recipient-b", cluster)
	requireConnection(t, ctx, node1, "tag-recipient-c", cluster)

	apiBaseURL := "http://" + node1.Spec.APIAddr
	channelID := "e2e-delivery-tag-group"
	postLegacyJSON(t, ctx, apiBaseURL+"/channel", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"tag-recipient-a", "tag-recipient-b"},
	})

	sendGroupAndRequireRecipients(t, cluster, sender, channelID, 1, "delivery-tag-group-1", []byte("delivery tag group first"), map[string]*suite.WKProtoClient{
		"tag-recipient-a": recipientA,
		"tag-recipient-b": recipientB,
	})

	postLegacyJSON(t, ctx, apiBaseURL+"/channel/subscriber_add", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"tag-recipient-c"},
	})

	sendGroupAndRequireRecipients(t, cluster, sender, channelID, 2, "delivery-tag-group-2", []byte("delivery tag group after add"), map[string]*suite.WKProtoClient{
		"tag-recipient-a": recipientA,
		"tag-recipient-b": recipientB,
		"tag-recipient-c": recipientC,
	})

	postLegacyJSON(t, ctx, apiBaseURL+"/channel/subscriber_remove", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"tag-recipient-a"},
	})

	sendGroupAndRequireRecipients(t, cluster, sender, channelID, 3, "delivery-tag-group-3", []byte("delivery tag group after remove"), map[string]*suite.WKProtoClient{
		"tag-recipient-b": recipientB,
		"tag-recipient-c": recipientC,
	})
	requireNoRecv(t, recipientA, "tag-recipient-a", cluster)
}

func TestDeliveryTagLargeGroupDeliveryUsesPagedSubscribers(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	node1 := cluster.MustNode(1)
	node2 := cluster.MustNode(2)
	node3 := cluster.MustNode(3)

	sender := connectClient(t, node1, "tag-large-sender", "tag-large-sender-device", cluster)
	defer func() { _ = sender.Close() }()
	recipientA := connectClient(t, node1, "tag-large-recipient-0000", "tag-large-recipient-0000-device", cluster)
	defer func() { _ = recipientA.Close() }()
	recipientB := connectClient(t, node2, "tag-large-recipient-0001", "tag-large-recipient-0001-device", cluster)
	defer func() { _ = recipientB.Close() }()
	recipientC := connectClient(t, node3, "tag-large-recipient-0002", "tag-large-recipient-0002-device", cluster)
	defer func() { _ = recipientC.Close() }()
	recipientLatePage := connectClient(t, node2, "tag-large-recipient-1001", "tag-large-recipient-1001-device", cluster)
	defer func() { _ = recipientLatePage.Close() }()
	recipientTail := connectClient(t, node3, "tag-large-recipient-1199", "tag-large-recipient-1199-device", cluster)
	defer func() { _ = recipientTail.Close() }()
	addedRecipient := connectClient(t, node1, "tag-large-recipient-added", "tag-large-recipient-added-device", cluster)
	defer func() { _ = addedRecipient.Close() }()
	outsider := connectClient(t, node2, "tag-large-outsider", "tag-large-outsider-device", cluster)
	defer func() { _ = outsider.Close() }()

	for _, item := range []struct {
		node *suite.StartedNode
		uid  string
	}{
		{node1, "tag-large-sender"},
		{node1, "tag-large-recipient-0000"},
		{node2, "tag-large-recipient-0001"},
		{node3, "tag-large-recipient-0002"},
		{node2, "tag-large-recipient-1001"},
		{node3, "tag-large-recipient-1199"},
		{node1, "tag-large-recipient-added"},
		{node2, "tag-large-outsider"},
	} {
		requireConnection(t, ctx, item.node, item.uid, cluster)
	}

	apiBaseURL := "http://" + node1.Spec.APIAddr
	channelID := "e2e-delivery-tag-large-group"
	postLegacyJSON(t, ctx, apiBaseURL+"/channel", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  largeGroupSubscribers(1200),
	})

	initialRecipients := map[string]*suite.WKProtoClient{
		"tag-large-recipient-0000": recipientA,
		"tag-large-recipient-0001": recipientB,
		"tag-large-recipient-0002": recipientC,
		"tag-large-recipient-1001": recipientLatePage,
		"tag-large-recipient-1199": recipientTail,
	}
	sendGroupAndRequireRecipientsFrom(t, cluster, sender, "tag-large-sender", channelID, 1, "delivery-tag-large-group-1", []byte("delivery tag large group first"), initialRecipients)
	requireNoRecv(t, outsider, "tag-large-outsider", cluster)

	postLegacyJSON(t, ctx, apiBaseURL+"/channel/subscriber_add", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"tag-large-recipient-added"},
	})

	afterAddRecipients := cloneRecipientMap(initialRecipients)
	afterAddRecipients["tag-large-recipient-added"] = addedRecipient
	sendGroupAndRequireRecipientsFrom(t, cluster, sender, "tag-large-sender", channelID, 2, "delivery-tag-large-group-2", []byte("delivery tag large group after add"), afterAddRecipients)

	postLegacyJSON(t, ctx, apiBaseURL+"/channel/subscriber_remove", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"tag-large-recipient-1001"},
	})

	afterRemoveRecipients := cloneRecipientMap(afterAddRecipients)
	delete(afterRemoveRecipients, "tag-large-recipient-1001")
	sendGroupAndRequireRecipientsFrom(t, cluster, sender, "tag-large-sender", channelID, 3, "delivery-tag-large-group-3", []byte("delivery tag large group after remove"), afterRemoveRecipients)
	requireNoRecv(t, recipientLatePage, "tag-large-recipient-1001", cluster)
}

func TestDeliveryTagHundredKGroupSubscriberStress(t *testing.T) {
	if os.Getenv("WK_E2E_100K_GROUP") != "1" {
		t.Skip("set WK_E2E_100K_GROUP=1 to run the 100k subscriber delivery-tag stress test")
	}

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	node1 := cluster.MustNode(1)
	node2 := cluster.MustNode(2)
	node3 := cluster.MustNode(3)

	sender := connectClient(t, node1, "tag-100k-sender", "tag-100k-sender-device", cluster)
	defer func() { _ = sender.Close() }()
	recipientHeadA := connectClient(t, node1, hundredKSubscriberUID(0), hundredKSubscriberUID(0)+"-device", cluster)
	defer func() { _ = recipientHeadA.Close() }()
	recipientHeadB := connectClient(t, node2, hundredKSubscriberUID(1), hundredKSubscriberUID(1)+"-device", cluster)
	defer func() { _ = recipientHeadB.Close() }()
	recipientHeadC := connectClient(t, node3, hundredKSubscriberUID(2), hundredKSubscriberUID(2)+"-device", cluster)
	defer func() { _ = recipientHeadC.Close() }()
	recipientMiddle := connectClient(t, node2, hundredKSubscriberUID(50000), hundredKSubscriberUID(50000)+"-device", cluster)
	defer func() { _ = recipientMiddle.Close() }()
	recipientTail := connectClient(t, node3, hundredKSubscriberUID(99999), hundredKSubscriberUID(99999)+"-device", cluster)
	defer func() { _ = recipientTail.Close() }()
	addedRecipient := connectClient(t, node1, "tag-100k-recipient-added", "tag-100k-recipient-added-device", cluster)
	defer func() { _ = addedRecipient.Close() }()
	outsider := connectClient(t, node2, "tag-100k-outsider", "tag-100k-outsider-device", cluster)
	defer func() { _ = outsider.Close() }()

	for _, item := range []struct {
		node *suite.StartedNode
		uid  string
	}{
		{node1, "tag-100k-sender"},
		{node1, hundredKSubscriberUID(0)},
		{node2, hundredKSubscriberUID(1)},
		{node3, hundredKSubscriberUID(2)},
		{node2, hundredKSubscriberUID(50000)},
		{node3, hundredKSubscriberUID(99999)},
		{node1, "tag-100k-recipient-added"},
		{node2, "tag-100k-outsider"},
	} {
		requireConnection(t, ctx, item.node, item.uid, cluster)
	}

	apiBaseURL := "http://" + node1.Spec.APIAddr
	channelID := "e2e-delivery-tag-100k-group"
	postLegacyJSON(t, ctx, apiBaseURL+"/channel", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  hundredKGroupSubscribers(),
	})

	initialRecipients := map[string]*suite.WKProtoClient{
		hundredKSubscriberUID(0):     recipientHeadA,
		hundredKSubscriberUID(1):     recipientHeadB,
		hundredKSubscriberUID(2):     recipientHeadC,
		hundredKSubscriberUID(50000): recipientMiddle,
		hundredKSubscriberUID(99999): recipientTail,
	}
	sendGroupAndRequireRecipientsFrom(t, cluster, sender, "tag-100k-sender", channelID, 1, "delivery-tag-100k-group-1", []byte("delivery tag 100k group first"), initialRecipients)
	requireNoRecv(t, outsider, "tag-100k-outsider", cluster)

	postLegacyJSON(t, ctx, apiBaseURL+"/channel/subscriber_add", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"tag-100k-recipient-added"},
	})

	afterAddRecipients := cloneRecipientMap(initialRecipients)
	afterAddRecipients["tag-100k-recipient-added"] = addedRecipient
	sendGroupAndRequireRecipientsFrom(t, cluster, sender, "tag-100k-sender", channelID, 2, "delivery-tag-100k-group-2", []byte("delivery tag 100k group after add"), afterAddRecipients)

	postLegacyJSON(t, ctx, apiBaseURL+"/channel/subscriber_remove", map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{hundredKSubscriberUID(50000)},
	})

	afterRemoveRecipients := cloneRecipientMap(afterAddRecipients)
	delete(afterRemoveRecipients, hundredKSubscriberUID(50000))
	sendGroupAndRequireRecipientsFrom(t, cluster, sender, "tag-100k-sender", channelID, 3, "delivery-tag-100k-group-3", []byte("delivery tag 100k group after remove"), afterRemoveRecipients)
	requireNoRecv(t, recipientMiddle, hundredKSubscriberUID(50000), cluster)
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
	require.True(t, ok, cluster.DumpDiagnostics())
}

func sendGroupAndRequireRecipients(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender *suite.WKProtoClient,
	channelID string,
	clientSeq uint64,
	clientMsgNo string,
	payload []byte,
	recipients map[string]*suite.WKProtoClient,
) {
	t.Helper()
	sendGroupAndRequireRecipientsFrom(t, cluster, sender, "tag-sender", channelID, clientSeq, clientMsgNo, payload, recipients)
}

func sendGroupAndRequireRecipientsFrom(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender *suite.WKProtoClient,
	expectedFromUID string,
	channelID string,
	clientSeq uint64,
	clientMsgNo string,
	payload []byte,
	recipients map[string]*suite.WKProtoClient,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypeGroup,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}))

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)
	t.Logf("group send ack client_msg_no=%s message_id=%d seq=%d", clientMsgNo, sendack.MessageID, sendack.MessageSeq)

	for uid, recipient := range recipients {
		recv, err := recipient.ReadRecv()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, expectedFromUID, recv.FromUID)
		require.Equal(t, channelID, recv.ChannelID)
		require.Equal(t, frame.ChannelTypeGroup, recv.ChannelType)
		require.Equal(t, payload, recv.Payload)
		require.Equal(t, sendack.MessageID, recv.MessageID, "recipient %s", uid)
		require.Equal(t, sendack.MessageSeq, recv.MessageSeq, "recipient %s", uid)
		require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq))
	}
}

func requireNoRecv(t *testing.T, recipient *suite.WKProtoClient, uid string, cluster *suite.StartedCluster) {
	t.Helper()

	recv, err := recipient.ReadRecv()
	if err == nil {
		require.Failf(t, "unexpected group message received", "uid=%s message_id=%d seq=%d payload=%q\n%s", uid, recv.MessageID, recv.MessageSeq, string(recv.Payload), cluster.DumpDiagnostics())
	}
	require.Truef(t, isTimeoutError(err), "uid=%s unexpected read error: %v\n%s", uid, err, cluster.DumpDiagnostics())
}

func postLegacyJSON(t *testing.T, ctx context.Context, url string, payload any) {
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
	require.JSONEq(t, `{"status":200}`, string(body))
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "i/o timeout") || strings.Contains(err.Error(), "deadline exceeded")
}

func TestHelperTimeoutPredicateIncludesReadDeadline(t *testing.T) {
	require.True(t, isTimeoutError(fmt.Errorf("read tcp 127.0.0.1:1->127.0.0.1:2: i/o timeout")))
	require.False(t, isTimeoutError(fmt.Errorf("connection reset by peer")))
}

func largeGroupSubscribers(count int) []string {
	subscribers := make([]string, count)
	for i := range subscribers {
		subscribers[i] = fmt.Sprintf("tag-large-recipient-%04d", i)
	}
	return subscribers
}

func hundredKGroupSubscribers() []string {
	const count = 100000
	subscribers := make([]string, count)
	for i := range subscribers {
		subscribers[i] = hundredKSubscriberUID(i)
	}
	return subscribers
}

func hundredKSubscriberUID(index int) string {
	return fmt.Sprintf("tag-100k-recipient-%06d", index)
}

func cloneRecipientMap(in map[string]*suite.WKProtoClient) map[string]*suite.WKProtoClient {
	out := make(map[string]*suite.WKProtoClient, len(in))
	for uid, client := range in {
		out[uid] = client
	}
	return out
}
