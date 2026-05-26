//go:build integration
// +build integration

package app

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestDeliveryTagHundredKGroupProfile(t *testing.T) {
	cfg := loadDeliveryTagProfileConfig(t)
	requireDeliveryTagProfileEnabled(t, cfg)

	preset := sendStressAcceptancePreset()
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(appCfg *Config) {
		applySendPathTuning(t, appCfg, preset)
	})
	ownerID := harness.waitForStableLeader(t, 1)
	owner := harness.apps[ownerID]

	id := channel.ChannelID{
		ID:   "delivery-tag-profile-100k-group",
		Type: frame.ChannelTypeGroup,
	}
	seedDeliveryTagProfileRuntimeMeta(t, harness, owner, id, preset.MinISR)

	seedStarted := time.Now()
	subscribers := deliveryTagProfileSubscribers(cfg.SubscriberCount)
	require.NoError(t, owner.channelApp.AddSubscribers(context.Background(), channelusecase.SubscriberCommand{
		ChannelID:   id.ID,
		ChannelType: id.Type,
		Subscribers: subscribers,
	}))
	t.Logf("delivery tag profile seed: subscribers=%d duration=%s", cfg.SubscriberCount, time.Since(seedStarted))
	subscribers = nil
	runtime.GC()

	spread := deliveryTagProfileNodeSpread(harness, ownerID)
	senderUID := "delivery-tag-profile-sender"
	senderConn := connectMultinodeWKProtoClient(t, owner, senderUID, senderUID+"-device")
	initialRecipients := connectDeliveryTagProfileSparseRecipients(t, spread, cfg.SubscriberCount)

	addedUID := "delivery-tag-profile-added"
	addedConn := connectMultinodeWKProtoClient(t, spread[0], addedUID, addedUID+"-device")
	outsiderUID := "delivery-tag-profile-outsider"
	outsiderConn := connectMultinodeWKProtoClient(t, spread[1], outsiderUID, outsiderUID+"-device")
	for _, uid := range append(deliveryTagProfileRecipientUIDs(initialRecipients), addedUID, outsiderUID) {
		waitForDeliveryTagProfilePresence(t, harness, uid)
	}

	removedUID := deliveryTagProfileSubscriberUID(cfg.SubscriberCount / 2)
	var timings []deliveryTagProfileMessageTiming
	stats := runDeliveryTagProfile(t, cfg, func() {
		timings = append(timings, sendDeliveryTagProfileGroupAndRequireRecipients(
			t,
			senderConn,
			senderUID,
			id,
			1,
			"delivery-tag-profile-100k-1",
			[]byte("delivery tag profile 100k initial"),
			initialRecipients,
			cfg.SendTimeout,
		))
		requireDeliveryTagProfileNoRecv(t, outsiderConn, outsiderUID, time.Second)

		addStarted := time.Now()
		require.NoError(t, owner.channelApp.AddSubscribers(context.Background(), channelusecase.SubscriberCommand{
			ChannelID:   id.ID,
			ChannelType: id.Type,
			Subscribers: []string{addedUID},
		}))
		t.Logf("delivery tag profile mutation: op=add uid=%s duration=%s", addedUID, time.Since(addStarted))

		afterAddRecipients := cloneDeliveryTagProfileRecipients(initialRecipients)
		afterAddRecipients[addedUID] = addedConn
		timings = append(timings, sendDeliveryTagProfileGroupAndRequireRecipients(
			t,
			senderConn,
			senderUID,
			id,
			2,
			"delivery-tag-profile-100k-2",
			[]byte("delivery tag profile 100k after add"),
			afterAddRecipients,
			cfg.SendTimeout,
		))

		removeStarted := time.Now()
		require.NoError(t, owner.channelApp.RemoveSubscribers(context.Background(), channelusecase.SubscriberCommand{
			ChannelID:   id.ID,
			ChannelType: id.Type,
			Subscribers: []string{removedUID},
		}))
		t.Logf("delivery tag profile mutation: op=remove uid=%s duration=%s", removedUID, time.Since(removeStarted))

		afterRemoveRecipients := cloneDeliveryTagProfileRecipients(afterAddRecipients)
		delete(afterRemoveRecipients, removedUID)
		timings = append(timings, sendDeliveryTagProfileGroupAndRequireRecipients(
			t,
			senderConn,
			senderUID,
			id,
			3,
			"delivery-tag-profile-100k-3",
			[]byte("delivery tag profile 100k after remove"),
			afterRemoveRecipients,
			cfg.SendTimeout,
		))
		if removedConn, ok := initialRecipients[removedUID]; ok {
			requireDeliveryTagProfileNoRecv(t, removedConn, removedUID, time.Second)
		}
	})

	t.Logf("delivery tag profile summary: subscribers=%d owner=%d profile_elapsed=%s message_timings=%s", cfg.SubscriberCount, ownerID, stats.Elapsed, formatDeliveryTagProfileTimings(timings))
}

