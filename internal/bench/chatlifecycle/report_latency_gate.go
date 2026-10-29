package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// UnmarshalJSON keeps the new named child strict without relaxing the existing
// verdict decoder. A whole absent/null child remains unavailable; aliases and
// duplicate occurrences of its name cannot silently replace native evidence.
func (r *ReportVerdictEvidence) UnmarshalJSON(encoded []byte) error {
	type plainReportVerdict ReportVerdictEvidence
	if !bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		opening, err := decoder.Token()
		if err != nil || opening != json.Delim('{') {
			return ErrReportInvalid
		}
		seen := false
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return ErrReportInvalid
			}
			key, ok := token.(string)
			if !ok {
				return ErrReportInvalid
			}
			if strings.EqualFold(key, "latency_gate") {
				if key != "latency_gate" || seen {
					return ErrReportInvalid
				}
				seen = true
			}
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return ErrReportInvalid
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
			return ErrReportInvalid
		}
		if err := latencyGateJSONEnd(decoder); err != nil {
			return err
		}
	}
	var plain plainReportVerdict
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plain); err != nil {
		return err
	}
	if err := latencyGateJSONEnd(decoder); err != nil {
		return err
	}
	*r = ReportVerdictEvidence(plain)
	return nil
}

// UnmarshalJSON requires the complete bounded child contract. Struct decoding
// alone accepts null scalars, case-folded keys and short/overlong fixed arrays.
func (g *VerdictLatencyGateSnapshot) UnmarshalJSON(encoded []byte) error {
	fields, err := strictLatencyGateFields(encoded, "", []string{
		"schema_version", "status", "evaluated_elapsed", "last_latency_elapsed", "latency_age", "terminal_frozen",
		"baseline_elapsed", "window_start_elapsed", "window_end_elapsed", "warmup_duration", "window_duration",
		"max_age", "p99_fraction_denominator", "p999_fraction_denominator", "window_capacity", "classes",
	})
	if err != nil {
		return err
	}
	classes := json.NewDecoder(bytes.NewReader(fields["classes"]))
	if opening, err := classes.Token(); err != nil || opening != json.Delim('[') {
		return ErrReportInvalid
	}
	count := 0
	for classes.More() {
		if count == 3 {
			return ErrReportInvalid
		}
		var row json.RawMessage
		if err := classes.Decode(&row); err != nil {
			return ErrReportInvalid
		}
		count++
	}
	if closing, err := classes.Token(); err != nil || closing != json.Delim(']') || count != 3 {
		return ErrReportInvalid
	}
	if err := latencyGateJSONEnd(classes); err != nil {
		return err
	}
	type plainLatencyGate VerdictLatencyGateSnapshot
	var plain plainLatencyGate
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return err
	}
	*g = VerdictLatencyGateSnapshot(plain)
	return nil
}

// UnmarshalJSON rejects malformed row observations rather than converting
// missing or null values into an observed zero/false result.
func (r *VerdictLatencyGateClass) UnmarshalJSON(encoded []byte) error {
	if _, err := strictLatencyGateFields(encoded, "breach_since_elapsed", []string{
		"operation", "p99_limit", "p999_limit", "p99_count", "p99_above", "p999_count", "p999_above",
		"p99_samples", "p999_samples", "breached", "breach_since_elapsed", "breach_attribution",
	}); err != nil {
		return err
	}
	type plainLatencyGateClass VerdictLatencyGateClass
	var plain plainLatencyGateClass
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return err
	}
	*r = VerdictLatencyGateClass(plain)
	return nil
}

// strictLatencyGateFields retains only a fixed schema's keys and rejects an
// unknown or duplicate key before it can hide a previous observation.
func strictLatencyGateFields(encoded []byte, nullable string, names []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, ErrReportInvalid
	}
	fields := make(map[string]json.RawMessage, len(names))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrReportInvalid
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrReportInvalid
		}
		allowed := false
		for _, name := range names {
			allowed = allowed || key == name
		}
		if _, duplicate := fields[key]; !allowed || duplicate {
			return nil, ErrReportInvalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || (key != nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return nil, ErrReportInvalid
		}
		fields[key] = value
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') || len(fields) != len(names) {
		return nil, ErrReportInvalid
	}
	if err := latencyGateJSONEnd(decoder); err != nil {
		return nil, err
	}
	return fields, nil
}

func latencyGateJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrReportInvalid
	}
	return nil
}

