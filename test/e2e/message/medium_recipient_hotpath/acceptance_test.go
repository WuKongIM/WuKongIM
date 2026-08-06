//go:build e2e

package medium_recipient_hotpath

import (
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

func TestPermissionSoakConfigFromEnv(t *testing.T) {
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK", "1")
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION", "")
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_QPS", "")
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS", "")

	config, err := permissionSoakConfigFromEnv()
	if err != nil {
		t.Fatalf("permissionSoakConfigFromEnv(): %v", err)
	}
	if !config.enabled || config.duration != 30*time.Minute || config.offeredQPS != 4_500 || config.groupChannels != 5_000 {
		t.Fatalf("default soak config = %+v, want enabled 30m/4500 QPS/5000 channels", config)
	}

	t.Run("bounded smoke override", func(t *testing.T) {
		t.Setenv("WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION", "10s")
		t.Setenv("WK_E2E_MEDIUM_RECIPIENT_QPS", "5000")
		t.Setenv("WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS", "100")
		config, err := permissionSoakConfigFromEnv()
		if err != nil {
			t.Fatalf("permissionSoakConfigFromEnv(): %v", err)
		}
		if config.duration != 10*time.Second || config.offeredQPS != 5_000 || config.groupChannels != 100 {
			t.Fatalf("smoke config = %+v, want 10s/5000 QPS/100 channels", config)
		}
	})

	for _, test := range []struct {
		name     string
		duration string
		channels string
		want     string
	}{
		{name: "duration below bound", duration: "9s", channels: "100", want: "duration"},
		{name: "duration above bound", duration: "31m", channels: "100", want: "duration"},
		{name: "channels must cover senders", duration: "10s", channels: "20", want: "GROUP_CHANNELS"},
		{name: "channels align to senders", duration: "10s", channels: "51", want: "multiple"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION", test.duration)
			t.Setenv("WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS", test.channels)
			_, err := permissionSoakConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPermissionSoakAcceptanceError(t *testing.T) {
	config := permissionSoakConfig{
		enabled:       true,
		duration:      10 * time.Second,
		offeredQPS:    mediumOfferedQPS,
		groupChannels: 100,
	}
	passing := permissionSoakEvidence{
		Schema:                         mediumPermissionSoakEvidenceSchema,
		ConfiguredDurationMS:           milliseconds(config.duration),
		SendLoopDurationMS:             milliseconds(config.duration),
		MeasuredDurationMS:             milliseconds(config.duration + time.Second),
		Messages:                       45_000,
		GroupChannels:                  100,
		ActiveGroupChannels:            100,
		Senders:                        mediumSenderConnections,
		Recipients:                     mediumSenderConnections,
		OfferedQPS:                     mediumOfferedQPS,
		IngressPerSecond:               mediumOfferedQPS * mediumCIMinIngressFraction,
		CompletionPerSecond:            4_000,
		SendackP99MS:                   900,
		RecvP99MS:                      1_500,
		TransportRPCMetricNodes:        mediumReplicaCount,
		MaxTransportRPCQueueRatio:      0.99,
		MaxTransportRPCBusyRatio:       0.99,
		PermissionSlotRPCCalls:         1,
		MaxPermissionSlotRPCQueueRatio: 0.25,
		PermissionBatchStarted:         1,
		MaxPermissionBatchActive:       15,
		MaxHeapBytes:                   256 << 20,
		MaxAggregateHeapBytes:          768 << 20,
		AllocatedBytes:                 45_000 * 350_000,
		GCCountDelta:                   100,
		PluginReceiveAccepted:          45_000,
		PluginReceiveInvokeOK:          45_000,
		MetricSamples:                  1,
		Drained:                        true,
		ProcessContinuous:              true,
	}
	if err := permissionSoakAcceptanceError(passing, config); err != nil {
		t.Fatalf("passing soak evidence rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*permissionSoakEvidence)
		want string
	}{
		{name: "messages", edit: func(e *permissionSoakEvidence) { e.Messages-- }, want: "messages"},
		{name: "duration", edit: func(e *permissionSoakEvidence) { e.SendLoopDurationMS *= 0.9 }, want: "send loop duration"},
		{name: "active channels", edit: func(e *permissionSoakEvidence) { e.ActiveGroupChannels-- }, want: "active group channels"},
		{name: "ingress", edit: func(e *permissionSoakEvidence) { e.IngressPerSecond-- }, want: "ingress"},
		{name: "sendack", edit: func(e *permissionSoakEvidence) { e.SendackP99MS = 1_001 }, want: "SENDACK P99"},
		{name: "recv", edit: func(e *permissionSoakEvidence) { e.RecvP99MS = 2_001 }, want: "RECV P99"},
		{name: "transport metrics", edit: func(e *permissionSoakEvidence) { e.TransportRPCMetricNodes-- }, want: "transport RPC metric nodes"},
		{name: "transport queue", edit: func(e *permissionSoakEvidence) { e.MaxTransportRPCQueueRatio = 1 }, want: "transport RPC queue"},
		{name: "transport busy", edit: func(e *permissionSoakEvidence) { e.MaxTransportRPCBusyRatio = 1 }, want: "transport RPC busy"},
		{name: "transport rejected", edit: func(e *permissionSoakEvidence) { e.TransportRPCRejected = 1 }, want: "transport RPC rejected"},
		{name: "permission Slot RPC missing", edit: func(e *permissionSoakEvidence) { e.PermissionSlotRPCCalls = 0 }, want: "permission Slot RPC calls"},
		{name: "permission Slot RPC error", edit: func(e *permissionSoakEvidence) { e.PermissionSlotRPCErrors = 1 }, want: "permission Slot RPC errors"},
		{name: "permission Slot RPC queue saturated", edit: func(e *permissionSoakEvidence) { e.MaxPermissionSlotRPCQueueRatio = 1 }, want: "permission Slot RPC queue ratio"},
		{name: "permission Slot RPC admission error", edit: func(e *permissionSoakEvidence) { e.PermissionSlotRPCAdmissionErrors = 1 }, want: "permission Slot RPC admission errors"},
		{name: "permission workers", edit: func(e *permissionSoakEvidence) { e.PermissionBatchStarted = 0 }, want: "permission batch started"},
		{name: "permission panic", edit: func(e *permissionSoakEvidence) { e.PermissionBatchPanics = 1 }, want: "permission batch panics"},
		{name: "membership mutation", edit: func(e *permissionSoakEvidence) { e.MembershipMutationRows = 1 }, want: "membership mutation"},
		{name: "plugin full", edit: func(e *permissionSoakEvidence) { e.PluginReceiveFull = 1 }, want: "plugin receive enqueue"},
		{name: "plugin invoke", edit: func(e *permissionSoakEvidence) { e.PluginReceiveInvokeOK-- }, want: "plugin receive invoke"},
		{name: "heap", edit: func(e *permissionSoakEvidence) { e.MaxHeapBytes = mediumMaxHeapBytes + 1 }, want: "max heap"},
		{name: "aggregate heap", edit: func(e *permissionSoakEvidence) { e.MaxAggregateHeapBytes = 3*mediumMaxHeapBytes + 1 }, want: "aggregate heap"},
		{name: "pending", edit: func(e *permissionSoakEvidence) { e.PendingMessages = 1 }, want: "pending messages"},
		{name: "samples", edit: func(e *permissionSoakEvidence) { e.MetricSamples = 0 }, want: "metric samples"},
		{name: "drain", edit: func(e *permissionSoakEvidence) { e.Drained = false }, want: "did not drain"},
		{name: "continuity", edit: func(e *permissionSoakEvidence) { e.ProcessContinuous = false }, want: "continuity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := passing
			test.edit(&evidence)
			err := permissionSoakAcceptanceError(evidence, config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestHotPathAcceptanceError(t *testing.T) {
	passing := hotPathEvidence{
		Schema:                   mediumEvidenceSchema,
		PhysicalHashSlots:        mediumPhysicalHashSlots,
		LogicalSlots:             mediumLogicalSlots,
		Replicas:                 mediumReplicaCount,
		SlotTickIntervalMS:       milliseconds(mediumSlotTickInterval),
		SlotHeartbeatTick:        mediumSlotHeartbeatTick,
		SlotElectionTick:         mediumSlotElectionTick,
		Messages:                 mediumMessageCount * mediumMeasuredRounds,
		RecipientRows:            mediumRecipientRows * mediumMeasuredRounds,
		OnlineRoutes:             expectedMeasuredOnlineRoutes(mediumMeasuredRounds),
		Connections:              expectedConnectionCount(),
		GroupChannels:            mediumGroupChannelCount,
		ActiveGroupChannels:      expectedActiveGroupChannels(mediumGroupChannelCount, mediumMeasuredRounds),
		OfferedQPS:               mediumOfferedQPS,
		ClusterConvergenceMS:     2_500,
		ClusterStableWindowMS:    milliseconds(mediumConvergenceStableWindow),
		SlotLeaders:              []uint64{1, 2, 3, 1, 2, 3, 1, 2, 3, 1},
		MeasuredDurationMS:       float64(mediumMessageCount*mediumMeasuredRounds) / mediumOfferedQPS * 1000,
		IngressPerSecond:         mediumOfferedQPS,
		SendackP99MS:             1_000,
		RecvP99MS:                2_000,
		MaxGatewayQueueRatio:     0.99,
		MaxRecipientQueueRatio:   0.99,
		MaxRecipientWorkerRatio:  0.99,
		ChannelRPCMetricNodes:    mediumReplicaCount,
		MinChannelRPCWorkers:     mediumChannelRPCWorkers,
		MaxChannelRPCWorkers:     mediumChannelRPCWorkers,
		ChannelRPCBatchMaxItems:  mediumChannelRPCBatchMaxItems,
		ChannelRPCPullBatches:    1,
		ChannelRPCPullBatchItems: 2,
		ChannelRPCHintBatches:    1,
		ChannelRPCHintBatchItems: 2,
		PluginReceiveAccepted:    float64(pluginReceiveBatchCount() * mediumMeasuredRounds),
		PluginReceiveInvokeOK:    float64(pluginReceiveBatchCount() * mediumMeasuredRounds),
		AllocatedBytes:           float64(mediumMessageCount*mediumMeasuredRounds) * 350_000,
		GCCountDelta:             100,
		MaxHeapBytes:             256 << 20,
		MaxAggregateHeapBytes:    768 << 20,
		MetricSamples:            1,
		Drained:                  true,
		ProcessContinuous:        true,
	}
	if err := hotPathAcceptanceError(passing, mediumOfferedQPS, mediumMeasuredRounds); err != nil {
		t.Fatalf("passing evidence rejected: %v", err)
	}

	t.Run("bounded rounds use their own acceptance totals", func(t *testing.T) {
		const rounds = 20
		evidence := passing
		evidence.Messages = mediumMessageCount * rounds
		evidence.RecipientRows = mediumRecipientRows * rounds
		evidence.OnlineRoutes = expectedMeasuredOnlineRoutes(rounds)
		evidence.ActiveGroupChannels = expectedActiveGroupChannels(evidence.GroupChannels, rounds)
		evidence.MeasuredDurationMS = float64(evidence.Messages) / mediumOfferedQPS * 1000
		evidence.PluginReceiveAccepted = float64(pluginReceiveBatchCount() * rounds)
		evidence.PluginReceiveInvokeOK = evidence.PluginReceiveAccepted
		evidence.AllocatedBytes = float64(evidence.Messages) * 350_000
		evidence.GCCountDelta = 25
		if err := hotPathAcceptanceError(evidence, mediumOfferedQPS, rounds); err != nil {
			t.Fatalf("bounded-round evidence rejected: %v", err)
		}
	})

	tests := []struct {
		name string
		edit func(*hotPathEvidence)
		want string
	}{
		{name: "schema", edit: func(e *hotPathEvidence) { e.Schema = "other" }, want: "acceptance schema"},
		{name: "physical hash slots", edit: func(e *hotPathEvidence) { e.PhysicalHashSlots-- }, want: "physical hash slots"},
		{name: "logical slots", edit: func(e *hotPathEvidence) { e.LogicalSlots-- }, want: "logical slots"},
		{name: "replicas", edit: func(e *hotPathEvidence) { e.Replicas-- }, want: "acceptance replicas"},
		{name: "slot tick interval", edit: func(e *hotPathEvidence) { e.SlotTickIntervalMS-- }, want: "Slot tick interval"},
		{name: "slot heartbeat tick", edit: func(e *hotPathEvidence) { e.SlotHeartbeatTick-- }, want: "Slot heartbeat tick"},
		{name: "slot election tick", edit: func(e *hotPathEvidence) { e.SlotElectionTick-- }, want: "Slot election tick"},
		{name: "messages", edit: func(e *hotPathEvidence) { e.Messages-- }, want: "acceptance messages"},
		{name: "recipient rows", edit: func(e *hotPathEvidence) { e.RecipientRows-- }, want: "recipient rows"},
		{name: "online routes", edit: func(e *hotPathEvidence) { e.OnlineRoutes-- }, want: "online routes"},
		{name: "connections", edit: func(e *hotPathEvidence) { e.Connections-- }, want: "acceptance connections"},
		{name: "group channels", edit: func(e *hotPathEvidence) { e.GroupChannels = 0 }, want: "acceptance group channels"},
		{name: "active group channels", edit: func(e *hotPathEvidence) { e.ActiveGroupChannels-- }, want: "acceptance active group channels"},
		{name: "offered load", edit: func(e *hotPathEvidence) { e.OfferedQPS-- }, want: "offered QPS"},
		{name: "cluster convergence missing", edit: func(e *hotPathEvidence) { e.ClusterConvergenceMS = 0 }, want: "cluster convergence"},
		{name: "cluster stability short", edit: func(e *hotPathEvidence) { e.ClusterStableWindowMS-- }, want: "cluster stable window"},
		{name: "actual slot leader missing", edit: func(e *hotPathEvidence) { e.SlotLeaders[0] = 0 }, want: "actual Slot leaders"},
		{name: "actual slot leader count", edit: func(e *hotPathEvidence) { e.SlotLeaders = e.SlotLeaders[:9] }, want: "actual Slot leaders"},
		{name: "actual slot leader skew missing", edit: func(e *hotPathEvidence) {
			e.SlotLeaders[2] = 2
		}, want: "actual Slot leaders"},
		{name: "ingress", edit: func(e *hotPathEvidence) {
			e.IngressPerSecond = mediumOfferedQPS - 0.001
		}, want: "acceptance ingress"},
		{name: "sendack", edit: func(e *hotPathEvidence) { e.SendackP99MS++ }, want: "SENDACK P99"},
		{name: "recv", edit: func(e *hotPathEvidence) { e.RecvP99MS++ }, want: "RECV P99"},
		{name: "gateway queue", edit: func(e *hotPathEvidence) { e.MaxGatewayQueueRatio = 1 }, want: "gateway queue"},
		{name: "recipient queue", edit: func(e *hotPathEvidence) { e.MaxRecipientQueueRatio = 1 }, want: "recipient queue"},
		{name: "recipient worker", edit: func(e *hotPathEvidence) { e.MaxRecipientWorkerRatio = 1 }, want: "recipient worker"},
		{name: "Channel RPC metrics missing", edit: func(e *hotPathEvidence) { e.ChannelRPCMetricNodes-- }, want: "Channel RPC metric nodes"},
		{name: "Channel RPC worker drift", edit: func(e *hotPathEvidence) { e.MinChannelRPCWorkers-- }, want: "Channel RPC workers"},
		{name: "Channel RPC batch drift", edit: func(e *hotPathEvidence) { e.ChannelRPCBatchMaxItems-- }, want: "Channel RPC batch max items"},
		{name: "Channel RPC admission full", edit: func(e *hotPathEvidence) { e.ChannelRPCAdmissionFull = 1 }, want: "Channel RPC full admissions"},
		{name: "Channel RPC Pull batch missing", edit: func(e *hotPathEvidence) { e.ChannelRPCPullBatches = 0 }, want: "Channel RPC Pull batch evidence"},
		{name: "Channel RPC PullHint batch missing", edit: func(e *hotPathEvidence) { e.ChannelRPCHintBatches = 0 }, want: "Channel RPC PullHint batch evidence"},
		{name: "Channel RPC queue", edit: func(e *hotPathEvidence) { e.MaxChannelRPCQueueRatio = 1 }, want: "Channel RPC queue"},
		{name: "Channel RPC worker", edit: func(e *hotPathEvidence) { e.MaxChannelRPCWorkerRatio = 1 }, want: "Channel RPC worker"},
		{name: "membership mutation", edit: func(e *hotPathEvidence) { e.MembershipMutationRows = 1 }, want: "membership mutation rows"},
		{name: "plugin accepted", edit: func(e *hotPathEvidence) { e.PluginReceiveAccepted-- }, want: "plugin receive accepted"},
		{name: "plugin full", edit: func(e *hotPathEvidence) { e.PluginReceiveFull = 1 }, want: "enqueue non-accepted"},
		{name: "plugin invoke", edit: func(e *hotPathEvidence) { e.PluginReceiveInvokeOK-- }, want: "plugin receive invoke"},
		{name: "recipient process", edit: func(e *hotPathEvidence) { e.RecipientProcessError = 1 }, want: "recipient worker process errors"},
		{name: "measured duration missing", edit: func(e *hotPathEvidence) { e.MeasuredDurationMS = 0 }, want: "measured duration"},
		{name: "allocated missing", edit: func(e *hotPathEvidence) { e.AllocatedBytes = 0 }, want: "allocated bytes"},
		{name: "allocated regression", edit: func(e *hotPathEvidence) {
			e.AllocatedBytes = maxAcceptedAllocatedBytes(*e) + 1
		}, want: "allocated bytes/message"},
		{name: "gc missing", edit: func(e *hotPathEvidence) { e.GCCountDelta = 0 }, want: "GC count delta"},
		{name: "gc regression", edit: func(e *hotPathEvidence) { e.GCCountDelta = float64(e.Messages)*mediumMaxGCPerMessage + 1 }, want: "GC/message"},
		{name: "heap missing", edit: func(e *hotPathEvidence) { e.MaxHeapBytes = 0 }, want: "max heap bytes"},
		{name: "heap regression", edit: func(e *hotPathEvidence) { e.MaxHeapBytes = mediumMaxHeapBytes + 1 }, want: "max heap bytes"},
		{name: "samples", edit: func(e *hotPathEvidence) { e.MetricSamples = 0 }, want: "no public metric"},
		{name: "sample errors", edit: func(e *hotPathEvidence) { e.MetricSampleErrors = 1 }, want: "sample errors"},
		{name: "drain", edit: func(e *hotPathEvidence) { e.Drained = false }, want: "did not drain"},
		{name: "continuity", edit: func(e *hotPathEvidence) { e.ProcessContinuous = false }, want: "continuity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := passing
			evidence.SlotLeaders = append([]uint64(nil), passing.SlotLeaders...)
			test.edit(&evidence)
			err := hotPathAcceptanceError(evidence, mediumOfferedQPS, mediumMeasuredRounds)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	t.Run("CI scaled pacing tolerance", func(t *testing.T) {
		evidence := passing
		evidence.OfferedQPS = mediumCIAcceptanceQPS
		evidence.MeasuredDurationMS = float64(evidence.Messages) / mediumCIAcceptanceQPS * 1000
		evidence.IngressPerSecond = float64(mediumCIAcceptanceQPS) * mediumCIMinIngressFraction
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err != nil {
			t.Fatalf("CI-scaled evidence rejected: %v", err)
		}
		evidence.IngressPerSecond = float64(mediumCIAcceptanceQPS)*mediumCIMinIngressFraction - 0.001
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err == nil || !strings.Contains(err.Error(), "acceptance ingress") {
			t.Fatalf("below-tolerance ingress error = %v, want acceptance ingress", err)
		}
	})

	t.Run("overdrive proves target margin", func(t *testing.T) {
		evidence := passing
		evidence.OfferedQPS = mediumOfferedQPS + 500
		evidence.IngressPerSecond = mediumOfferedQPS + 250
		if err := hotPathAcceptanceError(evidence, mediumOfferedQPS, mediumMeasuredRounds); err != nil {
			t.Fatalf("overdrive evidence rejected: %v", err)
		}
	})

	t.Run("allocation allowance uses paced duration", func(t *testing.T) {
		evidence := passing
		evidence.OfferedQPS = mediumCIAcceptanceQPS
		evidence.IngressPerSecond = mediumCIAcceptanceQPS
		evidence.MeasuredDurationMS = float64(evidence.Messages) / mediumCIAcceptanceQPS * 2000
		wantPerMessage := float64(440_000)
		if got := maxAcceptedAllocatedBytes(evidence) / float64(evidence.Messages); got != wantPerMessage {
			t.Fatalf("CI allocation allowance = %.0f bytes/message, want %.0f", got, wantPerMessage)
		}
		evidence.AllocatedBytes = maxAcceptedAllocatedBytes(evidence)
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err != nil {
			t.Fatalf("bounded CI allocation rejected: %v", err)
		}
		evidence.AllocatedBytes++
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err == nil || !strings.Contains(err.Error(), "allocated bytes/message") {
			t.Fatalf("slow-drain allocation error = %v, want allocated bytes/message", err)
		}
	})

}

func TestPrimePersonMessagesUseMeasuredSenders(t *testing.T) {
	personUIDs := make([]string, mediumSenderConnections*2)
	for index := range personUIDs {
		personUIDs[index] = "person"
	}
	messages := buildPrimeMessages(nil, personUIDs)
	for index, message := range messages {
		want := mediumSenderUID(index % mediumSenderConnections)
		if got := primeSenderUID(message); got != want {
			t.Fatalf("prime sender at person message %d = %q, want measured sender %q", index, got, want)
		}
	}
}

func TestPressureSamplerObservesCompleteClusterAggregateHeap(t *testing.T) {
	sampler := &pressureSampler{}
	sampler.observeAggregateHeap([]hotPathMetricValues{
		{heapBytes: 100},
		{heapBytes: 200},
		{heapBytes: 300},
	})
	if got := sampler.state.maxAggregateHeapBytes; got != 600 {
		t.Fatalf("aggregate heap = %.0f, want 600", got)
	}
	sampler.observeAggregateHeap([]hotPathMetricValues{
		{heapBytes: 50},
		{heapBytes: 50},
		{heapBytes: 50},
	})
	if got := sampler.state.maxAggregateHeapBytes; got != 600 {
		t.Fatalf("aggregate heap peak regressed to %.0f, want 600", got)
	}
}

func TestPressureSamplerObservesPermissionSoakMetrics(t *testing.T) {
	transportLabels := map[string]string{"module": "transport", "task": "rpc_executor", "kind": "pool"}
	permissionLabels := map[string]string{"module": "message", "task": "permission_batch", "kind": "burst"}
	values := metricValues([]suite.MetricSample{
		{Name: "wukongim_goroutine_pool_busy_tasks", Labels: transportLabels, Value: 8},
		{Name: "wukongim_goroutine_pool_capacity", Labels: transportLabels, Value: 16},
		{Name: "wukongim_goroutine_pool_queue_depth", Labels: transportLabels, Value: 4},
		{Name: "wukongim_goroutine_pool_queue_capacity", Labels: transportLabels, Value: 32},
		{Name: "wukongim_goroutines_active", Labels: permissionLabels, Value: 7},
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot channel metadata", "priority": "none"}, Value: 3},
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot subscriber metadata", "priority": "none"}, Value: 5},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot channel metadata", "priority": "none"}, Value: 16},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot subscriber metadata", "priority": "none"}, Value: 16},
		{Name: "wukongim_runtime_pool_inflight", Labels: map[string]string{"component": "transport", "pool": "slot channel metadata"}, Value: 3},
		{Name: "wukongim_runtime_pool_inflight", Labels: map[string]string{"component": "transport", "pool": "slot subscriber metadata"}, Value: 4},
	})
	if !values.transportRPCMetricsPresent {
		t.Fatal("transport RPC pool metrics not detected")
	}
	if values.transportRPCBusy != 8 || values.transportRPCCapacity != 16 || values.transportRPCQueueDepth != 4 || values.transportRPCQueueCapacity != 32 {
		t.Fatalf("transport RPC values = %+v", values)
	}
	if values.permissionBatchActive != 7 {
		t.Fatalf("permission batch active = %.0f, want 7", values.permissionBatchActive)
	}
	if values.permissionSlotRPCInflight != 7 {
		t.Fatalf("permission Slot RPC inflight = %.0f, want 7", values.permissionSlotRPCInflight)
	}
	if values.permissionSlotRPCQueueDepth != 8 || values.permissionSlotRPCQueueCapacity != 32 {
		t.Fatalf("permission Slot RPC queue = %.0f/%.0f, want 8/32", values.permissionSlotRPCQueueDepth, values.permissionSlotRPCQueueCapacity)
	}

	sampler := &pressureSampler{}
	sampler.observeValues(values)
	if sampler.state.maxTransportRPCBusyRatio != 0.5 || sampler.state.maxTransportRPCQueueRatio != 0.125 {
		t.Fatalf("transport RPC pressure = %+v, want busy 0.5 queue 0.125", sampler.state)
	}
	if sampler.state.maxPermissionBatchActive != 7 {
		t.Fatalf("permission batch peak = %.0f, want 7", sampler.state.maxPermissionBatchActive)
	}
	if sampler.state.maxPermissionSlotRPCInflight != 7 {
		t.Fatalf("permission Slot RPC inflight peak = %.0f, want 7", sampler.state.maxPermissionSlotRPCInflight)
	}
	if sampler.state.maxPermissionSlotRPCQueueRatio != 0.25 {
		t.Fatalf("permission Slot RPC queue ratio = %.2f, want 0.25", sampler.state.maxPermissionSlotRPCQueueRatio)
	}
}

func TestHotPathCountersObservePermissionSlotRPCServerMetrics(t *testing.T) {
	var counters hotPathCounters
	for _, sample := range []suite.MetricSample{
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "slot channel metadata", "result": "ok"}, Value: 20},
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "slot channel metadata", "result": "error"}, Value: 2},
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "slot subscriber metadata", "result": "ok"}, Value: 40},
		{Name: "wukongim_runtime_pool_admission_total", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot channel metadata", "priority": "none", "result": "busy"}, Value: 3},
		{Name: "wukongim_runtime_pool_admission_total", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot subscriber metadata", "priority": "none", "result": "busy"}, Value: 2},
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "other", "result": "error"}, Value: 100},
	} {
		observeHotPathCounterSample(&counters, sample)
	}
	if counters.permissionSlotRPCCalls != 62 || counters.permissionSlotRPCErrors != 2 {
		t.Fatalf("permission Slot RPC counters = calls %.0f errors %.0f, want 62/2", counters.permissionSlotRPCCalls, counters.permissionSlotRPCErrors)
	}
	if counters.permissionSlotRPCAdmissionErrors != 5 {
		t.Fatalf("permission Slot RPC admission errors = %.0f, want 5", counters.permissionSlotRPCAdmissionErrors)
	}
}

func TestPermissionSoakChannelIDsIncludeLocalAndRemoteSlotLeaders(t *testing.T) {
	hashSlotLeaders := make([]uint64, mediumPhysicalHashSlots)
	for hashSlot := range hashSlotLeaders {
		hashSlotLeaders[hashSlot] = uint64(hashSlot%mediumReplicaCount + 1)
	}
	channels, err := permissionSoakChannelIDs(mediumSenderConnections*4, hashSlotLeaders)
	if err != nil {
		t.Fatalf("build permission soak channels: %v", err)
	}
	if len(channels) != mediumSenderConnections*4 {
		t.Fatalf("channels = %d, want %d", len(channels), mediumSenderConnections*4)
	}
	localChannels := 0
	remoteChannels := 0
	for index, channelID := range channels {
		ingressNodeID := uint64(index%mediumSenderConnections%mediumReplicaCount + 1)
		hashSlot := permissionSoakChannelHashSlot(channelID)
		if leaderID := hashSlotLeaders[hashSlot]; leaderID == ingressNodeID {
			localChannels++
		} else {
			remoteChannels++
		}
	}
	if localChannels == 0 || remoteChannels == 0 {
		t.Fatalf("permission soak route mix = local %d remote %d, want both positive", localChannels, remoteChannels)
	}
}

func TestPermissionSoakLatencyAndInflightTrackingStayBounded(t *testing.T) {
	histogram := newBoundedLatencyHistogram()
	for _, latency := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 100 * time.Millisecond, 20 * time.Second} {
		histogram.observe(latency)
	}
	if got := histogram.percentile(0.50); got != 2*time.Millisecond {
		t.Fatalf("P50 = %s, want 2ms", got)
	}
	if got := histogram.percentile(0.99); got != mediumPermissionSoakMaxLatency {
		t.Fatalf("P99 = %s, want bounded %s", got, mediumPermissionSoakMaxLatency)
	}
	if got := histogram.maximum(); got != 20*time.Second {
		t.Fatalf("max = %s, want exact 20s", got)
	}

	tracker := newPermissionSoakTracker()
	tracker.begin("message-1", 2)
	if got := tracker.pending.Load(); got != 1 {
		t.Fatalf("pending after begin = %d, want 1", got)
	}
	if _, err := tracker.observeSendack("message-1"); err != nil {
		t.Fatalf("observe sendack: %v", err)
	}
	if _, err := tracker.observeRecv("message-1"); err != nil {
		t.Fatalf("observe first recv: %v", err)
	}
	if _, err := tracker.observeRecv("message-1"); err != nil {
		t.Fatalf("observe second recv: %v", err)
	}
	if got := tracker.pending.Load(); got != 0 {
		t.Fatalf("pending after all observations = %d, want 0", got)
	}
	if _, err := tracker.observeRecv("message-1"); err == nil || !strings.Contains(err.Error(), "no send start") {
		t.Fatalf("late observation error = %v, want no send start", err)
	}
}

func TestScaleGroupChannelCounts(t *testing.T) {
	tests := []struct {
		total int
		want  []int
	}{
		{total: mediumGroupChannelCount, want: []int{1, 1, 1, 1}},
		{total: mediumCloudGroupChannelCount, want: []int{3_321, 1_186, 237, 256}},
	}
	for _, test := range tests {
		got := scaleGroupChannelCounts(test.total)
		if len(got) != len(test.want) {
			t.Fatalf("total %d counts = %v, want %v", test.total, got, test.want)
		}
		sum := 0
		for index := range got {
			sum += got[index]
			if got[index] != test.want[index] {
				t.Fatalf("total %d counts = %v, want %v", test.total, got, test.want)
			}
		}
		if sum != test.total {
			t.Fatalf("total %d counts sum = %d, want %d", test.total, sum, test.total)
		}
	}
}

func TestBoundedPositiveEnvInt(t *testing.T) {
	const name = "WK_E2E_MEDIUM_RECIPIENT_TEST_VALUE"
	t.Setenv(name, "")
	if got := boundedPositiveEnvInt(t, name, 80, 1, 200); got != 80 {
		t.Fatalf("fallback = %d, want 80", got)
	}
	t.Setenv(name, "120")
	if got := boundedPositiveEnvInt(t, name, 80, 1, 200); got != 120 {
		t.Fatalf("parsed = %d, want 120", got)
	}
}
