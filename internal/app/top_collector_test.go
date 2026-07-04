package app

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	gatewaypkg "github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	"github.com/shirou/gopsutil/v4/process"
)

func TestTopCollectorSnapshotDoesNotRequireMetrics(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          2,
		NodeName:        "node-2",
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{
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

func TestTopCollectorSnapshotIncludesProcessResources(t *testing.T) {
	samples := []topResourceSample{
		{
			CPUPercent:     12.5,
			MemoryRSSBytes: 100 << 20,
			MemoryVMSBytes: 200 << 20,
			Goroutines:     10,
			Threads:        7,
		},
		{
			CPUPercent:     25,
			MemoryRSSBytes: 128 << 20,
			MemoryVMSBytes: 256 << 20,
			Goroutines:     12,
			Threads:        8,
		},
	}
	var sampleIndex int
	collector := newTopCollector(topCollectorOptions{
		ResourceSampler: func() topResourceSample {
			sample := samples[sampleIndex]
			sampleIndex++
			return sample
		},
	})

	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewOverview,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Resources == nil {
		t.Fatalf("Resources = nil")
	}
	if math.Abs(snapshot.Resources.CPUPercent-25) > 0.000001 {
		t.Fatalf("CPUPercent = %v, want 25", snapshot.Resources.CPUPercent)
	}
	if snapshot.Resources.MemoryRSSBytes != 128<<20 || snapshot.Resources.MemoryVMSBytes != 256<<20 {
		t.Fatalf("memory resources = %#v, want rss 128MiB vms 256MiB", snapshot.Resources)
	}
	if snapshot.Resources.Goroutines != 12 || snapshot.Resources.Threads != 8 {
		t.Fatalf("scheduler resources = %#v, want goroutines 12 threads 8", snapshot.Resources)
	}
}

func TestTopCollectorRecordsResourceMetrics(t *testing.T) {
	reg := obsmetrics.New(9, "node-9")
	collector := newTopCollector(topCollectorOptions{
		ResourceMetrics: reg.NodeResource,
		ResourceSampler: func() topResourceSample {
			return topResourceSample{
				CPUPercent:     41.25,
				MemoryRSSBytes: 256 << 20,
				MemoryVMSBytes: 512 << 20,
				Goroutines:     64,
				Threads:        11,
			}
		},
	})

	collector.recordSampleAt(time.Unix(100, 0))

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	cpu := requireAppMetricFamily(t, families, "wukongim_node_cpu_percent")
	if got := cpu.GetMetric()[0].GetGauge().GetValue(); got != 41.25 {
		t.Fatalf("cpu metric = %v, want 41.25", got)
	}
	rss := requireAppMetricFamily(t, families, "wukongim_node_memory_rss_bytes")
	if got := rss.GetMetric()[0].GetGauge().GetValue(); got != 256<<20 {
		t.Fatalf("rss metric = %v, want %d", got, 256<<20)
	}
	goroutines := requireAppMetricFamily(t, families, "wukongim_node_goroutines")
	if got := goroutines.GetMetric()[0].GetGauge().GetValue(); got != 64 {
		t.Fatalf("goroutine metric = %v, want 64", got)
	}
}

func TestTopCollectorRecordsStoragePebbleMetrics(t *testing.T) {
	reg := obsmetrics.New(10, "node-10")
	collector := newTopCollector(topCollectorOptions{
		StorageMetrics: reg.Storage,
		StorageMetricsSnapshot: func() cluster.StorageMetricsSnapshot {
			return cluster.StorageMetricsSnapshot{Stores: []cluster.StorageStoreMetricsSnapshot{
				{
					Store: "channel_log",
					Engine: cluster.StorageEngineMetrics{
						DiskSpaceUsageBytes:          1024,
						ReadAmplification:            3,
						MemTableSizeBytes:            2048,
						WALBytesIn:                   300,
						CompactionEstimatedDebtBytes: 4096,
					},
				},
			}}
		},
		ResourceSampler: func() topResourceSample {
			return topResourceSample{}
		},
	})

	collector.recordSampleAt(time.Unix(100, 0))

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	usage := requireAppMetricFamily(t, families, "wukongim_storage_pebble_disk_usage_bytes")
	if got := findAppMetricByLabels(t, usage, map[string]string{"store": "channel_log"}).GetGauge().GetValue(); got != 1024 {
		t.Fatalf("storage pebble disk usage metric = %v, want 1024", got)
	}
	readAmp := requireAppMetricFamily(t, families, "wukongim_storage_pebble_read_amplification")
	if got := findAppMetricByLabels(t, readAmp, map[string]string{"store": "channel_log"}).GetGauge().GetValue(); got != 3 {
		t.Fatalf("storage pebble read amplification metric = %v, want 3", got)
	}
	debt := requireAppMetricFamily(t, families, "wukongim_storage_pebble_compaction_estimated_debt_bytes")
	if got := findAppMetricByLabels(t, debt, map[string]string{"store": "channel_log"}).GetGauge().GetValue(); got != 4096 {
		t.Fatalf("storage pebble compaction debt metric = %v, want 4096", got)
	}
}

func TestTopCollectorInstallsStableDefaultResourceSamplerForCPUPercent(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{})

	if collector.options.ResourceSampler == nil {
		t.Fatalf("ResourceSampler = nil, want stable default sampler for gopsutil Percent(0)")
	}
}

func TestTopCollectorKeepsResolvedReadinessAlertVisible(t *testing.T) {
	snapshots := []cluster.Snapshot{
		{NodeID: 1, RoutesReady: true, SlotsReady: false, ChannelsReady: true},
		{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true},
		{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true},
	}
	var snapshotIndex int
	collector := newTopCollector(topCollectorOptions{
		NodeID: 1,
		ClusterSnapshot: func() cluster.Snapshot {
			if snapshotIndex >= len(snapshots) {
				return snapshots[len(snapshots)-1]
			}
			snapshot := snapshots[snapshotIndex]
			snapshotIndex++
			return snapshot
		},
	})

	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))
	collector.recordSampleAt(time.Unix(120, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewOverview,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Node.Ready != true {
		t.Fatalf("Node.Ready = false, want recovered ready node")
	}
	if snapshot.Alerts == nil || len(snapshot.Alerts.Recent) == 0 {
		t.Fatalf("Alerts = %#v, want resolved readiness alert retained", snapshot.Alerts)
	}
	alert := snapshot.Alerts.Recent[0]
	if alert.Active {
		t.Fatalf("alert.Active = true, want resolved alert")
	}
	if alert.Severity != "critical" || alert.Component != "cluster" || alert.Kind != "ready_part_down" {
		t.Fatalf("alert identity = %#v, want critical cluster ready_part_down", alert)
	}
	if alert.Count != 1 {
		t.Fatalf("alert.Count = %d, want 1", alert.Count)
	}
	if alert.FirstSeen != time.Unix(100, 0).UTC() || alert.LastSeen != time.Unix(100, 0).UTC() {
		t.Fatalf("alert seen times = %s/%s, want 100/100", alert.FirstSeen, alert.LastSeen)
	}
	if alert.ResolvedAt == nil || !alert.ResolvedAt.Equal(time.Unix(110, 0).UTC()) {
		t.Fatalf("alert.ResolvedAt = %#v, want 110", alert.ResolvedAt)
	}
}

func TestTopCollectorPressureAlertIncludesEvidence(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.SetQueue("channelv2", "append", "worker", "none", 91, 100)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewOverview,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Alerts == nil || len(snapshot.Alerts.Active) == 0 {
		t.Fatalf("Alerts = %#v, want active pressure alert", snapshot.Alerts)
	}
	alert := snapshot.Alerts.Active[0]
	for key, want := range map[string]string{
		"component":          "channelv2",
		"pool":               "append",
		"queue":              "worker",
		"level":              "degraded",
		"score":              "0.91",
		"depth":              "91",
		"capacity":           "100",
		"threshold.busy":     "0.60",
		"threshold.degraded": "0.80",
		"threshold.critical": "0.95",
	} {
		if got := alert.Evidence[key]; got != want {
			t.Fatalf("Evidence[%q] = %q, want %q; alert=%#v", key, got, want, alert)
		}
	}
}

func TestTopCollectorGatewaySessionErrorAlertIncludesEvidence(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := topGatewayObserver{top: collector}

	collector.recordSampleAt(time.Unix(100, 0))
	observer.OnSessionError(gatewaypkg.SessionErrorEvent{
		ConnectionEvent: gatewaypkg.ConnectionEvent{
			Protocol:    "wkproto",
			CloseReason: gatewaypkg.CloseReasonProtocolError,
		},
		Class: "protocol_error",
	})
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewOverview,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Alerts == nil || len(snapshot.Alerts.Active) == 0 {
		t.Fatalf("Alerts = %#v, want active gateway session error alert", snapshot.Alerts)
	}
	alert := snapshot.Alerts.Active[0]
	if alert.Severity != "error" || alert.Component != "gateway" || alert.Kind != "session_error" {
		t.Fatalf("alert = %#v, want gateway session_error", alert)
	}
	for key, want := range map[string]string{
		"class":        "protocol_error",
		"close_reason": "protocol_error",
		"protocol":     "wkproto",
		"count":        "1",
		"window":       "10s",
	} {
		if got := alert.Evidence[key]; got != want {
			t.Fatalf("Evidence[%q] = %q, want %q; alert=%#v", key, got, want, alert)
		}
	}
}

func TestMultiGatewayObserverForwardsSessionErrorsToOptionalObservers(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := multiGatewayObserver{
		gatewayMetricsObserver{},
		topGatewayObserver{top: collector},
	}

	collector.recordSampleAt(time.Unix(100, 0))
	observer.OnSessionError(gatewaypkg.SessionErrorEvent{
		ConnectionEvent: gatewaypkg.ConnectionEvent{
			Protocol:    "wkproto",
			CloseReason: gatewaypkg.CloseReasonProtocolError,
		},
		Class: "protocol_error",
	})
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: 10 * time.Second})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Alerts == nil || len(snapshot.Alerts.Active) != 1 {
		t.Fatalf("Alerts = %#v, want forwarded gateway session error alert", snapshot.Alerts)
	}
	alert := snapshot.Alerts.Active[0]
	if alert.Component != "gateway" || alert.Kind != "session_error" {
		t.Fatalf("alert = %#v, want gateway session_error", alert)
	}
}

