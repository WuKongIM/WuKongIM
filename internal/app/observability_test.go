package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clustertasks "github.com/WuKongIM/WuKongIM/pkg/cluster/tasks"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	dto "github.com/prometheus/client_model/go"
)

func TestChannelWorkerKindLabelIncludesMetaResolve(t *testing.T) {
	if got := channelWorkerKindLabel(worker.TaskMetaResolve); got != "meta_resolve" {
		t.Fatalf("channelWorkerKindLabel(TaskMetaResolve) = %q, want meta_resolve", got)
	}
	if got := channelWorkerKindLabel(worker.TaskColdMetaResolve); got != "cold_meta_resolve" {
		t.Fatalf("channelWorkerKindLabel(TaskColdMetaResolve) = %q, want cold_meta_resolve", got)
	}
	if got := channelWorkerKindLabel(worker.TaskColdStoreLoad); got != "cold_store_load" {
		t.Fatalf("channelWorkerKindLabel(TaskColdStoreLoad) = %q, want cold_store_load", got)
	}
}

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

	channelObserver := channelMetricsObserver{metrics: reg}
	channelObserver.SetWorkerQueueDepth("store_append", 1)
	channelObserver.SetWorkerQueueCapacity("store_append", 64)
	channelObserver.SetWorkerWorkers("store_append", 4)
	channelObserver.SetWorkerInflight("store_append", 2)
	channelObserver.SetWorkerAntsPoolUsage("store_append", 5, 8, 1)
	channelObserver.ObserveWorkerAdmission("store_append", "ok")
	channelObserver.ObserveWorkerWait("store_append", worker.TaskStoreAppend, time.Millisecond)
	channelObserver.ObserveWorkerTask("store_append", worker.TaskStoreAppend, nil, time.Millisecond)
	channelObserver.ObserveWorkerBatch("rpc", worker.TaskRPCPull, 3, nil)
	channelObserver.ObservePullBatch(ch.PullBatchObservation{
		Items:                      4,
		Submitted:                  4,
		Errors:                     1,
		Records:                    7,
		PayloadBytes:               2048,
		SubmitDuration:             2 * time.Millisecond,
		AwaitDuration:              11 * time.Millisecond,
		MaxSequentialAwaitDuration: 7 * time.Millisecond,
		TotalDuration:              13 * time.Millisecond,
	})
	channelObserver.ObserveLeaderPullStage(1, "mailbox_wait", 5*time.Millisecond)
	channelObserver.ObserveLeaderPullStage(1, "handler", 8*time.Millisecond)
	channelObserver.ObserveLeaderPullCompletedWaiters(1, 3)
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
	slotObserver.ObserveSlotProposal(7, 2*time.Millisecond)
	slotObserver.ObserveSlotProposalAdmission(7, multiraft.ProposalClassBackground, "throttled")
	slotObserver.SetSlotApplyState(7, 11, 8)

	transportObserver := transportMetricsObserver{metrics: reg}
	transportObserver.ObserveTransport(transport.Event{
		Name:          "scheduler_queue",
		Priority:      transport.PriorityRPC,
		Items:         3,
		Capacity:      32,
		Bytes:         128,
		BytesCapacity: 4096,
		Result:        "ok",
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:         "service_task",
		ServiceID:    9,
		ServiceAlias: "slot runtime report",
		Result:       "ok",
		Duration:     time.Millisecond,
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:         "service_inflight",
		ServiceID:    9,
		Inflight:     2,
		Capacity:     4,
		PoolRunning:  3,
		PoolCapacity: 8,
		PoolWaiting:  1,
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:  "sent_bytes",
		Kind:  transport.FrameKindRPCRequest,
		Bytes: 64,
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:  "received_bytes",
		Kind:  transport.FrameKindRPCResponse,
		Bytes: 72,
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:     "write_batch",
		Items:    1,
		Capacity: 64,
		Bytes:    128,
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:     "write_batch",
		Items:    64,
		Capacity: 64,
		Bytes:    8192,
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:     "controller_raft_queue",
		Priority: transport.PriorityRaft,
		Items:    2,
		Capacity: 16,
		Result:   "ok",
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:     "controller_raft_admission",
		Priority: transport.PriorityRaft,
		Result:   "full",
	})
	transportObserver.ObserveTransport(transport.Event{
		Name:     "controller_raft_task",
		Priority: transport.PriorityRaft,
		Result:   "err",
		Duration: time.Millisecond,
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
		"wukongim_ants_pool_running",
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
		"component": "channel",
		"pool":      "reactor_0",
		"queue":     "mailbox",
		"priority":  "high",
	})
	if got := reactorZero.GetGauge().GetValue(); got != 2 {
		t.Fatalf("reactor_0 mailbox depth = %v, want 2", got)
	}
	reactorOne := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "channel",
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
	slotProposals := requireAppMetricFamily(t, families, "wukongim_slot_proposals_total")
	slotSevenProposal := findAppMetricByLabels(t, slotProposals, map[string]string{
		"slot_id": "7",
	})
	if got := slotSevenProposal.GetCounter().GetValue(); got != 1 {
		t.Fatalf("slot proposals = %v, want 1", got)
	}
	slotProposalAdmission := requireAppMetricFamily(t, families, "wukongim_slot_proposal_admission_total")
	slotBackgroundThrottle := findAppMetricByLabels(t, slotProposalAdmission, map[string]string{
		"class":  "background",
		"result": "throttled",
	})
	if got := slotBackgroundThrottle.GetCounter().GetValue(); got != 1 {
		t.Fatalf("slot proposal admission = %v, want 1", got)
	}
	slotApplyGap := requireAppMetricFamily(t, families, "wukongim_slot_apply_gap")
	slotSevenGap := findAppMetricByLabels(t, slotApplyGap, map[string]string{
		"slot_id": "7",
	})
	if got := slotSevenGap.GetGauge().GetValue(); got != 3 {
		t.Fatalf("slot apply gap = %v, want 3", got)
	}
	transportScheduler := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "transport",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := transportScheduler.GetGauge().GetValue(); got != 3 {
		t.Fatalf("transport scheduler depth = %v, want 3", got)
	}
	controllerRaftSend := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "transport",
		"pool":      "controller_raft",
		"queue":     "send",
		"priority":  "raft",
	})
	if got := controllerRaftSend.GetGauge().GetValue(); got != 2 {
		t.Fatalf("controller raft send queue depth = %v, want 2", got)
	}
	sentBytes := requireAppMetricFamily(t, families, "wukongim_transport_sent_bytes_total")
	sentRPCRequest := findAppMetricByLabels(t, sentBytes, map[string]string{
		"msg_type": "rpc_request",
	})
	if got := sentRPCRequest.GetCounter().GetValue(); got != 64 {
		t.Fatalf("transport sent bytes = %v, want 64", got)
	}
	receivedBytes := requireAppMetricFamily(t, families, "wukongim_transport_received_bytes_total")
	receivedRPCResponse := findAppMetricByLabels(t, receivedBytes, map[string]string{
		"msg_type": "rpc_response",
	})
	if got := receivedRPCResponse.GetCounter().GetValue(); got != 72 {
		t.Fatalf("transport received bytes = %v, want 72", got)
	}
	for name, want := range map[string]float64{
		"wukongim_transport_write_batches_total":              2,
		"wukongim_transport_write_frames_total":               65,
		"wukongim_transport_write_payload_bytes_total":        8320,
		"wukongim_transport_write_single_frame_batches_total": 1,
		"wukongim_transport_write_frame_limit_batches_total":  1,
	} {
		family := requireAppMetricFamily(t, families, name)
		if got := family.GetMetric()[0].GetCounter().GetValue(); got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	rpcTotal := requireAppMetricFamily(t, families, "wukongim_transport_rpc_total")
	slotRuntimeRPC := findAppMetricByLabels(t, rpcTotal, map[string]string{
		"service": "slot runtime report",
		"result":  "ok",
	})
	if got := slotRuntimeRPC.GetCounter().GetValue(); got != 1 {
		t.Fatalf("transport service rpc total = %v, want 1", got)
	}
	rpcDuration := requireAppMetricFamily(t, families, "wukongim_transport_rpc_duration_seconds")
	slotRuntimeDuration := findAppMetricByLabels(t, rpcDuration, map[string]string{
		"service": "slot runtime report",
	})
	if got := slotRuntimeDuration.GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("transport service rpc duration count = %v, want 1", got)
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
		"component": "channel",
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
	findAppMetricByLabels(t, admissions, map[string]string{
		"component": "transport",
		"pool":      "controller_raft",
		"queue":     "send",
		"priority":  "raft",
		"result":    "full",
	})

	inflight := requireAppMetricFamily(t, families, "wukongim_runtime_pool_inflight")
	channelWorkerInflight := findAppMetricByLabels(t, inflight, map[string]string{
		"component": "channel",
		"pool":      "store_append",
	})
	if got := channelWorkerInflight.GetGauge().GetValue(); got != 2 {
		t.Fatalf("channel worker inflight = %v, want 2", got)
	}
	transportServiceInflight := findAppMetricByLabels(t, inflight, map[string]string{
		"component": "transport",
		"pool":      "service_9",
	})
	if got := transportServiceInflight.GetGauge().GetValue(); got != 2 {
		t.Fatalf("transport service inflight = %v, want 2", got)
	}

	workers := requireAppMetricFamily(t, families, "wukongim_runtime_pool_workers")
	transportServiceWorkers := findAppMetricByLabels(t, workers, map[string]string{
		"component": "transport",
		"pool":      "service_9",
	})
	if got := transportServiceWorkers.GetGauge().GetValue(); got != 4 {
		t.Fatalf("transport service workers = %v, want 4", got)
	}
	antsRunning := requireAppMetricFamily(t, families, "wukongim_ants_pool_running")
	transportServiceExecutor := findAppMetricByLabels(t, antsRunning, map[string]string{
		"component": "transport",
		"pool":      "service_executor",
	})
	if got := transportServiceExecutor.GetGauge().GetValue(); got != 3 {
		t.Fatalf("transport service executor ants running = %v, want 3", got)
	}
	channelWorkerExecutor := findAppMetricByLabels(t, antsRunning, map[string]string{
		"component": "channel",
		"pool":      "store_append",
	})
	if got := channelWorkerExecutor.GetGauge().GetValue(); got != 5 {
		t.Fatalf("channel store append worker ants running = %v, want 5", got)
	}
	antsCapacity := requireAppMetricFamily(t, families, "wukongim_ants_pool_capacity")
	transportServiceExecutorCapacity := findAppMetricByLabels(t, antsCapacity, map[string]string{
		"component": "transport",
		"pool":      "service_executor",
	})
	if got := transportServiceExecutorCapacity.GetGauge().GetValue(); got != 8 {
		t.Fatalf("transport service executor ants capacity = %v, want 8", got)
	}
	channelWorkerExecutorCapacity := findAppMetricByLabels(t, antsCapacity, map[string]string{
		"component": "channel",
		"pool":      "store_append",
	})
	if got := channelWorkerExecutorCapacity.GetGauge().GetValue(); got != 8 {
		t.Fatalf("channel store append worker ants capacity = %v, want 8", got)
	}
	antsWaiting := requireAppMetricFamily(t, families, "wukongim_ants_pool_waiting")
	transportServiceExecutorWaiting := findAppMetricByLabels(t, antsWaiting, map[string]string{
		"component": "transport",
		"pool":      "service_executor",
	})
	if got := transportServiceExecutorWaiting.GetGauge().GetValue(); got != 1 {
		t.Fatalf("transport service executor ants waiting = %v, want 1", got)
	}
	channelWorkerExecutorWaiting := findAppMetricByLabels(t, antsWaiting, map[string]string{
		"component": "channel",
		"pool":      "store_append",
	})
	if got := channelWorkerExecutorWaiting.GetGauge().GetValue(); got != 1 {
		t.Fatalf("channel store append worker ants waiting = %v, want 1", got)
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
	findAppMetricByLabels(t, taskDuration, map[string]string{
		"component": "transport",
		"pool":      "controller_raft",
		"task":      "send",
		"result":    "err",
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
	pullBatchDuration := requireAppMetricFamily(t, families, "wukongim_channelv2_pull_batch_duration_seconds")
	pullBatchMaxAwait := findAppMetricByLabels(t, pullBatchDuration, map[string]string{
		"node_id":   "1",
		"node_name": "n1",
		"stage":     "max_sequential_await",
		"result":    "partial",
	})
	if got := pullBatchMaxAwait.GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("channelv2 pull batch max await count = %v, want 1", got)
	}
	leaderPullStages := requireAppMetricFamily(t, families, "wukongim_channelv2_leader_pull_stage_duration_seconds")
	leaderPullMailboxWait := findAppMetricByLabels(t, leaderPullStages, map[string]string{
		"node_id":   "1",
		"node_name": "n1",
		"stage":     "mailbox_wait",
	})
	if got := leaderPullMailboxWait.GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("channelv2 leader pull mailbox wait count = %v, want 1", got)
	}
	leaderPullWaiters := requireAppMetricFamily(t, families, "wukongim_channelv2_leader_pull_completed_waiters")
	if got := leaderPullWaiters.GetMetric()[0].GetHistogram().GetSampleSum(); got != 3 {
		t.Fatalf("channelv2 leader pull completed waiters sum = %v, want 3", got)
	}
}

func TestMultiChannelObserverForwardsOptionalPullObservations(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := multiChannelObserver{channelMetricsObserver{metrics: reg}}
	if !observer.LeaderPullObservationEnabled() {
		t.Fatal("leader Pull observation should be enabled by the metrics child")
	}
	if got := observer.LeaderPullObservationSampleEvery(); got != channelLeaderPullObservationEvery {
		t.Fatalf("leader Pull sample interval = %d, want %d", got, channelLeaderPullObservationEvery)
	}

	observer.ObservePullBatch(ch.PullBatchObservation{
		Items:                      2,
		Submitted:                  2,
		Records:                    3,
		PayloadBytes:               384,
		SubmitDuration:             time.Millisecond,
		AwaitDuration:              4 * time.Millisecond,
		MaxSequentialAwaitDuration: 3 * time.Millisecond,
		TotalDuration:              5 * time.Millisecond,
	})
	observer.ObserveLeaderPullStage(16, "mailbox_wait", 2*time.Millisecond)
	observer.ObserveLeaderPullCompletedWaiters(16, 1)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	pullBatch := requireAppMetricFamily(t, families, "wukongim_channelv2_pull_batch_items")
	if got := pullBatch.GetMetric()[0].GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("pull batch sample count = %d, want 1", got)
	}
	leaderPull := requireAppMetricFamily(t, families, "wukongim_channelv2_leader_pull_stage_duration_seconds")
	if got := leaderPull.GetMetric()[0].GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("leader Pull stage sample count = %d, want 1", got)
	}
	waiters := requireAppMetricFamily(t, families, "wukongim_channelv2_leader_pull_completed_waiters")
	if got := waiters.GetMetric()[0].GetHistogram().GetSampleSum(); got != 1 {
		t.Fatalf("leader Pull waiter sample sum = %v, want 1", got)
	}
}

