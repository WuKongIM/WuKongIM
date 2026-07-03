//go:build e2e && legacy_e2e

package node_scalein_channel_drain

import (
	"context"
	"fmt"
	"hash/crc32"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/legacy/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	scaleInTargetNodeID      uint64 = 3
	scaleInPhysicalSlotCount uint32 = 2
)

func TestNodeScaleInDrainsChannelOwnershipBeforeSafeRemoval(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(scaleInClusterOptions()...)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	managerNode := cluster.MustNode(1)
	channel := activateTargetBackedPersonChannel(ctx, t, cluster, *managerNode, scaleInTargetNodeID)
	require.Contains(t, channel.before.Channel.Replicas, scaleInTargetNodeID, "precondition must exercise channel drain")
	t.Logf("channel replicas before scale-in: %s", channel.beforeBody)
	targetSession := connectTargetGatewaySession(ctx, t, cluster, scaleInTargetNodeID)

	planReq := suite.ManagerNodeScaleInPlanRequest{
		ConfirmStatefulSetTail: true,
		ExpectedTailNodeID:     scaleInTargetNodeID,
	}
	plan, planBody := waitScaleInPlanCanStart(ctx, t, cluster, *managerNode, scaleInTargetNodeID, planReq)
	require.False(t, plan.SafeToRemove, "plan must not be removable before start: %s", planBody)
	require.True(t, plan.Progress.ChannelInventoryScanned, "plan should scan channel inventory: %s", planBody)
	require.False(t, plan.Progress.ChannelInventoryPartial, "channel inventory must be complete: %s", planBody)
	require.Greater(t, plan.Progress.ChannelReplicas, 0, "target channel ownership should be visible before drain: %s", planBody)

	started, startBody, err := suite.StartNodeScaleIn(ctx, *managerNode, scaleInTargetNodeID, planReq)
	require.NoError(t, err, cluster.DumpDiagnostics()+"\nscale-in start: "+string(startBody))
	require.False(t, started.SafeToRemove, "start must not skip directly to safe removal: %s", startBody)
	require.NotEqual(t, "ready_to_remove", started.Status, "start must preserve drain phase ordering: %s", startBody)
	require.True(t, scaleInReportHasTargetSession(started), "start must observe target runtime sessions before drain: %s", startBody)

	final, finalBody := driveScaleInToReady(ctx, t, cluster, *managerNode, scaleInTargetNodeID, channel.channelID, targetSession)
	require.Equal(t, "ready_to_remove", final.Status, "scale-in final status: %s", finalBody)
	require.True(t, final.SafeToRemove, "ready report must be safe to remove: %s", finalBody)
	require.Zero(t, final.Progress.AssignedSlotReplicas, "target assigned Slot replicas remain: %s", finalBody)
	require.Zero(t, final.Progress.ObservedSlotReplicas, "target observed Slot replicas remain: %s", finalBody)
	require.Zero(t, final.Progress.SlotLeaders, "target Slot leaders remain: %s", finalBody)
	require.Zero(t, final.Progress.ChannelLeaders, "target channel leaders remain: %s", finalBody)
	require.Zero(t, final.Progress.ChannelReplicas, "target channel replicas remain: %s", finalBody)
	require.Zero(t, final.Progress.ActiveChannelMigrationsInvolvingNode, "target channel migrations remain: %s", finalBody)
	require.Zero(t, final.Progress.ActiveConnections, "target active runtime connections remain: %s", finalBody)
	require.Zero(t, final.Progress.ClosingConnections, "target closing runtime connections remain: %s", finalBody)
	require.Zero(t, final.Progress.GatewaySessions, "target gateway sessions remain: %s", finalBody)
	require.True(t, final.Progress.ChannelInventoryScanned, "final channel inventory should be scanned: %s", finalBody)
	require.False(t, final.Progress.ChannelInventoryPartial, "final channel inventory should be complete: %s", finalBody)

	after, afterBody, err := suite.WaitForChannelClusterReplicas(ctx, *managerNode, frame.ChannelTypePerson, channel.channelID, func(detail suite.ManagerChannelClusterReplicaDetail) bool {
		return detail.Channel.Status == "active" && !slices.Contains(detail.Channel.Replicas, scaleInTargetNodeID)
	})
	require.NoError(t, err, cluster.DumpDiagnostics()+"\nscale-in final: "+string(finalBody))
	require.NotContains(t, after.Channel.Replicas, scaleInTargetNodeID, "channel replicas after scale-in: %s", afterBody)
	t.Logf("channel replicas after scale-in: %s", afterBody)

	sendAndRequireRecv(t, cluster, channel.sender, channel.recipient, channel.senderUID, channel.recipientUID, "scalein-after-drain", 2, []byte("after node scale-in channel drain"))
}

