package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/stretchr/testify/require"
)

func TestSingleNodeAppendStoresMessage(t *testing.T) {
	factory := store.NewMemoryFactory()
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer cluster.Close()

	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, cluster.ApplyMeta(meta))

	appendRes, err := cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), appendRes.MessageSeq)

	messages := readServiceStoreMessages(t, factory, meta, 1)
	require.Len(t, messages, 1)
	require.Equal(t, uint64(1), messages[0].MessageSeq)
}

func TestAppendRequiresLoadedChannelState(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := ch.Meta{Key: ch.ChannelKey("1:not-loaded"), ID: ch.ChannelID{ID: "not-loaded", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	resolver := &countingMetaResolver{meta: meta}
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer cluster.Close()

	_, err = cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
	require.Equal(t, int32(0), resolver.calls.Load())
}

func TestAppendBatchRequiresLoadedChannelState(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := ch.Meta{Key: ch.ChannelKey("1:batch-not-loaded"), ID: ch.ChannelID{ID: "batch-not-loaded", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	resolver := &countingMetaResolver{meta: meta}
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer cluster.Close()

	_, err = cluster.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: meta.ID, Messages: []ch.Message{{Payload: []byte("hello")}}})
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
	require.Equal(t, int32(0), resolver.calls.Load())
}

func TestAppendUsesExistingReactorStateWithoutResolvingMeta(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := ch.Meta{Key: ch.ChannelKey("1:cached"), ID: ch.ChannelID{ID: "cached", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	resolver := &countingMetaResolver{meta: meta}
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer cluster.Close()

	require.NoError(t, cluster.ApplyMeta(meta))
	_, err = cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("first")}})
	require.NoError(t, err)
	_, err = cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("second")}})
	require.NoError(t, err)
	require.Equal(t, int32(0), resolver.calls.Load())
}

