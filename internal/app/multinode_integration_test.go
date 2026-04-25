//go:build integration
// +build integration

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestAppThreeNodeClusterStartsWithoutStaticGroupPeers(t *testing.T) {

	harness := newThreeNodeManagedAppHarness(t)

	require.Eventually(t, func() bool {
		for _, app := range harness.orderedApps() {
			assignments, err := app.Cluster().ListSlotAssignments(context.Background())
			if err != nil || len(assignments) != 1 {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond)

	require.NotZero(t, harness.waitForStableLeader(t, 1))
}

func TestAppMajorityAvailableAfterSingleReplicaNodeFailure(t *testing.T) {
	harness := newThreeNodeManagedAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	harness.stopNode(t, leaderID)
	survivorLeaderID := harness.waitForLeaderChange(t, 1, leaderID)
	survivorLeader := harness.apps[survivorLeaderID]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	channelID := fmt.Sprintf("majority-channel-%d", time.Now().UnixNano())
	require.NoError(t, survivorLeader.Store().CreateChannel(ctx, channelID, int64(frame.ChannelTypeGroup)))
	for _, app := range harness.runningApps() {
		app := app
		require.Eventually(t, func() bool {
			channel, err := app.Store().GetChannel(context.Background(), channelID, int64(frame.ChannelTypeGroup))
			return err == nil && channel.ChannelID == channelID
		}, 5*time.Second, 20*time.Millisecond)
	}
}

func TestAppManagedSlotStartupAllowsSubsetAssignmentsPerNode(t *testing.T) {
	harness := newThreeNodeManagedAppHarnessWithLayout(t, 4, 2)

	require.Eventually(t, func() bool {
		for _, app := range harness.orderedApps() {
			assignments, err := app.Cluster().ListSlotAssignments(context.Background())
			if err != nil || len(assignments) != 4 {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond)
}

func TestSlotLeaderChangeDoesNotDriftHealthyChannelLeader(t *testing.T) {
	harness := newThreeNodeManagedAppHarness(t)
	slotID := uint64(1)
	slotLeaderID := harness.waitForStableLeader(t, slotID)
	channelLeaderID := slotLeaderID%3 + 1
	targetSlotLeaderID := channelLeaderID%3 + 1
	slotLeader := harness.apps[slotLeaderID]
	id := channel.ChannelID{
		ID:   fmt.Sprintf("slot-leader-stable-%d", time.Now().UnixNano()),
		Type: frame.ChannelTypeGroup,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 21,
		LeaderEpoch:  13,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       channelLeaderID,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}

	require.NoError(t, slotLeader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range harness.orderedApps() {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	require.NoError(t, slotLeader.Cluster().TransferSlotLeader(context.Background(), uint32(slotID), multiraft.NodeID(targetSlotLeaderID)))
	require.Equal(t, targetSlotLeaderID, harness.waitForLeaderChange(t, slotID, slotLeaderID))

	require.Never(t, func() bool {
		got, err := slotLeader.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		return err == nil && got.Leader != channelLeaderID
	}, 3*time.Second, 100*time.Millisecond)

	for _, app := range harness.runningApps() {
		got, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
		require.Equal(t, channel.NodeID(channelLeaderID), got.Leader)
	}
}

func TestDeadChannelLeaderRepairPersistsAcrossRestart(t *testing.T) {
	harness := newThreeNodeManagedAppHarness(t)
	slotID := uint64(1)
	slotLeaderID := harness.waitForStableLeader(t, slotID)
	channelLeaderID := slotLeaderID%3 + 1
	slotLeader := harness.apps[slotLeaderID]
	id := channel.ChannelID{
		ID:   fmt.Sprintf("dead-channel-repair-%d", time.Now().UnixNano()),
		Type: frame.ChannelTypeGroup,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 31,
		LeaderEpoch:  14,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       channelLeaderID,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}

	require.NoError(t, slotLeader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range harness.orderedApps() {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	harness.stopNode(t, channelLeaderID)

	var repairedLeader uint64
	require.Eventually(t, func() bool {
		got, err := slotLeader.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		if err != nil || got.Leader == 0 || uint64(got.Leader) == channelLeaderID {
			return false
		}
		repairedLeader = uint64(got.Leader)
		return true
	}, 25*time.Second, 100*time.Millisecond)

	require.NotZero(t, repairedLeader)
	require.NotEqual(t, channelLeaderID, repairedLeader)

	require.Eventually(t, func() bool {
		got, err := slotLeader.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		return err == nil && got.Leader == repairedLeader && got.LeaderEpoch == meta.LeaderEpoch+1
	}, 5*time.Second, 100*time.Millisecond)

	restarted := harness.restartNode(t, channelLeaderID)
	harness.waitForStableLeader(t, slotID)

	require.Eventually(t, func() bool {
		got, err := restarted.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		return err == nil && uint64(got.Leader) == repairedLeader
	}, 10*time.Second, 100*time.Millisecond)
}

func TestThreeNodeAppGatewaySendUsesDurableCommitWithMinISR2(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	recipientUID := "three-node-gateway-user"
	channelID := deliveryusecase.EncodePersonChannel("sender", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 15,
		LeaderEpoch:  6,
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

	conn := connectAppWKProtoClient(t, leader, "sender")

	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-app-1",
		Payload:     []byte("hello durable gateway"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, conn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageSeq)
	require.NotZero(t, sendack.MessageID)
	require.NoError(t, conn.Close())

	harness.stopNode(t, leaderID)
	newLeaderID := harness.waitForLeaderChange(t, 1, leaderID)
	require.NotZero(t, newLeaderID)

	for _, app := range harness.runningApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("hello durable gateway"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}

	harness.restartNode(t, leaderID)
	harness.waitForStableLeader(t, 1)

	msg := waitForAppCommittedMessage(t, harness.apps[leaderID], id, sendack.MessageSeq, 5*time.Second)
	require.Equal(t, []byte("hello durable gateway"), msg.Payload)
	require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
}

func TestThreeNodeAppGatewaySendUsesLongPollQuorumCommitWithMinISR2(t *testing.T) {
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(cfg *Config) {
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	recipientUID := "three-node-long-poll-user"
	channelID := deliveryusecase.EncodePersonChannel("sender-long-poll", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 115,
		LeaderEpoch:  16,
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

	conn := connectAppWKProtoClient(t, leader, "sender-long-poll")

	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-long-poll-1",
		Payload:     []byte("hello long poll gateway"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, conn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageSeq)
	require.NotZero(t, sendack.MessageID)
	require.NoError(t, conn.Close())

	harness.stopNode(t, leaderID)
	newLeaderID := harness.waitForLeaderChange(t, 1, leaderID)
	require.NotZero(t, newLeaderID)

	for _, app := range harness.runningApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("hello long poll gateway"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}

	harness.restartNode(t, leaderID)
	harness.waitForStableLeader(t, 1)

	msg := waitForAppCommittedMessage(t, harness.apps[leaderID], id, sendack.MessageSeq, 5*time.Second)
	require.Equal(t, []byte("hello long poll gateway"), msg.Payload)
	require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
}

func TestThreeNodeAppMessageSendUsesLongPollLocalCommitWithoutAdvancingQuorum(t *testing.T) {
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(cfg *Config) {
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	recipientUID := "three-node-local-commit-user"
	channelID := deliveryusecase.EncodePersonChannel("sender-local-commit", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 116,
		LeaderEpoch:  17,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))

	for _, app := range harness.appsWithLeaderFirst(leaderID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	offlineFollowerID := leaderID%3 + 1
	require.NotEqual(t, leaderID, offlineFollowerID)
	harness.stopNode(t, offlineFollowerID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := leader.Message().Send(ctx, messageusecase.SendCommand{
		FromUID:     "sender-local-commit",
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-local-commit-1",
		Payload:     []byte("hello local commit long poll"),
		CommitMode:  channel.CommitModeLocal,
	})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.NotZero(t, result.MessageSeq)

	require.Never(t, func() bool {
		status, statusErr := leader.channelLog.Status(id)
		if statusErr != nil {
			return false
		}
		return status.CommittedSeq >= result.MessageSeq
	}, 500*time.Millisecond, 50*time.Millisecond)
}

func TestThreeNodeAppMessageSendTimesOutWaitingForQuorumWithMinISR3(t *testing.T) {
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(cfg *Config) {
		cfg.Gateway.SendTimeout = 350 * time.Millisecond
		cfg.Cluster.ChannelBootstrapDefaultMinISR = 3
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	recipientUID := "three-node-timeout-direct-user"
	channelID := deliveryusecase.EncodePersonChannel("sender-timeout-direct", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 117,
		LeaderEpoch:  18,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))

	for _, app := range harness.appsWithLeaderFirst(leaderID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	offlineFollowerID := leaderID%3 + 1
	require.NotEqual(t, leaderID, offlineFollowerID)
	harness.stopNode(t, offlineFollowerID)

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	result, err := leader.Message().Send(ctx, messageusecase.SendCommand{
		FromUID:     "sender-timeout-direct",
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-timeout-direct-1",
		Payload:     []byte("hello timeout direct"),
	})
	require.Zero(t, result.MessageSeq)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestThreeNodeAppGatewaySendFromFollowerForwardsDurableAppendToLeader(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	followerID := leaderID%3 + 1
	require.NotEqual(t, leaderID, followerID)
	follower := harness.apps[followerID]
	recipientUID := "three-node-follower-user"
	channelID := deliveryusecase.EncodePersonChannel("sender-follower", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 16,
		LeaderEpoch:  7,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	_, err := leader.channelMetaSync.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	_, err = follower.channelMetaSync.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)

	conn := connectAppWKProtoClient(t, follower, "sender-follower")

	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-follower-1",
		Payload:     []byte("hello durable follower"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, conn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageSeq)
	require.NotZero(t, sendack.MessageID)

	for _, app := range harness.orderedApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("hello durable follower"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}
}

func TestThreeNodeAppGatewaySendFromFollowerBootstrapsLeaderOnDemand(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	followerID := leaderID%3 + 1
	require.NotEqual(t, leaderID, followerID)
	follower := harness.apps[followerID]
	recipientUID := "three-node-ondemand-user"
	channelID := deliveryusecase.EncodePersonChannel("sender-ondemand", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 17,
		LeaderEpoch:  8,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	_, err := follower.channelMetaSync.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)

	conn := connectAppWKProtoClient(t, follower, "sender-ondemand")

	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-ondemand-1",
		Payload:     []byte("hello durable on demand"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, conn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageSeq)
	require.NotZero(t, sendack.MessageID)

	for _, app := range harness.orderedApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("hello durable on demand"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}
}

func TestThreeNodeAppGatewaySendFromFollowerAfterLeaseExpiryRecoversViaMetaSync(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	followerID := leaderID%3 + 1
	require.NotEqual(t, leaderID, followerID)
	follower := harness.apps[followerID]
	recipientUID := "three-node-expired-lease-user"
	channelID := deliveryusecase.EncodePersonChannel("sender-expired-lease", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	initialLeaseUntilMS := time.Now().Add(200 * time.Millisecond).UnixMilli()
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 18,
		LeaderEpoch:  9,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: initialLeaseUntilMS,
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))

	require.Eventually(t, func() bool {
		got, err := leader.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		return err == nil && got.LeaseUntilMS > initialLeaseUntilMS
	}, 5*time.Second, 50*time.Millisecond)

	conn := connectAppWKProtoClient(t, follower, "sender-expired-lease")

	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-expired-lease-1",
		Payload:     []byte("hello after lease expiry"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, conn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageSeq)
	require.NotZero(t, sendack.MessageID)

	for _, app := range harness.orderedApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("hello after lease expiry"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}
}

func TestThreeNodeAppGatewaySendTimesOutWaitingForQuorumWithMinISR3(t *testing.T) {
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(cfg *Config) {
		cfg.Gateway.SendTimeout = 350 * time.Millisecond
		cfg.Cluster.ChannelBootstrapDefaultMinISR = 3
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	senderID := leaderID%3 + 1
	require.NotEqual(t, leaderID, senderID)
	sender := harness.apps[senderID]
	blockedFollowerID := senderID%3 + 1
	if blockedFollowerID == leaderID {
		blockedFollowerID = blockedFollowerID%3 + 1
	}
	require.NotEqual(t, leaderID, blockedFollowerID)
	require.NotEqual(t, senderID, blockedFollowerID)
	recipientUID := "three-node-timeout-user"
	channelID := deliveryusecase.EncodePersonChannel("sender-timeout", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 219,
		LeaderEpoch:  33,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range []*App{leader, sender} {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	harness.stopNode(t, blockedFollowerID)

	staleMeta := projectChannelMeta(meta)
	staleMeta.LeaderEpoch--
	staleMeta.Leader = channel.NodeID(sender.cfg.Node.ID)
	sender.channelLog.service.RestoreMeta(staleMeta.Key, staleMeta, true)

	conn := connectAppWKProtoClient(t, sender, "sender-timeout")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-timeout-1",
		Payload:     []byte("hello timeout"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, conn, 5*time.Second).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSystemError, sendack.ReasonCode)
	require.Equal(t, uint64(1), sendack.ClientSeq)
	require.Equal(t, "three-node-timeout-1", sendack.ClientMsgNo)
	require.NoError(t, conn.Close())
}

func TestThreeNodeAppDurableSendReturnsBeforeRemoteAck(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	ownerID := harness.waitForStableLeader(t, 1)
	senderNodeID := ownerID
	recipientNodeID := ownerID%3 + 1
	owner := harness.apps[ownerID]
	senderNode := harness.apps[senderNodeID]
	recipientNode := harness.apps[recipientNodeID]
	recipientUID := "remote-recipient"
	channelID := deliveryusecase.EncodePersonChannel("sender-remote", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 21,
		LeaderEpoch:  8,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       owner.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, owner.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range harness.appsWithLeaderFirst(ownerID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	senderConn, err := net.Dial("tcp", senderNode.Gateway().ListenerAddr("tcp-wkproto"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = senderConn.Close() })
	recipientConn, err := net.Dial("tcp", recipientNode.Gateway().ListenerAddr("tcp-wkproto"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = recipientConn.Close() })

	sendAppWKProtoFrame(t, senderConn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "sender-remote",
		DeviceID:        "sender-remote-device",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})
	connack, ok := readAppWKProtoFrameWithin(t, senderConn, multinodeAppReadTimeout).(*frame.ConnackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)

	sendAppWKProtoFrame(t, recipientConn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             recipientUID,
		DeviceID:        "recipient-remote-device",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})
	recipientConnack, ok := readAppWKProtoFrameWithin(t, recipientConn, multinodeAppReadTimeout).(*frame.ConnackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, recipientConnack.ReasonCode)

	sendAppWKProtoFrame(t, senderConn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-async-1",
		Payload:     []byte("hello realtime"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, senderConn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)

	recv, ok := readAppWKProtoFrameWithin(t, recipientConn, multinodeAppReadTimeout).(*frame.RecvPacket)
	require.True(t, ok)
	require.Equal(t, "sender-remote", recv.FromUID)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	var sessionID uint64
	require.Eventually(t, func() bool {
		conns := recipientNode.messageApp.OnlineRegistry().ConnectionsByUID(recipientUID)
		if len(conns) == 0 {
			return false
		}
		sessionID = conns[0].SessionID
		return owner.deliveryRuntime.HasAckBinding(sessionID, uint64(sendack.MessageID))
	}, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		_, recipientHasBinding := recipientNode.deliveryAcks.Lookup(sessionID, uint64(sendack.MessageID))
		return recipientHasBinding
	}, 5*time.Second, 20*time.Millisecond)

	sendAppWKProtoFrame(t, recipientConn, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	})
	require.Eventually(t, func() bool {
		_, recipientHasBinding := recipientNode.deliveryAcks.Lookup(sessionID, uint64(sendack.MessageID))
		return !owner.deliveryRuntime.HasAckBinding(sessionID, uint64(sendack.MessageID)) && !recipientHasBinding
	}, 5*time.Second, 20*time.Millisecond)
}

func TestThreeNodeAppGroupChannelRealtimeDeliveryUsesStoredSubscribers(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	ownerID := harness.waitForStableLeader(t, 1)
	owner := harness.apps[ownerID]
	senderNode := harness.apps[ownerID]
	recipientNodeA := harness.apps[ownerID%3+1]
	recipientNodeB := harness.apps[(ownerID+1)%3+1]

	id := channel.ChannelID{
		ID:   "slot-realtime",
		Type: frame.ChannelTypeGroup,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 31,
		LeaderEpoch:  9,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       owner.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, owner.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	require.NoError(t, owner.Store().AddChannelSubscribers(context.Background(), id.ID, int64(id.Type), []string{"slot-user-a", "slot-user-b"}))

	for _, app := range harness.appsWithLeaderFirst(ownerID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	senderConn := connectMultinodeWKProtoClient(t, senderNode, "slot-sender", "slot-sender-device")
	recipientConnA := connectMultinodeWKProtoClient(t, recipientNodeA, "slot-user-a", "slot-device-a")
	recipientConnB := connectMultinodeWKProtoClient(t, recipientNodeB, "slot-user-b", "slot-device-b")

	sendAppWKProtoFrame(t, senderConn, &frame.SendPacket{
		ChannelID:   id.ID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "three-node-slot-1",
		Payload:     []byte("hello slot members"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, senderConn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)

	recvA, ok := readAppWKProtoFrameWithin(t, recipientConnA, multinodeAppReadTimeout).(*frame.RecvPacket)
	require.True(t, ok)
	require.Equal(t, id.ID, recvA.ChannelID)
	require.Equal(t, id.Type, recvA.ChannelType)
	require.Equal(t, "slot-sender", recvA.FromUID)

	recvB, ok := readAppWKProtoFrameWithin(t, recipientConnB, multinodeAppReadTimeout).(*frame.RecvPacket)
	require.True(t, ok)
	require.Equal(t, id.ID, recvB.ChannelID)
	require.Equal(t, id.Type, recvB.ChannelType)
	require.Equal(t, "slot-sender", recvB.FromUID)
}

func TestThreeNodeAppPersonRealtimeDeliveryAfterIdleLeaseExpiry(t *testing.T) {
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(cfg *Config) {
		cfg.Cluster.PoolSize = 0
		cfg.Cluster.DataPlanePoolSize = 1
		cfg.Cluster.DataPlaneMaxFetchInflight = 2
		cfg.Cluster.DataPlaneMaxPendingFetch = 2
	})
	ownerID := harness.waitForStableLeader(t, 1)
	owner := harness.apps[ownerID]
	senderNode := harness.apps[ownerID%3+1]
	recipientNode := harness.apps[(ownerID+1)%3+1]

	senderUID := "idle-realtime-sender"
	recipientUID := "idle-realtime-recipient"
	channelID := deliveryusecase.EncodePersonChannel(senderUID, recipientUID)
	id := channel.ChannelID{ID: channelID, Type: frame.ChannelTypePerson}

	senderConn := connectMultinodeWKProtoClient(t, senderNode, senderUID, senderUID+"-device")
	recipientConn := connectMultinodeWKProtoClient(t, recipientNode, recipientUID, recipientUID+"-device")

	sendAppWKProtoFrame(t, senderConn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "idle-realtime-bootstrap-1",
		Payload:     []byte("hello before idle"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, senderConn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)

	recv, ok := readAppWKProtoFrameWithin(t, recipientConn, multinodeAppReadTimeout).(*frame.RecvPacket)
	require.True(t, ok)
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	initialMeta := waitForAuthoritativeChannelRuntimeMeta(t, owner, id, 5*time.Second)
	require.Eventually(t, func() bool {
		routes, err := owner.presenceApp.EndpointsByUID(context.Background(), recipientUID)
		if err != nil {
			return false
		}
		for _, route := range routes {
			if route.NodeID == recipientNode.cfg.Node.ID && route.SessionID != 0 {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		meta, err := owner.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		if err != nil {
			return false
		}
		return meta.LeaseUntilMS > 0 && meta.LeaseUntilMS < time.Now().UnixMilli()
	}, time.Until(time.UnixMilli(initialMeta.LeaseUntilMS).Add(5*time.Second)), 50*time.Millisecond)

	require.Eventually(t, func() bool {
		routes, err := owner.presenceApp.EndpointsByUID(context.Background(), recipientUID)
		if err != nil {
			return false
		}
		for _, route := range routes {
			if route.NodeID == recipientNode.cfg.Node.ID && route.SessionID != 0 {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)

	sendAppWKProtoFrame(t, senderConn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   2,
		ClientMsgNo: "idle-realtime-after-expiry-1",
		Payload:     []byte("hello after idle expiry"),
	})

	sendack, ok = readAppWKProtoFrameWithin(t, senderConn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)

	recv, ok = readAppWKProtoFrameWithin(t, recipientConn, multinodeAppReadTimeout).(*frame.RecvPacket)
	require.True(t, ok, idleRealtimeDeliveryState(t, harness, owner, senderNode, recipientNode, id, senderUID, recipientUID, sendack.MessageSeq))
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)
}

func TestThreeNodeAppHotGroupDoesNotBlockNormalGroupDelivery(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	ownerID := harness.waitForStableLeader(t, 1)
	owner := harness.apps[ownerID]
	recipientNode := harness.apps[ownerID%3+1]

	hotID := channel.ChannelID{ID: "slot-hot", Type: frame.ChannelTypeGroup}
	normalID := channel.ChannelID{ID: "slot-normal", Type: frame.ChannelTypeGroup}
	for _, id := range []channel.ChannelID{hotID, normalID} {
		require.NoError(t, owner.Store().UpsertChannelRuntimeMeta(context.Background(), metadb.ChannelRuntimeMeta{
			ChannelID:    id.ID,
			ChannelType:  int64(id.Type),
			ChannelEpoch: 41,
			LeaderEpoch:  10,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 2, 3},
			Leader:       owner.cfg.Node.ID,
			MinISR:       3,
			Status:       uint8(channel.StatusActive),
			Features:     uint64(channel.MessageSeqFormatLegacyU32),
			LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
		}))
	}
	require.NoError(t, owner.Store().AddChannelSubscribers(context.Background(), hotID.ID, int64(hotID.Type), []string{"hot-user"}))
	require.NoError(t, owner.Store().AddChannelSubscribers(context.Background(), normalID.ID, int64(normalID.Type), []string{"normal-user"}))

	for _, app := range harness.appsWithLeaderFirst(ownerID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), hotID)
		require.NoError(t, err)
		_, err = app.channelMetaSync.RefreshChannelMeta(context.Background(), normalID)
		require.NoError(t, err)
	}

	for i := 0; i < 20; i++ {
		require.NoError(t, owner.deliveryRuntime.Submit(context.Background(), deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				ChannelID:   hotID.ID,
				ChannelType: hotID.Type,
				MessageID:   uint64(i + 1),
				MessageSeq:  uint64(i + 1),
				FromUID:     "hot-sender",
				Payload:     []byte("hot"),
			},
		}))
	}
	require.Eventually(t, func() bool {
		return owner.deliveryRuntime.ActorLane(hotID.ID, hotID.Type) == deliveryruntime.LaneDedicated
	}, 5*time.Second, 20*time.Millisecond)

	senderConn := connectMultinodeWKProtoClient(t, owner, "normal-sender", "normal-sender-device")
	normalRecipientConn := connectMultinodeWKProtoClient(t, recipientNode, "normal-user", "normal-user-device")

	sendAppWKProtoFrame(t, senderConn, &frame.SendPacket{
		ChannelID:   normalID.ID,
		ChannelType: normalID.Type,
		ClientSeq:   100,
		ClientMsgNo: "normal-1",
		Payload:     []byte("normal"),
	})
	_, ok := readAppWKProtoFrameWithin(t, senderConn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)

	recvNormal, ok := readAppWKProtoFrameWithin(t, normalRecipientConn, multinodeAppReadTimeout).(*frame.RecvPacket)
	require.True(t, ok)
	require.Equal(t, normalID.ID, recvNormal.ChannelID)
}

func TestThreeNodeAppUserTokenEndpointPersistsThroughClusterForwarding(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)

	targetNodeID := leaderID%3 + 1
	require.NotEqual(t, leaderID, targetNodeID)
	target := harness.apps[targetNodeID]

	req, err := http.NewRequest(
		http.MethodPost,
		"http://"+target.API().Addr()+"/user/token",
		bytes.NewBufferString(`{"uid":"multi-token-user","token":"token-cluster","device_flag":1,"device_level":1}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"status":200}`, string(body))

	for _, app := range harness.orderedApps() {
		app := app
		require.Eventually(t, func() bool {
			gotUser, err := app.DB().ForSlot(1).GetUser(context.Background(), "multi-token-user")
			return err == nil && gotUser == (metadb.User{UID: "multi-token-user"})
		}, 5*time.Second, 20*time.Millisecond)
	}

	for _, app := range harness.orderedApps() {
		app := app
		require.Eventually(t, func() bool {
			gotDevice, err := app.DB().ForSlot(1).GetDevice(context.Background(), "multi-token-user", 1)
			return err == nil && gotDevice == (metadb.Device{
				UID:         "multi-token-user",
				DeviceFlag:  1,
				Token:       "token-cluster",
				DeviceLevel: 1,
			})
		}, 5*time.Second, 20*time.Millisecond)
	}
}

func TestThreeNodeAppSendAckSurvivesLeaderCrash(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	recipientUID := "crash-recipient"
	channelID := deliveryusecase.EncodePersonChannel("crash-sender", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 41,
		LeaderEpoch:  12,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       leader.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range harness.appsWithLeaderFirst(leaderID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	conn := connectMultinodeWKProtoClient(t, leader, "crash-sender", "crash-sender-device")
	sendAppWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: id.Type,
		ClientSeq:   1,
		ClientMsgNo: "sendack-crash-1",
		Payload:     []byte("survive leader crash"),
	})

	sendack, ok := readAppWKProtoFrameWithin(t, conn, multinodeAppReadTimeout).(*frame.SendackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NoError(t, conn.Close())

	harness.stopNode(t, leaderID)
	newLeaderID := harness.waitForLeaderChange(t, 1, leaderID)
	require.NotZero(t, newLeaderID)

	for _, app := range harness.runningApps() {
		msg := waitForAppCommittedMessage(t, app, id, sendack.MessageSeq, 5*time.Second)
		require.Equal(t, []byte("survive leader crash"), msg.Payload)
		require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
	}

	harness.restartNode(t, leaderID)
	harness.waitForStableLeader(t, 1)

	msg := waitForAppCommittedMessage(t, harness.apps[leaderID], id, sendack.MessageSeq, 5*time.Second)
	require.Equal(t, []byte("survive leader crash"), msg.Payload)
	require.Equal(t, sendack.MessageSeq, msg.MessageSeq)
}

func TestThreeNodeAppRollingRestartPreservesWriteAvailability(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	ownerID := harness.waitForStableLeader(t, 1)
	owner := harness.apps[ownerID]
	recipientUID := "rolling-recipient"
	channelID := deliveryusecase.EncodePersonChannel("rolling-sender", recipientUID)

	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 51,
		LeaderEpoch:  13,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       owner.cfg.Node.ID,
		MinISR:       3,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, owner.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, app := range harness.appsWithLeaderFirst(ownerID) {
		_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	type sentMessage struct {
		seq     uint64
		payload []byte
	}
	sent := make([]sentMessage, 0, 4)

	sendViaOwner := func(clientSeq uint64, clientMsgNo string, payload []byte) {
		t.Helper()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := harness.apps[ownerID].Message().Send(ctx, messageusecase.SendCommand{
			FromUID:     "rolling-sender",
			ChannelID:   recipientUID,
			ChannelType: id.Type,
			ClientSeq:   clientSeq,
			ClientMsgNo: clientMsgNo,
			Payload:     payload,
		})
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, result.Reason)
		sent = append(sent, sentMessage{seq: result.MessageSeq, payload: payload})
	}

	restartOrder := make([]uint64, 0, 3)
	for _, nodeID := range []uint64{1, 2, 3} {
		if nodeID == ownerID {
			continue
		}
		restartOrder = append(restartOrder, nodeID)
	}
	restartOrder = append(restartOrder, ownerID)

	sendViaOwner(1, "rolling-1", []byte(fmt.Sprintf("before restart node-%d", restartOrder[0])))
	harness.stopNode(t, restartOrder[0])
	harness.restartNode(t, restartOrder[0])
	harness.waitForStableLeader(t, 1)

	sendViaOwner(2, "rolling-2", []byte(fmt.Sprintf("before restart node-%d", restartOrder[1])))
	harness.stopNode(t, restartOrder[1])
	harness.restartNode(t, restartOrder[1])
	harness.waitForStableLeader(t, 1)

	sendViaOwner(3, "rolling-3", []byte("before restart owner"))
	harness.stopNode(t, ownerID)
	harness.restartNode(t, ownerID)
	harness.waitForStableLeader(t, 1)

	sendViaOwner(4, "rolling-4", []byte("after rolling restart"))

	for _, item := range sent {
		for _, app := range harness.orderedApps() {
			msg := waitForAppCommittedMessage(t, app, id, item.seq, 5*time.Second)
			require.Equal(t, item.payload, msg.Payload)
			require.Equal(t, item.seq, msg.MessageSeq)
		}
	}
}

func TestThreeNodeAppHarnessRestartNodePreservesDataDir(t *testing.T) {
	harness := newThreeNodeAppHarness(t)

	oldDataDir := harness.apps[2].cfg.Node.DataDir
	oldGatewayAddr := harness.apps[2].Gateway().ListenerAddr("tcp-wkproto")

	harness.stopNode(t, 2)
	restarted := harness.restartNode(t, 2)

	require.Equal(t, oldDataDir, restarted.cfg.Node.DataDir)
	require.Equal(t, oldGatewayAddr, restarted.Gateway().ListenerAddr("tcp-wkproto"))

	harness.waitForStableLeader(t, 1)
}

func TestThreeNodeAppHarnessUsesExplicitDataPlaneConcurrency(t *testing.T) {
	harness := newThreeNodeAppHarness(t)

	for _, app := range harness.orderedApps() {
		require.Equal(t, 1, app.cfg.Cluster.PoolSize)
		require.Equal(t, 8, app.cfg.Cluster.DataPlanePoolSize)
		require.Equal(t, 16, app.cfg.Cluster.DataPlaneMaxFetchInflight)
		require.Equal(t, 16, app.cfg.Cluster.DataPlaneMaxPendingFetch)
	}
}

func waitForAuthoritativeChannelRuntimeMeta(t *testing.T, app *App, id channel.ChannelID, timeout time.Duration) metadb.ChannelRuntimeMeta {
	t.Helper()

	var meta metadb.ChannelRuntimeMeta
	require.Eventually(t, func() bool {
		current, err := app.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		if err != nil {
			return false
		}
		meta = current
		return meta.LeaseUntilMS > 0
	}, timeout, 20*time.Millisecond)
	return meta
}

func idleRealtimeDeliveryState(
	t *testing.T,
	harness *threeNodeAppHarness,
	owner, senderNode, recipientNode *App,
	id channel.ChannelID,
	senderUID, recipientUID string,
	wantSeq uint64,
) string {
	t.Helper()

	var b bytes.Buffer

	meta, err := owner.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	if err != nil {
		fmt.Fprintf(&b, "owner meta error: %v\n", err)
	} else {
		fmt.Fprintf(&b, "owner meta: leader=%d leader_epoch=%d channel_epoch=%d lease_until_ms=%d replicas=%v isr=%v\n",
			meta.Leader, meta.LeaderEpoch, meta.ChannelEpoch, meta.LeaseUntilMS, meta.Replicas, meta.ISR)
	}

	for _, uid := range []string{senderUID, recipientUID} {
		routes, routeErr := owner.presenceApp.EndpointsByUID(context.Background(), uid)
		if routeErr != nil {
			fmt.Fprintf(&b, "presence %s error: %v\n", uid, routeErr)
			continue
		}
		fmt.Fprintf(&b, "presence %s routes: %+v\n", uid, routes)
	}

	key := channelhandler.KeyFromChannelID(id)
	for _, app := range harness.orderedApps() {
		handle, ok := app.ISRRuntime().Channel(key)
		if !ok {
			fmt.Fprintf(&b, "node %d runtime missing for %s\n", app.cfg.Node.ID, key)
			continue
		}
		state := handle.Status()
		localMeta := handle.Meta()
		fmt.Fprintf(&b, "node %d runtime: role=%d leader=%d epoch=%d leader_epoch=%d commit_ready=%t hw=%d leo=%d lease_until=%s replicas=%v isr=%v\n",
			app.cfg.Node.ID,
			state.Role,
			state.Leader,
			state.Epoch,
			localMeta.LeaderEpoch,
			state.CommitReady,
			state.HW,
			state.LEO,
			localMeta.LeaseUntil.UTC().Format(time.RFC3339Nano),
			localMeta.Replicas,
			localMeta.ISR,
		)
		msg, loadErr := channelhandler.LoadMsg(channelStoreForID(app.ChannelLogDB(), id), state.HW, wantSeq)
		if loadErr != nil {
			fmt.Fprintf(&b, "node %d load seq %d error: %v\n", app.cfg.Node.ID, wantSeq, loadErr)
			continue
		}
		fmt.Fprintf(&b, "node %d seq %d payload=%q\n", app.cfg.Node.ID, wantSeq, string(msg.Payload))
	}

	fmt.Fprintf(&b, "sender node=%d recipient node=%d owner node=%d\n", senderNode.cfg.Node.ID, recipientNode.cfg.Node.ID, owner.cfg.Node.ID)
	return b.String()
}
