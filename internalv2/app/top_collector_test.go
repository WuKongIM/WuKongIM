package app

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func TestTopCollectorSnapshotDoesNotRequireMetrics(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          2,
		NodeName:        "node-2",
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{
				NodeID:        2,
				RoutesReady:   true,
				SlotsReady:    true,
				ChannelsReady: true,
			}
		},
		MetricsEnabled: false,
	})

	collector.recordSampleAt(time.Unix(100, 0))
	collector.ObserveGatewaySend("wkproto", 128)
	collector.ObserveGatewaySendack("success", "gateway", "send")
	collector.ObserveMessageAppend("channelv2", "ok", 10*time.Millisecond)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Sources.Metrics.Required {
		t.Fatalf("Metrics.Required = true, want false")
	}
	if snapshot.Node.ID != 2 || !snapshot.Node.Ready {
		t.Fatalf("Node = %#v, want id 2 ready", snapshot.Node)
	}
	if snapshot.Traffic == nil {
		t.Fatalf("Traffic = nil")
	}
	if math.Abs(snapshot.Traffic.SendPerSec-0.1) > 0.000001 {
		t.Fatalf("SendPerSec = %v, want 0.1", snapshot.Traffic.SendPerSec)
	}
	if math.Abs(snapshot.Traffic.AppendP50MS-10) > 0.000001 {
		t.Fatalf("AppendP50MS = %v, want 10", snapshot.Traffic.AppendP50MS)
	}
}

