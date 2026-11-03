package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"math"
	"sync/atomic"
	"time"
)

const (
	driverSDKAdmissionVersion = 1
	driverSDKAdmissionEmitter = "sdk_start"
)

const (
	driverSDKQueuePickupKnown uint8 = 1 << iota
	driverSDKPickupOfferKnown
	driverSDKOfferOwnerKnown
	driverSDKNativeKnown
)

// DriverSDKAdmissionSnapshot is a final-only numeric observation, independent
// of owner Turns. It neither grants dispatch nor changes native failure codes.
type DriverSDKAdmissionSnapshot struct {
	Version uint8       `json:"version"`
	Emitter string      `json:"emitter"`
	Code    FailureCode `json:"code"`
	Dropped uint64      `json:"dropped"`
	// Records retains at most16 actual SDK-start dispatch misses per generation.
	Records []DriverSDKAdmissionMiss `json:"records"`
}

// DriverSDKAdmissionMiss keeps phase clocks separate from the original native
// due/sample/fresh relations. No phase is actor CPU or pure mailbox queue time.
type DriverSDKAdmissionMiss struct {
	Ordinal          uint64        `json:"ordinal"`
	SampledAfterDue  time.Duration `json:"sampled_after_due"`
	DeadlineAfterDue time.Duration `json:"deadline_after_due"`
	FreshAfterDue    time.Duration `json:"fresh_after_due"`
	// KnownMask bits0..2 qualify the three phases; bit3 qualifies native relations.
	// A clear bit requires zero storage but never claims a measured zero duration.
	KnownMask      uint8         `json:"known_mask"`
	QueuedToPickup time.Duration `json:"queued_to_pickup"`
	PickupToOffer  time.Duration `json:"pickup_to_offer"`
	OfferToOwner   time.Duration `json:"offer_to_owner"`
}

// driverSDKAdmissionFields requires exact field presence and rejects null
// scalars before Go's zero-value decoding could turn unknown into measured0.
func driverSDKAdmissionFields(encoded []byte, names ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil || len(fields) != len(names) {
		return nil, false
	}
	for _, name := range names {
		raw, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, false
		}
	}
	return fields, true
}

func decodeDriverSDKAdmission(encoded []byte) *DriverSDKAdmissionSnapshot {
	fields, valid := driverSDKAdmissionFields(encoded, "version", "emitter", "code", "dropped", "records")
	if !valid {
		return nil
	}
	var rows []json.RawMessage
	if json.Unmarshal(fields["records"], &rows) != nil || rows == nil || len(rows) > driverDiagnosticCapacity {
		return nil
	}
	for _, row := range rows {
		if _, valid := driverSDKAdmissionFields(row, "ordinal", "sampled_after_due", "deadline_after_due", "fresh_after_due", "known_mask", "queued_to_pickup", "pickup_to_offer", "offer_to_owner"); !valid {
			return nil
		}
	}
	var result DriverSDKAdmissionSnapshot
	if json.Unmarshal(encoded, &result) != nil || !validDriverSDKAdmission(&result) {
		return nil
	}
	return &result
}

func validDriverSDKAdmission(d *DriverSDKAdmissionSnapshot) bool {
	if d == nil {
		return true
	}
	if d.Version != driverSDKAdmissionVersion || d.Emitter != driverSDKAdmissionEmitter ||
		d.Code != FailureCodeOfferedDispatchFreshDeadline || d.Records == nil || len(d.Records) > driverDiagnosticCapacity {
		return false
	}
	var previous uint64
	for _, r := range d.Records {
		if r.Ordinal == 0 || r.Ordinal <= previous || r.Ordinal > driverDiagnosticCapacity || r.KnownMask & ^uint8(15) != 0 {
			return false
		}
		if !validDriverSDKPhase(r.KnownMask, driverSDKQueuePickupKnown, r.QueuedToPickup) ||
			!validDriverSDKPhase(r.KnownMask, driverSDKPickupOfferKnown, r.PickupToOffer) ||
			!validDriverSDKPhase(r.KnownMask, driverSDKOfferOwnerKnown, r.OfferToOwner) {
			return false
		}
		if r.KnownMask&driverSDKNativeKnown == 0 {
			if r.SampledAfterDue != 0 || r.DeadlineAfterDue != 0 || r.FreshAfterDue != 0 {
				return false
			}
		} else if r.SampledAfterDue < 0 || r.SampledAfterDue > r.DeadlineAfterDue ||
			r.DeadlineAfterDue != grantDispatchTolerance || r.FreshAfterDue <= r.DeadlineAfterDue {
			return false
		}
		previous = r.Ordinal
	}
	return true
}

func validDriverSDKPhase(mask, bit uint8, duration time.Duration) bool {
	return duration >= 0 && (mask&bit != 0 || duration == 0)
}

const (
	driverSDKQueueOffer = iota
	driverSDKExecutorPickup
	driverSDKStartOffer
	driverSDKOwnerHandle
)

// engineSDKAdmissionRecorder owns only the first16 actual SDK code38 records.
// Its owner writes records; executors only read the immutable probe and full flag.
// This cap is independent of the original owner-turn, yield and CPU recorders.
type engineSDKAdmissionRecorder struct {
	records         [driverDiagnosticCapacity]DriverSDKAdmissionMiss
	count           int
	dropped         uint64
	full            atomic.Bool
	configuredProbe func() (int64, bool)
	probe           func() (int64, bool)
}

