package app

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeAppSendAckReturnsBeforeLeaderCheckpointDurability(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	id := pendingCheckpointChannelID("pending-checkpoint-sender", "pending-checkpoint-recipient")

	pendingCheckpointEnsureMeta(t, harness, leaderID, id, 42, 13)
	blocked := installBlockedCheckpointCoordinator(t, leader.ChannelLogDB())

	conn := connectMultinodeWKProtoClient(t, leader, "pending-checkpoint-sender", "pending-checkpoint-sender-device")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   "pending-checkpoint-recipient",
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "pending-checkpoint-ack-1",
		Payload:     []byte("ack before checkpoint"),
	})

	select {
	case <-blocked.startedFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint coordinator never observed the leader checkpoint request")
	}

	sendack, err := readPendingCheckpointSendack(conn, time.Second)
	require.NoError(t, err, "expected sendack before checkpoint durability completes")
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageSeq)
	require.NotZero(t, sendack.MessageID)
}

func TestThreeNodeAppSendAckSurvivesLeaderCrashWithPendingCheckpoint(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	id := pendingCheckpointChannelID("pending-crash-sender", "pending-crash-recipient")

	pendingCheckpointEnsureMeta(t, harness, leaderID, id, 43, 14)
	blocked := installBlockedCheckpointCoordinator(t, leader.ChannelLogDB())

	conn := connectMultinodeWKProtoClient(t, leader, "pending-crash-sender", "pending-crash-sender-device")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   "pending-crash-recipient",
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "pending-checkpoint-crash-1",
		Payload:     []byte("survive pending checkpoint"),
	})

	select {
	case <-blocked.startedFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint coordinator never observed the leader checkpoint request")
	}

	type sendackResult struct {
		sendack *frame.SendackPacket
		err     error
	}
	readStarted := make(chan struct{})
	sendackCh := make(chan sendackResult, 1)
	go func() {
		close(readStarted)
		sendack, err := readPendingCheckpointSendack(conn, time.Second)
		sendackCh <- sendackResult{sendack: sendack, err: err}
	}()
	<-readStarted

	blocked.shutdown(t)
	harness.stopNode(t, leaderID)
	harness.waitForLeaderChange(t, 1, leaderID)
	observeDeadLeaderOnRunningApps(harness, leaderID)

	result := <-sendackCh
	require.NoError(t, result.err, "expected sendack before leader crash while checkpoint durability was still pending")
	sendack := result.sendack
	require.NotNil(t, sendack, "expected sendack before leader crash while checkpoint durability was still pending")
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)

	for _, app := range harness.runningApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("survive pending checkpoint"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}
}

func TestThreeNodeAppSendAckSurvivesLeaderRestartAfterPendingCheckpointReconcile(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	id := pendingCheckpointChannelID("pending-restart-sender", "pending-restart-recipient")

	pendingCheckpointEnsureMeta(t, harness, leaderID, id, 44, 15)
	blocked := installBlockedCheckpointCoordinator(t, leader.ChannelLogDB())

	conn := connectMultinodeWKProtoClient(t, leader, "pending-restart-sender", "pending-restart-sender-device")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   "pending-restart-recipient",
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "pending-restart-1",
		Payload:     []byte("reconcile after restart"),
	})

	select {
	case <-blocked.startedFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint coordinator never observed the leader checkpoint request")
	}

	sendack, err := readPendingCheckpointSendack(conn, time.Second)
	require.NoError(t, err, "expected sendack before leader restart while checkpoint durability was still pending")
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NoError(t, conn.Close())

	blocked.shutdown(t)
	harness.stopNode(t, leaderID)

	newLeaderID := harness.waitForLeaderChange(t, 1, leaderID)
	require.NotZero(t, newLeaderID)
	observeDeadLeaderOnRunningApps(harness, leaderID)

	for _, app := range harness.runningApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("reconcile after restart"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}

	harness.restartNode(t, leaderID)
	harness.waitForStableLeader(t, 1)

	msg := waitForAppCommittedMessage(t, harness.apps[leaderID], id, sendack.MessageSeq, 5*time.Second)
	require.Equal(t, []byte("reconcile after restart"), msg.Payload)
	require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
}