func TestMultiChannelObserverPreservesChildLeaderPullSampleRates(t *testing.T) {
	first := &sampledLeaderPullObserver{every: 4}
	second := &sampledLeaderPullObserver{every: 6}
	observer := multiChannelObserver{first, second}
	if got := observer.LeaderPullObservationSampleEvery(); got != 2 {
		t.Fatalf("leader Pull composite sample interval = %d, want gcd 2", got)
	}

	for opID := ch.OpID(2); opID <= 12; opID += 2 {
		observer.ObserveLeaderPullStage(opID, "handler", time.Millisecond)
		observer.ObserveLeaderPullCompletedWaiters(opID, 1)
	}
	if got, want := first.opIDs, []ch.OpID{4, 4, 8, 8, 12, 12}; !slices.Equal(got, want) {
		t.Fatalf("first child op ids = %v, want %v", got, want)
	}
	if got, want := second.opIDs, []ch.OpID{6, 6, 12, 12}; !slices.Equal(got, want) {
		t.Fatalf("second child op ids = %v, want %v", got, want)
	}
}

type sampledLeaderPullObserver struct {
	reactor.Observer
	every uint64
	opIDs []ch.OpID
}

func (o *sampledLeaderPullObserver) LeaderPullObservationSampleEvery() uint64 {
	return o.every
}

func (o *sampledLeaderPullObserver) ObserveLeaderPullStage(opID ch.OpID, _ string, _ time.Duration) {
	o.opIDs = append(o.opIDs, opID)
}

func (o *sampledLeaderPullObserver) ObserveLeaderPullCompletedWaiters(opID ch.OpID, _ int) {
	o.opIDs = append(o.opIDs, opID)
}

func TestSlotMetricsObserverMapsLeaderChanges(t *testing.T) {
	reg := obsmetrics.New(2, "node-2")
	observer := combineSlotObservers(slotMetricsObserver{metrics: reg}, topSlotObserver{})
	leaderObserver, ok := observer.(interface {
		ObserveSlotLeaderChangeWithCause(slotID multiraft.SlotID, from, to multiraft.NodeID, cause multiraft.LeaderChangeCause)
	})
	if !ok {
		t.Fatal("combined slot observer does not observe leader changes")
	}

	leaderObserver.ObserveSlotLeaderChangeWithCause(7, 1, 2, multiraft.LeaderChangeCauseElection)
	leaderObserver.ObserveSlotLeaderChangeWithCause(7, 2, 3, multiraft.LeaderChangeCausePlannedTransfer)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	leaderChanges := requireAppMetricFamily(t, families, "wukongim_slot_leader_elections_total")
	metric := findAppMetricByLabels(t, leaderChanges, nil)
	if got := metric.GetCounter().GetValue(); got != 1 {
		t.Fatalf("slot leader changes = %v, want 1", got)
	}
	leaderChangeDetails := requireAppMetricFamily(t, families, "wukongim_slot_leader_changes_total")
	electionMetric := findAppMetricByLabels(t, leaderChangeDetails, map[string]string{
		"slot_id": "7",
		"cause":   "election",
	})
	if got := electionMetric.GetCounter().GetValue(); got != 1 {
		t.Fatalf("slot leader election details = %v, want 1", got)
	}
	plannedMetric := findAppMetricByLabels(t, leaderChangeDetails, map[string]string{
		"slot_id": "7",
		"cause":   "planned_transfer",
	})
	if got := plannedMetric.GetCounter().GetValue(); got != 1 {
		t.Fatalf("slot leader planned transfer details = %v, want 1", got)
	}
}

func TestSlotMetricsObserverMapsPreferredLeaderDecisions(t *testing.T) {
	reg := obsmetrics.New(2, "node-2")
	observer := combinePreferredLeaderObservers(slotMetricsObserver{metrics: reg}, nil)
	observer.ObservePreferredLeaderDecision("preferred_inactive")
	observer.ObservePreferredLeaderStrictWait("transfer_started", 8*time.Millisecond)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	decisions := requireAppMetricFamily(t, families, "wukongim_slot_preferred_leader_reconcile_total")
	if got := findAppMetricByLabels(t, decisions, map[string]string{"decision": "preferred_inactive"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("preferred leader decisions = %v, want 1", got)
	}
	wait := requireAppMetricFamily(t, families, "wukongim_slot_preferred_leader_strict_wait_duration_seconds")
	if got := findAppMetricByLabels(t, wait, map[string]string{"decision": "transfer_started"}).GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("preferred leader strict waits = %v, want 1", got)
	}
	for _, family := range []*dto.MetricFamily{decisions, wait} {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "slot_id" {
					t.Fatalf("preferred-leader metric %s unexpectedly exposes slot_id", family.GetName())
				}
			}
		}
	}
}