func scaleInClusterOptions() []suite.Option {
	overrides := map[string]string{
		"WK_CLUSTER_CONTROLLER_REPLICA_N":             "2",
		"WK_CLUSTER_SLOT_REPLICA_N":                   "2",
		"WK_CLUSTER_SLOT_COUNT":                       fmt.Sprint(scaleInPhysicalSlotCount),
		"WK_CLUSTER_INITIAL_SLOT_COUNT":               fmt.Sprint(scaleInPhysicalSlotCount),
		"WK_CHANNEL_MIGRATION_SCAN_INTERVAL":          "100ms",
		"WK_CHANNEL_MIGRATION_RETRY_BACKOFF":          "200ms",
		"WK_CHANNEL_MIGRATION_CATCH_UP_STABLE_WINDOW": "100ms",
	}
	opts := make([]suite.Option, 0, 3)
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		opts = append(opts, suite.WithNodeConfigOverrides(nodeID, overrides))
	}
	return opts
}

type activatedChannel struct {
	sender       *suite.WKProtoClient
	recipient    *suite.WKProtoClient
	senderUID    string
	recipientUID string
	channelID    string
	before       suite.ManagerChannelClusterReplicaDetail
	beforeBody   []byte
}

type targetGatewaySession struct {
	client *suite.WKProtoClient
	uid    string
	closed bool
}

func activateTargetBackedPersonChannel(ctx context.Context, t *testing.T, cluster *suite.StartedCluster, managerNode suite.StartedNode, targetNodeID uint64) activatedChannel {
	t.Helper()

	slotDetails := make(map[uint32]suite.ManagerSlotDetail)
	for i := 1; i <= 60; i++ {
		senderUID := fmt.Sprintf("scalein-u%02d-a", i)
		recipientUID := fmt.Sprintf("scalein-u%02d-b", i)
		channelID := suite.PersonChannelID(senderUID, recipientUID)
		senderNodeID, recipientNodeID := selectBootstrapGatewayNodes(ctx, t, managerNode, targetNodeID, channelID, slotDetails)

		sender := mustWKProtoClient(t)
		recipient := mustWKProtoClient(t)
		require.NoError(t, sender.Connect(cluster.MustNode(senderNodeID).GatewayAddr(), senderUID, senderUID+"-device"))
		require.NoError(t, recipient.Connect(cluster.MustNode(recipientNodeID).GatewayAddr(), recipientUID, recipientUID+"-device"))
		sendAndRequireRecv(t, cluster, sender, recipient, senderUID, recipientUID, fmt.Sprintf("scalein-bootstrap-%02d", i), 1, []byte("before node scale-in channel drain"))

		detail, body, err := suite.WaitForChannelClusterReplicas(ctx, managerNode, frame.ChannelTypePerson, channelID, func(detail suite.ManagerChannelClusterReplicaDetail) bool {
			return detail.Channel.Status == "active" && len(detail.Channel.Replicas) == 2 && detail.Channel.Leader != 0
		})
		require.NoError(t, err, cluster.DumpDiagnostics())
		if slices.Contains(detail.Channel.Replicas, targetNodeID) {
			return activatedChannel{
				sender:       sender,
				recipient:    recipient,
				senderUID:    senderUID,
				recipientUID: recipientUID,
				channelID:    channelID,
				before:       detail,
				beforeBody:   body,
			}
		}

		_ = sender.Close()
		_ = recipient.Close()
	}

	require.FailNow(t, "could not activate a person channel backed by target node", cluster.DumpDiagnostics())
	return activatedChannel{}
}