func requireDeliveryTagProfileEnabled(t *testing.T, cfg deliveryTagProfileConfig) {
	t.Helper()
	if !cfg.Enabled {
		t.Skipf("set %s=1 to run the 100k delivery tag profile test", deliveryTagProfileEnabledEnv)
	}
}

type deliveryTagProfileMessageTiming struct {
	ClientMsgNo string
	AckLatency  time.Duration
	RecvLatency time.Duration
	Recipients  int
}

func seedDeliveryTagProfileRuntimeMeta(t *testing.T, harness *threeNodeAppHarness, owner *App, id channel.ChannelID, minISR int) {
	t.Helper()

	require.NotNil(t, harness)
	require.NotNil(t, owner)
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 950001,
		LeaderEpoch:  950001,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       owner.cfg.Node.ID,
		MinISR:       int64(minISR),
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(10 * time.Minute).UnixMilli(),
	}
	require.NoError(t, owner.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range harness.appsWithLeaderFirst(owner.cfg.Node.ID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}
}

func deliveryTagProfileSubscribers(count int) []string {
	subscribers := make([]string, count)
	for i := range subscribers {
		subscribers[i] = deliveryTagProfileSubscriberUID(i)
	}
	return subscribers
}

func deliveryTagProfileSubscriberUID(index int) string {
	return fmt.Sprintf("delivery-tag-profile-user-%06d", index)
}

func deliveryTagProfileNodeSpread(harness *threeNodeAppHarness, ownerID uint64) []*App {
	ids := []uint64{ownerID, ownerID%3 + 1, (ownerID+1)%3 + 1}
	out := make([]*App, 0, len(ids))
	for _, id := range ids {
		if app := harness.apps[id]; app != nil {
			out = append(out, app)
		}
	}
	return out
}

func connectDeliveryTagProfileSparseRecipients(t *testing.T, nodes []*App, subscriberCount int) map[string]net.Conn {
	t.Helper()

	require.GreaterOrEqual(t, subscriberCount, 5)
	require.NotEmpty(t, nodes)
	recipients := make(map[string]net.Conn)
	for pos, index := range deliveryTagProfileSparseSubscriberIndexes(subscriberCount) {
		uid := deliveryTagProfileSubscriberUID(index)
		node := nodes[pos%len(nodes)]
		recipients[uid] = connectMultinodeWKProtoClient(t, node, uid, uid+"-device")
	}
	return recipients
}

