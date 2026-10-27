package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"path/filepath"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const attemptLinkDiagnosticCapacity = 16

// AttemptLinkRegistration copies only the accepted actual CONNECT scope.
type AttemptLinkRegistration struct{ ConnectFingerprint [16]byte }

// AttemptLinkResultEvidence borrows already observed SDK boundaries. No caller
// may sample another clock to populate this optional diagnostic value.
type AttemptLinkResultEvidence struct {
	ConnectFingerprint [16]byte
	Kind               string
	PendingStartedAt   time.Time
	WriteStartedAt     time.Time
}

// AttemptLinkRecord identifies an original confirmed-late result separately
// from native counters. SDK segments overlap the complete end-to-end latency.
type AttemptLinkRecord struct {
	Ordinal                   uint64               `json:"ordinal"`
	Key                       sendtrace.AttemptKey `json:"key"`
	Kind                      string               `json:"kind"`
	Attribution               string               `json:"attribution"`
	DeadlineAfterRegistration time.Duration        `json:"deadline_after_registration"`
	ObservedAfterRegistration time.Duration        `json:"observed_after_registration"`
	SDKPendingToWrite         time.Duration        `json:"sdk_pending_to_write"`
	SDKWriteToObserved        time.Duration        `json:"sdk_write_to_observed"`
	TimingMask                uint8                `json:"timing_mask"`
}
type AttemptLinkDiagnosticsSnapshot struct {
	Generation uint64              `json:"generation"`
	Dropped    uint64              `json:"dropped"`
	Records    []AttemptLinkRecord `json:"records"`
}
type verifierAttemptLinkRecorder struct {
	records [attemptLinkDiagnosticCapacity]AttemptLinkRecord
	count   int
	dropped uint64
}
type attemptLinkObservation struct {
	message string
	seq     uint64
	result  AttemptLinkResultEvidence
}

func (v *Verifier) attemptLinkEnabled() bool { return v != nil && v.attemptLinkDiagnostics != nil }

// ObserveAttemptWithDeadlineEvidence preserves native registration and copies
// optional connection metadata under the same exact-attempt owner lock.
func (v *Verifier) ObserveAttemptWithDeadlineEvidence(logical LogicalSend, attempt RetryAttempt, seq uint64, deadline time.Time, evidence AttemptLinkRegistration) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	if err := v.observeAttemptLocked(v.pending[logical.ClientMsgNo], logical, attempt, seq, deadline); err != nil {
		return err
	}
	if v.attemptLinkDiagnostics != nil && attempt.Attempt == 0 {
		v.pending[logical.ClientMsgNo].attempt(seq).firstDeadline.connectFingerprint = evidence.ConnectFingerprint
	}
	return nil
}
func (v *Verifier) HandleSendackWithEvidenceAt(ack *frame.SendackPacket, at time.Time, evidence AttemptLinkResultEvidence) error {
	return v.handleSendackAt(ack, at, evidence)
}
func (v *Verifier) ObserveAttemptErrorWithEvidenceAt(message string, seq uint64, at time.Time, evidence AttemptLinkResultEvidence) {
	v.observeAttemptErrorAt(message, seq, at, evidence)
}
func (v *Verifier) recordAttemptLinkLocked(deadline *firstAttemptDeadline, observedAt time.Time, observation attemptLinkObservation) {
	d := v.attemptLinkDiagnostics
	if d == nil {
		return
	}
	if d.count == attemptLinkDiagnosticCapacity {
		if d.dropped < math.MaxUint64 {
			d.dropped++
		}
		return
	}
	if observation.seq == 0 || observation.seq > math.MaxUint32 || observation.message == "" {
		return
	}
	kind := observation.result.Kind
	if kind != "sendack_success" && kind != "sendack_rejected" && kind != "transport_error" {
		kind = "transport_error"
	}
	row := AttemptLinkRecord{Ordinal: uint64(d.count + 1), Key: sendtrace.AttemptKey{MessageFingerprint: sendtrace.MessageFingerprint(observation.message), ClientSeq: uint32(observation.seq), ConnectFingerprint: deadline.connectFingerprint}, Kind: kind, Attribution: "connection_unknown", DeadlineAfterRegistration: deadline.deadline.Sub(deadline.registeredAt), ObservedAfterRegistration: observedAt.Sub(deadline.registeredAt)}
	if deadline.connectFingerprint != [16]byte{} && deadline.connectFingerprint == observation.result.ConnectFingerprint {
		row.Attribution = "candidate"
	}
	timing := observation.result
	if !timing.PendingStartedAt.IsZero() && !timing.WriteStartedAt.IsZero() && !timing.WriteStartedAt.Before(timing.PendingStartedAt) && !observedAt.Before(timing.WriteStartedAt) {
		row.SDKPendingToWrite = timing.WriteStartedAt.Sub(timing.PendingStartedAt)
		row.SDKWriteToObserved = observedAt.Sub(timing.WriteStartedAt)
		row.TimingMask = 3
	}
	d.records[d.count] = row
	d.count++
}

