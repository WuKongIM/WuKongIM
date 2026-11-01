package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"slices"
	"time"
)

const slowHotTailRetention = "p999_reserve8/v1"

// SlowHotSuccessRecord borrows the original SDK observation clock and opaque
// exact-attempt identity. It never represents a timeout or a durability proof.
type SlowHotSuccessRecord struct {
	AttemptLinkRecord
	ObservedAt time.Time `json:"observed_at"`
}

// SlowHotSuccessDiagnosticsSnapshot retains bounded original hot ACK samples.
// Selection is diagnostic and never represents a population percentile.
type SlowHotSuccessDiagnosticsSnapshot struct {
	// Percentile binds opt-in P95 selection; absent retains the legacy P99 meaning.
	Percentile  uint64                 `json:"percentile,omitempty"`
	LimitNanos  time.Duration          `json:"limit_nanos"`
	Seen        uint64                 `json:"seen"`
	Overwritten uint64                 `json:"overwritten"`
	Records     []SlowHotSuccessRecord `json:"records"`
	// Empty RetentionPolicy preserves the historical latest-sixteen contract.
	RetentionPolicy string `json:"retention_policy,omitempty"`
	// TailLimitNanos binds reserved samples to the configured independent P999.
	TailLimitNanos time.Duration `json:"tail_limit_nanos,omitempty"`
}

// slowHotSuccessRecorder shares the existing optional verifier owner/lock.
// No per-UID index, payload reference, clock source or background work is added.
type slowHotSuccessRecorder struct {
	records [attemptLinkDiagnosticCapacity]SlowHotSuccessRecord
	seen    uint64
	count   int
}

func (v *Verifier) recordSlowHotSuccessLocked(deadline *firstAttemptDeadline, at time.Time, latency time.Duration, observation attemptLinkObservation) {
	if !v.attemptLinkEnabled() || deadline == nil || at.IsZero() || deadline.registeredAt.IsZero() ||
		latency <= v.config.HotLatency.primaryLimit() || !at.Before(deadline.deadline) {
		return
	}
	d := &v.attemptLinkDiagnostics.slowHot
	if d.seen == math.MaxUint64 {
		return // Saturated observation cannot acquire another unique ordinal.
	}
	row, ok := makeAttemptLinkRecord(deadline, at, observation, d.seen+1)
	if !ok {
		return
	}
	d.seen++
	slot := d.count
	if d.count < attemptLinkDiagnosticCapacity {
		d.count++
	} else {
		slot = d.replacementSlot(row.ObservedAfterRegistration, v.config.HotLatency.P999)
	}
	d.records[slot] = SlowHotSuccessRecord{AttemptLinkRecord: row, ObservedAt: at}
}

// replacementSlot reserves eight recent P999 violations against primary-only
// churn. The remaining slots retain recency; only sixteen owned rows are read.
func (d *slowHotSuccessRecorder) replacementSlot(incoming, tailLimit time.Duration) int {
	tailCount := 0
	for _, row := range d.records {
		if row.ObservedAfterRegistration > tailLimit {
			tailCount++
		}
	}
	// Above the reserve, a fresh primary sample can reclaim a tail slot. A fresh
	// tail replaces its oldest peer once the reserve has already been filled.
	replaceTail := tailCount > attemptLinkDiagnosticCapacity/2 ||
		(incoming > tailLimit && tailCount >= attemptLinkDiagnosticCapacity/2)
	oldest := -1
	for i, row := range d.records {
		if (row.ObservedAfterRegistration > tailLimit) == replaceTail &&
			(oldest == -1 || row.Ordinal < d.records[oldest].Ordinal) {
			oldest = i
		}
	}
	return oldest // The full array always contains a row in the selected class.
}

func (v *Verifier) slowHotSuccessSnapshotLocked() *SlowHotSuccessDiagnosticsSnapshot {
	d := &v.attemptLinkDiagnostics.slowHot
	records := make([]SlowHotSuccessRecord, d.count)
	copy(records, d.records[:d.count])
	slices.SortFunc(records, func(a, b SlowHotSuccessRecord) int {
		if a.Ordinal < b.Ordinal {
			return -1
		}
		if a.Ordinal > b.Ordinal {
			return 1
		}
		return 0
	})
	var percentile uint64
	if v.config.HotLatency.P95 != 0 {
		percentile = 95
	}
	return &SlowHotSuccessDiagnosticsSnapshot{Percentile: percentile, LimitNanos: v.config.HotLatency.primaryLimit(), Seen: d.seen,
		Overwritten: d.seen - uint64(d.count), Records: records,
		RetentionPolicy: slowHotTailRetention, TailLimitNanos: v.config.HotLatency.P999}
}