func TestPreferredLeaderDiagnosticsObserverRetainsMismatchWithoutMatchChurn(t *testing.T) {
	store := diagnostics.NewStore(diagnostics.StoreOptions{NodeID: 2, Capacity: 8})
	observer := combinePreferredLeaderObservers(
		slotMetricsObserver{},
		&preferredLeaderDiagnosticsObserver{store: store, nodeID: 2},
	)
	detailed, ok := observer.(clustertasks.PreferredLeaderDiagnosticObserver)
	if !ok {
		t.Fatal("combined preferred-leader observer does not expose detailed diagnostics")
	}
	detailed.ObservePreferredLeaderReconcile(clustertasks.PreferredLeaderObservation{
		Decision:           "preferred_lagging",
		SlotID:             7,
		ActualLeaderID:     1,
		PreferredLeaderID:  2,
		RaftTerm:           11,
		ConfigEpoch:        4,
		StrictWaitDuration: 8 * time.Millisecond,
	})
	detailed.ObservePreferredLeaderReconcile(clustertasks.PreferredLeaderObservation{
		Decision:          "match",
		SlotID:            8,
		ActualLeaderID:    2,
		PreferredLeaderID: 2,
		RaftTerm:          12,
		ConfigEpoch:       5,
	})

	result := store.Query(context.Background(), diagnostics.Query{Stage: diagnostics.StageSlotPreferredLeaderReconcile, Limit: 10})
	if len(result.Events) != 1 {
		t.Fatalf("diagnostics events = %#v, want only the non-match decision", result.Events)
	}
	event := result.Events[0]
	if event.NodeID != 2 || event.SlotID != 7 || event.Decision != "preferred_lagging" ||
		event.ActualLeaderID != 1 || event.PreferredLeaderID != 2 || event.RaftTerm != 11 || event.ConfigEpoch != 4 ||
		event.Result != diagnostics.ResultSkipped || event.Duration != 8*time.Millisecond {
		t.Fatalf("diagnostics event = %#v, want explicit physical Slot decision context", event)
	}
}

func TestPreferredLeaderDiagnosticsObserverBoundsRepeatedSlotDecisions(t *testing.T) {
	now := time.Unix(100, 0)
	store := diagnostics.NewStore(diagnostics.StoreOptions{NodeID: 2, Capacity: 16, Now: func() time.Time { return now }})
	observer := &preferredLeaderDiagnosticsObserver{
		store:          store,
		nodeID:         2,
		now:            func() time.Time { return now },
		repeatInterval: 30 * time.Second,
	}
	base := clustertasks.PreferredLeaderObservation{
		Decision: "voter_mismatch", SlotID: 7, ActualLeaderID: 1,
		PreferredLeaderID: 2, RaftTerm: 11, ConfigEpoch: 4,
	}

	observer.ObservePreferredLeaderReconcile(base)
	observer.ObservePreferredLeaderReconcile(base)
	changedDecision := base
	changedDecision.Decision = "preferred_lagging"
	observer.ObservePreferredLeaderReconcile(changedDecision)
	changedTerm := changedDecision
	changedTerm.RaftTerm = 12
	observer.ObservePreferredLeaderReconcile(changedTerm)
	matched := changedTerm
	matched.Decision = "match"
	matched.ActualLeaderID = matched.PreferredLeaderID
	observer.ObservePreferredLeaderReconcile(matched)
	observer.ObservePreferredLeaderReconcile(changedTerm)
	now = now.Add(30 * time.Second)
	observer.ObservePreferredLeaderReconcile(changedTerm)

	result := store.Query(context.Background(), diagnostics.Query{SlotID: 7, Limit: 10})
	if len(result.Events) != 6 {
		t.Fatalf("diagnostics events = %#v, want duplicate suppressed, changes immediate, recovery match retained, and interval resample", result.Events)
	}
	if result.Events[0].Decision != "voter_mismatch" || result.Events[1].Decision != "preferred_lagging" ||
		result.Events[2].RaftTerm != 12 || result.Events[3].Decision != "match" ||
		result.Events[3].Result != diagnostics.ResultOK || result.Events[4].Decision != "preferred_lagging" ||
		result.Events[5].Decision != "preferred_lagging" {
		t.Fatalf("diagnostics events = %#v, want stable transition ordering", result.Events)
	}
}

func TestPreferredLeaderDiagnosticsObserverSuppressesRepeatedHealthyMatch(t *testing.T) {
	now := time.Unix(100, 0)
	store := diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 8, Now: func() time.Time { return now }})
	observer := &preferredLeaderDiagnosticsObserver{store: store, nodeID: 2, now: func() time.Time { return now }}
	match := clustertasks.PreferredLeaderObservation{
		Decision: "match", SlotID: 7, ActualLeaderID: 2,
		PreferredLeaderID: 2, RaftTerm: 11, ConfigEpoch: 4,
	}

	observer.ObservePreferredLeaderReconcile(match)
	observer.ObservePreferredLeaderReconcile(match)
	now = now.Add(time.Minute)
	observer.ObservePreferredLeaderReconcile(match)

	result := store.Query(context.Background(), diagnostics.Query{SlotID: 7, Limit: 10})
	if len(result.Events) != 0 {
		t.Fatalf("steady healthy match events = %#v, want none", result.Events)
	}
}

func TestPreferredLeaderDiagnosticsObserverBoundsRememberedPhysicalSlots(t *testing.T) {
	store := diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 1})
	observer := &preferredLeaderDiagnosticsObserver{store: store, nodeID: 2}
	for slotID := uint32(1); slotID <= maxPreferredLeaderDiagnosticSlots+1; slotID++ {
		observer.ObservePreferredLeaderReconcile(clustertasks.PreferredLeaderObservation{
			Decision: "voter_mismatch", SlotID: slotID, ActualLeaderID: 1,
			PreferredLeaderID: 2, RaftTerm: 11, ConfigEpoch: 4,
		})
	}

	if got := len(observer.lastBySlot); got != maxPreferredLeaderDiagnosticSlots {
		t.Fatalf("remembered physical Slots = %d, want %d", got, maxPreferredLeaderDiagnosticSlots)
	}
	if _, exists := observer.lastBySlot[1]; exists {
		t.Fatal("oldest physical Slot signature was not evicted")
	}
	if _, exists := observer.lastBySlot[maxPreferredLeaderDiagnosticSlots+1]; !exists {
		t.Fatal("latest physical Slot signature was not retained")
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

func TestControllerRaftMetricsObserverMapsApplyGap(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := controllerRaftMetricsObserver{metrics: reg}

	observer.SetApplyState(15, 12)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	applyGap := requireAppMetricFamily(t, families, "wukongim_controller_apply_gap")
	gapMetric := findAppMetricByLabels(t, applyGap, nil)
	if got := gapMetric.GetGauge().GetValue(); got != 3 {
		t.Fatalf("controller apply gap = %v, want 3", got)
	}

	observer.SetApplyState(11, 12)
	families, err = reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error after no gap = %v", err)
	}
	applyGap = requireAppMetricFamily(t, families, "wukongim_controller_apply_gap")
	gapMetric = findAppMetricByLabels(t, applyGap, nil)
	if got := gapMetric.GetGauge().GetValue(); got != 0 {
		t.Fatalf("controller apply gap after applied catches up = %v, want 0", got)
	}
}