// AttemptLinkDiagnostics returns a detached prefix on existing control cuts.
func (v *Verifier) AttemptLinkDiagnostics(generation uint64) *AttemptLinkDiagnosticsSnapshot {
	if !v.attemptLinkEnabled() {
		return nil
	}
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	d := v.attemptLinkDiagnostics
	return &AttemptLinkDiagnosticsSnapshot{Generation: generation, Dropped: d.dropped, Records: append([]AttemptLinkRecord{}, d.records[:d.count]...)}
}
func validAttemptLinkDiagnostics(d *AttemptLinkDiagnosticsSnapshot, generation uint64) bool {
	if d == nil {
		return true
	}
	if d.Generation != generation || len(d.Records) > attemptLinkDiagnosticCapacity {
		return false
	}
	var ordinal uint64
	for _, r := range d.Records {
		if r.Ordinal == 0 || r.Ordinal <= ordinal || r.Key.ClientSeq == 0 || r.Key.MessageFingerprint == [16]byte{} || r.DeadlineAfterRegistration < 0 || r.ObservedAfterRegistration < r.DeadlineAfterRegistration || r.SDKPendingToWrite < 0 || r.SDKWriteToObserved < 0 || r.TimingMask > 3 {
			return false
		}
		if r.Kind != "sendack_success" && r.Kind != "sendack_rejected" && r.Kind != "transport_error" {
			return false
		}
		if r.Attribution != "candidate" && r.Attribution != "connection_unknown" {
			return false
		}
		if r.Attribution == "candidate" && !r.Key.Valid() {
			return false
		}
		ordinal = r.Ordinal
	}
	return true
}
func decodeAttemptLinkDiagnostics(raw []byte, generation uint64) *AttemptLinkDiagnosticsSnapshot {
	var out AttemptLinkDiagnosticsSnapshot
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || !validAttemptLinkDiagnostics(&out, generation) {
		return nil
	}
	return &out
}

// writeAttemptLinkDiagnostics publishes only detached bounded control evidence.
// Existing native report/evidence writes and cleanup retain their own outcomes.
func (r *liveDiagnosticRecorder) writeAttemptLinkDiagnostics(at time.Time, snapshots []WorkerSnapshot, live bool) error {
	if r == nil {
		return nil
	}
	enabled := false
	for _, s := range snapshots {
		if s.AttemptLinkDiagnostics != nil && validAttemptLinkDiagnostics(s.AttemptLinkDiagnostics, s.Generation) {
			enabled = true
			break
		}
	}
	if !enabled {
		return nil
	}
	type worker struct {
		WorkerID         uint64                          `json:"worker_id"`
		AssignmentID     string                          `json:"assignment_id"`
		Generation       uint64                          `json:"generation"`
		SnapshotSequence uint64                          `json:"snapshot_sequence"`
		Uptime           time.Duration                   `json:"uptime"`
		Phase            WorkerPhase                     `json:"phase"`
		Diagnostics      *AttemptLinkDiagnosticsSnapshot `json:"attempt_link_diagnostics"`
	}
	document := struct {
		Schema  string                         `json:"schema"`
		RunID   string                         `json:"run_id"`
		At      time.Time                      `json:"at"`
		Workers [coordinatorWorkerCount]worker `json:"workers"`
	}{Schema: "wukongim/chat-lifecycle-attempt-link-diagnostics/v1", RunID: r.runID, At: at}
	for _, s := range snapshots {
		if s.WorkerID >= coordinatorWorkerCount {
			continue
		}
		d := s.AttemptLinkDiagnostics
		if !validAttemptLinkDiagnostics(d, s.Generation) || (s.Phase != WorkerPhaseRunning && s.Phase != WorkerPhaseFinal) {
			d = nil
		}
		document.Workers[s.WorkerID] = worker{WorkerID: s.WorkerID, AssignmentID: s.AssignmentID, Generation: s.Generation, SnapshotSequence: s.SnapshotSequence, Uptime: s.Uptime, Phase: s.Phase, Diagnostics: d}
	}
	_ = live // Phase already carries the existing periodic/final ownership fence.
	return writeBoundedDiagnosticDocument(filepath.Join(filepath.Dir(r.path), "attempt-link-diagnostics.json"), document, 64<<10)
}
