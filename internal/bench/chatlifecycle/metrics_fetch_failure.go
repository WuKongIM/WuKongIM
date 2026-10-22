package chatlifecycle

import "github.com/WuKongIM/WuKongIM/internal/bench/target"

// MetricsFetchFailure identifies failed declared service-source positions only.
// It is a bounded diagnostic, never a node identity, verdict or retry input.
type MetricsFetchFailure struct {
	// SourceMask uses bits 0..2 in observation.service_nodes declaration order.
	SourceMask uint8 `json:"source_mask"`
	// Kinds is aligned to SourceMask; successful positions must remain empty.
	Kinds [coordinatorWorkerCount]target.MetricsFailureKind `json:"kinds"`
}

func (f *MetricsFetchFailure) bounded() *MetricsFetchFailure {
	if f == nil || f.SourceMask == 0 || f.SourceMask&^0b111 != 0 {
		return nil
	}
	bounded := &MetricsFetchFailure{SourceMask: f.SourceMask}
	for i, kind := range f.Kinds {
		failed := f.SourceMask&(1<<i) != 0
		if failed != (kind != "") {
			return nil
		}
		if failed {
			bounded.Kinds[i] = kind.Bounded()
		}
	}
	return bounded
}

// productionMetaMetricsFailure retains three private causes without changing
// the old harness-only error chain. Child context/product errors are not proof.
type productionMetaMetricsFailure struct {
	// causes retain original references only; they are never unwrapped/formatted.
	causes [coordinatorWorkerCount]error
	// diagnostic contains only source positions and validated closed kinds.
	diagnostic MetricsFetchFailure
}

func (*productionMetaMetricsFailure) Error() string {
	return "chat lifecycle metrics collection failed"
}
func (*productionMetaMetricsFailure) Unwrap() error { return ErrLifecycleHarnessInvalid }