func TestTopGopsutilResourceSamplerMapsProcessStats(t *testing.T) {
	sampler := topGopsutilResourceSampler{
		process: fakeTopProcessStats{
			cpuPercent: 17.25,
			memoryInfo: &process.MemoryInfoStat{
				RSS: 64 << 20,
				VMS: 512 << 20,
			},
			threads: 11,
		},
		goroutines: func() int { return 21 },
	}

	sample := sampler.sample()

	if sample.CPUPercent != 17.25 {
		t.Fatalf("CPUPercent = %v, want 17.25", sample.CPUPercent)
	}
	if sample.MemoryRSSBytes != 64<<20 || sample.MemoryVMSBytes != 512<<20 {
		t.Fatalf("memory sample = %#v, want RSS 64MiB VMS 512MiB", sample)
	}
	if sample.Goroutines != 21 || sample.Threads != 11 {
		t.Fatalf("execution sample = %#v, want goroutines 21 threads 11", sample)
	}
}

type fakeTopProcessStats struct {
	cpuPercent float64
	memoryInfo *process.MemoryInfoStat
	threads    int32
}

func (f fakeTopProcessStats) Percent(time.Duration) (float64, error) {
	return f.cpuPercent, nil
}

func (f fakeTopProcessStats) MemoryInfo() (*process.MemoryInfoStat, error) {
	return f.memoryInfo, nil
}