func TestTopCollectorAllViewIncludesRuntimeSections(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID: 1,
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := topChannelV2Observer{top: collector}
	storage := topStorageObserver{top: collector}
	delivery := topDeliveryObserver{top: collector}

	collector.recordSampleAt(time.Unix(100, 0))
	observer.SetChannelRuntimeCount(0, ch.RoleLeader, 2)
	observer.SetChannelRuntimeCount(1, ch.RoleFollower, 3)
	observer.SetFollowerParkedCount(1, 1)
	observer.SetReactorMailboxDepth(0, "normal", 7)
	observer.SetReactorMailboxCapacity(0, "normal", 10)
	observer.SetWorkerQueueDepth("store_append", 4)
	observer.SetWorkerQueueCapacity("store_append", 8)
	observer.SetWorkerInflight("store_append", 3)
	observer.SetWorkerWorkers("store_append", 6)
	observer.ObserveAppendLatency(ch.CommitModeQuorum, 12*time.Millisecond)
	observer.ObserveChannelAppendStage("store_append", "ok", 15*time.Millisecond)
	observer.ObserveWorkerResult(worker.TaskStoreAppend, nil, 9*time.Millisecond)

	storage.SetCommitCoordinatorQueue(5, 10)
	storage.ObserveCommitCoordinatorRequest(messagedb.CommitCoordinatorRequestEvent{
		Lane:     "message_append",
		Result:   "ok",
		Duration: 6 * time.Millisecond,
	})
	storage.ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent{
		Records:        4,
		CommitDuration: 11 * time.Millisecond,
		TotalDuration:  13 * time.Millisecond,
	})

	delivery.ObserveFanoutResolve(runtimedelivery.FanoutResolveEvent{
		Result:   runtimedelivery.DeliveryResultOK,
		Duration: 3 * time.Millisecond,
		Routes:   9,
	})
	delivery.ObserveFanoutPush(runtimedelivery.FanoutPushEvent{
		Result:   runtimedelivery.DeliveryResultOK,
		Duration: 8 * time.Millisecond,
		Routes:   9,
		Accepted: 7,
	})
	delivery.ObserveRetry(runtimedelivery.RetryEvent{QueueDepth: 2})
	delivery.ObserveManagerTerminal(runtimedelivery.ManagerTerminalEvent{Result: runtimedelivery.DeliveryResultOK, QueueDepth: 1})
	collector.SetDeliveryAckBindings(12)
	collector.SetDeliveryRecipientQueue(3, 16)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.ChannelV2 == nil {
		t.Fatal("ChannelV2 section is nil")
	}
	if snapshot.ChannelV2.ActiveLeader != 2 || snapshot.ChannelV2.ActiveFollower != 3 || snapshot.ChannelV2.ActiveTotal != 5 {
		t.Fatalf("ChannelV2 active counts = %#v, want leader 2 follower 3 total 5", snapshot.ChannelV2)
	}
	if snapshot.ChannelV2.FollowerParked != 1 || snapshot.ChannelV2.ReactorMailboxDepthMax != 7 || snapshot.ChannelV2.ReactorMailboxCapacityMax != 10 {
		t.Fatalf("ChannelV2 pressure gauges = %#v, want parked 1 mailbox 7 capacity 10", snapshot.ChannelV2)
	}
	if snapshot.ChannelV2.WorkerQueueDepthByPool["store_append"] != 4 ||
		snapshot.ChannelV2.WorkerQueueCapacityByPool["store_append"] != 8 ||
		snapshot.ChannelV2.WorkerInflightByPool["store_append"] != 3 ||
		snapshot.ChannelV2.WorkerCapacityByPool["store_append"] != 6 {
		t.Fatalf("ChannelV2 worker maps = queue:%#v queueCap:%#v inflight:%#v workerCap:%#v",
			snapshot.ChannelV2.WorkerQueueDepthByPool,
			snapshot.ChannelV2.WorkerQueueCapacityByPool,
			snapshot.ChannelV2.WorkerInflightByPool,
			snapshot.ChannelV2.WorkerCapacityByPool,
		)
	}
	if snapshot.ChannelV2.AppendP99MS != 12 || snapshot.ChannelV2.HotStage != "store_append" || snapshot.ChannelV2.StageP99MS["store_append"] != 15 {
		t.Fatalf("ChannelV2 latency section = %#v", snapshot.ChannelV2)
	}

	if snapshot.Storage == nil || len(snapshot.Storage.CommitQueues) != 1 {
		t.Fatalf("Storage = %#v, want one commit queue", snapshot.Storage)
	}
	queue := snapshot.Storage.CommitQueues[0]
	if queue.Store != "message" || queue.Depth != 5 || queue.Capacity != 10 || queue.RequestP99MSByLane["message_append/ok"] != 6 || queue.BatchRecordsP50 != 4 || queue.BatchCommitP99MS != 11 {
		t.Fatalf("storage queue = %#v", queue)
	}

	if snapshot.Delivery == nil {
		t.Fatal("Delivery section is nil")
	}
	if snapshot.Delivery.RetryQueueDepth != 2 || snapshot.Delivery.AckBindings != 12 || snapshot.Delivery.RecipientQueueDepth != 3 || snapshot.Delivery.RecipientQueueCapacity != 16 {
		t.Fatalf("Delivery gauges = %#v", snapshot.Delivery)
	}
	if math.Abs(snapshot.Delivery.RoutesPerSec-0.9) > 0.000001 || math.Abs(snapshot.Delivery.PushPerSec-0.7) > 0.000001 || snapshot.Delivery.PushP99MS != 8 {
		t.Fatalf("Delivery rates/latency = %#v", snapshot.Delivery)
	}
}

func TestTopCollectorSpecificViewsIncludeOnlyRequestedRuntimeSection(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.SetChannelV2WorkerQueue("store_append", 1, 2)
	collector.SetStorageCommitQueue(1, 2)
	collector.SetDeliveryRetryQueueDepth(1)
	collector.recordSampleAt(time.Unix(110, 0))

	channelSnapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: 10 * time.Second, View: accessapi.TopViewChannel})
	if err != nil {
		t.Fatalf("channel SnapshotTop() error = %v", err)
	}
	if channelSnapshot.ChannelV2 == nil || channelSnapshot.Storage != nil || channelSnapshot.Delivery != nil {
		t.Fatalf("channel view sections = channel:%#v storage:%#v delivery:%#v", channelSnapshot.ChannelV2, channelSnapshot.Storage, channelSnapshot.Delivery)
	}

	storageSnapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: 10 * time.Second, View: accessapi.TopViewStorage})
	if err != nil {
		t.Fatalf("storage SnapshotTop() error = %v", err)
	}
	if storageSnapshot.Storage == nil || storageSnapshot.ChannelV2 != nil || storageSnapshot.Delivery != nil {
		t.Fatalf("storage view sections = channel:%#v storage:%#v delivery:%#v", storageSnapshot.ChannelV2, storageSnapshot.Storage, storageSnapshot.Delivery)
	}

	deliverySnapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: 10 * time.Second, View: accessapi.TopViewDelivery})
	if err != nil {
		t.Fatalf("delivery SnapshotTop() error = %v", err)
	}
	if deliverySnapshot.Delivery == nil || deliverySnapshot.ChannelV2 != nil || deliverySnapshot.Storage != nil {
		t.Fatalf("delivery view sections = channel:%#v storage:%#v delivery:%#v", deliverySnapshot.ChannelV2, deliverySnapshot.Storage, deliverySnapshot.Delivery)
	}
}

