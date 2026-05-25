//go:build integration
// +build integration

package app

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestChannelMigrationLeaderTransferWithRemoteDrainAndLaggingTarget(t *testing.T) {
	harness := newChannelMigrationAppHarness(t)
	slotLeaderID := harness.waitForStableLeader(t, 1)
	sourceID := nextChannelMigrationNode(slotLeaderID)
	targetID := nextChannelMigrationNode(sourceID)
	otherID := remainingChannelMigrationNode(sourceID, targetID)
	require.NotEqual(t, slotLeaderID, sourceID, "channel leader must be remote from slot leader for FenceAndDrain RPC coverage")

	fromUID := "migration-lt-sender"
	recipientUID := fmt.Sprintf("migration-lt-recipient-%d", time.Now().UnixNano())
	id := channel.ChannelID{
		ID:   deliveryusecase.EncodePersonChannel(fromUID, recipientUID),
		Type: frame.ChannelTypePerson,
	}
	meta := channelMigrationRuntimeMeta(id, 41, 11, []uint64{sourceID, otherID, targetID}, []uint64{sourceID, otherID, targetID}, sourceID, 2)
	channelMigrationSeedMeta(t, harness.apps[slotLeaderID], id, meta)
	channelMigrationRefreshMeta(t, harness.appsWithLeaderFirst(sourceID), id, meta)

	harness.stopNode(t, targetID)
	beforeSeq := channelMigrationSendPerson(t, harness.apps[sourceID], fromUID, recipientUID, 1, "migration-lt-before", []byte("before transfer while target is down"))
	for _, nodeID := range []uint64{sourceID, otherID} {
		msg := waitForAppCommittedMessage(t, harness.apps[nodeID], id, beforeSeq, 10*time.Second)
		require.Equal(t, []byte("before transfer while target is down"), msg.Payload)
	}

	restartedTarget := harness.restartNode(t, targetID)
	harness.waitForStableLeader(t, 1)
	channelMigrationRefreshMeta(t, []*App{restartedTarget}, id, meta)

	task := channelMigrationLeaderTransferTask(id, meta, sourceID, targetID)
	channelMigrationCreateTask(t, harness.apps[slotLeaderID], task, meta)
	completed := channelMigrationWaitForTask(t, harness, id, task.TaskID, metadb.ChannelMigrationStatusCompleted, 30*time.Second)
	require.Equal(t, metadb.ChannelMigrationPhaseClearFence, completed.Phase)

	finalMeta := channelMigrationWaitForMeta(t, harness, id, func(got metadb.ChannelRuntimeMeta) bool {
		return got.Leader == targetID && got.LeaderEpoch == meta.LeaderEpoch+1 && got.WriteFenceToken == ""
	})
	require.Equal(t, meta.ChannelEpoch, finalMeta.ChannelEpoch)

	harness.waitForStableLeader(t, 1)
	channelMigrationRefreshMeta(t, harness.appsWithLeaderFirst(targetID), id, finalMeta)
	afterSeq := channelMigrationSendPerson(t, harness.apps[targetID], fromUID, recipientUID, 2, "migration-lt-after", []byte("after transfer on target leader"))
	require.Greater(t, afterSeq, beforeSeq)

	for _, app := range harness.orderedApps() {
		before := waitForAppCommittedMessage(t, app, id, beforeSeq, 10*time.Second)
		after := waitForAppCommittedMessage(t, app, id, afterSeq, 10*time.Second)
		require.Equal(t, []byte("before transfer while target is down"), before.Payload)
		require.Equal(t, []byte("after transfer on target leader"), after.Payload)
	}
}

