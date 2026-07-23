//go:build e2e

package medium_recipient_hotpath

import (
	"strings"
	"testing"
)

func TestHotPathAcceptanceError(t *testing.T) {
	passing := hotPathEvidence{
		Schema:                  mediumEvidenceSchema,
		PhysicalHashSlots:       mediumPhysicalHashSlots,
		LogicalSlots:            mediumLogicalSlots,
		Replicas:                mediumReplicaCount,
		Messages:                mediumMessageCount * mediumMeasuredRounds,
		RecipientRows:           mediumRecipientRows * mediumMeasuredRounds,
		OnlineRoutes:            expectedMeasuredOnlineRoutes(),
		Connections:             expectedConnectionCount(),
		OfferedQPS:              mediumOfferedQPS,
		IngressPerSecond:        mediumOfferedQPS,
		SendackP99MS:            1_000,
		RecvP99MS:               2_000,
		MaxGatewayQueueRatio:    0.99,
		MaxRecipientQueueRatio:  0.99,
		MaxRecipientWorkerRatio: 0.99,
		PluginReceiveAccepted:   float64(pluginReceiveBatchCount() * mediumMeasuredRounds),
		PluginReceiveInvokeOK:   float64(pluginReceiveBatchCount() * mediumMeasuredRounds),
		AllocatedBytes:          float64(mediumMessageCount*mediumMeasuredRounds) * 350_000,
		GCCountDelta:            100,
		MaxHeapBytes:            256 << 20,
		MetricSamples:           1,
		Drained:                 true,
		ProcessContinuous:       true,
	}
	if err := hotPathAcceptanceError(passing, mediumOfferedQPS); err != nil {
		t.Fatalf("passing evidence rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*hotPathEvidence)
		want string
	}{
		{name: "schema", edit: func(e *hotPathEvidence) { e.Schema = "other" }, want: "acceptance schema"},
		{name: "physical hash slots", edit: func(e *hotPathEvidence) { e.PhysicalHashSlots-- }, want: "physical hash slots"},
		{name: "logical slots", edit: func(e *hotPathEvidence) { e.LogicalSlots-- }, want: "logical slots"},
		{name: "replicas", edit: func(e *hotPathEvidence) { e.Replicas-- }, want: "acceptance replicas"},
		{name: "messages", edit: func(e *hotPathEvidence) { e.Messages-- }, want: "acceptance messages"},
		{name: "recipient rows", edit: func(e *hotPathEvidence) { e.RecipientRows-- }, want: "recipient rows"},
		{name: "online routes", edit: func(e *hotPathEvidence) { e.OnlineRoutes-- }, want: "online routes"},
		{name: "connections", edit: func(e *hotPathEvidence) { e.Connections-- }, want: "acceptance connections"},
		{name: "offered load", edit: func(e *hotPathEvidence) { e.OfferedQPS++ }, want: "offered QPS"},
		{name: "ingress", edit: func(e *hotPathEvidence) {
			e.IngressPerSecond = float64(mediumOfferedQPS)*mediumMinIngressFraction - 0.001
		}, want: "acceptance ingress"},
		{name: "sendack", edit: func(e *hotPathEvidence) { e.SendackP99MS++ }, want: "SENDACK P99"},
		{name: "recv", edit: func(e *hotPathEvidence) { e.RecvP99MS++ }, want: "RECV P99"},
		{name: "gateway queue", edit: func(e *hotPathEvidence) { e.MaxGatewayQueueRatio = 1 }, want: "gateway queue"},
		{name: "recipient queue", edit: func(e *hotPathEvidence) { e.MaxRecipientQueueRatio = 1 }, want: "recipient queue"},
		{name: "recipient worker", edit: func(e *hotPathEvidence) { e.MaxRecipientWorkerRatio = 1 }, want: "recipient worker"},
		{name: "plugin accepted", edit: func(e *hotPathEvidence) { e.PluginReceiveAccepted-- }, want: "plugin receive accepted"},
		{name: "plugin full", edit: func(e *hotPathEvidence) { e.PluginReceiveFull = 1 }, want: "enqueue non-accepted"},
		{name: "plugin invoke", edit: func(e *hotPathEvidence) { e.PluginReceiveInvokeOK-- }, want: "plugin receive invoke"},
		{name: "allocated missing", edit: func(e *hotPathEvidence) { e.AllocatedBytes = 0 }, want: "allocated bytes"},
		{name: "allocated regression", edit: func(e *hotPathEvidence) {
			e.AllocatedBytes = float64(e.Messages) * (mediumMaxAllocatedBytesPerMessage + 1)
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
			test.edit(&evidence)
			err := hotPathAcceptanceError(evidence, mediumOfferedQPS)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	t.Run("CI scaled pacing tolerance", func(t *testing.T) {
		evidence := passing
		evidence.OfferedQPS = mediumCIAcceptanceQPS
		evidence.IngressPerSecond = float64(mediumCIAcceptanceQPS) * mediumMinIngressFraction
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS); err != nil {
			t.Fatalf("CI-scaled evidence rejected: %v", err)
		}
		evidence.IngressPerSecond = float64(mediumCIAcceptanceQPS)*mediumMinIngressFraction - 0.001
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS); err == nil || !strings.Contains(err.Error(), "acceptance ingress") {
			t.Fatalf("below-tolerance ingress error = %v, want acceptance ingress", err)
		}
	})
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