func validSlowHotSuccess(d *SlowHotSuccessDiagnosticsSnapshot) bool {
	if d == nil {
		return true
	}
	count := uint64(len(d.Records))
	if (d.Percentile != 0 && d.Percentile != 95) || d.LimitNanos <= 0 || count != min(d.Seen, uint64(attemptLinkDiagnosticCapacity)) ||
		d.Overwritten != d.Seen-count {
		return false
	}
	if (d.RetentionPolicy == "" && d.TailLimitNanos != 0) ||
		(d.RetentionPolicy != "" && (d.RetentionPolicy != slowHotTailRetention || d.TailLimitNanos < d.LimitNanos)) {
		return false
	}
	for i, r := range d.Records {
		// Subtract before adding, so a saturated Seen never wraps to zero.
		ordinalValid := r.Ordinal == d.Seen-count+uint64(i)+1
		if d.RetentionPolicy == slowHotTailRetention {
			ordinalValid = r.Ordinal > 0 && r.Ordinal <= d.Seen &&
				(i == 0 || r.Ordinal > d.Records[i-1].Ordinal) &&
				(i != len(d.Records)-1 || r.Ordinal == d.Seen)
		}
		if !ordinalValid || r.ObservedAt.IsZero() ||
			r.Key.ClientSeq == 0 || r.Key.MessageFingerprint == [16]byte{} ||
			r.ObservedAfterRegistration <= d.LimitNanos || r.ObservedAfterRegistration >= r.DeadlineAfterRegistration ||
			r.Kind != "sendack_success" || r.SDKPendingToWrite < 0 || r.SDKWriteToObserved < 0 || r.TimingMask > 3 ||
			(r.Attribution != "candidate" && r.Attribution != "connection_unknown") ||
			(r.Attribution == "candidate" && !r.Key.Valid()) {
			return false
		}
	}
	return true
}

func decodeSlowHotSuccess(raw []byte) *SlowHotSuccessDiagnosticsSnapshot {
	// A JSON null must not become an omitted zero value and thereby acquire
	// historical legacy semantics after this optional child is re-exported.
	scanner := json.NewDecoder(bytes.NewReader(raw))
	if token, err := scanner.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	fields := make(map[string]json.RawMessage)
	for scanner.More() {
		token, err := scanner.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil
		}
		// Keep raw checks and the typed decoder on one canonical field set;
		// encoding/json would otherwise accept case-insensitive aliases.
		switch key {
		case "percentile", "limit_nanos", "seen", "overwritten", "records", "retention_policy", "tail_limit_nanos":
		default:
			return nil
		}
		if _, duplicate := fields[key]; duplicate {
			return nil
		}
		var value json.RawMessage
		if scanner.Decode(&value) != nil {
			return nil
		}
		fields[key] = value
	}
	if token, err := scanner.Token(); err != nil || token != json.Delim('}') || scanner.Decode(new(any)) != io.EOF {
		return nil
	}
	var policy *string
	if value, present := fields["retention_policy"]; present {
		if json.Unmarshal(value, &policy) != nil || policy == nil {
			return nil
		}
	}
	if _, present := fields["tail_limit_nanos"]; present && (policy == nil || *policy != slowHotTailRetention) {
		return nil
	}
	var out SlowHotSuccessDiagnosticsSnapshot
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || !validSlowHotSuccess(&out) {
		return nil
	}
	return &out
}

// UnmarshalJSON isolates only the new optional child; original late-prefix
// fields remain strict. Missing legacy evidence stays explicitly absent.
func (d *AttemptLinkDiagnosticsSnapshot) UnmarshalJSON(body []byte) error {
	type plain AttemptLinkDiagnosticsSnapshot
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	child, exists := fields["slow_hot_success"]
	delete(fields, "slow_hot_success")
	base, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var out plain
	decoder := json.NewDecoder(bytes.NewReader(base))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*d = AttemptLinkDiagnosticsSnapshot(out)
	if exists {
		d.SlowHotSuccess = decodeSlowHotSuccess(child)
	}
	return nil
}

// copyAttemptLinkDiagnostics detaches both retained sets and omits a malformed
// new child independently, preserving original confirmed-late evidence.
func copyAttemptLinkDiagnostics(d *AttemptLinkDiagnosticsSnapshot) *AttemptLinkDiagnosticsSnapshot {
	if d == nil {
		return nil
	}
	out := *d
	out.Records = append([]AttemptLinkRecord{}, d.Records...)
	out.SlowHotSuccess = nil
	if d.SlowHotSuccess != nil && validSlowHotSuccess(d.SlowHotSuccess) {
		child := *d.SlowHotSuccess
		child.Records = append([]SlowHotSuccessRecord{}, child.Records...)
		out.SlowHotSuccess = &child
	}
	return &out
}