func TestChannelMigrationReplicaReplacementPromotesFollowerSourceTarget(t *testing.T) {
	harness := newChannelMigrationAppHarness(t)
	slotLeaderID := harness.waitForStableLeader(t, 1)
	leaderID := slotLeaderID
	sourceID := nextChannelMigrationNode(leaderID)
	targetID := remainingChannelMigrationNode(leaderID, sourceID)

	fromUID := "migration-rr-follower-sender"
	recipientUID := fmt.Sprintf("migration-rr-follower-recipient-%d", time.Now().UnixNano())
	id := channel.ChannelID{
		ID:   deliveryusecase.EncodePersonChannel(fromUID, recipientUID),
		Type: frame.ChannelTypePerson,
	}
	meta := channelMigrationRuntimeMeta(id, 51, 12, []uint64{leaderID, sourceID}, []uint64{leaderID, sourceID}, leaderID, 2)
	channelMigrationSeedMeta(t, harness.apps[slotLeaderID], id, meta)
	channelMigrationRefreshMeta(t, []*App{harness.apps[leaderID], harness.apps[sourceID]}, id, meta)

	beforeSeq := channelMigrationSendPerson(t, harness.apps[leaderID], fromUID, recipientUID, 1, "migration-rr-follower-before", []byte("before follower replacement"))
	for _, nodeID := range []uint64{leaderID, sourceID} {
		msg := waitForAppCommittedMessage(t, harness.apps[nodeID], id, beforeSeq, 10*time.Second)
		require.Equal(t, []byte("before follower replacement"), msg.Payload)
	}

	task := channelMigrationReplicaReplaceTask(id, meta, sourceID, targetID)
	channelMigrationCreateTask(t, harness.apps[slotLeaderID], task, meta)
	completed := channelMigrationWaitForTask(t, harness, id, task.TaskID, metadb.ChannelMigrationStatusCompleted, 30*time.Second)
	require.Equal(t, metadb.ChannelMigrationPhaseClearFence, completed.Phase)

	finalMeta := channelMigrationWaitForMeta(t, harness, id, func(got metadb.ChannelRuntimeMeta) bool {
		return got.Leader == leaderID &&
			channelMigrationContainsNode(got.Replicas, targetID) &&
			channelMigrationContainsNode(got.ISR, targetID) &&
			!channelMigrationContainsNode(got.Replicas, sourceID) &&
			!channelMigrationContainsNode(got.ISR, sourceID) &&
			got.WriteFenceToken == ""
	})
	require.Equal(t, meta.ChannelEpoch+2, finalMeta.ChannelEpoch)

	harness.waitForStableLeader(t, 1)
	channelMigrationRefreshMeta(t, []*App{harness.apps[leaderID], harness.apps[targetID]}, id, finalMeta)
	afterSeq := channelMigrationSendPerson(t, harness.apps[leaderID], fromUID, recipientUID, 2, "migration-rr-follower-after", []byte("after follower replacement"))
	for _, nodeID := range []uint64{leaderID, targetID} {
		before := waitForAppCommittedMessage(t, harness.apps[nodeID], id, beforeSeq, 10*time.Second)
		after := waitForAppCommittedMessage(t, harness.apps[nodeID], id, afterSeq, 10*time.Second)
		require.Equal(t, []byte("before follower replacement"), before.Payload)
		require.Equal(t, []byte("after follower replacement"), after.Payload)
	}
}