func TestTopCollectorWarmingUp(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{})

	_, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  10,
	})
	if !errors.Is(err, accessapi.ErrTopWarmingUp) {
		t.Fatalf("SnapshotTop() error = %v, want %v", err, accessapi.ErrTopWarmingUp)
	}
}

func TestTopCollectorPressureVerdict(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID: 2,
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{
				NodeID:        2,
				RoutesReady:   true,
				SlotsReady:    true,
				ChannelsReady: true,
			}
		},
	})
	collector.SetQueue("channelv2", "store_append", "write", "none", 86, 100)
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Verdict.Level != "degraded" {
		t.Fatalf("Verdict.Level = %q, want degraded", snapshot.Verdict.Level)
	}
	if snapshot.Verdict.Summary != "runtime pressure detected" {
		t.Fatalf("Verdict.Summary = %q, want runtime pressure detected", snapshot.Verdict.Summary)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) == 0 {
		t.Fatalf("Pressure = %#v, want top pressure item", snapshot.Pressure)
	}
	if snapshot.Pressure.Top[0].Component != "channelv2" {
		t.Fatalf("top pressure component = %q, want channelv2", snapshot.Pressure.Top[0].Component)
	}
	if len(snapshot.Verdict.Reasons) == 0 || snapshot.Verdict.Reasons[0] != "channelv2/store_append pressure" {
		t.Fatalf("Verdict.Reasons = %#v, want channelv2/store_append pressure", snapshot.Verdict.Reasons)
	}
}

func TestTopCollectorPressureSortsPriorityDeterministically(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{})
	collector.SetQueue("transportv2", "scheduler", "scheduler", "rpc", 8, 10)
	collector.SetQueue("transportv2", "scheduler", "scheduler", "bulk", 8, 10)
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) < 2 {
		t.Fatalf("Pressure = %#v, want two tied pressure items", snapshot.Pressure)
	}
	if snapshot.Pressure.Top[0].Priority != "bulk" || snapshot.Pressure.Top[1].Priority != "rpc" {
		t.Fatalf("pressure priorities = %q, %q; want bulk, rpc", snapshot.Pressure.Top[0].Priority, snapshot.Pressure.Top[1].Priority)
	}
}

func TestTopCollectorVerdictStrings(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewOverview,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Verdict.Summary != "runtime healthy" {
		t.Fatalf("ok summary = %q, want runtime healthy", snapshot.Verdict.Summary)
	}

	notReady := buildTopVerdict(clusterv2.Snapshot{RoutesReady: true, SlotsReady: true}, nil, nil)
	if notReady.Summary != "cluster runtime is not ready" {
		t.Fatalf("not-ready summary = %q, want cluster runtime is not ready", notReady.Summary)
	}
	if len(notReady.Reasons) != 1 || notReady.Reasons[0] != "channelv2 not ready" {
		t.Fatalf("not-ready reasons = %#v, want channelv2 not ready", notReady.Reasons)
	}
	sendack := buildTopVerdict(
		clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true},
		&accessapi.TopTraffic{SendackErrorRate: 0.05},
		nil,
	)
	if len(sendack.Reasons) != 1 || sendack.Reasons[0] != "sendack error rate >= 5%" {
		t.Fatalf("sendack reasons = %#v, want sendack error rate >= 5%%", sendack.Reasons)
	}
	if sendack.Summary != "sendack error rate is high" {
		t.Fatalf("sendack summary = %q, want sendack error rate is high", sendack.Summary)
	}
}

func TestTopCollectorEmptyViewDoesNotIncludeOptionalSections(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Traffic != nil || snapshot.Pressure != nil {
		t.Fatalf("Traffic/Pressure = %#v/%#v, want nil for empty view", snapshot.Traffic, snapshot.Pressure)
	}
}