func selectBootstrapGatewayNodes(
	ctx context.Context,
	t *testing.T,
	managerNode suite.StartedNode,
	targetNodeID uint64,
	channelID string,
	slotDetails map[uint32]suite.ManagerSlotDetail,
) (uint64, uint64) {
	t.Helper()

	slotID := physicalSlotForChannel(channelID)
	detail, ok := slotDetails[slotID]
	if !ok {
		detail = waitSlotDetailReady(ctx, t, managerNode, slotID)
		slotDetails[slotID] = detail
	}
	senderNodeID := detail.Runtime.LeaderID
	for _, nodeID := range detail.Runtime.CurrentPeers {
		if nodeID != targetNodeID {
			senderNodeID = nodeID
			break
		}
	}
	recipientNodeID := uint64(1)
	for candidate := uint64(1); candidate <= 3; candidate++ {
		if candidate != targetNodeID && candidate != senderNodeID {
			recipientNodeID = candidate
			break
		}
	}
	return senderNodeID, recipientNodeID
}

func waitSlotDetailReady(ctx context.Context, t *testing.T, managerNode suite.StartedNode, slotID uint32) suite.ManagerSlotDetail {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastDetail suite.ManagerSlotDetail
		lastBody   []byte
		lastErr    error
	)
	for {
		detail, body, err := suite.FetchSlotDetail(ctx, managerNode, slotID)
		if err == nil {
			lastDetail = detail
			lastBody = body
			if detail.State.Quorum == "ready" && detail.State.Sync == "matched" && detail.Runtime.LeaderID != 0 && len(detail.Runtime.CurrentPeers) > 0 {
				return detail
			}
			lastErr = fmt.Errorf("slot %d not ready: quorum=%s sync=%s leader=%d peers=%v", slotID, detail.State.Quorum, detail.State.Sync, detail.Runtime.LeaderID, detail.Runtime.CurrentPeers)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "last slot detail: "+string(lastBody)+fmt.Sprintf("\nlast detail: %+v\nlast error: %v", lastDetail, lastErr))
		case <-ticker.C:
		}
	}
}

func physicalSlotForChannel(channelID string) uint32 {
	return crc32.ChecksumIEEE([]byte(channelID))%scaleInPhysicalSlotCount + 1
}

func connectTargetGatewaySession(ctx context.Context, t *testing.T, cluster *suite.StartedCluster, targetNodeID uint64) *targetGatewaySession {
	t.Helper()

	uid := fmt.Sprintf("scalein-target-session-%d", targetNodeID)
	client := mustWKProtoClient(t)
	require.NoError(t, client.Connect(cluster.MustNode(targetNodeID).GatewayAddr(), uid, uid+"-device"))
	session := &targetGatewaySession{client: client, uid: uid}
	t.Cleanup(func() { session.close() })
	ok, err := suite.ConnectionsContainUID(ctx, *cluster.MustNode(targetNodeID), uid)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, "target node must report the scale-in session before drain")
	return session
}

func (s *targetGatewaySession) close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.client != nil {
		_ = s.client.Close()
	}
}

func scaleInReportHasTargetSession(report suite.ManagerNodeScaleInReport) bool {
	return report.Progress.ActiveConnections > 0 || report.Progress.GatewaySessions > 0 ||
		report.Runtime.ActiveOnline > 0 || report.Runtime.GatewaySessions > 0
}