func TestChannelMigrationReplicaReplacementSourceLeaderRunsEmbeddedTransfer(t *testing.T) {
	harness := newChannelMigrationAppHarness(t)
	slotLeaderID := harness.waitForStableLeader(t, 1)
	sourceID := nextChannelMigrationNode(slotLeaderID)
	embeddedLeaderID := remainingChannelMigrationNode(sourceID, slotLeaderID)
	targetID := slotLeaderID

	fromUID := "migration-rr-embedded-sender"
	recipientUID := fmt.Sprintf("migration-rr-embedded-recipient-%d", time.Now().UnixNano())
	id := channel.ChannelID{
		ID:   deliveryusecase.EncodePersonChannel(fromUID, recipientUID),
		Type: frame.ChannelTypePerson,
	}
	meta := channelMigrationRuntimeMeta(id, 61, 13, []uint64{sourceID, embeddedLeaderID}, []uint64{sourceID, embeddedLeaderID}, sourceID, 2)
	channelMigrationSeedMeta(t, harness.apps[slotLeaderID], id, meta)
	channelMigrationRefreshMeta(t, []*App{harness.apps[sourceID], harness.apps[embeddedLeaderID]}, id, meta)

	beforeSeq := channelMigrationSendPerson(t, harness.apps[sourceID], fromUID, recipientUID, 1, "migration-rr-embedded-before", []byte("before embedded replacement"))
	for _, nodeID := range []uint64{sourceID, embeddedLeaderID} {
		msg := waitForAppCommittedMessage(t, harness.apps[nodeID], id, beforeSeq, 10*time.Second)
		require.Equal(t, []byte("before embedded replacement"), msg.Payload)
	}

	task := channelMigrationReplicaReplaceTask(id, meta, sourceID, targetID)
	channelMigrationCreateTask(t, harness.apps[slotLeaderID], task, meta)
	completed := channelMigrationWaitForTask(t, harness, id, task.TaskID, metadb.ChannelMigrationStatusCompleted, 40*time.Second)
	require.Equal(t, metadb.ChannelMigrationPhaseClearFence, completed.Phase)

	finalMeta := channelMigrationWaitForMeta(t, harness, id, func(got metadb.ChannelRuntimeMeta) bool {
		return got.Leader == embeddedLeaderID &&
			got.LeaderEpoch == meta.LeaderEpoch+1 &&
			channelMigrationContainsNode(got.Replicas, embeddedLeaderID) &&
			channelMigrationContainsNode(got.Replicas, targetID) &&
			!channelMigrationContainsNode(got.Replicas, sourceID) &&
			channelMigrationContainsNode(got.ISR, targetID) &&
			!channelMigrationContainsNode(got.ISR, sourceID) &&
			got.WriteFenceToken == ""
	})
	require.Equal(t, meta.ChannelEpoch+2, finalMeta.ChannelEpoch)

	harness.waitForStableLeader(t, 1)
	channelMigrationRefreshMeta(t, []*App{harness.apps[embeddedLeaderID], harness.apps[targetID]}, id, finalMeta)
	afterSeq := channelMigrationSendPerson(t, harness.apps[embeddedLeaderID], fromUID, recipientUID, 2, "migration-rr-embedded-after", []byte("after embedded replacement"))
	for _, nodeID := range []uint64{embeddedLeaderID, targetID} {
		before := waitForAppCommittedMessage(t, harness.apps[nodeID], id, beforeSeq, 10*time.Second)
		after := waitForAppCommittedMessage(t, harness.apps[nodeID], id, afterSeq, 10*time.Second)
		require.Equal(t, []byte("before embedded replacement"), before.Payload)
		require.Equal(t, []byte("after embedded replacement"), after.Payload)
	}
}

func newChannelMigrationAppHarness(t *testing.T) *threeNodeAppHarness {
	t.Helper()
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(cfg *Config) {
		cfg.ChannelMigration.ScanInterval = time.Hour
		cfg.ChannelMigration.RetryBackoff = 25 * time.Millisecond
		cfg.ChannelMigration.CatchUpStableWindow = 25 * time.Millisecond
		cfg.ChannelMigration.OwnerLeaseTTL = 2 * time.Second
		cfg.ChannelMigration.FenceTTL = 10 * time.Second
		cfg.ChannelMigration.LeaderLeaseTTL = time.Minute
		cfg.ChannelMigration.CompletedRetentionTTL = time.Hour
	})
	for _, app := range harness.orderedApps() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		require.NoError(t, app.stopChannelMigration(ctx))
		cancel()
	}
	t.Cleanup(func() {
		for _, app := range harness.runningApps() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			require.NoError(t, app.stopConversationActiveHints(ctx))
			cancel()
		}
	})
	return harness
}

func channelMigrationRuntimeMeta(id channel.ChannelID, channelEpoch, leaderEpoch uint64, replicas, isr []uint64, leader uint64, minISR int64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: channelEpoch,
		LeaderEpoch:  leaderEpoch,
		Replicas:     channelMigrationSortedNodes(replicas),
		ISR:          channelMigrationSortedNodes(isr),
		Leader:       leader,
		MinISR:       minISR,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
}

func channelMigrationSortedNodes(nodes []uint64) []uint64 {
	out := append([]uint64(nil), nodes...)
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out
}