func TestAppendRejectsStaleExpectedEpochs(t *testing.T) {
	clusterAPI, err := New(Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1})
	require.NoError(t, err)
	defer clusterAPI.Close()

	meta := ch.Meta{
		Key:         ch.ChannelKey("1:expected-append"),
		ID:          ch.ChannelID{ID: "expected-append", Type: 1},
		Epoch:       2,
		LeaderEpoch: 3,
		Leader:      1,
		Replicas:    []ch.NodeID{1},
		ISR:         []ch.NodeID{1},
		MinISR:      1,
		Status:      ch.StatusActive,
	}
	require.NoError(t, clusterAPI.ApplyMeta(meta))

	_, err = clusterAPI.Append(context.Background(), ch.AppendRequest{
		ChannelID:            meta.ID,
		Message:              ch.Message{Payload: []byte("stale-channel")},
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
	})
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	_, err = clusterAPI.Append(context.Background(), ch.AppendRequest{
		ChannelID:            meta.ID,
		Message:              ch.Message{Payload: []byte("stale-leader")},
		ExpectedChannelEpoch: meta.Epoch,
		ExpectedLeaderEpoch:  2,
	})
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	res, err := clusterAPI.Append(context.Background(), ch.AppendRequest{
		ChannelID:            meta.ID,
		Message:              ch.Message{Payload: []byte("current")},
		ExpectedChannelEpoch: meta.Epoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
}

func TestHandlePullHintLazyLoadsFollowerMeta(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := ch.Meta{Key: ch.ChannelKey("1:hint-lazy"), ID: ch.ChannelID{ID: "hint-lazy", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	resolver := &countingMetaResolver{meta: meta}
	clusterAPI, err := New(Config{LocalNode: 2, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	err = svc.HandlePullHint(context.Background(), transport.PullHintRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Leader: 1, LeaderLEO: 1, ActivityVersion: 1, Reason: transport.PullHintReasonAppend})
	require.NoError(t, err)
	require.Equal(t, int32(1), resolver.calls.Load())

	loaded, err := svc.group.HasChannelState(context.Background(), meta.Key)
	require.NoError(t, err)
	require.True(t, loaded)
}

func TestHandlePullHintResolverFailureDoesNotLoadState(t *testing.T) {
	factory := store.NewMemoryFactory()
	id := ch.ChannelID{ID: "hint-missing", Type: 1}
	key := ch.ChannelKeyForID(id)
	resolver := &failingMetaResolver{err: ch.ErrChannelNotFound}
	clusterAPI, err := New(Config{LocalNode: 2, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	err = svc.HandlePullHint(context.Background(), transport.PullHintRequest{ChannelKey: key, ChannelID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, LeaderLEO: 1, ActivityVersion: 1, Reason: transport.PullHintReasonAppend})
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
	require.Equal(t, int32(1), resolver.calls.Load())

	loaded, err := svc.group.HasChannelState(context.Background(), key)
	require.NoError(t, err)
	require.False(t, loaded)
}

func TestHandlePullHintRejectsResolvedMetaWithDifferentID(t *testing.T) {
	factory := store.NewMemoryFactory()
	reqID := ch.ChannelID{ID: "hint-stale-id", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(reqID), ID: ch.ChannelID{ID: "different-id", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	resolver := &staticMetaResolver{meta: meta}
	clusterAPI, err := New(Config{LocalNode: 2, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	err = svc.HandlePullHint(context.Background(), transport.PullHintRequest{ChannelKey: meta.Key, ChannelID: reqID, Epoch: 1, LeaderEpoch: 1, Leader: 1, LeaderLEO: 1, ActivityVersion: 1, Reason: transport.PullHintReasonAppend})
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	loaded, err := svc.group.HasChannelState(context.Background(), meta.Key)
	require.NoError(t, err)
	require.False(t, loaded)
}

func TestHandlePullHintRejectsLocalLeaderLazyActivation(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := ch.Meta{Key: ch.ChannelKey("1:hint-leader"), ID: ch.ChannelID{ID: "hint-leader", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	resolver := &countingMetaResolver{meta: meta}
	clusterAPI, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	err = svc.HandlePullHint(context.Background(), transport.PullHintRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Leader: 1, LeaderLEO: 1, ActivityVersion: 1, Reason: transport.PullHintReasonAppend})
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	loaded, err := svc.group.HasChannelState(context.Background(), meta.Key)
	require.NoError(t, err)
	require.False(t, loaded)
}

func TestHandleNotifyKeepsLegacyNoOpForInvalidHints(t *testing.T) {
	tests := []struct {
		name      string
		localNode ch.NodeID
		resolver  ch.MetaResolver
		req       transport.NotifyRequest
		loadedKey ch.ChannelKey
	}{
		{
			name:      "unloaded without resolver",
			localNode: 2,
			req:       transport.NotifyRequest{ChannelKey: ch.ChannelKey("1:legacy-pull-hint-missing"), ChannelID: ch.ChannelID{ID: "legacy-pull-hint-missing", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1},
			loadedKey: ch.ChannelKey("1:legacy-pull-hint-missing"),
		},
		{
			name:      "stale resolved metadata",
			localNode: 2,
			resolver:  &staticMetaResolver{meta: ch.Meta{Key: ch.ChannelKey("1:legacy-pull-hint-stale"), ID: ch.ChannelID{ID: "different", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}},
			req:       transport.NotifyRequest{ChannelKey: ch.ChannelKey("1:legacy-pull-hint-stale"), ChannelID: ch.ChannelID{ID: "legacy-pull-hint-stale", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1},
			loadedKey: ch.ChannelKey("1:legacy-pull-hint-stale"),
		},
		{
			name:      "local leader validation",
			localNode: 1,
			resolver:  &countingMetaResolver{meta: ch.Meta{Key: ch.ChannelKey("1:legacy-pull-hint-local-leader"), ID: ch.ChannelID{ID: "legacy-pull-hint-local-leader", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}},
			req:       transport.NotifyRequest{ChannelKey: ch.ChannelKey("1:legacy-pull-hint-local-leader"), ChannelID: ch.ChannelID{ID: "legacy-pull-hint-local-leader", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1},
			loadedKey: ch.ChannelKey("1:legacy-pull-hint-local-leader"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := store.NewMemoryFactory()
			clusterAPI, err := New(Config{LocalNode: tt.localNode, Store: factory, ReactorCount: 1, MetaResolver: tt.resolver})
			require.NoError(t, err)
			defer clusterAPI.Close()
			svc := clusterAPI.(*cluster)

			require.NoError(t, svc.HandleNotify(context.Background(), tt.req))

			loaded, err := svc.group.HasChannelState(context.Background(), tt.loadedKey)
			require.NoError(t, err)
			require.False(t, loaded)
		})
	}
}

func TestHandleNotifyUsesLeaderLEOAsPullHintActivityVersion(t *testing.T) {
	factory := store.NewMemoryFactory()
	net := newServiceCaptureTransport()
	meta := ch.Meta{
		Key: ch.ChannelKey("1:legacy-notify-activity"), ID: ch.ChannelID{ID: "legacy-notify-activity", Type: 1},
		Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive,
	}
	net.SetPullResponse(transport.PullResponse{
		ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
		LeaderHW: 0, LeaderLEO: 0, ActivityVersion: 5, NextPullAfter: time.Hour, Control: transport.PullControlContinue,
	})
	clusterAPI, err := New(Config{LocalNode: 2, Store: factory, Transport: net, ReactorCount: 1, ReplicationIdlePollInterval: time.Hour})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)
	require.NoError(t, svc.ApplyMeta(meta))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.True(t, awaitServiceTick(t, svc, meta.Key, time.Now()))

	net.SetPullResponse(transport.PullResponse{
		ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
		LeaderHW: 0, LeaderLEO: 0, ActivityVersion: 6, NextPullAfter: time.Hour, Control: transport.PullControlContinue,
	})
	require.NoError(t, svc.HandleNotify(context.Background(), transport.NotifyRequest{
		ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Leader: meta.Leader, LeaderLEO: 6,
	}))

	require.Eventually(t, func() bool { return net.PullCalls() >= 2 }, time.Second, time.Millisecond)
}

func TestHandlePullUsesAllocatedOpIDAndReturnsRecords(t *testing.T) {
	factory := newServiceBlockingReadLogFactory()
	clusterAPI, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, LeaderRecentRecordCacheSize: -1})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, clusterAPI.ApplyMeta(meta))
	_, err = clusterAPI.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: 10, Payload: []byte("a")}})
	require.NoError(t, err)

	pullCh := make(chan transport.PullResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		pull, err := svc.HandlePull(context.Background(), transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024})
		pullCh <- pull
		errCh <- err
	}()
	factory.waitReadLogStarted(t)

	metaFuture, err := svc.group.Submit(context.Background(), meta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	metaCtx, metaCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer metaCancel()
	_, err = metaFuture.Await(metaCtx)
	require.NoError(t, err)

	factory.UnblockReadLogs()
	select {
	case err := <-errCh:
		require.NoError(t, err)
		pull := <-pullCh
		require.Len(t, pull.Records, 1)
		require.Equal(t, uint64(1), pull.Records[0].Index)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pull response")
	}
}

func TestHandleAckAdvancesHWAndCompletesQuorumAppend(t *testing.T) {
	factory := store.NewMemoryFactory()
	clusterAPI, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	meta := ch.Meta{
		Key:         ch.ChannelKey("1:ack-quorum"),
		ID:          ch.ChannelID{ID: "ack-quorum", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	require.NoError(t, svc.ApplyMeta(meta))

	appendDone := make(chan serviceAppendOutcome, 1)
	go func() {
		res, err := svc.Append(context.Background(), ch.AppendRequest{
			ChannelID: meta.ID,
			Message:   ch.Message{MessageID: 10, Payload: []byte("hello")},
		})
		appendDone <- serviceAppendOutcome{result: res, err: err}
	}()

	require.Eventually(t, func() bool {
		select {
		case outcome := <-appendDone:
			t.Fatalf("append completed before follower ack: result=%+v err=%v", outcome.result, outcome.err)
			return false
		default:
		}
		return awaitServiceTick(t, svc, meta.Key, time.Now())
	}, time.Second, time.Millisecond)

	require.NoError(t, svc.HandleAck(context.Background(), transport.AckRequest{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		Follower:    2,
		MatchOffset: 1,
	}))

	select {
	case outcome := <-appendDone:
		require.NoError(t, outcome.err)
		require.Equal(t, uint64(1), outcome.result.MessageSeq)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for quorum append")
	}

	messages := readServiceStoreMessages(t, factory, meta, 1)
	require.Len(t, messages, 1)
	require.Equal(t, uint64(1), messages[0].MessageSeq)
}

func TestAppendBatchContextCancelAfterAdmissionCleansQueuedWaiter(t *testing.T) {
	factory := newServiceCountingStoreFactory()
	clusterAPI, err := New(Config{
		LocalNode:             1,
		Store:                 factory,
		ReactorCount:          1,
		AppendBatchMaxRecords: 10,
		AppendBatchMaxWait:    time.Hour,
	})
	require.NoError(t, err)
	cluster := clusterAPI.(*cluster)
	defer cluster.Close()

	meta := ch.Meta{Key: ch.ChannelKey("1:cancel-after-admission"), ID: ch.ChannelID{ID: "cancel-after-admission", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, cluster.ApplyMeta(meta))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := cluster.AppendBatch(ctx, ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeLocal,
			Messages: []ch.Message{{
				MessageID:   1,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("cancel-me"),
			}},
		})
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			t.Fatalf("append returned before cancellation: %v", err)
			return false
		default:
		}
		return awaitServiceTick(t, cluster, meta.Key, time.Now()) && factory.appendCalls(meta.Key) == 0
	}, time.Second, time.Millisecond)

	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled append")
	}

	require.True(t, awaitServiceTick(t, cluster, meta.Key, time.Now().Add(2*time.Hour)))
	require.Equal(t, 0, factory.appendCalls(meta.Key))
}

func TestAppendBatchContextCancelReturnsContextErrorWhenCleanupBackpressured(t *testing.T) {
	factory := newServiceCountingStoreFactory()
	clusterAPI, err := New(Config{
		LocalNode:             1,
		Store:                 factory,
		ReactorCount:          1,
		MailboxSize:           1,
		AppendBatchMaxRecords: 10,
		AppendBatchMaxWait:    time.Hour,
	})
	require.NoError(t, err)
	cluster := clusterAPI.(*cluster)
	defer cluster.Close()

	meta := ch.Meta{Key: ch.ChannelKey("1:cancel-cleanup-backpressured"), ID: ch.ChannelID{ID: "cancel-cleanup-backpressured", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, cluster.ApplyMeta(meta))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := cluster.AppendBatch(ctx, ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeLocal,
			Messages: []ch.Message{{
				MessageID:   1,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("cancel-me"),
			}},
		})
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			t.Fatalf("append returned before cancellation: %v", err)
			return false
		default:
		}
		return awaitServiceTick(t, cluster, meta.Key, time.Now()) && factory.appendCalls(meta.Key) == 0
	}, time.Second, time.Millisecond)

	blockMeta := ch.Meta{Key: ch.ChannelKey("1:block-cleanup"), ID: ch.ChannelID{ID: "block-cleanup", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	factory.blockLoad(blockMeta.Key)
	defer factory.unblockLoad()
	blockFuture, err := cluster.group.Submit(context.Background(), blockMeta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: blockMeta.Key, Meta: blockMeta})
	require.NoError(t, err)
	factory.waitLoadStarted(t)

	fillerMeta := ch.Meta{Key: ch.ChannelKey("1:fill-cleanup"), ID: ch.ChannelID{ID: "fill-cleanup", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	fillerFuture, err := cluster.group.Submit(context.Background(), fillerMeta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: fillerMeta.Key, Meta: fillerMeta})
	require.NoError(t, err)

	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ch.ErrBackpressured)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled append")
	}

	factory.unblockLoad()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	_, err = blockFuture.Await(waitCtx)
	require.NoError(t, err)
	_, err = fillerFuture.Await(waitCtx)
	require.NoError(t, err)
}

type serviceAppendOutcome struct {
	result ch.AppendResult
	err    error
}

func readServiceStoreMessages(t testing.TB, factory *store.MemoryFactory, meta ch.Meta, maxSeq uint64) []ch.Message {
	t.Helper()
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	read, err := cs.ReadCommitted(context.Background(), store.ReadCommittedRequest{FromSeq: 1, MaxSeq: maxSeq, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	return read.Messages
}

type countingMetaResolver struct {
	meta  ch.Meta
	calls atomic.Int32
}

func (r *countingMetaResolver) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if id != r.meta.ID {
		return ch.Meta{}, ch.ErrChannelNotFound
	}
	r.calls.Add(1)
	return r.meta, nil
}

type staticMetaResolver struct {
	meta  ch.Meta
	calls atomic.Int32
}

func (r *staticMetaResolver) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	r.calls.Add(1)
	return r.meta, nil
}

type failingMetaResolver struct {
	err   error
	calls atomic.Int32
}

func (r *failingMetaResolver) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	r.calls.Add(1)
	return ch.Meta{}, r.err
}

type serviceCaptureTransport struct {
	mu       sync.Mutex
	pulls    int
	pullResp transport.PullResponse
}

func newServiceCaptureTransport() *serviceCaptureTransport {
	return &serviceCaptureTransport{}
}

func (t *serviceCaptureTransport) Pull(ctx context.Context, node ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	t.mu.Lock()
	t.pulls++
	resp := t.pullResp
	t.mu.Unlock()
	if resp.ChannelKey == "" {
		resp = transport.PullResponse{ChannelKey: req.ChannelKey, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, LeaderHW: req.NextOffset - 1, LeaderLEO: req.NextOffset - 1}
	}
	return resp, nil
}

func (t *serviceCaptureTransport) Ack(ctx context.Context, node ch.NodeID, req transport.AckRequest) error {
	return nil
}

func (t *serviceCaptureTransport) PullHint(ctx context.Context, node ch.NodeID, req transport.PullHintRequest) error {
	return nil
}

func (t *serviceCaptureTransport) Notify(ctx context.Context, node ch.NodeID, req transport.NotifyRequest) error {
	return nil
}

func (t *serviceCaptureTransport) SetPullResponse(resp transport.PullResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullResp = resp
}

func (t *serviceCaptureTransport) PullCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pulls
}

func awaitServiceTick(t *testing.T, cluster *cluster, key ch.ChannelKey, now time.Time) bool {
	t.Helper()
	future, err := cluster.group.Submit(context.Background(), key, reactor.Event{Kind: reactor.EventTick, Key: key, TickNow: now})
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = future.Await(ctx)
	return err == nil
}

type serviceCountingStoreFactory struct {
	base        *store.MemoryFactory
	mu          sync.Mutex
	calls       map[ch.ChannelKey]int
	blockLoadOn ch.ChannelKey
	loadStarted chan struct{}
	unblock     chan struct{}
}

func newServiceCountingStoreFactory() *serviceCountingStoreFactory {
	return &serviceCountingStoreFactory{base: store.NewMemoryFactory(), calls: make(map[ch.ChannelKey]int)}
}

func (f *serviceCountingStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &serviceCountingStore{factory: f, key: key, base: base}, nil
}

func (f *serviceCountingStoreFactory) appendCalls(key ch.ChannelKey) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *serviceCountingStoreFactory) blockLoad(key ch.ChannelKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockLoadOn = key
	f.loadStarted = make(chan struct{}, 1)
	f.unblock = make(chan struct{})
}

func (f *serviceCountingStoreFactory) waitLoadStarted(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	started := f.loadStarted
	f.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for load to block")
	}
}