func TestTopCollectorRingWindowIteratesOldestToNewest(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		CollectInterval: time.Second,
		HistoryWindow:   2 * time.Second,
	})
	for i := 0; i < 5; i++ {
		collector.recordSampleAt(time.Unix(int64(100+i), 0))
	}

	window := collector.windowLocked(10 * time.Second)
	if collector.count != len(collector.ring) {
		t.Fatalf("count = %d, want ring capacity %d", collector.count, len(collector.ring))
	}
	if len(window) != len(collector.ring) {
		t.Fatalf("window len = %d, want %d", len(window), len(collector.ring))
	}
	for i, sample := range window {
		want := time.Unix(int64(101+i), 0).UTC()
		if !sample.at.Equal(want) {
			t.Fatalf("window[%d] = %s, want %s", i, sample.at, want)
		}
	}
}

func TestTopCollectorHistogramSamplesAreBoundedAndReset(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{})
	collector.recordSampleAt(time.Unix(100, 0))

	for i := 0; i < topMaxHistogramValuesPerSample+500; i++ {
		collector.observeDurationMS(topHistogramMessageAppend, 10*time.Millisecond)
	}
	if got := len(collector.histos[topHistogramMessageAppend]); got != topMaxHistogramValuesPerSample {
		t.Fatalf("current histogram len = %d, want cap %d", got, topMaxHistogramValuesPerSample)
	}

	collector.recordSampleAt(time.Unix(110, 0))
	if got := len(collector.histos[topHistogramMessageAppend]); got != 0 {
		t.Fatalf("current histogram len after sample = %d, want reset", got)
	}
	window := collector.windowLocked(10 * time.Second)
	if len(window) != 2 {
		t.Fatalf("window len = %d, want 2", len(window))
	}
	if got := len(window[1].histos[topHistogramMessageAppend]); got != topMaxHistogramValuesPerSample {
		t.Fatalf("sample histogram len = %d, want cap %d", got, topMaxHistogramValuesPerSample)
	}

	for i := 0; i < 20; i++ {
		collector.observeDurationMS(topHistogramMessageAppend, time.Duration(i)*time.Millisecond)
		collector.recordSampleAt(time.Unix(int64(111+i), 0))
	}
	if got := len(collector.histos[topHistogramMessageAppend]); got != 0 {
		t.Fatalf("current histogram len after many samples = %d, want reset", got)
	}
	for i, sample := range collector.windowLocked(time.Minute) {
		if got := len(sample.histos[topHistogramMessageAppend]); got > topMaxHistogramValuesPerSample {
			t.Fatalf("window[%d] histogram len = %d, want <= %d", i, got, topMaxHistogramValuesPerSample)
		}
	}

	for i := 0; i < topMaxHistogramValuesPerSample+500; i++ {
		collector.ObserveDeliveryPush(runtimedelivery.DeliveryResultOK, 1, 10*time.Millisecond)
	}
	if got := len(collector.histos[topHistogramDeliveryPush]); got != topMaxHistogramValuesPerSample {
		t.Fatalf("delivery histogram len = %d, want cap %d", got, topMaxHistogramValuesPerSample)
	}
	collector.recordSampleAt(time.Unix(140, 0))
	if got := len(collector.histos[topHistogramDeliveryPush]); got != 0 {
		t.Fatalf("delivery histogram len after sample = %d, want reset", got)
	}
}

func TestTopCollectorStopHonorsContextWhenSnapshotBlocks(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	collector := newTopCollector(topCollectorOptions{
		CollectInterval: time.Hour,
		ClusterSnapshot: func() clusterv2.Snapshot {
			once.Do(func() { close(entered) })
			<-release
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	if err := collector.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatalf("collector did not enter ClusterSnapshot")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- collector.Stop(stopCtx)
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			close(release)
			t.Fatalf("Stop() error = %v, want deadline exceeded", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(release)
		err := <-errCh
		t.Fatalf("Stop() did not honor context before unblock; returned %v after release", err)
	}

	close(release)
	if err := collector.Stop(context.Background()); err != nil {
		t.Fatalf("final Stop() error = %v", err)
	}
}