func (f fakeTopProcessStats) NumThreads() (int32, error) {
	return f.threads, nil
}

func TestTopCollectorTrafficUsesTotalGatewaySendCounter(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{})

	collector.recordSampleAt(time.Unix(100, 0))
	collector.ObserveGatewaySend("tcp", 128)
	collector.ObserveGatewaySend("ws", 256)
	collector.ObserveDeliveryRoutes(6)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewOverview,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Traffic == nil {
		t.Fatalf("Traffic = nil")
	}
	if math.Abs(snapshot.Traffic.SendPerSec-0.2) > 0.000001 {
		t.Fatalf("SendPerSec = %v, want 0.2", snapshot.Traffic.SendPerSec)
	}
	if math.Abs(snapshot.Traffic.FanoutRate-3) > 0.000001 {
		t.Fatalf("FanoutRate = %v, want 3", snapshot.Traffic.FanoutRate)
	}
}

func TestTopCollectorClientsUseGatewayCounters(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{})
	observer := topGatewayObserver{top: collector}

	collector.recordSampleAt(time.Unix(100, 0))
	observer.OnConnectionOpen(gatewaypkg.ConnectionEvent{Protocol: "tcp"})
	observer.OnConnectionOpen(gatewaypkg.ConnectionEvent{Protocol: "tcp"})
	observer.OnConnectionOpen(gatewaypkg.ConnectionEvent{Protocol: "ws"})
	observer.OnConnectionClose(gatewaypkg.ConnectionEvent{Protocol: "tcp"})
	observer.OnAuth(gatewaypkg.AuthEvent{ConnectionEvent: gatewaypkg.ConnectionEvent{Protocol: "tcp"}, Status: "error"})
	observer.OnAuth(gatewaypkg.AuthEvent{ConnectionEvent: gatewaypkg.ConnectionEvent{Protocol: "ws"}, Status: "error"})
	collector.recordSampleAt(time.Unix(110, 0))

	for _, view := range []accessapi.TopView{accessapi.TopViewOverview, accessapi.TopViewAll} {
		snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
			Window: 10 * time.Second,
			View:   view,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("SnapshotTop(%s) error = %v", view, err)
		}
		if snapshot.Clients == nil {
			t.Fatalf("Clients for view %s = nil", view)
		}
		if snapshot.Clients.Connections != 2 {
			t.Fatalf("Connections for view %s = %d, want 2", view, snapshot.Clients.Connections)
		}
		if snapshot.Clients.ConnectionsByProtocol["tcp"] != 1 || snapshot.Clients.ConnectionsByProtocol["ws"] != 1 {
			t.Fatalf("ConnectionsByProtocol for view %s = %#v, want tcp=1 ws=1", view, snapshot.Clients.ConnectionsByProtocol)
		}
		if math.Abs(snapshot.Clients.AuthFailPerSec-0.2) > 0.000001 {
			t.Fatalf("AuthFailPerSec for view %s = %v, want 0.2", view, snapshot.Clients.AuthFailPerSec)
		}
		if math.Abs(snapshot.Clients.ClosePerSec-0.1) > 0.000001 {
			t.Fatalf("ClosePerSec for view %s = %v, want 0.1", view, snapshot.Clients.ClosePerSec)
		}
	}
}