func channelMigrationSeedMeta(t *testing.T, owner *App, id channel.ChannelID, meta metadb.ChannelRuntimeMeta) {
	t.Helper()
	require.NoError(t, owner.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	require.Eventually(t, func() bool {
		got, err := owner.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		return err == nil && got.ChannelEpoch == meta.ChannelEpoch && got.LeaderEpoch == meta.LeaderEpoch && got.Leader == meta.Leader
	}, 5*time.Second, 20*time.Millisecond)
}

func channelMigrationRefreshMeta(t *testing.T, apps []*App, id channel.ChannelID, authoritative metadb.ChannelRuntimeMeta) {
	t.Helper()
	for _, app := range apps {
		app := app
		require.NotNil(t, app)
		app.channelMetaSync.InvalidateChannelMeta(id)
		var lastErr error
		var lastApplyErr error
		var lastMeta channel.Meta
		converged := false
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			_, err := app.channelMetaSync.applyAuthoritativeMeta(authoritative)
			if err != nil {
				lastApplyErr = err
			}
			meta, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
			if err != nil {
				lastErr = err
				time.Sleep(20 * time.Millisecond)
				continue
			}
			lastMeta = meta
			if meta.ID != id {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if !channelMigrationChannelMetaMatchesAuthoritative(meta, authoritative) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if channelMigrationRuntimeMatchesMeta(app, id, meta) {
				converged = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if converged {
			continue
		}
		t.Fatalf("channel migration meta refresh did not converge on node %d: last_err=%v last_apply_err=%v last_meta=(leader=%d epoch=%d/%d replicas=%v isr=%v fence=%q/%d) runtime=%s",
			app.cfg.Node.ID,
			lastErr,
			lastApplyErr,
			lastMeta.Leader,
			lastMeta.Epoch,
			lastMeta.LeaderEpoch,
			lastMeta.Replicas,
			lastMeta.ISR,
			lastMeta.WriteFence.Token,
			lastMeta.WriteFence.Version,
			channelMigrationAppRuntimeDebugString(app, id),
		)
	}
}

func channelMigrationLeaderTransferTask(id channel.ChannelID, meta metadb.ChannelRuntimeMeta, sourceID, targetID uint64) metadb.ChannelMigrationTask {
	nowMS := time.Now().UnixMilli()
	return metadb.ChannelMigrationTask{
		TaskID:           fmt.Sprintf("lt-%d", nowMS),
		Kind:             metadb.ChannelMigrationKindLeaderTransfer,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        id.ID,
		ChannelType:      int64(id.Type),
		SourceNode:       sourceID,
		TargetNode:       targetID,
		DesiredLeader:    targetID,
		BaseChannelEpoch: meta.ChannelEpoch,
		BaseLeaderEpoch:  meta.LeaderEpoch,
		CreatedAtMS:      nowMS,
		UpdatedAtMS:      nowMS,
	}
}

func channelMigrationReplicaReplaceTask(id channel.ChannelID, meta metadb.ChannelRuntimeMeta, sourceID, targetID uint64) metadb.ChannelMigrationTask {
	nowMS := time.Now().UnixMilli()
	return metadb.ChannelMigrationTask{
		TaskID:           fmt.Sprintf("rr-%d", nowMS),
		Kind:             metadb.ChannelMigrationKindReplicaReplace,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        id.ID,
		ChannelType:      int64(id.Type),
		SourceNode:       sourceID,
		TargetNode:       targetID,
		BaseChannelEpoch: meta.ChannelEpoch,
		BaseLeaderEpoch:  meta.LeaderEpoch,
		CreatedAtMS:      nowMS,
		UpdatedAtMS:      nowMS,
	}
}

func channelMigrationCreateTask(t *testing.T, app *App, task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, app.Store().CreateChannelMigrationTaskWithRuntimeGuard(ctx, metadb.ChannelMigrationTaskCreate{
		Task: task,
		RuntimeGuard: metadb.ChannelMigrationRuntimeGuard{
			ChannelID:            meta.ChannelID,
			ChannelType:          meta.ChannelType,
			ExpectedChannelEpoch: meta.ChannelEpoch,
			ExpectedLeaderEpoch:  meta.LeaderEpoch,
			ExpectedLeader:       meta.Leader,
			ExpectedFenceToken:   meta.WriteFenceToken,
			ExpectedFenceVersion: meta.WriteFenceVersion,
		},
	}))
}

func channelMigrationWaitForTask(t *testing.T, harness *threeNodeAppHarness, id channel.ChannelID, taskID string, status metadb.ChannelMigrationStatus, timeout time.Duration) metadb.ChannelMigrationTask {
	t.Helper()
	var got metadb.ChannelMigrationTask
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		channelMigrationPropagateAuthoritativeMeta(harness, id)
		channelMigrationTickExecutors(harness)
		channelMigrationPropagateAuthoritativeMeta(harness, id)
		for _, app := range harness.runningApps() {
			task, err := app.DB().ForHashSlot(app.Cluster().HashSlotForKey(id.ID)).GetChannelMigrationTask(context.Background(), id.ID, int64(id.Type), taskID)
			if err != nil {
				continue
			}
			got = task
			if task.Status == metadb.ChannelMigrationStatusFailed || task.Status == metadb.ChannelMigrationStatusBlocked || task.Status == metadb.ChannelMigrationStatusAborted {
				t.Logf("channel migration task terminal non-success status=%d phase=%d last_error=%q blocker=%q", task.Status, task.Phase, task.LastError, task.BlockerCode)
				break
			}
			if task.Status == status {
				return task
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("channel migration task %s did not reach status=%d after %s: last=%s runtime=%s",
		taskID, status, timeout, channelMigrationTaskDebugString(got), channelMigrationRuntimeDebugString(harness, id))
	return metadb.ChannelMigrationTask{}
}

func channelMigrationWaitForMeta(t *testing.T, harness *threeNodeAppHarness, id channel.ChannelID, accept func(metadb.ChannelRuntimeMeta) bool) metadb.ChannelRuntimeMeta {
	t.Helper()
	var got metadb.ChannelRuntimeMeta
	require.Eventually(t, func() bool {
		channelMigrationPropagateAuthoritativeMeta(harness, id)
		meta, ok := channelMigrationLatestRuntimeMeta(harness, id)
		if !ok {
			return false
		}
		got = meta
		if accept(meta) {
			return true
		}
		for _, app := range harness.runningApps() {
			meta, err := app.DB().ForHashSlot(app.Cluster().HashSlotForKey(id.ID)).GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
			if err != nil || !accept(meta) {
				continue
			}
			got = meta
			return true
		}
		return false
	}, 10*time.Second, 25*time.Millisecond)
	return got
}

func channelMigrationTickExecutors(harness *threeNodeAppHarness) {
	for _, app := range harness.runningApps() {
		if app.channelMigrationExecutor == nil {
			continue
		}
		_ = app.channelMigrationExecutor.Tick(context.Background())
	}
}

func channelMigrationPropagateAuthoritativeMeta(harness *threeNodeAppHarness, id channel.ChannelID) {
	meta, ok := channelMigrationLatestRuntimeMeta(harness, id)
	if !ok {
		return
	}
	for _, app := range harness.runningApps() {
		_, _ = app.channelMetaSync.applyAuthoritativeMeta(meta)
	}
}

func channelMigrationLatestRuntimeMeta(harness *threeNodeAppHarness, id channel.ChannelID) (metadb.ChannelRuntimeMeta, bool) {
	apps := harness.runningApps()
	if len(apps) == 0 {
		return metadb.ChannelRuntimeMeta{}, false
	}
	slotID := apps[0].Cluster().SlotForKey(id.ID)
	if leaderID, ok := harness.consensusLeader(slotID); ok && leaderID != 0 {
		if leader := harness.apps[uint64(leaderID)]; leader != nil {
			if meta, err := leader.DB().ForHashSlot(leader.Cluster().HashSlotForKey(id.ID)).GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type)); err == nil {
				return meta, true
			}
		}
	}
	var latest metadb.ChannelRuntimeMeta
	ok := false
	for _, app := range apps {
		meta, err := app.DB().ForHashSlot(app.Cluster().HashSlotForKey(id.ID)).GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		if err != nil {
			continue
		}
		if !ok || channelMigrationRuntimeMetaNewer(meta, latest) {
			latest = meta
			ok = true
		}
	}
	return latest, ok
}

func channelMigrationRuntimeMetaNewer(candidate, current metadb.ChannelRuntimeMeta) bool {
	if candidate.ChannelEpoch != current.ChannelEpoch {
		return candidate.ChannelEpoch > current.ChannelEpoch
	}
	if candidate.LeaderEpoch != current.LeaderEpoch {
		return candidate.LeaderEpoch > current.LeaderEpoch
	}
	return candidate.WriteFenceVersion > current.WriteFenceVersion
}

func channelMigrationTaskDebugString(task metadb.ChannelMigrationTask) string {
	return fmt.Sprintf("status=%d phase=%d attempt=%d next_run=%d last_error=%q blocker=%q fence=(%q,%d until=%d) cutover=(leo=%d hw=%d drained_node=%d drained_epoch=%d/%d drained_fence=%d) progress=(leader_leo=%d leader_hw=%d target_leo=%d target_hw=%d lag=%d)",
		task.Status,
		task.Phase,
		task.Attempt,
		task.NextRunAtMS,
		task.LastError,
		task.BlockerCode,
		task.FenceToken,
		task.FenceVersion,
		task.FenceUntilMS,
		task.CutoverLEO,
		task.CutoverHW,
		task.DrainedLeaderNode,
		task.DrainedChannelEpoch,
		task.DrainedLeaderEpoch,
		task.DrainedFenceVersion,
		task.Progress.LeaderLEO,
		task.Progress.LeaderHW,
		task.Progress.TargetLEO,
		task.Progress.TargetCheckpointHW,
		task.Progress.LagRecords,
	)
}

func channelMigrationRuntimeDebugString(harness *threeNodeAppHarness, id channel.ChannelID) string {
	key := channelhandler.KeyFromChannelID(id)
	out := ""
	for _, app := range harness.runningApps() {
		nodeID := app.cfg.Node.ID
		meta, metaErr := app.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
		metaText := fmt.Sprintf("meta_err=%v", metaErr)
		if metaErr == nil {
			metaText = fmt.Sprintf("meta leader=%d epoch=%d/%d replicas=%v isr=%v fence=(%q,%d until=%d)",
				meta.Leader, meta.ChannelEpoch, meta.LeaderEpoch, meta.Replicas, meta.ISR, meta.WriteFenceToken, meta.WriteFenceVersion, meta.WriteFenceUntilMS)
		}
		stateText := "runtime missing"
		if handle, ok := app.ISRRuntime().Channel(key); ok {
			meta := handle.Meta()
			st := handle.Status()
			stateText = fmt.Sprintf("runtime meta=(leader=%d epoch=%d/%d replicas=%v isr=%v fence=%q/%d) status=(role=%d leader=%d epoch=%d offset_epoch=%d leo=%d hw=%d checkpoint_hw=%d commit_ready=%t)",
				meta.Leader, meta.Epoch, meta.LeaderEpoch, meta.Replicas, meta.ISR, meta.WriteFence.Token, meta.WriteFence.Version,
				st.Role, st.Leader, st.Epoch, st.OffsetEpoch, st.LEO, st.HW, st.CheckpointHW, st.CommitReady)
		}
		out += fmt.Sprintf(" node=%d {%s; %s}", nodeID, metaText, stateText)
	}
	return out
}

func channelMigrationAppRuntimeDebugString(app *App, id channel.ChannelID) string {
	storeMeta, storeErr := app.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	storeText := fmt.Sprintf("store_meta_err=%v", storeErr)
	if storeErr == nil {
		storeText = fmt.Sprintf("store_meta leader=%d epoch=%d/%d replicas=%v isr=%v fence=(%q,%d until=%d)",
			storeMeta.Leader, storeMeta.ChannelEpoch, storeMeta.LeaderEpoch, storeMeta.Replicas, storeMeta.ISR, storeMeta.WriteFenceToken, storeMeta.WriteFenceVersion, storeMeta.WriteFenceUntilMS)
	}
	localMeta, localErr := app.DB().ForHashSlot(app.Cluster().HashSlotForKey(id.ID)).GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	localText := fmt.Sprintf("local_meta_err=%v", localErr)
	if localErr == nil {
		localText = fmt.Sprintf("local_meta leader=%d epoch=%d/%d replicas=%v isr=%v fence=(%q,%d until=%d)",
			localMeta.Leader, localMeta.ChannelEpoch, localMeta.LeaderEpoch, localMeta.Replicas, localMeta.ISR, localMeta.WriteFenceToken, localMeta.WriteFenceVersion, localMeta.WriteFenceUntilMS)
	}
	stateText := "runtime missing"
	if handle, ok := app.ISRRuntime().Channel(channelhandler.KeyFromChannelID(id)); ok {
		meta := handle.Meta()
		st := handle.Status()
		stateText = fmt.Sprintf("runtime meta=(leader=%d epoch=%d/%d replicas=%v isr=%v fence=%q/%d) status=(role=%d leader=%d epoch=%d offset_epoch=%d leo=%d hw=%d checkpoint_hw=%d commit_ready=%t)",
			meta.Leader, meta.Epoch, meta.LeaderEpoch, meta.Replicas, meta.ISR, meta.WriteFence.Token, meta.WriteFence.Version,
			st.Role, st.Leader, st.Epoch, st.OffsetEpoch, st.LEO, st.HW, st.CheckpointHW, st.CommitReady)
	}
	leader, leaderErr := app.Cluster().LeaderOf(app.Cluster().SlotForKey(id.ID))
	return fmt.Sprintf("%s; %s; slot_leader=%d/%v; %s", storeText, localText, leader, leaderErr, stateText)
}

func channelMigrationRuntimeMatchesMeta(app *App, id channel.ChannelID, meta channel.Meta) bool {
	handle, ok := app.ISRRuntime().Channel(channelhandler.KeyFromChannelID(id))
	if !ok {
		return false
	}
	handleMeta := handle.Meta()
	state := handle.Status()
	expectedRole := channel.ReplicaRoleFollower
	if meta.Leader == channel.NodeID(app.cfg.Node.ID) {
		expectedRole = channel.ReplicaRoleLeader
	}
	return handleMeta.Epoch == meta.Epoch &&
		handleMeta.LeaderEpoch == meta.LeaderEpoch &&
		handleMeta.Leader == meta.Leader &&
		handleMeta.WriteFence == meta.WriteFence &&
		channelMigrationNodeIDsEqual(handleMeta.Replicas, meta.Replicas) &&
		channelMigrationNodeIDsEqual(handleMeta.ISR, meta.ISR) &&
		state.Role == expectedRole &&
		state.Leader == meta.Leader &&
		state.Epoch == meta.Epoch &&
		state.CommitReady
}

func channelMigrationChannelMetaMatchesAuthoritative(meta channel.Meta, authoritative metadb.ChannelRuntimeMeta) bool {
	return meta.ID.ID == authoritative.ChannelID &&
		int64(meta.ID.Type) == authoritative.ChannelType &&
		meta.Epoch == authoritative.ChannelEpoch &&
		meta.LeaderEpoch == authoritative.LeaderEpoch &&
		meta.Leader == channel.NodeID(authoritative.Leader) &&
		meta.MinISR == int(authoritative.MinISR) &&
		meta.Status == channel.Status(authoritative.Status) &&
		uint64(meta.Features.MessageSeqFormat) == authoritative.Features &&
		meta.RetentionThroughSeq == authoritative.RetentionThroughSeq &&
		meta.WriteFence.Token == authoritative.WriteFenceToken &&
		meta.WriteFence.Version == authoritative.WriteFenceVersion &&
		uint8(meta.WriteFence.Reason) == authoritative.WriteFenceReason &&
		channelMigrationFenceUntilMS(meta.WriteFence.Until) == authoritative.WriteFenceUntilMS &&
		channelMigrationNodeIDsEqual(meta.Replicas, channelMigrationNodeIDsFromUint64(authoritative.Replicas)) &&
		channelMigrationNodeIDsEqual(meta.ISR, channelMigrationNodeIDsFromUint64(authoritative.ISR))
}

func channelMigrationFenceUntilMS(until time.Time) int64 {
	if until.IsZero() {
		return 0
	}
	return until.UnixMilli()
}

func channelMigrationNodeIDsFromUint64(nodes []uint64) []channel.NodeID {
	out := make([]channel.NodeID, len(nodes))
	for i, node := range nodes {
		out[i] = channel.NodeID(node)
	}
	return out
}

func channelMigrationNodeIDsEqual(a, b []channel.NodeID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func channelMigrationSendPerson(t *testing.T, app *App, fromUID, recipientUID string, clientSeq uint64, clientMsgNo string, payload []byte) uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := app.Message().Send(ctx, messageusecase.SendCommand{
		FromUID:     fromUID,
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	})
	require.NoErrorf(t, err, "send failed on node %d runtime=%s", app.cfg.Node.ID, channelMigrationAppRuntimeDebugString(app, channel.ChannelID{ID: deliveryusecase.EncodePersonChannel(fromUID, recipientUID), Type: frame.ChannelTypePerson}))
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.NotZero(t, result.MessageSeq)
	return result.MessageSeq
}

func nextChannelMigrationNode(nodeID uint64) uint64 {
	return nodeID%3 + 1
}

func remainingChannelMigrationNode(a, b uint64) uint64 {
	for _, nodeID := range []uint64{1, 2, 3} {
		if nodeID != a && nodeID != b {
			return nodeID
		}
	}
	return 0
}

func channelMigrationContainsNode(nodes []uint64, want uint64) bool {
	for _, node := range nodes {
		if node == want {
			return true
		}
	}
	return false
}

var _ = multiraft.SlotID(0)