func TestControllerRaftMetricsObserverMapsMembershipAndPromotion(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := controllerRaftMetricsObserver{metrics: reg}

	observer.ObserveControllerRaftStatus(managementusecase.ControllerRaftStatus{
		NodeID:   1,
		Voters:   []uint64{1, 2, 4},
		Learners: []uint64{5},
	})
	observer.ObserveControllerVoterPromotionAttempt("blocked")
	observer.ObserveControllerVoterPromotionBlocker("target_revision_stale")
	observer.ObserveControllerVoterPromotionPhase("commit_state", 25*time.Millisecond)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	voters := requireAppMetricFamily(t, families, "wukongim_controller_raft_voters")
	if got := voters.GetMetric()[0].GetGauge().GetValue(); got != 3 {
		t.Fatalf("controller raft voters = %v, want 3", got)
	}
	learners := requireAppMetricFamily(t, families, "wukongim_controller_raft_learners")
	if got := learners.GetMetric()[0].GetGauge().GetValue(); got != 1 {
		t.Fatalf("controller raft learners = %v, want 1", got)
	}
	attempts := requireAppMetricFamily(t, families, "wukongim_controller_voter_promotion_attempts_total")
	if got := findAppMetricByLabels(t, attempts, map[string]string{"result": "blocked"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("blocked promotion attempts = %v, want 1", got)
	}
	blockers := requireAppMetricFamily(t, families, "wukongim_controller_voter_promotion_blockers_total")
	if got := findAppMetricByLabels(t, blockers, map[string]string{"reason": "target_revision_stale"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("target_revision_stale blockers = %v, want 1", got)
	}
	phases := requireAppMetricFamily(t, families, "wukongim_controller_voter_promotion_phase_seconds")
	if got := findAppMetricByLabels(t, phases, map[string]string{"phase": "commit_state"}).GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("commit_state phase samples = %v, want 1", got)
	}
}

func TestControlSnapshotMetricsObserverMapsControllerHealth(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := controlSnapshotMetricsObserver{metrics: reg}

	observer.ObserveControlSnapshot(control.Snapshot{
		Revision:     42,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeDown},
			{NodeID: 4, Roles: []control.Role{control.RoleController}, Status: control.NodeSuspect},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2}, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{1, 2}, PreferredLeader: 1},
			{SlotID: 3, DesiredPeers: []uint64{1, 2}, PreferredLeader: 2},
		},
		Tasks: []control.ReconcileTask{
			{TaskID: "slot-1-transfer", SlotID: 1, Kind: control.TaskKindLeaderTransfer, Step: control.TaskStepTransferLeader, Status: control.TaskStatusFailed},
			{TaskID: "slot-2-bootstrap", SlotID: 2, Kind: control.TaskKindBootstrap, Step: control.TaskStepCreateSlot, Status: control.TaskStatusPending},
		},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	revision := requireAppMetricFamily(t, families, "wukongim_controller_state_revision")
	if got := revision.GetMetric()[0].GetGauge().GetValue(); got != 42 {
		t.Fatalf("controller revision = %v, want 42", got)
	}
	leaderPresent := requireAppMetricFamily(t, families, "wukongim_controller_leader_present")
	if got := leaderPresent.GetMetric()[0].GetGauge().GetValue(); got != 1 {
		t.Fatalf("controller leader present = %v, want 1", got)
	}
	nodesSuspect := requireAppMetricFamily(t, families, "wukongim_controller_nodes_suspect")
	if got := nodesSuspect.GetMetric()[0].GetGauge().GetValue(); got != 1 {
		t.Fatalf("controller suspect nodes = %v, want 1", got)
	}
	nodesDead := requireAppMetricFamily(t, families, "wukongim_controller_nodes_dead")
	if got := nodesDead.GetMetric()[0].GetGauge().GetValue(); got != 1 {
		t.Fatalf("controller dead nodes = %v, want 1", got)
	}
	active := requireAppMetricFamily(t, families, "wukongim_controller_tasks_active")
	if got := findAppMetricByLabels(t, active, map[string]string{"type": "leader_transfer"}).GetGauge().GetValue(); got != 0 {
		t.Fatalf("leader transfer active tasks = %v, want 0", got)
	}
	if got := findAppMetricByLabels(t, active, map[string]string{"type": "bootstrap"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("bootstrap active tasks = %v, want 1", got)
	}
	failed := requireAppMetricFamily(t, families, "wukongim_controller_tasks_failed")
	if got := findAppMetricByLabels(t, failed, map[string]string{"type": "leader_transfer"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("leader transfer failed tasks = %v, want 1", got)
	}
	skew := requireAppMetricFamily(t, families, "wukongim_controller_slot_leader_skew")
	if got := skew.GetMetric()[0].GetGauge().GetValue(); got != 1 {
		t.Fatalf("controller slot leader skew = %v, want 1", got)
	}
}

func TestControlSnapshotMetricsObserverMapsNodeLifecycleHealth(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := controlSnapshotMetricsObserver{metrics: reg}

	observer.ObserveControlSnapshot(control.Snapshot{
		Revision: 42,
		Nodes: []control.Node{
			{
				NodeID:    1,
				Roles:     []control.Role{control.RoleData},
				JoinState: control.NodeJoinStateActive,
				Status:    control.NodeAlive,
				Health: control.NodeHealth{
					Status:       control.NodeAlive,
					Freshness:    control.NodeHealthFresh,
					RuntimeReady: true,
					ReportAge:    4 * time.Second,
				},
			},
			{
				NodeID:    2,
				Roles:     []control.Role{control.RoleData},
				JoinState: control.NodeJoinStateLeaving,
				Status:    control.NodeAlive,
				Health: control.NodeHealth{
					Status:       control.NodeAlive,
					Freshness:    control.NodeHealthStale,
					RuntimeReady: true,
					ReportAge:    31 * time.Second,
				},
			},
			{
				NodeID:    3,
				Roles:     []control.Role{control.RoleData},
				JoinState: control.NodeJoinStateActive,
				Status:    control.NodeDown,
				Health: control.NodeHealth{
					Status:       control.NodeDown,
					Freshness:    control.NodeHealthFresh,
					RuntimeReady: false,
					ReportAge:    time.Second,
				},
			},
		},
		Tasks: []control.ReconcileTask{
			{Kind: control.TaskKindSlotReplicaMove, Status: control.TaskStatusPending},
			{Kind: control.TaskKindSlotReplicaMove, Status: control.TaskStatusRunning},
			{Kind: control.TaskKindSlotReplicaMove, Status: control.TaskStatusFailed},
			{Kind: control.TaskKindLeaderTransfer, Status: control.TaskStatusRunning},
		},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	lifecycle := requireAppMetricFamily(t, families, "wukongim_node_lifecycle_nodes")
	if got := findAppMetricByLabels(t, lifecycle, map[string]string{"join_state": "active", "status": "alive"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("active alive lifecycle nodes = %v, want 1", got)
	}
	freshness := requireAppMetricFamily(t, families, "wukongim_node_health_freshness_nodes")
	if got := findAppMetricByLabels(t, freshness, map[string]string{"freshness": "stale", "status": "alive"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("stale alive health nodes = %v, want 1", got)
	}
	age := requireAppMetricFamily(t, families, "wukongim_node_health_report_age_seconds")
	if got := findAppMetricByLabels(t, age, map[string]string{"freshness": "stale", "status": "alive"}).GetGauge().GetValue(); got != 31 {
		t.Fatalf("stale alive health age = %v, want 31", got)
	}
	tasks := requireAppMetricFamily(t, families, "wukongim_node_onboarding_tasks")
	if got := findAppMetricByLabels(t, tasks, map[string]string{"state": "running"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("running onboarding tasks = %v, want 1", got)
	}
	revision := requireAppMetricFamily(t, families, "wukongim_discovery_membership_revision")
	if got := revision.GetMetric()[0].GetGauge().GetValue(); got != 42 {
		t.Fatalf("membership revision = %v, want 42", got)
	}
}

func TestConfigureObservabilityWiresSlotReplicaMovePhaseObserver(t *testing.T) {
	app := &App{
		cfg: Config{Observability: ObservabilityConfig{MetricsEnabled: true}},
	}
	clusterCfg := cluster.Config{NodeID: 1}

	app.configureObservability(&clusterCfg)

	if clusterCfg.Slots.ReplicaMoveObserver == nil {
		t.Fatal("Slot replica move phase observer was not wired")
	}
	if clusterCfg.Slots.PreferredLeaderObserver == nil {
		t.Fatal("Slot preferred leader observer was not wired")
	}
}

func TestConfigureObservabilityWiresPreferredLeaderDiagnosticsWithoutMetrics(t *testing.T) {
	app := &App{
		cfg: Config{Observability: ObservabilityConfig{Diagnostics: DiagnosticsConfig{Enabled: true}}},
	}
	t.Cleanup(app.restoreDiagnosticsSink)
	clusterCfg := cluster.Config{NodeID: 2}

	app.configureObservability(&clusterCfg)

	detailed, ok := clusterCfg.Slots.PreferredLeaderObserver.(clustertasks.PreferredLeaderDiagnosticObserver)
	if !ok {
		t.Fatal("PreferredLeader detailed diagnostics observer was not wired")
	}
	detailed.ObservePreferredLeaderReconcile(clustertasks.PreferredLeaderObservation{
		Decision: "preferred_lagging", SlotID: 7, ActualLeaderID: 1,
		PreferredLeaderID: 2, RaftTerm: 11, ConfigEpoch: 4,
	})
	result := app.diagnostics.Query(context.Background(), diagnostics.Query{SlotID: 7})
	if len(result.Events) != 1 || result.Events[0].Decision != "preferred_lagging" {
		t.Fatalf("diagnostics result = %#v, want wired physical Slot decision", result)
	}
	if app.metrics != nil {
		t.Fatal("metrics registry was created while metrics were disabled")
	}
}

func TestNodeLifecycleMetricsObserverCountsScaleInBlockers(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := &nodeLifecycleMetricsObserver{metrics: reg}

	observer.ObserveScaleInStatus(managementusecase.NodeScaleInStatusResponse{
		NodeID:         4,
		StateRevision:  21,
		BlockedReasons: []string{"target_health_stale", "eligible_node_health_revision_stale"},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	blockers := requireAppMetricFamily(t, families, "wukongim_node_scale_in_blockers_total")
	if got := findAppMetricByLabels(t, blockers, map[string]string{"reason": "target_health_stale"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("target_health_stale blockers = %v, want 1", got)
	}
	if got := findAppMetricByLabels(t, blockers, map[string]string{"reason": "eligible_node_health_revision_stale"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("eligible stale revision blockers = %v, want 1", got)
	}
}

func TestNodeLifecycleMetricsObserverDeduplicatesScaleInBlockersPerRevision(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := &nodeLifecycleMetricsObserver{metrics: reg}
	status := managementusecase.NodeScaleInStatusResponse{
		NodeID:         4,
		StateRevision:  21,
		BlockedReasons: []string{"target_health_stale"},
	}

	observer.ObserveScaleInStatus(status)
	observer.ObserveScaleInStatus(status)
	status.StateRevision = 22
	observer.ObserveScaleInStatus(status)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	blockers := requireAppMetricFamily(t, families, "wukongim_node_scale_in_blockers_total")
	if got := findAppMetricByLabels(t, blockers, map[string]string{"reason": "target_health_stale"}).GetCounter().GetValue(); got != 2 {
		t.Fatalf("target_health_stale blockers = %v, want one count per node/revision/reason", got)
	}
}

func TestTransportMetricsObserverAggregatesConnectionLocalGauges(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := &transportMetricsObserver{metrics: reg}

	observer.ObserveTransport(transport.Event{
		Name:     "pending_rpc",
		SourceID: 101,
		Inflight: 2,
	})
	observer.ObserveTransport(transport.Event{
		Name:     "pending_rpc",
		SourceID: 102,
		Inflight: 3,
	})
	observer.ObserveTransport(transport.Event{
		Name:     "pending_rpc",
		SourceID: 101,
		Inflight: 0,
	})
	observer.ObserveTransport(transport.Event{
		Name:          "scheduler_queue",
		SourceID:      101,
		Priority:      transport.PriorityRPC,
		Items:         2,
		Capacity:      8,
		Bytes:         20,
		BytesCapacity: 80,
		Result:        "ok",
	})
	observer.ObserveTransport(transport.Event{
		Name:          "scheduler_queue",
		SourceID:      102,
		Priority:      transport.PriorityRPC,
		Items:         5,
		Capacity:      8,
		Bytes:         40,
		BytesCapacity: 80,
		Result:        "ok",
	})
	observer.ObserveTransport(transport.Event{
		Name:          "scheduler_queue",
		SourceID:      101,
		Priority:      transport.PriorityRPC,
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
		"component": "transport",
		"pool":      "rpc",
	})
	if got := rpcInflight.GetGauge().GetValue(); got != 3 {
		t.Fatalf("transport rpc inflight = %v, want aggregated 3", got)
	}

	queueDepth := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_depth")
	schedulerDepth := findAppMetricByLabels(t, queueDepth, map[string]string{
		"component": "transport",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerDepth.GetGauge().GetValue(); got != 5 {
		t.Fatalf("transport scheduler depth = %v, want aggregated 5", got)
	}

	queueCapacity := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_capacity")
	schedulerCapacity := findAppMetricByLabels(t, queueCapacity, map[string]string{
		"component": "transport",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerCapacity.GetGauge().GetValue(); got != 16 {
		t.Fatalf("transport scheduler capacity = %v, want aggregated 16", got)
	}

	queueBytes := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes")
	schedulerBytes := findAppMetricByLabels(t, queueBytes, map[string]string{
		"component": "transport",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerBytes.GetGauge().GetValue(); got != 40 {
		t.Fatalf("transport scheduler bytes = %v, want aggregated 40", got)
	}

	queueBytesCapacity := requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes_capacity")
	schedulerBytesCapacity := findAppMetricByLabels(t, queueBytesCapacity, map[string]string{
		"component": "transport",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerBytesCapacity.GetGauge().GetValue(); got != 160 {
		t.Fatalf("transport scheduler bytes capacity = %v, want aggregated 160", got)
	}

	observer.ObserveTransport(transport.Event{
		Name:          "scheduler_queue",
		SourceID:      101,
		Priority:      transport.PriorityRPC,
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
		"component": "transport",
		"pool":      "scheduler",
		"queue":     "scheduler",
		"priority":  "rpc",
	})
	if got := schedulerCapacity.GetGauge().GetValue(); got != 8 {
		t.Fatalf("transport scheduler capacity after stopped = %v, want remaining source capacity 8", got)
	}
}

func TestObservabilityConversationAuthorityMetricsObserverMapsCounters(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := conversationAuthorityMetricsObserver{metrics: reg}

	observer.ObserveConversationAuthorityAdmit(conversationAuthorityAdmitEvent{Result: "timeout"})
	observer.ObserveConversationAuthorityCachePressure(conversationAuthorityCachePressureEvent{Phase: "admit", Result: "cache_pressure"})
	observer.ObserveConversationAuthorityList(conversationAuthorityListEvent{Result: "route_not_ready"})
	observer.ObserveConversationAuthorityHandoff(conversationAuthorityHandoffEvent{Result: "drained"})
	observer.ObserveConversationActiveCache(conversationactive.CacheObservation{
		Revision:         1,
		Rows:             10,
		DirtyRows:        4,
		DirtyQueueRows:   4,
		DirtyAgeBuckets:  3,
		OldestDirtyAge:   3 * time.Second,
		PressureDraining: true,
		RowsByKind: map[metadb.ConversationKind]int{
			metadb.ConversationKindNormal: 7,
			metadb.ConversationKindCMD:    3,
		},
		DirtyRowsByKind: map[metadb.ConversationKind]int{
			metadb.ConversationKindNormal: 1,
			metadb.ConversationKindCMD:    3,
		},
	})
	observer.ObserveConversationActiveMutation(conversationactive.MutationObservation{
		Result: "ok", BecameDirty: 3, DirtyUpdated: 2, CooldownSuppressed: 4, Unchanged: 1,
		LockWaitDuration: time.Millisecond, LockHoldDuration: 2 * time.Millisecond,
		CacheObservationDuration: 3 * time.Millisecond,
	})
	observer.ObserveConversationActiveMutation(conversationactive.MutationObservation{
		Result: "cache_pressure", LockWaitDuration: 4 * time.Millisecond, LockHoldDuration: 5 * time.Millisecond,
	})
	observer.ObserveConversationActiveFlush(conversationactive.FlushObservation{
		Result:                "ok",
		Selected:              7,
		Persisted:             4,
		Skipped:               1,
		DeleteFenced:          2,
		Cleared:               4,
		VersionConflicts:      1,
		Requeued:              1,
		LaneWaitDuration:      time.Millisecond,
		SelectDuration:        2 * time.Millisecond,
		FilterDuration:        3 * time.Millisecond,
		PersistDuration:       4 * time.Millisecond,
		ClearDuration:         5 * time.Millisecond,
		ClearLockWaitDuration: time.Millisecond,
		ClearApplyDuration:    4 * time.Millisecond,
		Duration:              6 * time.Millisecond,
	})
	observer.ObserveConversationActivePressure(conversationactive.PressureObservation{Event: "signal_received", WakeupWaitDuration: time.Millisecond})

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
	activeRows := requireAppMetricFamily(t, families, "wukongim_conversation_active_cache_rows")
	if got := findAppMetricByLabels(t, activeRows, nil).GetGauge().GetValue(); got != 10 {
		t.Fatalf("active cache rows metric = %v, want 10", got)
	}
	dirtyRows := requireAppMetricFamily(t, families, "wukongim_conversation_active_cache_dirty_rows")
	if got := findAppMetricByLabels(t, dirtyRows, nil).GetGauge().GetValue(); got != 4 {
		t.Fatalf("active cache dirty rows metric = %v, want 4", got)
	}
	dirtyQueueRows := requireAppMetricFamily(t, families, "wukongim_conversation_active_cache_dirty_queue_rows")
	if got := findAppMetricByLabels(t, dirtyQueueRows, nil).GetGauge().GetValue(); got != 4 {
		t.Fatalf("active cache dirty queue rows metric = %v, want 4", got)
	}
	dirtyAgeBuckets := requireAppMetricFamily(t, families, "wukongim_conversation_active_cache_dirty_age_buckets")
	if got := findAppMetricByLabels(t, dirtyAgeBuckets, nil).GetGauge().GetValue(); got != 3 {
		t.Fatalf("active cache dirty age buckets metric = %v, want 3", got)
	}
	kindRows := requireAppMetricFamily(t, families, "wukongim_conversation_active_cache_kind_rows")
	if got := findAppMetricByLabels(t, kindRows, map[string]string{"kind": "normal"}).GetGauge().GetValue(); got != 7 {
		t.Fatalf("normal active cache rows metric = %v, want 7", got)
	}
	if got := findAppMetricByLabels(t, kindRows, map[string]string{"kind": "cmd"}).GetGauge().GetValue(); got != 3 {
		t.Fatalf("cmd active cache rows metric = %v, want 3", got)
	}
	kindDirtyRows := requireAppMetricFamily(t, families, "wukongim_conversation_active_cache_kind_dirty_rows")
	if got := findAppMetricByLabels(t, kindDirtyRows, map[string]string{"kind": "normal"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("normal active cache dirty rows metric = %v, want 1", got)
	}
	if got := findAppMetricByLabels(t, kindDirtyRows, map[string]string{"kind": "cmd"}).GetGauge().GetValue(); got != 3 {
		t.Fatalf("cmd active cache dirty rows metric = %v, want 3", got)
	}
	flushRows := requireAppMetricFamily(t, families, "wukongim_conversation_active_flush_rows")
	if got := findAppMetricByLabels(t, flushRows, map[string]string{"result": "ok", "kind": "persisted"}).GetHistogram().GetSampleSum(); got != 4 {
		t.Fatalf("active flush persisted rows metric = %v, want 4", got)
	}
	flushRowsTotal := requireAppMetricFamily(t, families, "wukongim_conversation_active_flush_rows_total")
	if got := findAppMetricByLabels(t, flushRowsTotal, map[string]string{"result": "ok", "stage": "requeued", "reason": "version_conflict"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("active flush version-conflict rows metric = %v, want 1", got)
	}
	if got := findAppMetricByLabels(t, flushRowsTotal, map[string]string{"result": "ok", "stage": "skipped", "reason": "active_cooldown"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("active flush cooldown rows metric = %v, want 1", got)
	}
	if got := findAppMetricByLabels(t, flushRowsTotal, map[string]string{"result": "ok", "stage": "skipped", "reason": "delete_barrier"}).GetCounter().GetValue(); got != 2 {
		t.Fatalf("active flush delete-barrier rows metric = %v, want 2", got)
	}
	dirtyMutations := requireAppMetricFamily(t, families, "wukongim_conversation_active_dirty_mutations_total")
	if got := findAppMetricByLabels(t, dirtyMutations, map[string]string{"event": "became_dirty"}).GetCounter().GetValue(); got != 3 {
		t.Fatalf("active dirty became metric = %v, want 3", got)
	}
	if got := findAppMetricByLabels(t, dirtyMutations, map[string]string{"event": "cooldown_suppressed"}).GetCounter().GetValue(); got != 4 {
		t.Fatalf("active cooldown suppressed metric = %v, want 4", got)
	}
	cacheLock := requireAppMetricFamily(t, families, "wukongim_conversation_active_cache_lock_duration_seconds")
	if got := findAppMetricByLabels(t, cacheLock, map[string]string{"result": "ok", "phase": "wait"}).GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("active cache wait samples = %d, want 1", got)
	}
	if got := findAppMetricByLabels(t, cacheLock, map[string]string{"result": "cache_pressure", "phase": "wait"}).GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("active cache pressure wait samples = %d, want 1", got)
	}
	flushStages := requireAppMetricFamily(t, families, "wukongim_conversation_active_flush_stage_duration_seconds")
	for _, stage := range []string{"clear_lock_wait", "clear_apply"} {
		if got := findAppMetricByLabels(t, flushStages, map[string]string{"result": "ok", "stage": stage}).GetHistogram().GetSampleCount(); got != 1 {
			t.Fatalf("active flush stage %s samples = %d, want 1", stage, got)
		}
	}
	pressureEvents := requireAppMetricFamily(t, families, "wukongim_conversation_active_pressure_events_total")
	if got := findAppMetricByLabels(t, pressureEvents, map[string]string{"event": "signal_received"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("active pressure signal_received metric = %v, want 1", got)
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

func TestObservabilityPresenceMetricsObserverMapsWorkerExpiryAndTouchFlush(t *testing.T) {
	reg := obsmetrics.New(7, "node-7")
	targetA := presence.RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 7, LeaderTerm: 3, ConfigEpoch: 4}
	targetB := presence.RouteTarget{HashSlot: 9, SlotID: 2, LeaderNodeID: 8, LeaderTerm: 5, ConfigEpoch: 6}
	directory := &recordingPresenceDirectory{result: authoritypresence.ExpireResult{
		Expired:      2,
		DueBuckets:   3,
		Examined:     5,
		IndexRoutes:  7,
		IndexBuckets: 11,
	}}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local: &recordingTouchRegistry{queued: []online.OwnerRoute{
			{UID: "u1", SessionID: 101},
			{UID: "u2", SessionID: 102},
			{UID: "u3", SessionID: 103},
		}},
		Authority: &recordingTouchAuthority{targets: map[string]presence.RouteTarget{
			"u1": targetA,
			"u2": targetA,
			"u3": targetB,
		}},
		Directory:         directory,
		Observer:          presenceMetricsObserver{metrics: reg},
		BatchSize:         10,
		MaxRoutesPerFlush: 10,
		RouteTTL:          90 * time.Second,
	})

	worker.flushOnce(context.Background(), time.Unix(2_000, 0))

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	expiryTotal := requireAppMetricFamily(t, families, "wukongim_presence_expiry_total")
	if got := findAppMetricByLabels(t, expiryTotal, map[string]string{"result": "success"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("presence expiry total = %v, want 1", got)
	}
	for name, want := range map[string]float64{
		"wukongim_presence_expiry_due_buckets":     3,
		"wukongim_presence_expiry_examined_routes": 5,
		"wukongim_presence_expired_routes":         2,
		"wukongim_presence_expiry_index_routes":    7,
		"wukongim_presence_expiry_index_buckets":   11,
	} {
		family := requireAppMetricFamily(t, families, name)
		if got := findAppMetricByLabels(t, family, nil).GetGauge().GetValue(); got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	touchTotal := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_total")
	if got := findAppMetricByLabels(t, touchTotal, map[string]string{
		"result":         "success",
		"budget_reached": "false",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("presence touch flush total = %v, want 1", got)
	}
	touchRoutes := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_routes")
	for stage, want := range map[string]float64{
		"drained":  3,
		"resolved": 3,
		"sent":     3,
		"requeued": 0,
	} {
		if got := findAppMetricByLabels(t, touchRoutes, map[string]string{"stage": stage}).GetCounter().GetValue(); got != want {
			t.Fatalf("presence touch %s routes = %v, want %v", stage, got, want)
		}
	}
	chunks := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_chunks")
	if got := findAppMetricByLabels(t, chunks, nil).GetCounter().GetValue(); got != 1 {
		t.Fatalf("presence touch chunks = %v, want 1", got)
	}
	targetGroups := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_target_groups")
	if got := findAppMetricByLabels(t, targetGroups, nil).GetCounter().GetValue(); got != 2 {
		t.Fatalf("presence touch target groups = %v, want 2", got)
	}
}

func TestObservabilityPresenceTouchFlushReportsEmptyWork(t *testing.T) {
	reg := obsmetrics.New(1, "node-1")
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:             &recordingTouchRegistry{},
		Authority:         &recordingTouchAuthority{},
		Observer:          presenceMetricsObserver{metrics: reg},
		BatchSize:         2,
		MaxRoutesPerFlush: 3,
	})

	worker.flushOnce(context.Background(), time.Now())

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	total := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_total")
	if got := findAppMetricByLabels(t, total, map[string]string{
		"result":         "empty",
		"budget_reached": "false",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("empty presence touch flush total = %v, want 1", got)
	}
}

func TestObservabilityPresenceTouchFlushCancellationTakesPriority(t *testing.T) {
	reg := obsmetrics.New(1, "node-1")
	targetA := presence.RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4}
	targetB := presence.RouteTarget{HashSlot: 9, SlotID: 2, LeaderNodeID: 2, LeaderTerm: 5, ConfigEpoch: 6}
	ctx, cancel := context.WithCancel(context.Background())
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local: &recordingTouchRegistry{queued: []online.OwnerRoute{
			{UID: "u1", SessionID: 101},
			{UID: "u2", SessionID: 102},
			{UID: "u3", SessionID: 103},
		}},
		Authority: &recordingTouchAuthority{
			targets: map[string]presence.RouteTarget{
				"u1": targetA,
				"u2": targetB,
				"u3": targetB,
			},
			touchHook: func(_ context.Context, target presence.RouteTarget, _ []presence.Route) {
				if target == targetA {
					cancel()
				}
			},
		},
		Observer:          presenceMetricsObserver{metrics: reg},
		BatchSize:         10,
		MaxRoutesPerFlush: 10,
	})

	worker.flushOnce(ctx, time.Now())

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	total := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_total")
	if got := findAppMetricByLabels(t, total, map[string]string{
		"result":         "canceled",
		"budget_reached": "false",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("canceled presence touch flush total = %v, want 1", got)
	}
	routes := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_routes")
	for stage, want := range map[string]float64{
		"drained":  3,
		"resolved": 3,
		"sent":     1,
		"requeued": 2,
	} {
		if got := findAppMetricByLabels(t, routes, map[string]string{"stage": stage}).GetCounter().GetValue(); got != want {
			t.Fatalf("canceled presence touch %s routes = %v, want %v", stage, got, want)
		}
	}
}

func TestObservabilityPresenceTouchFlushReportsPartialRoutingAndRPCFailures(t *testing.T) {
	targetA := presence.RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4}
	targetB := presence.RouteTarget{HashSlot: 9, SlotID: 2, LeaderNodeID: 2, LeaderTerm: 5, ConfigEpoch: 6}
	tests := []struct {
		name      string
		authority *recordingTouchAuthority
		resolved  float64
		sent      float64
		groups    float64
	}{
		{
			name: "routing failure",
			authority: &recordingTouchAuthority{resolveResults: [][]presence.RouteTargetResult{{
				{Target: targetA},
				{Target: targetA},
				{Err: errors.New("route unavailable")},
			}}},
			resolved: 2,
			sent:     2,
			groups:   1,
		},
		{
			name: "RPC failure",
			authority: &recordingTouchAuthority{
				targets: map[string]presence.RouteTarget{
					"u1": targetA,
					"u2": targetB,
					"u3": targetB,
				},
				touchErrors: map[presence.RouteTarget]error{targetA: errors.New("touch failed")},
			},
			resolved: 3,
			sent:     2,
			groups:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := obsmetrics.New(1, "node-1")
			worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
				Local: &recordingTouchRegistry{queued: []online.OwnerRoute{
					{UID: "u1", SessionID: 101},
					{UID: "u2", SessionID: 102},
					{UID: "u3", SessionID: 103},
				}},
				Authority:         tt.authority,
				Observer:          presenceMetricsObserver{metrics: reg},
				BatchSize:         10,
				MaxRoutesPerFlush: 10,
			})

			worker.flushOnce(context.Background(), time.Now())

			families, err := reg.Gather()
			if err != nil {
				t.Fatalf("Gather() error = %v", err)
			}
			total := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_total")
			if got := findAppMetricByLabels(t, total, map[string]string{
				"result":         "partial",
				"budget_reached": "false",
			}).GetCounter().GetValue(); got != 1 {
				t.Fatalf("partial presence touch flush total = %v, want 1", got)
			}
			routes := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_routes")
			for stage, want := range map[string]float64{
				"drained":  3,
				"resolved": tt.resolved,
				"sent":     tt.sent,
				"requeued": 1,
			} {
				if got := findAppMetricByLabels(t, routes, map[string]string{"stage": stage}).GetCounter().GetValue(); got != want {
					t.Fatalf("partial presence touch %s routes = %v, want %v", stage, got, want)
				}
			}
			groups := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_target_groups")
			if got := findAppMetricByLabels(t, groups, nil).GetCounter().GetValue(); got != tt.groups {
				t.Fatalf("presence touch target groups = %v, want %v", got, tt.groups)
			}
		})
	}
}

func TestObservabilityPresenceTouchFlushReportsBudgetReached(t *testing.T) {
	reg := obsmetrics.New(1, "node-1")
	target := presence.RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local: &recordingTouchRegistry{queued: []online.OwnerRoute{
			{UID: "u1", SessionID: 101},
			{UID: "u2", SessionID: 102},
			{UID: "u3", SessionID: 103},
			{UID: "u4", SessionID: 104},
			{UID: "u5", SessionID: 105},
		}},
		Authority: &recordingTouchAuthority{targets: map[string]presence.RouteTarget{
			"u1": target,
			"u2": target,
			"u3": target,
			"u4": target,
			"u5": target,
		}},
		Observer:          presenceMetricsObserver{metrics: reg},
		BatchSize:         2,
		MaxRoutesPerFlush: 3,
	})

	worker.flushOnce(context.Background(), time.Now())

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	total := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_total")
	if got := findAppMetricByLabels(t, total, map[string]string{
		"result":         "success",
		"budget_reached": "true",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("budgeted presence touch flush total = %v, want 1", got)
	}
	routes := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_routes")
	for stage, want := range map[string]float64{
		"drained":  3,
		"resolved": 3,
		"sent":     3,
		"requeued": 0,
	} {
		if got := findAppMetricByLabels(t, routes, map[string]string{"stage": stage}).GetCounter().GetValue(); got != want {
			t.Fatalf("budgeted presence touch %s routes = %v, want %v", stage, got, want)
		}
	}
	chunks := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_chunks")
	if got := findAppMetricByLabels(t, chunks, nil).GetCounter().GetValue(); got != 2 {
		t.Fatalf("budgeted presence touch chunks = %v, want 2", got)
	}
	groups := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_target_groups")
	if got := findAppMetricByLabels(t, groups, nil).GetCounter().GetValue(); got != 2 {
		t.Fatalf("budgeted presence touch target groups = %v, want 2", got)
	}
}

func TestObservabilityPresenceTouchFlushReportsUnavailableDependency(t *testing.T) {
	tests := []struct {
		name      string
		local     presenceTouchLocalRegistry
		authority presenceTouchAuthority
	}{
		{name: "missing local registry", authority: &recordingTouchAuthority{}},
		{name: "missing authority client", local: &recordingTouchRegistry{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := obsmetrics.New(1, "node-1")
			worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
				Local:             tt.local,
				Authority:         tt.authority,
				Observer:          presenceMetricsObserver{metrics: reg},
				BatchSize:         2,
				MaxRoutesPerFlush: 3,
			})

			worker.flushOnce(context.Background(), time.Now())

			families, err := reg.Gather()
			if err != nil {
				t.Fatalf("Gather() error = %v", err)
			}
			total := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_total")
			if got := findAppMetricByLabels(t, total, map[string]string{
				"result":         "unavailable",
				"budget_reached": "false",
			}).GetCounter().GetValue(); got != 1 {
				t.Fatalf("unavailable presence touch flush total = %v, want 1", got)
			}
		})
	}
}

func TestObservabilityPresenceTouchFlushPreCanceledObservesOnceWithoutExpiry(t *testing.T) {
	reg := obsmetrics.New(1, "node-1")
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:             &recordingTouchRegistry{},
		Authority:         &recordingTouchAuthority{},
		Directory:         directory,
		Observer:          presenceMetricsObserver{metrics: reg},
		BatchSize:         2,
		MaxRoutesPerFlush: 3,
		RouteTTL:          90 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	worker.flushOnce(ctx, time.Now())

	if got := len(directory.expires); got != 0 {
		t.Fatalf("pre-canceled expiry calls = %d, want 0", got)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	total := requireAppMetricFamily(t, families, "wukongim_presence_touch_flush_total")
	if got := len(total.GetMetric()); got != 1 {
		t.Fatalf("pre-canceled touch result series = %d, want exactly 1", got)
	}
	if got := findAppMetricByLabels(t, total, map[string]string{
		"result":         "canceled",
		"budget_reached": "false",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("pre-canceled presence touch flush total = %v, want 1", got)
	}
	for _, family := range families {
		if family.GetName() == "wukongim_presence_expiry_total" && len(family.GetMetric()) > 0 {
			t.Fatalf("pre-canceled flush emitted %d expiry series, want 0", len(family.GetMetric()))
		}
	}
}

func TestObservabilityConversationSyncMetricsObserverMapsCounters(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := conversationSyncMetricsObserver{metrics: reg}

	observer.ObserveConversationSync(accessapi.ConversationSyncObservation{
		Result:             "ok",
		Duration:           15 * time.Millisecond,
		OnlyUnread:         true,
		WithRecents:        true,
		ReturnedItems:      4,
		OverlayItems:       2,
		RecentLoadDuration: 3 * time.Millisecond,
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	total := requireAppMetricFamily(t, families, "wukongim_conversation_sync_total")
	if got := findAppMetricByLabels(t, total, map[string]string{
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("conversation sync total metric = %v, want 1", got)
	}
	duration := requireAppMetricFamily(t, families, "wukongim_conversation_sync_duration_seconds")
	if got := findAppMetricByLabels(t, duration, map[string]string{
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetHistogram().GetSampleSum(); math.Abs(got-0.015) > 0.000001 {
		t.Fatalf("conversation sync duration metric = %v, want 0.015", got)
	}
	returned := requireAppMetricFamily(t, families, "wukongim_conversation_sync_returned_items")
	if got := findAppMetricByLabels(t, returned, map[string]string{
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetHistogram().GetSampleSum(); got != 4 {
		t.Fatalf("conversation sync returned items metric = %v, want 4", got)
	}
	overlay := requireAppMetricFamily(t, families, "wukongim_conversation_sync_overlay_items")
	if got := findAppMetricByLabels(t, overlay, map[string]string{
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetHistogram().GetSampleSum(); got != 2 {
		t.Fatalf("conversation sync overlay items metric = %v, want 2", got)
	}
	recentLoad := requireAppMetricFamily(t, families, "wukongim_conversation_sync_recent_load_duration_seconds")
	if got := findAppMetricByLabels(t, recentLoad, map[string]string{
		"result":      "ok",
		"only_unread": "true",
	}).GetHistogram().GetSampleSum(); math.Abs(got-0.003) > 0.000001 {
		t.Fatalf("conversation sync recent load duration metric = %v, want 0.003", got)
	}
}

func TestPluginHookMetricsObserverMapsPersistAfterCounters(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := pluginHookMetricsObserver{metrics: reg}

	observer.ObservePersistAfterEnqueue("accepted", 2*time.Millisecond)
	observer.ObservePersistAfterInvoke("ok", 3*time.Millisecond)
	observer.ObserveSendInvoke("reject", 4*time.Millisecond)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	enqueueTotal := requireAppMetricFamily(t, families, "wukongim_plugin_hook_enqueue_total")
	if got := findAppMetricByLabels(t, enqueueTotal, map[string]string{
		"method": "persist_after",
		"result": "accepted",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("plugin hook enqueue total = %v, want 1", got)
	}
	invokeTotal := requireAppMetricFamily(t, families, "wukongim_plugin_hook_invoke_total")
	if got := findAppMetricByLabels(t, invokeTotal, map[string]string{
		"method": "persist_after",
		"result": "ok",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("plugin hook invoke total = %v, want 1", got)
	}
	if got := findAppMetricByLabels(t, invokeTotal, map[string]string{
		"method": "send",
		"result": "reject",
	}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("plugin send hook invoke total = %v, want 1", got)
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

func TestChannelPullHintMetricLabels(t *testing.T) {
	reasonCases := []struct {
		name   string
		reason channeltransport.PullHintReason
		want   string
	}{
		{name: "append", reason: channeltransport.PullHintReasonAppend, want: "append"},
		{name: "resume", reason: channeltransport.PullHintReasonResume, want: "resume"},
		{name: "unknown", reason: channeltransport.PullHintReason(99), want: "unknown"},
	}
	for _, tc := range reasonCases {
		t.Run("reason_"+tc.name, func(t *testing.T) {
			if got := channelPullHintReasonLabel(tc.reason); got != tc.want {
				t.Fatalf("channelPullHintReasonLabel() = %q, want %q", got, tc.want)
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
			if got := channelPullHintErrorLabel(tc.err); got != tc.want {
				t.Fatalf("channelPullHintErrorLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChannelAppendWaitCancelLogLineIncludesSnapshotState(t *testing.T) {
	line := channelAppendWaitCancelLogLine(reactor.AppendWaitCancelSnapshot{
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
		"internal/app: channel append waiter canceled",
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
		{name: "cluster_not_started", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, cluster.ErrNotStarted), want: "not_started"},
		{name: "cluster_stopping", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, cluster.ErrStopping), want: "closed"},
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
		Cluster: cluster.Config{NodeID: 7},
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
		TraceID: "trace-internal-new",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-internal-new"})
	if result.Status != diagnostics.StatusOK {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusOK, result)
	}
	if result.NodeID != 7 {
		t.Fatalf("diagnostics node id = %d, want 7", result.NodeID)
	}
	if len(result.Events) != 1 || result.Events[0].TraceID != "trace-internal-new" {
		t.Fatalf("diagnostics events = %#v, want trace-internal-new", result.Events)
	}
}

func TestNewWiresDiagnosticsDebugAPI(t *testing.T) {
	cfg := Config{
		Cluster: cluster.Config{NodeID: 7},
		API:     APIConfig{ListenAddr: "127.0.0.1:0"},
		Observability: ObservabilityConfig{
			DebugAPIEnabled: true,
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
	api, ok := app.api.(interface{ Handler() http.Handler })
	if !ok {
		t.Fatalf("app api = %T, want Handler", app.api)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-debug-api",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/trace/trace-debug-api", nil)
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "trace-debug-api") {
		t.Fatalf("body = %s, want trace-debug-api", rec.Body.String())
	}
}

func TestNewWiresDebugSnapshotAPI(t *testing.T) {
	cfg := Config{
		NodeID: 7,
		API:    APIConfig{ListenAddr: "127.0.0.1:0"},
		Observability: ObservabilityConfig{
			DebugAPIEnabled: true,
		},
	}
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	api, ok := app.api.(interface{ Handler() http.Handler })
	if !ok {
		t.Fatalf("app api = %T, want Handler", app.api)
	}

	for _, path := range []string{"/debug/config", "/debug/cluster"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		api.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d body=%s", path, rec.Code, http.StatusOK, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"node_id":7`) {
			t.Fatalf("%s body = %s, want node_id 7", path, rec.Body.String())
		}
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
		TraceID: "trace-after-internal-stop",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-after-internal-stop"})
	if result.Status != diagnostics.StatusNotFound {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusNotFound, result)
	}
}

func TestNewRestoresDiagnosticsSinkOnConstructionError(t *testing.T) {
	previous := &recordingInternalSendTraceSink{}
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
		TraceID: "trace-disabled-internal",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-disabled-internal"})
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

func TestPrometheusConfigDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	app := &App{cfg: Config{
		NodeID:  7,
		DataDir: dir,
		API:     APIConfig{ListenAddr: "0.0.0.0:5001"},
		Observability: ObservabilityConfig{
			MetricsEnabled: true,
			Prometheus: PrometheusConfig{
				Enabled: true,
			},
		},
	}}

	if err := app.applyConfigDefaults(); err != nil {
		t.Fatalf("applyConfigDefaults() error = %v", err)
	}

	prom := app.cfg.Observability.Prometheus
	if prom.BinaryPath != "" {
		t.Fatalf("Prometheus.BinaryPath = %q, want empty embedded default", prom.BinaryPath)
	}
	if prom.ListenAddr != "127.0.0.1:9099" {
		t.Fatalf("Prometheus.ListenAddr = %q, want 127.0.0.1:9099", prom.ListenAddr)
	}
	if prom.DataDir != filepath.Join(dir, "prometheus") {
		t.Fatalf("Prometheus.DataDir = %q", prom.DataDir)
	}
	if prom.RetentionTime != 15*24*time.Hour {
		t.Fatalf("Prometheus.RetentionTime = %s, want 360h", prom.RetentionTime)
	}
	if prom.ScrapeInterval != 15*time.Second {
		t.Fatalf("Prometheus.ScrapeInterval = %s, want 15s", prom.ScrapeInterval)
	}
	if len(prom.ScrapeTargets) != 1 || prom.ScrapeTargets[0] != "127.0.0.1:5001" {
		t.Fatalf("Prometheus.ScrapeTargets = %#v, want 127.0.0.1:5001", prom.ScrapeTargets)
	}

	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "requires metrics",
			cfg: Config{
				API: APIConfig{ListenAddr: "127.0.0.1:5001"},
				Observability: ObservabilityConfig{
					Prometheus: PrometheusConfig{Enabled: true},
				},
			},
		},
		{
			name: "requires api listener",
			cfg: Config{
				Observability: ObservabilityConfig{
					MetricsEnabled: true,
					Prometheus:     PrometheusConfig{Enabled: true},
				},
			},
		},
		{
			name: "rejects target URL",
			cfg: Config{
				API: APIConfig{ListenAddr: "127.0.0.1:5001"},
				Observability: ObservabilityConfig{
					MetricsEnabled: true,
					Prometheus: PrometheusConfig{
						Enabled:       true,
						ScrapeTargets: []string{"http://127.0.0.1:5001"},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &App{cfg: tt.cfg}
			if err := app.applyConfigDefaults(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("applyConfigDefaults() error = %v, want %v", err, ErrInvalidConfig)
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

	requireAppLogEvent(t, logger, "WARN", "internal.app.delivery.retry_failed")
	requireAppLogEvent(t, logger, "WARN", "internal.app.delivery.manager_terminal_failed")
}

func TestDeliveryMessageObserverMapsRecipientDeliveryWorkerMetrics(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := deliveryMessageObserver{app: &App{metrics: reg}}

	observer.SetChannelAppendRecipientDeliveryQueue(channelappend.RecipientDeliveryQueueObservation{
		QueueDepth:    3,
		QueueCapacity: 8,
	})
	observer.SetChannelAppendRecipientDeliveryWorkerPressure(channelappend.RecipientDeliveryWorkerPressureObservation{
		Inflight: 2,
		Capacity: 4,
	})
	observer.ObserveChannelAppendRecipientDeliveryAdmission(channelappend.RecipientDeliveryAdmissionObservation{
		Result:   "timeout",
		Duration: 2 * time.Millisecond,
	})
	observer.ObserveChannelAppendRecipientDeliveryProcess(channelappend.RecipientDeliveryProcessObservation{
		Result:     "ok",
		Recipients: 4,
		Duration:   5 * time.Millisecond,
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	queueDepth := requireAppMetricFamily(t, families, "wukongim_delivery_recipient_worker_queue_depth")
	if got := findAppMetricByLabels(t, queueDepth, nil).GetGauge().GetValue(); got != 3 {
		t.Fatalf("recipient worker queue depth = %v, want 3", got)
	}
	workerInflight := requireAppMetricFamily(t, families, "wukongim_delivery_recipient_worker_inflight")
	if got := findAppMetricByLabels(t, workerInflight, nil).GetGauge().GetValue(); got != 2 {
		t.Fatalf("recipient worker inflight = %v, want 2", got)
	}
	workerCapacity := requireAppMetricFamily(t, families, "wukongim_delivery_recipient_worker_capacity")
	if got := findAppMetricByLabels(t, workerCapacity, nil).GetGauge().GetValue(); got != 4 {
		t.Fatalf("recipient worker capacity = %v, want 4", got)
	}
	admission := requireAppMetricFamily(t, families, "wukongim_delivery_recipient_worker_admission_total")
	if got := findAppMetricByLabels(t, admission, map[string]string{"result": "timeout"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("recipient worker admission total = %v, want 1", got)
	}
	process := requireAppMetricFamily(t, families, "wukongim_delivery_recipient_worker_process_recipients")
	if got := findAppMetricByLabels(t, process, map[string]string{"result": "ok"}).GetHistogram().GetSampleSum(); got != 4 {
		t.Fatalf("recipient worker process recipients = %v, want 4", got)
	}
}

func TestDeliveryMessageObserverMapsChannelAppendPostCommitPressure(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := deliveryMessageObserver{app: &App{metrics: reg}}

	observer.SetChannelAppendWriterPressure(channelappend.WriterPressureObservation{
		PostCommitHandoffDepth:    11,
		PostCommitHandoffCapacity: 17,
		PostCommitRetryQueueDepth: 3,
		PostCommitRetryContended:  true,
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	assertGauge := func(name string, want float64) {
		t.Helper()
		family := requireAppMetricFamily(t, families, name)
		if got := findAppMetricByLabels(t, family, nil).GetGauge().GetValue(); got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertGauge("wukongim_channelappend_post_commit_handoff_depth", 11)
	assertGauge("wukongim_channelappend_post_commit_handoff_capacity", 17)
	assertGauge("wukongim_channelappend_post_commit_retry_queue_depth", 3)
	assertGauge("wukongim_channelappend_post_commit_retry_contended", 1)
}

func TestDeliveryMessageObserverLogsChannelAppendPostCommitFailure(t *testing.T) {
	logger := &recordingAppLogger{}
	app := &App{logger: logger}
	observer := deliveryMessageObserver{app: app}

	observer.ObserveChannelAppendPostCommitFailure(channelappend.PostCommitFailureObservation{
		ChannelID:             "room",
		ChannelType:           2,
		MessageID:             42,
		MessageSeq:            7,
		Attempt:               1,
		Result:                "route_not_ready",
		Phase:                 "recipient_target_validate",
		UID:                   "u1",
		UIDCount:              2,
		RecipientCount:        3,
		TargetHashSlot:        9,
		TargetSlotID:          4,
		TargetLeaderNodeID:    0,
		TargetRouteRevision:   11,
		TargetAuthorityEpoch:  5,
		DispatchTargetCount:   1,
		DispatchBatchSize:     3,
		DispatchOwnerNodeID:   7,
		DispatchOwnerRouteNum: 2,
		Err:                   errors.New("route not ready"),
	})

	entry := requireAppLogEvent(t, logger, "ERROR", "internal.app.channelappend.post_commit_failed")
	requireAppLogField(t, entry, "phase", "recipient_target_validate")
	requireAppLogField(t, entry, "uid", "u1")
	requireAppLogField(t, entry, "uidCount", 2)
	requireAppLogField(t, entry, "recipientCount", 3)
	requireAppLogField(t, entry, "targetHashSlot", uint64(9))
	requireAppLogField(t, entry, "targetSlotID", uint64(4))
	requireAppLogField(t, entry, "targetLeaderNodeID", uint64(0))
	requireAppLogField(t, entry, "targetRouteRevision", uint64(11))
	requireAppLogField(t, entry, "targetAuthorityEpoch", uint64(5))
	requireAppLogField(t, entry, "dispatchTargetCount", 1)
	requireAppLogField(t, entry, "dispatchBatchSize", 3)
	requireAppLogField(t, entry, "dispatchOwnerNodeID", uint64(7))
	requireAppLogField(t, entry, "dispatchOwnerRouteNum", 2)
}

func TestDeliveryMessageObserverWarnsExpectedRoutePostCommitFailure(t *testing.T) {
	logger := &recordingAppLogger{}
	app := &App{logger: logger}
	observer := deliveryMessageObserver{app: app}

	observer.ObserveChannelAppendPostCommitFailure(channelappend.PostCommitFailureObservation{
		ChannelID:   "room",
		ChannelType: 2,
		MessageID:   42,
		MessageSeq:  7,
		Attempt:     1,
		Result:      "stale_route",
		Phase:       "conversation_active",
		Err:         fmt.Errorf("conversation active: %w", conversationusecase.ErrStaleRoute),
	})

	entry := requireAppLogEvent(t, logger, "WARN", "internal.app.channelappend.post_commit_failed")
	requireAppLogField(t, entry, "phase", "conversation_active")
	requireAppLogField(t, entry, "result", "stale_route")
	for _, logged := range logger.entries {
		if logged.level == "ERROR" {
			t.Fatalf("unexpected ERROR log for retryable post-commit route failure: %#v", logged)
		}
	}
}

func TestDeliveryMetricsObserverMapsAckEventToGauge(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := deliveryMetricsObserver{metrics: reg}

	observer.ObserveAck(runtimedelivery.AckEvent{PendingCount: 6})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	ackBindings := requireAppMetricFamily(t, families, "wukongim_delivery_ack_bindings")
	if got := findAppMetricByLabels(t, ackBindings, nil).GetGauge().GetValue(); got != 6 {
		t.Fatalf("delivery ack bindings = %v, want 6", got)
	}
}

func TestCombinedDeliveryObserverFansOutAckEvents(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() cluster.Snapshot {
			return cluster.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := combineDeliveryObservers(
		deliveryMetricsObserver{metrics: reg},
		topDeliveryObserver{top: collector},
	)
	ackObserver, ok := observer.(runtimedelivery.AckObserver)
	if !ok {
		t.Fatalf("combined observer does not implement AckObserver")
	}

	collector.recordSampleAt(time.Unix(100, 0))
	ackObserver.ObserveAck(runtimedelivery.AckEvent{PendingCount: 9})
	collector.recordSampleAt(time.Unix(110, 0))

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	ackBindings := requireAppMetricFamily(t, families, "wukongim_delivery_ack_bindings")
	if got := findAppMetricByLabels(t, ackBindings, nil).GetGauge().GetValue(); got != 9 {
		t.Fatalf("metrics ack bindings = %v, want 9", got)
	}
	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewDelivery,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Delivery == nil || snapshot.Delivery.AckBindings != 9 {
		t.Fatalf("top ack bindings = %#v, want 9", snapshot.Delivery)
	}
}

type recordingInternalSendTraceSink struct {
	events []sendtrace.Event
}

func (s *recordingInternalSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}

func (s *recordingInternalSendTraceSink) snapshot() []sendtrace.Event {
	return append([]sendtrace.Event(nil), s.events...)
}

func requireAppLogEvent(t *testing.T, logger *recordingAppLogger, level, event string) recordedAppLogEntry {
	t.Helper()
	for _, entry := range logger.entries {
		if entry.level != level {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return entry
			}
		}
	}
	t.Fatalf("missing app log event level=%s event=%s entries=%#v", level, event, logger.entries)
	return recordedAppLogEntry{}
}

func requireAppLogField(t *testing.T, entry recordedAppLogEntry, key string, want any) {
	t.Helper()
	for _, field := range entry.fields {
		if field.Key == key {
			if !reflect.DeepEqual(field.Value, want) {
				t.Fatalf("log field %s = %#v, want %#v", key, field.Value, want)
			}
			return
		}
	}
	t.Fatalf("missing log field %s entries=%#v", key, entry.fields)
}