func TestMultiNodeColdFollowerActivatesAfterLeaderAppend(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	followerID := uint64(1)
	if followerID == leaderID {
		followerID = 2
	}
	follower := harness.apps[followerID]

	id := pendingCheckpointChannelID("cold-follower-sender", "cold-follower-recipient")
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 45,
		LeaderEpoch:  16,
		Replicas:     []uint64{leaderID, followerID},
		ISR:          []uint64{leaderID, followerID},
		Leader:       leader.cfg.Node.ID,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	_, err := leader.channelMetaSync.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)

	key := channelhandler.KeyFromChannelID(id)
	_, ok := follower.ISRRuntime().Channel(key)
	require.False(t, ok, "expected follower runtime to stay cold before leader append")

	conn := connectMultinodeWKProtoClient(t, leader, "cold-follower-sender", "cold-follower-device")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   "cold-follower-recipient",
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "cold-follower-append-1",
		Payload:     []byte("wake cold follower"),
	})

	sendack, err := readPendingCheckpointSendack(conn, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NoError(t, conn.Close())

	msg := waitForAppCommittedMessage(t, follower, id, sendack.MessageSeq, 5*time.Second)
	require.Equal(t, []byte("wake cold follower"), msg.Payload)
	require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
}

func TestLeaderFailoverRefreshesLocalChannelMetaAfterDeadLeaderObservation(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	id := pendingCheckpointChannelID("meta-refresh-sender", "meta-refresh-recipient")
	conn := connectMultinodeWKProtoClient(t, leader, "meta-refresh-sender", "meta-refresh-device")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   "meta-refresh-recipient",
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "meta-refresh-1",
		Payload:     []byte("refresh leader meta"),
	})

	sendack, err := readPendingCheckpointSendack(conn, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NoError(t, conn.Close())

	harness.stopNode(t, leaderID)
	newLeaderID := harness.waitForLeaderChange(t, 1, leaderID)
	newLeader := harness.apps[newLeaderID]

	observeDeadLeaderOnRunningApps(harness, leaderID)
	waitForAppCommittedMessage(t, newLeader, id, sendack.MessageSeq, 10*time.Second)
	var refreshed channel.Meta
	var lastErr error
	require.Eventually(t, func() bool {
		observeDeadLeaderOnRunningApps(harness, leaderID)
		meta, err := newLeader.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		if err != nil {
			lastErr = err
			return false
		}
		lastErr = nil
		refreshed = meta
		return meta.Leader != 0 && meta.Leader != channel.NodeID(leaderID)
	}, 10*time.Second, 100*time.Millisecond, "lastMeta=%+v lastErr=%v", refreshed, lastErr)
	require.NotZero(t, refreshed.Leader)
	require.NotEqual(t, channel.NodeID(leaderID), refreshed.Leader)
	require.True(t, containsNodeID(refreshed.ISR, refreshed.Leader))

	authoritative, err := newLeader.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, uint64(refreshed.Leader), authoritative.Leader)
	require.NotEqual(t, leaderID, authoritative.Leader)

	key := channelhandler.KeyFromChannelID(id)
	require.Eventually(t, func() bool {
		handle, ok := newLeader.ISRRuntime().Channel(key)
		if !ok {
			return false
		}
		return handle.Meta().Leader == refreshed.Leader
	}, 5*time.Second, 50*time.Millisecond)
}

