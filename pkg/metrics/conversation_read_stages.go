package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

// conversationReadStageMetrics prebinds a finite set of stage/result series.
// Totals overlap their children; serving-Slot durations also overlap across workers.
type conversationReadStageMetrics struct {
	durations map[[2]string][2]prometheus.Observer
}

func newConversationReadStageMetrics(registry prometheus.Registerer, labels prometheus.Labels) *conversationReadStageMetrics {
	v := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:        "wukongim_conversation_read_stage_duration_seconds",
		Help:        "Read stage wall time: list/sync handler and response; persisted/committed heads metadata, heads and edit overlay; edit_slot serving barrier and storage. Stages have different populations and overlap; sums are not additive request latency. Handler excludes outer middleware and client receive/decode.",
		ConstLabels: labels, Buckets: gatewayFrameDurationBuckets,
	}, []string{"scope", "stage", "result"})
	m := &conversationReadStageMetrics{durations: make(map[[2]string][2]prometheus.Observer)}
	for scope, stages := range map[string][]string{
		"list": {"handler", "response"}, "sync": {"handler", "response"},
		"persisted_heads": {"metadata", "heads", "edit_overlay"},
		"committed_heads": {"metadata", "heads", "edit_overlay"},
		"edit_slot":       {"barrier", "storage"},
	} {
		for _, stage := range stages {
			m.durations[[2]string{scope, stage}] = [2]prometheus.Observer{v.WithLabelValues(scope, stage, "ok"), v.WithLabelValues(scope, stage, "error")}
		}
	}
	registry.MustRegister(v)
	return m
}

// ObserveReadStage records only known scope/stage pairs without identity labels
// or hot-path vector lookups. Unknown results collapse to error; invalid pairs drop.
func (m *ConversationMetrics) ObserveReadStage(scope, stage, result string, duration time.Duration) {
	if m == nil || m.stages == nil {
		return
	}
	observers, ok := m.stages.durations[[2]string{scope, stage}]
	if !ok {
		return
	}
	index := 1
	if result == "ok" {
		index = 0
	}
	observers[index].Observe(nonNegativeConversationDuration(duration).Seconds())
}
