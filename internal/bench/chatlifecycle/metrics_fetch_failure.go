package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

// MetricsFetchFailure identifies failed declared service-source positions only.
// It is a bounded diagnostic, never a node identity, verdict or retry input.
type MetricsFetchFailure struct {
	// SourceMask uses bits 0..2 in observation.service_nodes declaration order.
	SourceMask uint8 `json:"source_mask"`
	// Kinds is aligned to SourceMask; successful positions must remain empty.
	Kinds [coordinatorWorkerCount]target.MetricsFailureKind `json:"kinds"`
	// ParseReasons optionally identifies parser leaves at failed parse positions.
	ParseReasons *[coordinatorWorkerCount]target.MetricsParseReason `json:"parse_reasons,omitempty"`
}

// UnmarshalJSON keeps old field errors intact while dropping malformed optional
// parser diagnostics. The field decoder receives the original bounded slice.
func (f *MetricsFetchFailure) UnmarshalJSON(encoded []byte) error {
	var wire struct {
		SourceMask   uint8                                             `json:"source_mask"`
		Kinds        [coordinatorWorkerCount]target.MetricsFailureKind `json:"kinds"`
		ParseReasons metricsParseReasonsField                          `json:"parse_reasons"`
	}
	// Preserve default decoding of omitted/null legacy fields on reused receivers.
	// The optional diagnostic starts absent for each independent object.
	wire.SourceMask, wire.Kinds = f.SourceMask, f.Kinds
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return err
	}
	f.SourceMask, f.Kinds, f.ParseReasons = wire.SourceMask, wire.Kinds, wire.ParseReasons.value
	return nil
}

// metricsParseReasonsField decodes only three short strings. It never copies an
// unbounded optional field into RawMessage, a slice, or an interface value.
type metricsParseReasonsField struct {
	value *[coordinatorWorkerCount]target.MetricsParseReason
}

func (f *metricsParseReasonsField) UnmarshalJSON(encoded []byte) error {
	f.value = nil
	if len(encoded) > 1024 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil
	}
	var values [coordinatorWorkerCount]target.MetricsParseReason
	for index := range values {
		if !decoder.More() {
			return nil
		}
		cell, err := decoder.Token()
		value, isString := cell.(string)
		if err != nil || !isString || len(value) > 64 {
			return nil
		}
		if value != "" {
			values[index] = target.MetricsParseReason(value).Bounded()
		}
	}
	if decoder.More() {
		return nil
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return nil
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil
	}
	f.value = &values
	return nil
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
	if f.ParseReasons != nil {
		var reasons [coordinatorWorkerCount]target.MetricsParseReason
		for index, kind := range bounded.Kinds {
			if kind == "parse" {
				reasons[index] = f.ParseReasons[index].Bounded()
				bounded.ParseReasons = &reasons
			}
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