func TestLeaderDrainRefreshesLocalChannelMetaAfterDrainingObservation(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	id := pendingCheckpointChannelID("meta-drain-sender", "meta-drain-recipient")
	conn := connectMultinodeWKProtoClient(t, leader, "meta-drain-sender", "meta-drain-device")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   "meta-drain-recipient",
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "meta-drain-1",
		Payload:     []byte("refresh draining leader meta"),
	})

	sendack, err := readPendingCheckpointSendack(conn, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NoError(t, conn.Close())

	for _, app := range harness.runningApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 10*time.Second)
		require.Equal(t, []byte("refresh draining leader meta"), msg.Payload)
	}

	observeDrainingLeaderOnRunningApps(harness, leaderID)
	var refreshed channel.Meta
	var lastErr error
	require.Eventually(t, func() bool {
		observeDrainingLeaderOnRunningApps(harness, leaderID)
		meta, err := leader.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		if err != nil {
			lastErr = err
			return false
		}
		lastErr = nil
		refreshed = meta
		return meta.Leader != 0 && meta.Leader != channel.NodeID(leaderID)
	}, 10*time.Second, 100*time.Millisecond, "lastMeta=%+v lastErr=%v", refreshed, lastErr)
	require.NotZero(t, refreshed.Leader)
	require.NotEqual(t, channel.NodeID(leaderID), refreshed.Leader)
	require.True(t, containsNodeID(refreshed.ISR, refreshed.Leader))

	authoritative, err := leader.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, uint64(refreshed.Leader), authoritative.Leader)
	require.NotEqual(t, leaderID, authoritative.Leader)

	key := channelhandler.KeyFromChannelID(id)
	for _, app := range harness.runningApps() {
		app := app
		require.Eventually(t, func() bool {
			handle, ok := app.ISRRuntime().Channel(key)
			if !ok {
				return false
			}
			return handle.Meta().Leader == refreshed.Leader
		}, 5*time.Second, 50*time.Millisecond)
	}
}

func pendingCheckpointChannelID(senderUID, recipientUID string) channel.ChannelID {
	return channel.ChannelID{
		ID:   deliveryusecase.EncodePersonChannel(senderUID, recipientUID),
		Type: frame.ChannelTypePerson,
	}
}

func observeDrainingLeaderOnRunningApps(harness *threeNodeAppHarness, leaderID uint64) {
	observeNodeStatusOnRunningApps(harness, leaderID, controllermeta.NodeStatusDraining)
}

func observeDeadLeaderOnRunningApps(harness *threeNodeAppHarness, leaderID uint64) {
	observeNodeStatusOnRunningApps(harness, leaderID, controllermeta.NodeStatusDead)
}

func observeNodeStatusOnRunningApps(harness *threeNodeAppHarness, nodeID uint64, status controllermeta.NodeStatus) {
	for _, app := range harness.runningApps() {
		app.channelMetaSync.UpdateNodeLiveness(nodeID, status)
	}
}

func pendingCheckpointEnsureMeta(t *testing.T, harness *threeNodeAppHarness, leaderID uint64, id channel.ChannelID, channelEpoch, leaderEpoch uint64) {
	t.Helper()

	leader := harness.apps[leaderID]
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: channelEpoch,
		LeaderEpoch:  leaderEpoch,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range harness.appsWithLeaderFirst(leaderID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}
	_ = channelStoreForID(leader.ChannelLogDB(), id)
}

type blockedCheckpointCoordinator struct {
	startedFirst chan struct{}
	releaseFirst chan struct{}

	stopCh       reflect.Value
	doneCh       reflect.Value
	engineField  reflect.Value
	previous     reflect.Value
	restoreOnce  sync.Once
	releaseOnce  sync.Once
	shutdownOnce sync.Once
}