func deliveryTagProfileSparseSubscriberIndexes(count int) []int {
	raw := []int{0, 1, 2, count / 2, count - 1}
	seen := make(map[int]struct{}, len(raw))
	out := make([]int, 0, len(raw))
	for _, index := range raw {
		if index < 0 || index >= count {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		out = append(out, index)
	}
	sort.Ints(out)
	return out
}

func deliveryTagProfileRecipientUIDs(recipients map[string]net.Conn) []string {
	uids := make([]string, 0, len(recipients))
	for uid := range recipients {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	return uids
}

func waitForDeliveryTagProfilePresence(t *testing.T, harness *threeNodeAppHarness, uid string) {
	t.Helper()

	require.Eventuallyf(t, func() bool {
		for _, app := range harness.orderedApps() {
			routes, err := app.presenceApp.EndpointsByUID(context.Background(), uid)
			if err == nil && len(routes) > 0 {
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond, "uid=%s should become visible in authoritative presence", uid)
}

func sendDeliveryTagProfileGroupAndRequireRecipients(
	t *testing.T,
	senderConn net.Conn,
	senderUID string,
	id channel.ChannelID,
	clientSeq uint64,
	clientMsgNo string,
	payload []byte,
	recipients map[string]net.Conn,
	timeout time.Duration,
) deliveryTagProfileMessageTiming {
	t.Helper()

	sendStarted := time.Now()
	sendAppWKProtoFrame(t, senderConn, &frame.SendPacket{
		ChannelID:   id.ID,
		ChannelType: id.Type,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	})

	sendackFrame, err := readSendStressFrameWithin(senderConn, timeout)
	require.NoError(t, err)
	sendack, ok := sendackFrame.(*frame.SendackPacket)
	require.Truef(t, ok, "expected *frame.SendackPacket, got %T", sendackFrame)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	ackLatency := time.Since(sendStarted)

	recvStarted := time.Now()
	for _, uid := range deliveryTagProfileRecipientUIDs(recipients) {
		conn := recipients[uid]
		recvFrame, err := readSendStressFrameWithin(conn, timeout)
		require.NoErrorf(t, err, "uid=%s client_msg_no=%s", uid, clientMsgNo)
		recv, ok := recvFrame.(*frame.RecvPacket)
		require.Truef(t, ok, "uid=%s expected *frame.RecvPacket, got %T", uid, recvFrame)
		require.Equal(t, id.ID, recv.ChannelID, "uid=%s", uid)
		require.Equal(t, id.Type, recv.ChannelType, "uid=%s", uid)
		require.Equal(t, senderUID, recv.FromUID, "uid=%s", uid)
		require.Equal(t, sendack.MessageID, recv.MessageID, "uid=%s", uid)
		require.Equal(t, sendack.MessageSeq, recv.MessageSeq, "uid=%s", uid)
		require.Equal(t, payload, recv.Payload, "uid=%s", uid)
		sendAppWKProtoFrame(t, conn, &frame.RecvackPacket{
			MessageID:  recv.MessageID,
			MessageSeq: recv.MessageSeq,
		})
	}
	recvLatency := time.Since(recvStarted)
	t.Logf("delivery tag profile message: client_msg_no=%s recipients=%d ack_latency=%s recv_latency=%s", clientMsgNo, len(recipients), ackLatency, recvLatency)
	return deliveryTagProfileMessageTiming{
		ClientMsgNo: clientMsgNo,
		AckLatency:  ackLatency,
		RecvLatency: recvLatency,
		Recipients:  len(recipients),
	}
}

func cloneDeliveryTagProfileRecipients(in map[string]net.Conn) map[string]net.Conn {
	out := make(map[string]net.Conn, len(in))
	for uid, conn := range in {
		out[uid] = conn
	}
	return out
}

func requireDeliveryTagProfileNoRecv(t *testing.T, conn net.Conn, uid string, timeout time.Duration) {
	t.Helper()

	f, err := readSendStressFrameWithin(conn, timeout)
	if err == nil {
		require.Failf(t, "unexpected profile delivery", "uid=%s frame=%T value=%v", uid, f, f)
		return
	}
	var netErr net.Error
	require.ErrorAsf(t, err, &netErr, "uid=%s unexpected read error: %v", uid, err)
	require.Truef(t, netErr.Timeout(), "uid=%s unexpected non-timeout read error: %v", uid, err)
}

func formatDeliveryTagProfileTimings(timings []deliveryTagProfileMessageTiming) string {
	parts := make([]string, 0, len(timings))
	for _, timing := range timings {
		parts = append(parts, fmt.Sprintf("%s(recipients=%d ack=%s recv=%s)", timing.ClientMsgNo, timing.Recipients, timing.AckLatency, timing.RecvLatency))
	}
	return strings.Join(parts, " ")
}
