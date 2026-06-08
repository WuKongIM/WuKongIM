package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	dto "github.com/prometheus/client_model/go"
)

func TestRuntimePressureAdapterMapsGatewayChannelSlotTransportAndDB(t *testing.T) {
	reg := obsmetrics.New(1, "n1")

	gatewayObserver := gatewayMetricsObserver{metrics: reg}
	gatewayObserver.OnAsyncAuthQueue(gateway.AsyncAuthQueueEvent{Depth: 1, Capacity: 8, Workers: 2})
	gatewayObserver.OnAsyncAuthAdmission(gateway.AsyncAuthAdmissionEvent{Result: "ok"})
	gatewayObserver.OnAsyncAuthWait(gateway.AsyncAuthWaitEvent{Duration: time.Millisecond})
	gatewayObserver.OnAsyncSendQueue(gateway.AsyncSendQueueEvent{Depth: 5, Capacity: 32})
	gatewayObserver.OnAsyncSendAdmission(gateway.AsyncSendAdmissionEvent{Result: "full"})
	gatewayObserver.OnAsyncSendDispatchWait(gateway.AsyncSendDispatchWaitEvent{Duration: time.Millisecond})
	gatewayObserver.OnTransportPressure(gateway.TransportPressureEvent{
		Name:          "gnet",
		Queue:         "write",
		Depth:         3,
		Capacity:      16,
		Bytes:         128,
		BytesCapacity: 4096,
		Result:        "ok",
	})

	channelObserver := channelV2MetricsObserver{metrics: reg}
	channelObserver.SetWorkerQueueDepth("store_append", 1)
	channelObserver.SetWorkerQueueCapacity("store_append", 64)
	channelObserver.SetWorkerWorkers("store_append", 4)
	channelObserver.SetWorkerInflight("store_append", 2)
	channelObserver.ObserveWorkerAdmission("store_append", "ok")
	channelObserver.ObserveWorkerWait("store_append", worker.TaskStoreAppend, time.Millisecond)
	channelObserver.ObserveWorkerTask("store_append", worker.TaskStoreAppend, nil, time.Millisecond)
	channelObserver.ObserveWorkerBatch("rpc", worker.TaskRPCPull, 3, nil)
	channelObserver.SetReactorMailboxDepth(0, "high", 2)
	channelObserver.SetReactorMailboxCapacity(0, "high", 16)
	channelObserver.ObserveReactorMailboxAdmission(0, "high", "full")
	channelObserver.SetReactorMailboxDepth(1, "high", 3)
	channelObserver.SetReactorMailboxCapacity(1, "high", 16)
	channelObserver.SetAppendQueuePressure(reactor.AppendQueuePressureEvent{
		ReactorID:     0,
		Depth:         2,
		Capacity:      32,
		Bytes:         100,
		BytesCapacity: 4096,
	})
	channelObserver.SetAppendQueuePressure(reactor.AppendQueuePressureEvent{
		ReactorID:     1,
		Depth:         4,
		Capacity:      32,
		Bytes:         200,
		BytesCapacity: 4096,
	})

	slotObserver := slotMetricsObserver{metrics: reg}
	slotObserver.SetSchedulerWorkers(1)
	slotObserver.SetSchedulerInflight(1)
	slotObserver.SetSchedulerState(multiraft.SchedulerStateEvent{Depth: 1, Capacity: 1024, Pending: 2, Queued: 3, Processing: 1, Dirty: 1})
	slotObserver.ObserveSchedulerAdmission("dirty")
	slotObserver.ObserveSchedulerTask("process_slot", time.Millisecond)

	transportObserver := transportV2MetricsObserver{metrics: reg}
	transportObserver.ObserveTransport(transportv2.Event{
		Name:          "scheduler_queue",
		Priority:      transportv2.PriorityRPC,
		Items:         3,
		Capacity:      32,
		Bytes:         128,
		BytesCapacity: 4096,
		Result:        "ok",
	})
	transportObserver.ObserveTransport(transportv2.Event{
		Name:      "service_task",
		ServiceID: 9,
		Result:    "ok",
		Duration:  time.Millisecond,
	})
	transportObserver.ObserveTransport(transportv2.Event{
		Name:      "service_inflight",
		ServiceID: 9,
		Inflight:  2,
		Capacity:  4,
	})

	storageObserver := storageCommitMetricsObserver{metrics: reg}
	storageObserver.SetCommitCoordinatorQueue(7, 1024)
	storageObserver.ObserveCommitCoordinatorRequest(messagedb.CommitCoordinatorRequestEvent{
		Lane:     "leader_append",
		Records:  2,
		Bytes:    128,
		Duration: time.Millisecond,
		Result:   "timeout",
	})
	storageObserver.ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent{
		Requests:      1,
		Records:       2,
		Bytes:         128,
		TotalDuration: time.Millisecond,
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, name := range []string{
		"wukongim_runtime_pool_queue_depth",
		"wukongim_runtime_pool_admission_total",
		"wukongim_runtime_pool_task_duration_seconds",
	} {
		family := requireAppMetricFamily(t, families, name)
		if len(family.GetMetric()) == 0 {
			t.Fatalf("metric family %q has no samples", name)
		}
	}
	queueDepth := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_depth")
	gatewayTransport := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "gateway",
		"pool":      "gnet",
		"queue":     "write",
		"priority":  "none",
	})
	if got := gatewayTransport.GetGauge().GetValue(); got != 3 {
		t.Fatalf("gateway transport depth = %v, want 3", got)
	}
	gatewayAsyncSend := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "gateway",
		"pool":      "async_send",
		"queue":     "send",
		"priority":  "none",
	})
	if got := gatewayAsyncSend.GetGauge().GetValue(); got != 5 {
		t.Fatalf("gateway async send depth = %v, want 5", got)
	}
	reactorZero := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "channelv2",
		"pool":      "reactor_0",
		"queue":     "mailbox",
		"priority":  "high",
	})
	if got := reactorZero.GetGauge().GetValue(); got != 2 {
		t.Fatalf("reactor_0 mailbox depth = %v, want 2", got)
	}
	reactorOne := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "channelv2",
		"pool":      "reactor_1",
		"queue":     "mailbox",
		"priority":  "high",
	})
	if got := reactorOne.GetGauge().GetValue(); got != 3 {
		t.Fatalf("reactor_1 mailbox depth = %v, want 3", got)
	}
	slotScheduler := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "slot",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "none",
	})
	if got := slotScheduler.GetGauge().GetValue(); got != 3 {
		t.Fatalf("slot scheduler depth = %v, want 3", got)
	}
	transportScheduler := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "transportv2",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := transportScheduler.GetGauge().GetValue(); got != 3 {
		t.Fatalf("transport scheduler depth = %v, want 3", got)
	}
	dbCommitQueue := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "db",
		"pool":      "message_commit",
		"queue":     "commit",
		"priority":  "none",
	})
	if got := dbCommitQueue.GetGauge().GetValue(); got != 7 {
		t.Fatalf("db message commit queue depth = %v, want 7", got)
	}

	queueCapacity := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_capacity")
	dbCommitQueueCapacity := findAppMetricByLabels(t, queueCapacity, map[string]string{
		"component": "db",
		"pool":      "message_commit",
		"queue":     "commit",
		"priority":  "none",
	})
	if got := dbCommitQueueCapacity.GetGauge().GetValue(); got != 1024 {
		t.Fatalf("db message commit queue capacity = %v, want 1024", got)
	}

	admissions := requireAppMetricFamily(t, families, "wukongim_runtime_pool_admission_total")
	findAppMetricByLabels(t, admissions, map[string]string{
		"component": "channelv2",
		"pool":      "reactor_0",
		"queue":     "mailbox",
		"priority":  "high",
		"result":    "full",
	})
	findAppMetricByLabels(t, admissions, map[string]string{
		"component": "gateway",
		"pool":      "async_send",
		"queue":     "send",
		"priority":  "none",
		"result":    "full",
	})
	findAppMetricByLabels(t, admissions, map[string]string{
		"component": "slot",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "none",
		"result":    "dirty",
	})
	findAppMetricByLabels(t, admissions, map[string]string{
		"component": "db",
		"pool":      "message_commit",
		"queue":     "commit",
		"priority":  "none",
		"result":    "timeout",
	})

	inflight := requireAppMetricFamily(t, families, "wukongim_runtime_pool_inflight")
	channelWorkerInflight := findAppMetricByLabels(t, inflight, map[string]string{
		"component": "channelv2",
		"pool":      "store_append",
	})
	if got := channelWorkerInflight.GetGauge().GetValue(); got != 2 {
		t.Fatalf("channelv2 worker inflight = %v, want 2", got)
	}
	transportServiceInflight := findAppMetricByLabels(t, inflight, map[string]string{
		"component": "transportv2",
		"pool":      "service_9",
	})
	if got := transportServiceInflight.GetGauge().GetValue(); got != 2 {
		t.Fatalf("transportv2 service inflight = %v, want 2", got)
	}

	workers := requireAppMetricFamily(t, families, "wukongim_runtime_pool_workers")
	transportServiceWorkers := findAppMetricByLabels(t, workers, map[string]string{
		"component": "transportv2",
		"pool":      "service_9",
	})
	if got := transportServiceWorkers.GetGauge().GetValue(); got != 4 {
		t.Fatalf("transportv2 service workers = %v, want 4", got)
	}
	dbCommitWorkers := findAppMetricByLabels(t, workers, map[string]string{
		"component": "db",
		"pool":      "message_commit",
	})
	if got := dbCommitWorkers.GetGauge().GetValue(); got != 1 {
		t.Fatalf("db message commit workers = %v, want 1", got)
	}

	waitDuration := requireAppMetricFamily(t, families, "wukongim_runtime_pool_wait_duration_seconds")
	findAppMetricByLabels(t, waitDuration, map[string]string{
		"component": "gateway",
		"pool":      "async_send",
		"queue":     "send",
		"priority":  "none",
		"result":    "ok",
	})
	findAppMetricByLabels(t, waitDuration, map[string]string{
		"component": "db",
		"pool":      "message_commit",
		"queue":     "commit",
		"priority":  "none",
		"result":    "timeout",
	})
	taskDuration := requireAppMetricFamily(t, families, "wukongim_runtime_pool_task_duration_seconds")
	findAppMetricByLabels(t, taskDuration, map[string]string{
		"component": "db",
		"pool":      "message_commit",
		"task":      "commit",
		"result":    "ok",
	})

	channelWorkerBatch := requireAppMetricFamily(t, families, "wukongim_channelv2_worker_batch_items")
	channelWorkerRPCPullBatch := findAppMetricByLabels(t, channelWorkerBatch, map[string]string{
		"node_id":   "1",
		"node_name": "n1",
		"kind":      "rpc_pull",
		"result":    "ok",
	})
	if got := channelWorkerRPCPullBatch.GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("channelv2 rpc pull batch count = %v, want 1", got)
	}
	if got := channelWorkerRPCPullBatch.GetHistogram().GetSampleSum(); got != 3 {
		t.Fatalf("channelv2 rpc pull batch sum = %v, want 3", got)
	}
}