// newEngineSDKAdmissionRecorder allocates only for an explicit opt-in; a nil
// configured probe creates a fresh generation-local monotonic origin.
func newEngineSDKAdmissionRecorder(probe func() (int64, bool)) *engineSDKAdmissionRecorder {
	d := &engineSDKAdmissionRecorder{configuredProbe: probe, probe: probe}
	if d.probe == nil {
		origin := time.Now()
		d.probe = func() (int64, bool) { return time.Since(origin).Nanoseconds(), true }
	}
	return d
}

// driverSDKAdmissionTrace transfers only numeric clocks and the original native
// due/sample values. The ticket owns it until pickup; the start command receives
// a value copy before publication. Neither copy retains a user, packet or context.
type driverSDKAdmissionTrace struct {
	recorder    *engineSDKAdmissionRecorder
	originalDue time.Time
	sampled     time.Time
	points      [4]int64
	known       uint8
}

// newDriverSDKAdmissionTrace keeps disabled, retry and full paths allocation-free.
// Live traces remain bounded by the dispatcher's existing retained-work credits.
func (e *Engine) newDriverSDKAdmissionTrace(originalDue, sampled, deadline time.Time) *driverSDKAdmissionTrace {
	d := e.driverSDKAdmission
	if d == nil || deadline.IsZero() || d.full.Load() {
		return nil
	}
	return &driverSDKAdmissionTrace{recorder: d, originalDue: originalDue, sampled: sampled}
}

// observe admits an optional clock read before calling the trusted probe. A
// read already admitted before full may finish afterward; no new read is admitted
// after observing full. There is no probe lock, handshake or scheduling change.
func (t *driverSDKAdmissionTrace) observe(point int) {
	if t == nil || t.recorder.full.Load() {
		return
	}
	nanos, valid := t.recorder.probe()
	if valid && nanos >= 0 {
		t.points[point] = nanos
		t.known |= 1 << point
	}
}

// startOfferCopy is executor-owned. Its returned value is complete before the
// existing command offer; the executor never mutates that command-owned copy.
func (t *driverSDKAdmissionTrace) startOfferCopy() *driverSDKAdmissionTrace {
	if t == nil {
		return nil
	}
	copy := *t
	copy.observe(driverSDKStartOffer)
	return &copy
}

func (t *driverSDKAdmissionTrace) phase(from, to int) (time.Duration, bool) {
	if t.known&(1<<from) == 0 || t.known&(1<<to) == 0 || t.points[to] < t.points[from] {
		return 0, false
	}
	// Both endpoints are nonnegative int64 values, so ordered subtraction fits.
	return time.Duration(t.points[to] - t.points[from]), true
}

func driverSDKNativeDuration(from, to time.Time) (time.Duration, bool) {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return 0, false
	}
	duration := to.Sub(from)
	// Sub saturates on overflow. Do not turn a saturated projection into proof.
	return duration, duration >= 0 && from.Add(duration).Equal(to)
}

// recordDriverSDKAdmissionMiss runs only after the unchanged native SDK code38
// branch emits its original evidence. It performs no clock read or settlement.
func (e *Engine) recordDriverSDKAdmissionMiss(t *driverSDKAdmissionTrace, rawFresh, effectiveFresh, deadline time.Time) {
	d := e.driverSDKAdmission
	if d == nil {
		return
	}
	if d.count == driverDiagnosticCapacity {
		if d.dropped < math.MaxUint64 {
			d.dropped++
		}
		return
	}
	r := DriverSDKAdmissionMiss{Ordinal: uint64(d.count + 1)}
	if t != nil && t.recorder == d {
		if duration, known := t.phase(driverSDKQueueOffer, driverSDKExecutorPickup); known {
			r.KnownMask |= driverSDKQueuePickupKnown
			r.QueuedToPickup = duration
		}
		if duration, known := t.phase(driverSDKExecutorPickup, driverSDKStartOffer); known {
			r.KnownMask |= driverSDKPickupOfferKnown
			r.PickupToOffer = duration
		}
		if duration, known := t.phase(driverSDKStartOffer, driverSDKOwnerHandle); known {
			r.KnownMask |= driverSDKOfferOwnerKnown
			r.OfferToOwner = duration
		}
		sampledAfterDue, sampledKnown := driverSDKNativeDuration(t.originalDue, t.sampled)
		deadlineAfterDue, deadlineKnown := driverSDKNativeDuration(t.originalDue, deadline)
		freshAfterDue, freshKnown := driverSDKNativeDuration(t.originalDue, effectiveFresh)
		if sampledKnown && deadlineKnown && freshKnown && !rawFresh.Before(t.sampled) &&
			rawFresh.Equal(effectiveFresh) && sampledAfterDue <= deadlineAfterDue &&
			deadlineAfterDue == grantDispatchTolerance && freshAfterDue > deadlineAfterDue {
			r.KnownMask |= driverSDKNativeKnown
			r.SampledAfterDue, r.DeadlineAfterDue, r.FreshAfterDue = sampledAfterDue, deadlineAfterDue, freshAfterDue
		}
	}
	d.records[d.count] = r
	d.count++
	if d.count == driverDiagnosticCapacity {
		d.full.Store(true)
	}
}

// finalDriverSDKAdmission is read only after the complete generation join. The
// enclosing copyDriverDiagnostics detaches these fixed records for every caller.
func (e *Engine) finalDriverSDKAdmission() *DriverSDKAdmissionSnapshot {
	d := e.driverSDKAdmission
	if d == nil {
		return nil
	}
	return &DriverSDKAdmissionSnapshot{Version: driverSDKAdmissionVersion, Emitter: driverSDKAdmissionEmitter,
		Code: FailureCodeOfferedDispatchFreshDeadline, Dropped: d.dropped, Records: d.records[:d.count]}
}