func TestTopCollectorAllViewIncludesRuntimeSections(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID: 1,
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := topChannelObserver{top: collector}
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
	if snapshot.ChannelRuntime == nil {
		t.Fatal("channel runtime section is nil")
	}
	if snapshot.ChannelRuntime.ActiveLeader != 2 || snapshot.ChannelRuntime.ActiveFollower != 3 || snapshot.ChannelRuntime.ActiveTotal != 5 {
		t.Fatalf("channel runtime active counts = %#v, want leader 2 follower 3 total 5", snapshot.ChannelRuntime)
	}
	if snapshot.ChannelRuntime.FollowerParked != 1 || snapshot.ChannelRuntime.ReactorMailboxDepthMax != 7 || snapshot.ChannelRuntime.ReactorMailboxCapacityMax != 10 {
		t.Fatalf("channel runtime pressure gauges = %#v, want parked 1 mailbox 7 capacity 10", snapshot.ChannelRuntime)
	}
	if snapshot.ChannelRuntime.WorkerQueueDepthByPool["store_append"] != 4 ||
		snapshot.ChannelRuntime.WorkerQueueCapacityByPool["store_append"] != 8 ||
		snapshot.ChannelRuntime.WorkerInflightByPool["store_append"] != 3 ||
		snapshot.ChannelRuntime.WorkerCapacityByPool["store_append"] != 6 {
		t.Fatalf("channel runtime worker maps = queue:%#v queueCap:%#v inflight:%#v workerCap:%#v",
			snapshot.ChannelRuntime.WorkerQueueDepthByPool,
			snapshot.ChannelRuntime.WorkerQueueCapacityByPool,
			snapshot.ChannelRuntime.WorkerInflightByPool,
			snapshot.ChannelRuntime.WorkerCapacityByPool,
		)
	}
	if snapshot.ChannelRuntime.AppendP99MS != 12 || snapshot.ChannelRuntime.HotStage != "store_append" || snapshot.ChannelRuntime.StageP99MS["store_append"] != 15 {
		t.Fatalf("channel runtime latency section = %#v", snapshot.ChannelRuntime)
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
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.SetChannelWorkerQueue("store_append", 1, 2)
	collector.SetStorageCommitQueue(1, 2)
	collector.SetDeliveryRetryQueueDepth(1)
	collector.recordSampleAt(time.Unix(110, 0))

	channelSnapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: 10 * time.Second, View: accessapi.TopViewChannel})
	if err != nil {
		t.Fatalf("channel SnapshotTop() error = %v", err)
	}
	if channelSnapshot.ChannelRuntime == nil || channelSnapshot.Storage != nil || channelSnapshot.Delivery != nil {
		t.Fatalf("channel view sections = channel:%#v storage:%#v delivery:%#v", channelSnapshot.ChannelRuntime, channelSnapshot.Storage, channelSnapshot.Delivery)
	}

	storageSnapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: 10 * time.Second, View: accessapi.TopViewStorage})
	if err != nil {
		t.Fatalf("storage SnapshotTop() error = %v", err)
	}
	if storageSnapshot.Storage == nil || storageSnapshot.ChannelRuntime != nil || storageSnapshot.Delivery != nil {
		t.Fatalf("storage view sections = channel:%#v storage:%#v delivery:%#v", storageSnapshot.ChannelRuntime, storageSnapshot.Storage, storageSnapshot.Delivery)
	}

	deliverySnapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: 10 * time.Second, View: accessapi.TopViewDelivery})
	if err != nil {
		t.Fatalf("delivery SnapshotTop() error = %v", err)
	}
	if deliverySnapshot.Delivery == nil || deliverySnapshot.ChannelRuntime != nil || deliverySnapshot.Storage != nil {
		t.Fatalf("delivery view sections = channel:%#v storage:%#v delivery:%#v", deliverySnapshot.ChannelRuntime, deliverySnapshot.Storage, deliverySnapshot.Delivery)
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
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{
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

func TestTopCollectorPressureIncludesWaitTaskAndAdmissionErrors(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          1,
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})

	collector.SetQueue("gateway", "async_send", "send", "none", 6, 10)
	collector.observeDurationMS("pressure.gateway.async_send.send.none.wait", 3*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.send.none.wait", 9*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.dispatch.ok.task", 5*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.dispatch.ok.task", 15*time.Millisecond)
	collector.addCounter("pressure.gateway.async_send.send.none.admission_error", 0)
	collector.recordSampleAt(time.Unix(100, 0))

	collector.SetQueue("gateway", "async_send", "send", "none", 8, 10)
	collector.observeDurationMS("pressure.gateway.async_send.send.none.wait", 10*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.dispatch.ok.task", 20*time.Millisecond)
	collector.addCounter("pressure.gateway.async_send.send.none.admission_error", 4)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewRuntime,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) != 1 {
		t.Fatalf("Pressure.Top = %#v, want one item", snapshot.Pressure)
	}
	item := snapshot.Pressure.Top[0]
	if item.Component != "gateway" || item.Pool != "async_send" || item.Queue != "send" || item.Priority != "none" {
		t.Fatalf("pressure item identity = %#v, want gateway async_send send none", item)
	}
	if math.Abs(item.Score-0.8) > 0.000001 || item.Level != "degraded" {
		t.Fatalf("score/level = %.3f/%s, want 0.8/degraded", item.Score, item.Level)
	}
	if item.WaitP99MS <= 0 {
		t.Fatalf("WaitP99MS = %v, want populated", item.WaitP99MS)
	}
	if item.TaskP99MS <= 0 {
		t.Fatalf("TaskP99MS = %v, want populated", item.TaskP99MS)
	}
	if math.Abs(item.AdmissionErrorPerSec-0.4) > 0.000001 {
		t.Fatalf("AdmissionErrorPerSec = %v, want 0.4", item.AdmissionErrorPerSec)
	}
}

func TestTopSlotObserverDoesNotCountSchedulerCoalescingAsAdmissionErrors(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          1,
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := topSlotObserver{top: collector}

	observer.SetSchedulerState(multiraft.SchedulerStateEvent{Depth: 0, Capacity: 1024})
	collector.recordSampleAt(time.Unix(100, 0))

	observer.ObserveSchedulerAdmission("coalesced")
	observer.ObserveSchedulerAdmission("dirty")
	observer.ObserveSchedulerAdmission("requeued")
	observer.SetSchedulerState(multiraft.SchedulerStateEvent{Depth: 0, Capacity: 1024})
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewRuntime,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) != 1 {
		t.Fatalf("Pressure.Top = %#v, want slot scheduler item", snapshot.Pressure)
	}
	item := snapshot.Pressure.Top[0]
	if item.Component != "slot" || item.Pool != "scheduler" || item.Queue != "scheduler" {
		t.Fatalf("pressure item identity = %#v, want slot scheduler scheduler", item)
	}
	if item.AdmissionErrorPerSec != 0 {
		t.Fatalf("AdmissionErrorPerSec = %v, want 0 for scheduler coalescing results", item.AdmissionErrorPerSec)
	}
}