func (f *serviceCountingStoreFactory) unblockLoad() {
	f.mu.Lock()
	unblock := f.unblock
	f.unblock = nil
	f.blockLoadOn = ""
	f.mu.Unlock()
	if unblock != nil {
		close(unblock)
	}
}

type serviceCountingStore struct {
	factory *serviceCountingStoreFactory
	key     ch.ChannelKey
	base    store.ChannelStore
}

func (s *serviceCountingStore) Load(ctx context.Context) (store.InitialState, error) {
	s.factory.mu.Lock()
	blocked := s.factory.blockLoadOn == s.key
	started := s.factory.loadStarted
	unblock := s.factory.unblock
	s.factory.mu.Unlock()
	if blocked {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-unblock:
		case <-ctx.Done():
			return store.InitialState{}, ctx.Err()
		}
	}
	return s.base.Load(ctx)
}

func (s *serviceCountingStore) AppendLeader(ctx context.Context, req store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	s.factory.mu.Lock()
	s.factory.calls[s.key]++
	s.factory.mu.Unlock()
	return s.base.AppendLeader(ctx, req)
}

func (s *serviceCountingStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	return s.base.ApplyFollower(ctx, req)
}

func (s *serviceCountingStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return s.base.ReadCommitted(ctx, req)
}

func (s *serviceCountingStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	return s.base.ReadLog(ctx, req)
}

func (s *serviceCountingStore) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	return s.base.StoreCheckpoint(ctx, checkpoint)
}

func (s *serviceCountingStore) Close() error {
	return s.base.Close()
}

type serviceBlockingReadLogFactory struct {
	base    *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
}

func newServiceBlockingReadLogFactory() *serviceBlockingReadLogFactory {
	return &serviceBlockingReadLogFactory{base: store.NewMemoryFactory(), started: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *serviceBlockingReadLogFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &serviceBlockingReadLogStore{ChannelStore: base, parent: f}, nil
}

func (f *serviceBlockingReadLogFactory) waitReadLogStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReadLog to start")
	}
}

func (f *serviceBlockingReadLogFactory) UnblockReadLogs() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type serviceBlockingReadLogStore struct {
	store.ChannelStore
	parent *serviceBlockingReadLogFactory
}

func (s *serviceBlockingReadLogStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	select {
	case s.parent.started <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ReadLogResult{}, ctx.Err()
	}
	return s.ChannelStore.ReadLog(ctx, req)
}