func waitScaleInPlanCanStart(
	ctx context.Context,
	t *testing.T,
	cluster *suite.StartedCluster,
	managerNode suite.StartedNode,
	targetNodeID uint64,
	req suite.ManagerNodeScaleInPlanRequest,
) (suite.ManagerNodeScaleInReport, []byte) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastReport suite.ManagerNodeScaleInReport
		lastBody   []byte
		lastErr    error
	)
	for {
		report, body, err := suite.PlanNodeScaleIn(ctx, managerNode, targetNodeID, req)
		if err == nil {
			lastReport = report
			lastBody = body
			if report.CanStart &&
				report.Checks.AllSlotsHaveQuorum &&
				report.Progress.ChannelInventoryScanned &&
				!report.Progress.ChannelInventoryPartial &&
				report.Progress.ChannelReplicas > 0 {
				return report, body
			}
			lastErr = fmt.Errorf("scale-in plan not startable: status=%s blocked=%v", report.Status, report.BlockedReasons)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), cluster.DumpDiagnostics()+"\nlast scale-in plan: "+string(lastBody)+fmt.Sprintf("\nlast report: %+v\nlast error: %v", lastReport, lastErr))
		case <-ticker.C:
		}
	}
}

func driveScaleInToReady(ctx context.Context, t *testing.T, cluster *suite.StartedCluster, managerNode suite.StartedNode, targetNodeID uint64, channelID string, targetSession *targetGatewaySession) (suite.ManagerNodeScaleInReport, []byte) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastReport          suite.ManagerNodeScaleInReport
		lastBody            []byte
		lastMigration       suite.ManagerChannelMigrationDetail
		lastMigrationBody   []byte
		lastMigrationErr    error
		lastErr             error
		sawChannelDrain     bool
		sawConnectionWait   bool
		nextMigrationDetail time.Time
	)
	for {
		report, body, err := suite.GetNodeScaleInStatus(ctx, managerNode, targetNodeID)
		if err == nil {
			lastReport = report
			lastBody = body
			require.False(t, report.SafeToRemove && report.Status != "ready_to_remove", "safe_to_remove set before ready_to_remove: %s", body)
			if report.Status == "draining_channels" || report.NextAction == "drain_channels" {
				sawChannelDrain = true
			}
			if report.Status == "waiting_connections" && scaleInReportHasTargetSession(report) {
				sawConnectionWait = true
				targetSession.close()
			}
			if report.Status == "ready_to_remove" {
				require.True(t, sawChannelDrain, "scale-in never exposed channel drain phase; final report: %s", body)
				require.True(t, sawConnectionWait, "scale-in never waited for target runtime sessions; final report: %s", body)
				return report, body
			}
			if report.CanAdvance {
				report, body, err = suite.AdvanceNodeScaleIn(ctx, managerNode, targetNodeID, suite.ManagerAdvanceNodeScaleInRequest{
					MaxLeaderTransfers:   3,
					MaxChannelMigrations: 5,
				})
				if err == nil {
					lastReport = report
					lastBody = body
					require.False(t, report.SafeToRemove && report.Status != "ready_to_remove", "advance set safe_to_remove before ready_to_remove: %s", body)
					if report.Status == "draining_channels" || report.NextAction == "drain_channels" {
						sawChannelDrain = true
					}
					if report.Status == "waiting_connections" && scaleInReportHasTargetSession(report) {
						sawConnectionWait = true
						targetSession.close()
					}
				}
			}
			if report.Progress.ActiveChannelMigrationsInvolvingNode > 0 && !time.Now().Before(nextMigrationDetail) {
				migration, migrationBody, migrationErr := suite.FetchChannelMigration(ctx, managerNode, frame.ChannelTypePerson, channelID)
				lastMigrationErr = migrationErr
				if migrationErr == nil {
					lastMigration = migration
					lastMigrationBody = migrationBody
				}
				nextMigrationDetail = time.Now().Add(500 * time.Millisecond)
			}
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), cluster.DumpDiagnostics()+
				"\nlast scale-in report: "+string(lastBody)+
				"\nlast channel migration: "+string(lastMigrationBody)+
				fmt.Sprintf("\nlast report: %+v\nlast migration: %+v\nlast error: %v\nlast migration error: %v", lastReport, lastMigration, lastErr, lastMigrationErr))
		case <-ticker.C:
		}
	}
}

func mustWKProtoClient(t *testing.T) *suite.WKProtoClient {
	t.Helper()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func sendAndRequireRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID, clientMsgNo string,
	clientSeq uint64,
	payload []byte,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}))

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	recv, err := recipient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, senderUID, recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, payload, recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq))
}
