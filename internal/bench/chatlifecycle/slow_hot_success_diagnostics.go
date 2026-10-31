package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"time"
)

// SlowHotSuccessRecord borrows the original SDK observation clock and opaque
// exact-attempt identity. It never represents a timeout or a durability proof.
type SlowHotSuccessRecord struct {
	AttemptLinkRecord
	ObservedAt time.Time `json:"observed_at"`
}

// SlowHotSuccessDiagnosticsSnapshot retains the latest fixed-size hot tail so
// a long run does not lose the failure window to an immutable startup prefix.
type SlowHotSuccessDiagnosticsSnapshot struct {
	LimitNanos  time.Duration          `json:"limit_nanos"`
	Seen        uint64                 `json:"seen"`
	Overwritten uint64                 `json:"overwritten"`
	Records     []SlowHotSuccessRecord `json:"records"`
}

// slowHotSuccessRecorder shares the existing optional verifier owner/lock.
// No per-UID index, payload reference, clock source or background work is added.
type slowHotSuccessRecorder struct {
	records [attemptLinkDiagnosticCapacity]SlowHotSuccessRecord
	seen    uint64
	count   int
	next    int
}

func (v *Verifier) recordSlowHotSuccessLocked(deadline *firstAttemptDeadline, at time.Time, latency time.Duration, observation attemptLinkObservation) {
	if !v.attemptLinkEnabled() || deadline == nil || at.IsZero() || deadline.registeredAt.IsZero() ||
		latency <= v.config.HotLatency.P99 || !at.Before(deadline.deadline) {
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
	d.records[d.next] = SlowHotSuccessRecord{AttemptLinkRecord: row, ObservedAt: at}
	d.next = (d.next + 1) % attemptLinkDiagnosticCapacity
	if d.count < attemptLinkDiagnosticCapacity {
		d.count++
	}
}

func (v *Verifier) slowHotSuccessSnapshotLocked() *SlowHotSuccessDiagnosticsSnapshot {
	d := &v.attemptLinkDiagnostics.slowHot
	records := make([]SlowHotSuccessRecord, d.count)
	first := 0
	if d.count == attemptLinkDiagnosticCapacity {
		first = d.next
	}
	for i := range records {
		records[i] = d.records[(first+i)%attemptLinkDiagnosticCapacity]
	}
	return &SlowHotSuccessDiagnosticsSnapshot{LimitNanos: v.config.HotLatency.P99, Seen: d.seen,
		Overwritten: d.seen - uint64(d.count), Records: records}
}

func validSlowHotSuccess(d *SlowHotSuccessDiagnosticsSnapshot) bool {
	if d == nil {
		return true
	}
	count := uint64(len(d.Records))
	if d.LimitNanos <= 0 || count != min(d.Seen, uint64(attemptLinkDiagnosticCapacity)) ||
		d.Overwritten != d.Seen-count {
		return false
	}
	for i, r := range d.Records {
		// Subtract before adding, so a saturated Seen never wraps to zero.
		if r.Ordinal != d.Seen-count+uint64(i)+1 || r.ObservedAt.IsZero() ||
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