func TestTopCollectorPressureUsesInflightWhenHigherThanQueueDepth(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          1,
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})

	collector.SetQueue("channelv2", "store_append", "worker", "none", 1, 10)
	collector.SetInflight("channelv2", "store_append", 9, 10)
	collector.recordSampleAt(time.Unix(100, 0))
	collector.SetQueue("channelv2", "store_append", "worker", "none", 1, 10)
	collector.SetInflight("channelv2", "store_append", 9, 10)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewRuntime,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	item := snapshot.Pressure.Top[0]
	if math.Abs(item.Score-0.9) > 0.000001 || item.Level != "degraded" {
		t.Fatalf("score/level = %.3f/%s, want inflight score 0.9 degraded", item.Score, item.Level)
	}
	if item.Inflight != 9 || item.Workers != 10 {
		t.Fatalf("inflight/workers = %d/%d, want 9/10", item.Inflight, item.Workers)
	}
}

func TestTopTransportV2ObserverUsesServiceAliasForInflightPool(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          1,
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := topTransportV2Observer{top: collector}

	observer.ObserveTransport(transportv2.Event{
		Name:         "service_inflight",
		ServiceID:    1,
		ServiceAlias: "slot propose",
		Inflight:     128,
		Capacity:     128,
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewRuntime,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) != 1 {
		t.Fatalf("Pressure.Top = %#v, want transport service item", snapshot.Pressure)
	}
	item := snapshot.Pressure.Top[0]
	if item.Component != "transportv2" || item.Pool != "slot propose" || item.Queue != "inflight" {
		t.Fatalf("pressure item identity = %#v, want transportv2 slot propose inflight", item)
	}
}

func TestTopCollectorUnknownInflightCapacityDoesNotCreateCriticalPressure(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.SetInflight("transportv2", "rpc", 3, 0)
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
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) == 0 {
		t.Fatalf("Pressure = %#v, want transportv2 item", snapshot.Pressure)
	}
	if snapshot.Pressure.OverallLevel != "ok" {
		t.Fatalf("Pressure.OverallLevel = %q, want ok", snapshot.Pressure.OverallLevel)
	}
	if snapshot.Verdict.Level != "ok" {
		t.Fatalf("Verdict.Level = %q, want ok", snapshot.Verdict.Level)
	}
	item := snapshot.Pressure.Top[0]
	if item.Component != "transportv2" || item.Pool != "rpc" || item.Inflight != 3 || item.Workers != 0 {
		t.Fatalf("top pressure item = %#v, want transportv2 rpc inflight=3 workers=0", item)
	}
	if item.Score != 0 {
		t.Fatalf("top pressure score = %v, want 0 for unknown worker capacity", item.Score)
	}
}

func TestTopCollectorVerdictStrings(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
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

	notReady := buildTopVerdict(cluster.Snapshot{RoutesReady: true, SlotsReady: true}, nil, nil)
	if notReady.Summary != "cluster runtime is not ready" {
		t.Fatalf("not-ready summary = %q, want cluster runtime is not ready", notReady.Summary)
	}
	if len(notReady.Reasons) != 1 || notReady.Reasons[0] != "channelv2 not ready" {
		t.Fatalf("not-ready reasons = %#v, want channelv2 not ready", notReady.Reasons)
	}
	sendack := buildTopVerdict(
		cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true},
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
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
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
		ClusterSnapshot: func() cluster.Snapshot {
			once.Do(func() { close(entered) })
			<-release
			return cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
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

func TestTopDeliveryObserverMapsAckEventToAckBindings(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := topDeliveryObserver{top: collector}

	collector.recordSampleAt(time.Unix(100, 0))
	observer.ObserveAck(runtimedelivery.AckEvent{PendingCount: 7})
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewDelivery,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Delivery == nil || snapshot.Delivery.AckBindings != 7 {
		t.Fatalf("delivery snapshot = %#v, want ack_bindings 7", snapshot.Delivery)
	}
}