// validReportLatencyGate never reconstructs legacy proof or contributes a new
// numerical verdict gate. It validates only the shape and internal consistency
// of optional native state, whose publication age may exceed its captured age.
func validReportLatencyGate(report Report) bool {
	gate := report.Verdict.LatencyGate
	if report.SchemaVersion == legacyReportSchemaVersion {
		return gate == nil
	}
	if report.SchemaVersion != ReportSchemaVersion {
		return false
	}
	if gate == nil {
		return true
	}
	if gate.SchemaVersion != verdictLatencyGateSchema || gate.WarmupDuration != report.Thresholds.Timeline.LatencyWarmup ||
		gate.WindowDuration != report.Thresholds.Latency.SustainedBreachWindow || gate.WindowDuration != 5*time.Minute ||
		gate.MaxAge != verdictLatencyQualificationMaxAge || gate.WindowCapacity != verdictLatencyWindowSamples ||
		gate.P99FractionDenominator != 100 || gate.P999FractionDenominator != 1_000 ||
		gate.EvaluatedElapsed < 0 || gate.EvaluatedElapsed > report.Window.Elapsed || gate.LastLatencyElapsed < 0 ||
		gate.LastLatencyElapsed > gate.EvaluatedElapsed || gate.LatencyAge < 0 || gate.BaselineElapsed < 0 ||
		gate.WindowStartElapsed < 0 || gate.WindowEndElapsed < 0 || (gate.TerminalFrozen && !report.Verdict.Terminal) {
		return false
	}
	if gate.LastLatencyElapsed == 0 {
		if gate.BaselineElapsed != 0 || gate.WindowStartElapsed != 0 || gate.WindowEndElapsed != 0 || gate.LatencyAge != 0 {
			return false
		}
	} else if gate.BaselineElapsed < gate.WarmupDuration || gate.BaselineElapsed > gate.LastLatencyElapsed ||
		gate.WindowEndElapsed != gate.LastLatencyElapsed ||
		gate.WindowStartElapsed != max(gate.BaselineElapsed, gate.WindowEndElapsed-gate.WindowDuration) ||
		gate.LatencyAge != gate.EvaluatedElapsed-gate.LastLatencyElapsed {
		return false
	}
	measured := false
	limits := [...]LatencyLimit{report.Thresholds.Latency.HotSendACK, report.Thresholds.Latency.Cold, report.Thresholds.Latency.Sync}
	operations := [...]LatencyOperation{LatencyHotSendACK, LatencyColdSendACK, LatencyFullSync}
	for index, row := range gate.Classes {
		if row.Operation != operations[index] || row.P99Limit != limits[index].P99 || row.P999Limit != limits[index].P999 ||
			row.P99Samples < 0 || row.P99Samples > gate.WindowCapacity || row.P999Samples < 0 || row.P999Samples > gate.WindowCapacity ||
			row.P99Above > row.P99Count || row.P999Above > row.P999Count ||
			(row.P99Samples == 0 && (row.P99Count != 0 || row.P99Above != 0)) ||
			(row.P999Samples == 0 && (row.P999Count != 0 || row.P999Above != 0)) ||
			row.Breached != (row.BreachSinceElapsed != nil) {
			return false
		}
		measured = measured || row.P99Samples > 0 || row.P999Samples > 0
		if !row.Breached {
			if row.BreachAttribution != CapacityAttributionNone {
				return false
			}
		} else if !validLatencyAttribution(row.BreachAttribution) || row.BreachAttribution == CapacityAttributionNone ||
			*row.BreachSinceElapsed < gate.WarmupDuration || *row.BreachSinceElapsed > gate.EvaluatedElapsed ||
			(gate.Status != verdictLatencyGateInvalid && *row.BreachSinceElapsed > gate.LastLatencyElapsed) {
			return false
		}
		// Invalid partial reducers remain explicit unknown evidence; do not
		// repair their independently retained sums or active breach state.
		if gate.Status != verdictLatencyGateInvalid && (row.P99Count != row.P999Count || row.P99Samples != row.P999Samples ||
			row.P999Above > row.P99Above || row.Breached != (ratioAboveUnitFraction(row.P99Above, row.P99Count, 100) || ratioAboveUnitFraction(row.P999Above, row.P999Count, 1_000))) {
			return false
		}
	}
	switch gate.Status {
	case verdictLatencyGateNotEvaluated:
		return !measured
	case verdictLatencyGateCurrent:
		return measured && gate.LastLatencyElapsed > 0 && gate.LatencyAge <= gate.MaxAge
	case verdictLatencyGateStale:
		return measured && gate.LastLatencyElapsed > 0 && gate.LatencyAge > gate.MaxAge
	case verdictLatencyGateInvalid:
		return true
	default:
		return false
	}
}