func installBlockedCheckpointCoordinator(t *testing.T, engine *channelstore.Engine) *blockedCheckpointCoordinator {
	t.Helper()

	blocked := &blockedCheckpointCoordinator{
		startedFirst: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}

	engineValue := unsafeStructField(t, engine, "checkpoint")
	previous := engineValue.Interface()
	coordinatorType := engineValue.Type()
	require.Equal(t, reflect.Pointer, coordinatorType.Kind(), "checkpoint coordinator field kind")

	coordinator := reflect.New(coordinatorType.Elem())
	unsafeStructField(t, coordinator.Interface(), "db").Set(unsafeStructField(t, engine, "db"))
	requests := reflect.MakeChan(unsafeStructField(t, coordinator.Interface(), "requests").Type(), 16)
	unsafeStructField(t, coordinator.Interface(), "requests").Set(requests)
	stopAcceptCh := reflect.MakeChan(unsafeStructField(t, coordinator.Interface(), "stopAcceptCh").Type(), 0)
	stopCh := reflect.MakeChan(unsafeStructField(t, coordinator.Interface(), "stopCh").Type(), 0)
	doneCh := reflect.MakeChan(unsafeStructField(t, coordinator.Interface(), "doneCh").Type(), 0)
	unsafeStructField(t, coordinator.Interface(), "stopAcceptCh").Set(stopAcceptCh)
	unsafeStructField(t, coordinator.Interface(), "stopCh").Set(stopCh)
	unsafeStructField(t, coordinator.Interface(), "doneCh").Set(doneCh)

	blocked.stopCh = stopCh
	blocked.doneCh = doneCh
	blocked.engineField = engineValue
	if previous != nil {
		blocked.previous = reflect.ValueOf(previous)
	}

	engineValue.Set(coordinator)
	t.Cleanup(func() {
		blocked.shutdown(t)
	})

	go func() {
		defer closeReflectChan(doneCh)
		for {
			chosen, recv, recvOK := reflect.Select([]reflect.SelectCase{
				{Dir: reflect.SelectRecv, Chan: stopCh},
				{Dir: reflect.SelectRecv, Chan: requests},
			})
			if chosen == 0 || !recvOK {
				return
			}

			select {
			case <-blocked.startedFirst:
			default:
				close(blocked.startedFirst)
			}

			req := reflect.New(recv.Type()).Elem()
			req.Set(recv)
			doneBase := req.FieldByName("done")
			doneField := reflect.NewAt(doneBase.Type(), unsafe.Pointer(doneBase.UnsafeAddr())).Elem()
			chosen, _, _ = reflect.Select([]reflect.SelectCase{
				{Dir: reflect.SelectRecv, Chan: stopCh},
				{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(blocked.releaseFirst)},
			})
			if chosen == 0 {
				doneField.Send(reflect.ValueOf(channel.ErrInvalidArgument))
				return
			}
			doneField.Send(reflect.Zero(doneField.Type().Elem()))
		}
	}()

	return blocked
}

func (b *blockedCheckpointCoordinator) release() {
	b.releaseOnce.Do(func() {
		close(b.releaseFirst)
	})
}

func (b *blockedCheckpointCoordinator) shutdown(t *testing.T) {
	t.Helper()
	b.shutdownOnce.Do(func() {
		if b.stopCh.IsValid() {
			closeReflectChan(b.stopCh)
		}
		if b.doneCh.IsValid() {
			waitForReflectClose(t, b.doneCh, 5*time.Second)
		}
		b.restore()
	})
}

func (b *blockedCheckpointCoordinator) restore() {
	b.restoreOnce.Do(func() {
		if !b.engineField.IsValid() {
			return
		}
		if b.previous.IsValid() {
			b.engineField.Set(b.previous)
			return
		}
		b.engineField.Set(reflect.Zero(b.engineField.Type()))
	})
}

func unsafeStructField(t *testing.T, owner any, field string) reflect.Value {
	t.Helper()

	value := reflect.ValueOf(owner)
	require.Equal(t, reflect.Pointer, value.Kind(), "owner %T must be a pointer", owner)
	value = value.Elem()

	got := value.FieldByName(field)
	require.True(t, got.IsValid(), "field %q not found on %T", field, owner)
	return reflect.NewAt(got.Type(), unsafe.Pointer(got.UnsafeAddr())).Elem()
}

func closeReflectChan(ch reflect.Value) {
	defer func() {
		_ = recover()
	}()
	ch.Close()
}

func waitForReflectClose(t *testing.T, ch reflect.Value, timeout time.Duration) {
	t.Helper()

	cases := []reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: ch},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(time.After(timeout))},
	}
	chosen, _, ok := reflect.Select(cases)
	if chosen == 1 {
		t.Fatal("timed out waiting for blocked checkpoint coordinator to stop")
	}
	if ok {
		t.Fatal("blocked checkpoint coordinator done channel delivered a value before closing")
	}
}

func readPendingCheckpointSendack(conn net.Conn, timeout time.Duration) (*frame.SendackPacket, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	packet, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return nil, err
	}
	sendack, ok := packet.(*frame.SendackPacket)
	if !ok {
		return nil, fmt.Errorf("unexpected frame while waiting for sendack: %T", packet)
	}
	return sendack, nil
}