func TestStorageCommitMetricsObserverUsesConfiguredWorkers(t *testing.T) {
	reg := obsmetrics.New(1, "node-1")
	observer := storageCommitMetricsObserver{metrics: reg, workers: 4}

	observer.SetCommitCoordinatorQueue(3, 4096)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	workers := requireAppMetricFamily(t, families, "wukongim_runtime_pool_workers")
	dbCommitWorkers := findAppMetricByLabels(t, workers, map[string]string{
		"component": "db",
		"pool":      "message_commit",
	})
	if got := dbCommitWorkers.GetGauge().GetValue(); got != 4 {
		t.Fatalf("db message commit workers = %v, want 4", got)
	}
}

func TestTransportV2MetricsObserverAggregatesConnectionLocalGauges(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := &transportV2MetricsObserver{metrics: reg}

	observer.ObserveTransport(transportv2.Event{
		Name:     "pending_rpc",
		SourceID: 101,
		Inflight: 2,
	})
	observer.ObserveTransport(transportv2.Event{
		Name:     "pending_rpc",
		SourceID: 102,
		Inflight: 3,
	})
	observer.ObserveTransport(transportv2.Event{
		Name:     "pending_rpc",
		SourceID: 101,
		Inflight: 0,
	})
	observer.ObserveTransport(transportv2.Event{
		Name:          "scheduler_queue",
		SourceID:      101,
		Priority:      transportv2.PriorityRPC,
		Items:         2,
		Capacity:      8,
		Bytes:         20,
		BytesCapacity: 80,
		Result:        "ok",
	})
	observer.ObserveTransport(transportv2.Event{
		Name:          "scheduler_queue",
		SourceID:      102,
		Priority:      transportv2.PriorityRPC,
		Items:         5,
		Capacity:      8,
		Bytes:         40,
		BytesCapacity: 80,
		Result:        "ok",
	})
	observer.ObserveTransport(transportv2.Event{
		Name:          "scheduler_queue",
		SourceID:      101,
		Priority:      transportv2.PriorityRPC,
		Items:         0,
		Capacity:      8,
		Bytes:         0,
		BytesCapacity: 80,
		Result:        "ok",
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	inflight := requireAppMetricFamily(t, families, "wukongim_runtime_pool_inflight")
	rpcInflight := findAppMetricByLabels(t, inflight, map[string]string{
		"component": "transportv2",
		"pool":      "rpc",
	})
	if got := rpcInflight.GetGauge().GetValue(); got != 3 {
		t.Fatalf("transportv2 rpc inflight = %v, want aggregated 3", got)
	}

	queueDepth := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_depth")
	schedulerDepth := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "transportv2",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerDepth.GetGauge().GetValue(); got != 5 {
		t.Fatalf("transportv2 scheduler depth = %v, want aggregated 5", got)
	}

	queueCapacity := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_capacity")
	schedulerCapacity := findAppMetricByLabels(t, queueCapacity, map[string]string{
		"component": "transportv2",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerCapacity.GetGauge().GetValue(); got != 16 {
		t.Fatalf("transportv2 scheduler capacity = %v, want aggregated 16", got)
	}

	queueBytes := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes")
	schedulerBytes := findAppMetricByLabels(t, queueBytes, map[string]string{
		"component": "transportv2",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerBytes.GetGauge().GetValue(); got != 40 {
		t.Fatalf("transportv2 scheduler bytes = %v, want aggregated 40", got)
	}

	queueBytesCapacity := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes_capacity")
	schedulerBytesCapacity := findAppMetricByLabels(t, queueBytesCapacity, map[string]string{
		"component": "transportv2",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerBytesCapacity.GetGauge().GetValue(); got != 160 {
		t.Fatalf("transportv2 scheduler bytes capacity = %v, want aggregated 160", got)
	}

	observer.ObserveTransport(transportv2.Event{
		Name:          "scheduler_queue",
		SourceID:      101,
		Priority:      transportv2.PriorityRPC,
		Capacity:      8,
		BytesCapacity: 80,
		Result:        "stopped",
	})
	families, err = reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error after stopped = %v", err)
	}
	queueCapacity = requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_capacity")
	schedulerCapacity = findAppMetricByLabels(t, queueCapacity, map[string]string{
		"component": "transportv2",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerCapacity.GetGauge().GetValue(); got != 8 {
		t.Fatalf("transportv2 scheduler capacity after stopped = %v, want remaining source capacity 8", got)
	}
}

func TestObservabilityConversationAuthorityMetricsObserverMapsCounters(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := conversationProjectorMetricsObserver{metrics: reg}

	observer.ObserveConversationAuthorityAdmit(conversationAuthorityAdmitEvent{Result: "timeout"})
	observer.ObserveConversationAuthorityCachePressure(conversationAuthorityCachePressureEvent{Phase: "admit", Result: "cache_pressure"})
	observer.ObserveConversationAuthorityList(conversationAuthorityListEvent{Result: "route_not_ready"})
	observer.ObserveConversationAuthorityHandoff(conversationAuthorityHandoffEvent{Result: "drained"})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	admit := requireAppMetricFamily(t, families, "wukongim_conversation_authority_admit_total")
	if got := findAppMetricByLabels(t, admit, map[string]string{"result": "timeout"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("authority admit metric = %v, want 1", got)
	}
	pressure := requireAppMetricFamily(t, families, "wukongim_conversation_authority_cache_pressure_total")
	if got := findAppMetricByLabels(t, pressure, map[string]string{"phase": "admit", "result": "cache_pressure"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("authority cache pressure metric = %v, want 1", got)
	}
	list := requireAppMetricFamily(t, families, "wukongim_conversation_authority_list_total")
	if got := findAppMetricByLabels(t, list, map[string]string{"result": "route_not_ready"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("authority list metric = %v, want 1", got)
	}
	handoff := requireAppMetricFamily(t, families, "wukongim_conversation_authority_handoff_total")
	if got := findAppMetricByLabels(t, handoff, map[string]string{"result": "drained"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("authority handoff metric = %v, want 1", got)
	}
}

func requireAppMetricFamily(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func findAppMetricByLabels(t *testing.T, family *dto.MetricFamily, want map[string]string) *dto.Metric {
	t.Helper()
	for _, metric := range family.GetMetric() {
		got := make(map[string]string, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			got[label.GetName()] = label.GetValue()
		}
		matched := true
		for key, value := range want {
			if got[key] != value {
				matched = false
				break
			}
		}
		if matched {
			return metric
		}
	}
	t.Fatalf("metric family %q with labels %v not found", family.GetName(), want)
	return nil
}

func TestChannelV2PullHintMetricLabels(t *testing.T) {
	reasonCases := []struct {
		name   string
		reason transport.PullHintReason
		want   string
	}{
		{name: "append", reason: transport.PullHintReasonAppend, want: "append"},
		{name: "resume", reason: transport.PullHintReasonResume, want: "resume"},
		{name: "unknown", reason: transport.PullHintReason(99), want: "unknown"},
	}
	for _, tc := range reasonCases {
		t.Run("reason_"+tc.name, func(t *testing.T) {
			if got := channelV2PullHintReasonLabel(tc.reason); got != tc.want {
				t.Fatalf("channelV2PullHintReasonLabel() = %q, want %q", got, tc.want)
			}
		})
	}

	errorCases := []struct {
		name string
		err  error
		want string
	}{
		{name: "none", want: "none"},
		{name: "not_ready", err: ch.ErrNotReady, want: "not_ready"},
		{name: "remote_not_ready", err: fmt.Errorf("nodetransport: remote error: %s", ch.ErrNotReady), want: "not_ready"},
		{name: "stale_meta", err: fmt.Errorf("wrapped: %w", ch.ErrStaleMeta), want: "stale_meta"},
		{name: "remote_stale_meta", err: fmt.Errorf("nodetransport: remote error: %s", ch.ErrStaleMeta), want: "stale_meta"},
		{name: "channel_not_found", err: ch.ErrChannelNotFound, want: "channel_not_found"},
		{name: "not_leader", err: ch.ErrNotLeader, want: "not_leader"},
		{name: "invalid_config", err: ch.ErrInvalidConfig, want: "invalid_config"},
		{name: "remote_invalid_config", err: fmt.Errorf("nodetransport: remote error: %s", ch.ErrInvalidConfig), want: "invalid_config"},
		{name: "closed", err: ch.ErrClosed, want: "closed"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "remote_canceled", err: fmt.Errorf("nodetransport: remote error: %s", context.Canceled), want: "canceled"},
		{name: "timeout", err: context.DeadlineExceeded, want: "timeout"},
		{name: "remote_timeout", err: fmt.Errorf("nodetransport: remote error: %s", context.DeadlineExceeded), want: "timeout"},
		{name: "remote_error", err: fmt.Errorf("nodetransport: remote error: unexpected"), want: "remote_error"},
		{name: "other", err: fmt.Errorf("boom"), want: "other"},
	}
	for _, tc := range errorCases {
		t.Run("error_"+tc.name, func(t *testing.T) {
			if got := channelV2PullHintErrorLabel(tc.err); got != tc.want {
				t.Fatalf("channelV2PullHintErrorLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChannelV2AppendWaitCancelLogLineIncludesSnapshotState(t *testing.T) {
	line := channelV2AppendWaitCancelLogLine(reactor.AppendWaitCancelSnapshot{
		ReactorID:             2,
		Key:                   ch.ChannelKey("1:room"),
		ChannelID:             ch.ChannelID{ID: "room", Type: 1},
		OpID:                  44,
		CommitMode:            ch.CommitModeQuorum,
		Role:                  ch.RoleLeader,
		Leader:                3,
		Epoch:                 5,
		LeaderEpoch:           6,
		LEO:                   9,
		HW:                    7,
		TargetOffset:          9,
		StoreSubmitted:        true,
		StoreCompleted:        true,
		FollowerPullServed:    true,
		AckOffsetObserved:     false,
		HWAdvanced:            false,
		Waiters:               1,
		PendingAppends:        1,
		PendingAppendOrder:    1,
		AppendQueuePending:    0,
		AppendQueueRecords:    0,
		AppendQueueBytes:      0,
		AppendInflight:        false,
		AppendInflightOpID:    0,
		AppendInflightWaiters: 0,
		AppendStoreBlocked:    false,
		PullWaiters:           3,
		FollowerStates:        "2:match=7,lag=2,hint_inflight=false,hint_retry=true",
		Err:                   context.DeadlineExceeded,
	})
	for _, want := range []string{
		"internalv2/app: channelv2 append waiter canceled",
		"reactor=2",
		"key=1:room",
		"channel_id=room",
		"channel_type=1",
		"op=44",
		"commit_mode=quorum",
		"role=leader",
		"leader=3",
		"epoch=5",
		"leader_epoch=6",
		"leo=9",
		"hw=7",
		"target=9",
		"store_completed=true",
		"ack_offset_observed=false",
		"waiters=1",
		"pending_appends=1",
		"pull_waiters=3",
		"follower_states=\"2:match=7,lag=2,hint_inflight=false,hint_retry=true\"",
		"err=context deadline exceeded",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q missing %q", line, want)
		}
	}
}

func TestMessageAppendMetricErrorLabels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "none", want: "none"},
		{name: "backpressured", err: messageusecase.ErrBackpressured, want: "backpressured"},
		{name: "route_not_ready", err: fmt.Errorf("%w: %w", messageusecase.ErrRouteNotReady, ch.ErrNotReady), want: "route_not_ready"},
		{name: "remote_not_ready", err: fmt.Errorf("%w: nodetransport: remote error: %s", messageusecase.ErrAppendFailed, ch.ErrNotReady), want: "route_not_ready"},
		{name: "stale_route", err: fmt.Errorf("%w: %w", messageusecase.ErrStaleRoute, ch.ErrStaleMeta), want: "stale_route"},
		{name: "not_leader", err: messageusecase.ErrNotLeader, want: "not_leader"},
		{name: "channel_not_found", err: messageusecase.ErrChannelNotFound, want: "channel_not_found"},
		{name: "short_result", err: messageusecase.ErrAppendResultMissing, want: "short_result"},
		{name: "invalid_config", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, ch.ErrInvalidConfig), want: "invalid_config"},
		{name: "remote_invalid_config", err: fmt.Errorf("%w: nodetransport: remote error: %s", messageusecase.ErrAppendFailed, ch.ErrInvalidConfig), want: "invalid_config"},
		{name: "closed", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, ch.ErrClosed), want: "closed"},
		{name: "too_many_channels", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, ch.ErrTooManyChannels), want: "too_many_channels"},
		{name: "cluster_not_started", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, clusterv2.ErrNotStarted), want: "not_started"},
		{name: "cluster_stopping", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, clusterv2.ErrStopping), want: "closed"},
		{name: "timeout", err: fmt.Errorf("%w: %s", messageusecase.ErrAppendFailed, context.DeadlineExceeded), want: "timeout"},
		{name: "wrapped_remote_error", err: fmt.Errorf("%w: nodetransport: remote error: unexpected", messageusecase.ErrAppendFailed), want: "remote_error"},
		{name: "append_failed", err: messageusecase.ErrAppendFailed, want: "append_failed"},
		{name: "remote_error", err: fmt.Errorf("nodetransport: remote error: unexpected"), want: "remote_error"},
		{name: "other", err: fmt.Errorf("boom"), want: "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageAppendErrorLabel(tc.err); got != tc.want {
				t.Fatalf("messageAppendErrorLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppendFailureLogLineIncludesRawError(t *testing.T) {
	err := fmt.Errorf("%w: storage worker returned boom", messageusecase.ErrAppendFailed)

	got := appendFailureLogLine("channelplane", err)

	if !strings.Contains(got, "channelplane") {
		t.Fatalf("appendFailureLogLine() = %q, want path", got)
	}
	if !strings.Contains(got, "storage worker returned boom") {
		t.Fatalf("appendFailureLogLine() = %q, want raw error", got)
	}
}

func TestMessageAppendErrorLogPolicyIncludesTimeout(t *testing.T) {
	cases := []struct {
		label string
		want  bool
	}{
		{label: "append_failed", want: true},
		{label: "timeout", want: true},
		{label: "route_not_ready", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := shouldLogMessageAppendError(tc.label); got != tc.want {
				t.Fatalf("shouldLogMessageAppendError(%q) = %v, want %v", tc.label, got, tc.want)
			}
		})
	}
}

func TestNewWiresDiagnosticsStoreAndSendTraceSink(t *testing.T) {
	cfg := Config{
		Cluster: clusterv2.Config{NodeID: 7},
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 1,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)

	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.diagnostics == nil {
		t.Fatal("diagnostics store was not wired")
	}
	if app.diagnosticsTracking == nil {
		t.Fatal("diagnostics tracking rules were not wired")
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-internalv2-new",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-internalv2-new"})
	if result.Status != diagnostics.StatusOK {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusOK, result)
	}
	if result.NodeID != 7 {
		t.Fatalf("diagnostics node id = %d, want 7", result.NodeID)
	}
	if len(result.Events) != 1 || result.Events[0].TraceID != "trace-internalv2-new" {
		t.Fatalf("diagnostics events = %#v, want trace-internalv2-new", result.Events)
	}
}

func TestDiagnosticsTrackingRulesDriveSampling(t *testing.T) {
	cfg := Config{
		NodeID: 5,
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 0,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rule, err := app.AddDiagnosticsTrackingRule(context.Background(), diagnostics.TrackingRuleInput{
		ID:         "sender-u1",
		Target:     diagnostics.TrackingTargetSenderUID,
		UID:        "u1",
		TTL:        time.Minute,
		SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("AddDiagnosticsTrackingRule() error = %v", err)
	}
	if rule.ID != "sender-u1" {
		t.Fatalf("tracking rule id = %q, want sender-u1", rule.ID)
	}
	rules, err := app.ListDiagnosticsTrackingRules(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticsTrackingRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].ID != "sender-u1" {
		t.Fatalf("tracking rules = %#v, want sender-u1", rules)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-tracked-u1",
		FromUID: "u1",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})
	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-tracked-u1"})
	if result.Status != diagnostics.StatusOK {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusOK, result)
	}

	if err := app.DeleteDiagnosticsTrackingRule(context.Background(), "sender-u1"); err != nil {
		t.Fatalf("DeleteDiagnosticsTrackingRule() error = %v", err)
	}
	rules, err = app.ListDiagnosticsTrackingRules(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticsTrackingRules() after delete error = %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("tracking rules after delete = %#v, want empty", rules)
	}
}

func TestStopRestoresDiagnosticsSendTraceSink(t *testing.T) {
	cfg := Config{
		NodeID: 3,
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 1,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.diagnostics == nil {
		t.Fatal("diagnostics store was not wired")
	}

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-after-internalv2-stop",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-after-internalv2-stop"})
	if result.Status != diagnostics.StatusNotFound {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusNotFound, result)
	}
}

func TestNewRestoresDiagnosticsSinkOnConstructionError(t *testing.T) {
	previous := &recordingInternalV2SendTraceSink{}
	restorePrevious := sendtrace.SetSink(previous)
	t.Cleanup(restorePrevious)

	cfg := Config{
		Gateway: GatewayConfig{
			Listeners: []gateway.ListenerOptions{{
				Network:   "tcp",
				Address:   "127.0.0.1:0",
				Transport: "gnet",
				Protocol:  "wkproto",
			}},
		},
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 1,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)

	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err == nil {
		t.Fatal("New() error = nil, want invalid gateway listener error")
	}
	if app != nil {
		t.Fatalf("New() app = %#v, want nil on construction error", app)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-after-new-error",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})
	events := previous.snapshot()
	if len(events) != 1 || events[0].TraceID != "trace-after-new-error" {
		t.Fatalf("previous sink events = %#v, want restored trace-after-new-error", events)
	}
}

func TestDisabledDiagnosticsLeavesStoreUnwired(t *testing.T) {
	cfg := Config{NodeID: 11}
	cfg.Observability.Diagnostics.Enabled = false
	cfg.Observability.SetDiagnosticsExplicitFlags(true, false, false)

	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.diagnostics != nil {
		t.Fatal("diagnostics store was wired when diagnostics were disabled")
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-disabled-internalv2",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-disabled-internalv2"})
	if result.Status != diagnostics.StatusNotFound {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusNotFound, result)
	}
	if result.NodeID != 11 {
		t.Fatalf("diagnostics node id = %d, want fallback node id 11", result.NodeID)
	}
}

func TestDiagnosticsConfigDefaultsAndValidation(t *testing.T) {
	cfg := Config{}
	cfg.Observability = defaultObservabilityConfig(cfg.Observability)
	if err := validateObservabilityConfig(cfg.Observability); err != nil {
		t.Fatalf("validateObservabilityConfig(default) error = %v", err)
	}
	if !cfg.Observability.Diagnostics.Enabled {
		t.Fatal("diagnostics enabled default = false, want true")
	}
	if cfg.Observability.Diagnostics.BufferSize != 50000 {
		t.Fatalf("diagnostics buffer size = %d, want 50000", cfg.Observability.Diagnostics.BufferSize)
	}
	if cfg.Observability.Diagnostics.SampleRate != 0.01 {
		t.Fatalf("diagnostics sample rate = %v, want 0.01", cfg.Observability.Diagnostics.SampleRate)
	}
	if cfg.Observability.Diagnostics.SlowThreshold != 500*time.Millisecond {
		t.Fatalf("diagnostics slow threshold = %v, want 500ms", cfg.Observability.Diagnostics.SlowThreshold)
	}
	if cfg.Observability.Diagnostics.ErrorSampleRate != 1 {
		t.Fatalf("diagnostics error sample rate = %v, want 1", cfg.Observability.Diagnostics.ErrorSampleRate)
	}
	if cfg.Observability.Diagnostics.DeepSampleRate != 0 {
		t.Fatalf("diagnostics deep sample rate = %v, want 0", cfg.Observability.Diagnostics.DeepSampleRate)
	}
	if cfg.Observability.Diagnostics.DeepSlowThreshold != cfg.Observability.Diagnostics.SlowThreshold {
		t.Fatalf("diagnostics deep slow threshold = %v, want %v", cfg.Observability.Diagnostics.DeepSlowThreshold, cfg.Observability.Diagnostics.SlowThreshold)
	}
	if cfg.Observability.Diagnostics.DeepMaxItemsPerBatch != 16 {
		t.Fatalf("diagnostics deep max items = %d, want 16", cfg.Observability.Diagnostics.DeepMaxItemsPerBatch)
	}

	explicit := ObservabilityConfig{
		Diagnostics: DiagnosticsConfig{
			Enabled:         false,
			SampleRate:      0,
			ErrorSampleRate: 0,
		},
	}
	explicit.SetDiagnosticsExplicitFlags(true, true, true)
	explicit = defaultObservabilityConfig(explicit)
	if explicit.Diagnostics.Enabled {
		t.Fatal("explicit diagnostics enabled=false was overwritten")
	}
	if explicit.Diagnostics.SampleRate != 0 {
		t.Fatalf("explicit diagnostics sample rate = %v, want 0", explicit.Diagnostics.SampleRate)
	}
	if explicit.Diagnostics.ErrorSampleRate != 0 {
		t.Fatalf("explicit diagnostics error sample rate = %v, want 0", explicit.Diagnostics.ErrorSampleRate)
	}

	tests := []struct {
		name string
		cfg  DiagnosticsConfig
	}{
		{name: "sample rate low", cfg: DiagnosticsConfig{SampleRate: -0.1}},
		{name: "sample rate high", cfg: DiagnosticsConfig{SampleRate: 1.1}},
		{name: "sample rate nan", cfg: DiagnosticsConfig{SampleRate: math.NaN()}},
		{name: "error sample rate low", cfg: DiagnosticsConfig{ErrorSampleRate: -0.1}},
		{name: "error sample rate high", cfg: DiagnosticsConfig{ErrorSampleRate: 1.1}},
		{name: "error sample rate inf", cfg: DiagnosticsConfig{ErrorSampleRate: math.Inf(1)}},
		{name: "deep sample rate low", cfg: DiagnosticsConfig{DeepSampleRate: -0.1}},
		{name: "deep sample rate high", cfg: DiagnosticsConfig{DeepSampleRate: 1.1}},
		{name: "deep sample rate nan", cfg: DiagnosticsConfig{DeepSampleRate: math.NaN()}},
		{name: "deep slow threshold negative", cfg: DiagnosticsConfig{DeepSlowThreshold: -time.Millisecond}},
		{name: "deep max items negative", cfg: DiagnosticsConfig{DeepMaxItemsPerBatch: -1}},
		{name: "debug match sample rate low", cfg: DiagnosticsConfig{DebugMatches: []DiagnosticsDebugMatchConfig{{SampleRate: -0.1}}}},
		{name: "debug match sample rate high", cfg: DiagnosticsConfig{DebugMatches: []DiagnosticsDebugMatchConfig{{SampleRate: 1.1}}}},
		{name: "debug match ttl", cfg: DiagnosticsConfig{DebugMatches: []DiagnosticsDebugMatchConfig{{TTLSeconds: -1}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observability := ObservabilityConfig{Diagnostics: tt.cfg}
			if err := validateObservabilityConfig(observability); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateObservabilityConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestNewRejectsNegativeDeepDiagnosticsConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  DiagnosticsConfig
	}{
		{name: "deep slow threshold", cfg: DiagnosticsConfig{DeepSlowThreshold: -time.Millisecond}},
		{name: "deep max items", cfg: DiagnosticsConfig{DeepMaxItemsPerBatch: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Config{Observability: ObservabilityConfig{Diagnostics: tt.cfg}}, WithCluster(&fakeCluster{}))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestDeliveryObserverLogsAsyncErrorsWithoutMetrics(t *testing.T) {
	logger := &recordingAppLogger{}
	observer := deliveryMetricsObserver{logger: logger}

	observer.ObserveRetry(runtimedelivery.RetryEvent{
		Event:      runtimedelivery.DeliveryRetryEventDrop,
		Result:     runtimedelivery.DeliveryResultMaxAttempts,
		ErrorClass: runtimedelivery.DeliveryErrorClassRetryable,
		Attempt:    3,
		QueueDepth: 7,
	})
	observer.ObserveManagerTerminal(runtimedelivery.ManagerTerminalEvent{
		Result:     runtimedelivery.DeliveryResultError,
		ErrorClass: runtimedelivery.DeliveryErrorClassError,
		QueueDepth: 1,
	})

	requireAppLogEvent(t, logger, "WARN", "internalv2.app.delivery.retry_failed")
	requireAppLogEvent(t, logger, "WARN", "internalv2.app.delivery.manager_terminal_failed")
}

type recordingInternalV2SendTraceSink struct {
	events []sendtrace.Event
}

func (s *recordingInternalV2SendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}

func (s *recordingInternalV2SendTraceSink) snapshot() []sendtrace.Event {
	return append([]sendtrace.Event(nil), s.events...)
}

func requireAppLogEvent(t *testing.T, logger *recordingAppLogger, level, event string) {
	t.Helper()
	for _, entry := range logger.entries {
		if entry.level != level {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return
			}
		}
	}
	t.Fatalf("missing app log event level=%s event=%s entries=%#v", level, event, logger.entries)
}
