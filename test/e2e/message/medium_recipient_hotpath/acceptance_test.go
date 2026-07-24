//go:build e2e

package medium_recipient_hotpath

import (
	"strings"
	"testing"
)

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
