// Package rpctrace retains bounded, request-local timing evidence. It never
// controls admission, cancellation, retries, or the outcome of a business RPC.
package rpctrace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// MaxSpans bounds per-RPC observation memory, including unfinished intervals.
const MaxSpans = 32

// MaxBarrierSpans holds five consecutive phases and at most two Save and two
// Send overlays for one Slot barrier. Publication still obeys MaxSpans.
const MaxBarrierSpans = 9

// BarrierPhaseCount fixes coverage storage to the five sequential read phases.
const BarrierPhaseCount = 5

// Side identifies the endpoint that measured a sample with its own clock.
type Side string

const (
	SideClient Side = "client"
	SideServer Side = "server"
)

// Stage is a closed timing vocabulary, never a user or payload identifier.
type Stage string

const (
	StagePeerAcquire             Stage = "peer_acquire"
	StageCapability              Stage = "capability"
	StageClientQueue             Stage = "client_queue"
	StageRequestWrite            Stage = "request_write"
	StageResponseWait            Stage = "response_wait"
	StageServiceQueue            Stage = "service_queue"
	StageHandler                 Stage = "handler"
	StageResponseEncode          Stage = "response_encode"
	StageResponseQueue           Stage = "response_queue"
	StageResponseWrite           Stage = "response_write"
	StageResponseReceiveObserver Stage = "response_receive_observer"
	StageResponseDispatch        Stage = "response_dispatch"
	StageResponseResume          Stage = "response_resume"
	StageRecoveryAdmission       Stage = "recovery_admission"
	StageRecoveryFlight          Stage = "recovery_flight_wait"
	StageRecoveryOwners          Stage = "recovery_owners_admission"
	StageRecoveryOwner           Stage = "recovery_owner_read"
	StageRecoveryInstall         Stage = "recovery_install"
	StagePermissionAdmission     Stage = "permission_admission"
	StagePermissionBarrier       Stage = "permission_barrier"
	StagePermissionSnapshot      Stage = "permission_snapshot"
	StagePermissionAuthority     Stage = "permission_authority"
	StageSlotControlWait         Stage = "slot_control_wait"
	StageSlotReadEligibility     Stage = "slot_read_eligibility"
	StageSlotRawQuorum           Stage = "slot_raw_quorum"
	StageSlotReadyToAccept       Stage = "slot_ready_to_accept"
	StageSlotApplyWait           Stage = "slot_apply_wait"
	StageSlotSaveEntries         Stage = "slot_ready_save_entries"
	StageSlotSaveHardState       Stage = "slot_ready_save_hardstate"
	StageSlotSaveSnapshot        Stage = "slot_ready_save_snapshot"
	StageSlotSaveMixed           Stage = "slot_ready_save_mixed"
	StageSlotRaftSend            Stage = "slot_raft_send"
	StageCommitAdmission         Stage = "commit_admission"
	StageCommitQueue             Stage = "commit_queue"
	StageCommitCollect           Stage = "commit_collect"
	StageCommitBuild             Stage = "commit_build"
	StageCommitDurable           Stage = "commit_durable"
	StageCommitPublish           Stage = "commit_publish"
)

// Channel authority stages retain a bounded first-effect view within the
// enclosing caller wait; they never imply a complete storage critical path.
const (
	StageChannelSubmit             Stage = "channel_submit"
	StageChannelFutureWait         Stage = "channel_future_wait"
	StageChannelPrepareWait        Stage = "channel_prepare_wait"
	StageChannelPrepare            Stage = "channel_prepare"
	StageChannelFirstAppendPending Stage = "channel_first_append_pending"
	StageChannelFirstAppendQueue   Stage = "channel_first_append_queue"
	StageChannelFirstAppend        Stage = "channel_first_append"
	StageChannelAfterFirstAppend   Stage = "channel_after_first_append"
)

// Shared first-effect children are partial observations. Runtime reserve,
// submit and future wait nest inside runtime append and must not be added to it.
const (
	StageChannelMetaResolve       Stage = "channel_meta_resolve"
	StageChannelMetaApply         Stage = "channel_meta_apply"
	StageChannelRuntimeAppend     Stage = "channel_runtime_append"
	StageChannelRuntimeReserve    Stage = "channel_runtime_reserve"
	StageChannelRuntimeSubmit     Stage = "channel_runtime_submit"
	StageChannelRuntimeFutureWait Stage = "channel_runtime_future_wait"
)

// Result is a closed observation outcome; errors and payloads are never stored.
type Result string

const (
	ResultOK         Result = "ok"
	ResultError      Result = "error"
	ResultTimeout    Result = "timeout"
	ResultCanceled   Result = "canceled"
	ResultBusy       Result = "busy"
	ResultStopped    Result = "stopped"
	ResultUnfinished Result = "unfinished"
)

// ResultFor classifies context failures without inspecting arbitrary error text.
func ResultFor(err error) Result {
	switch {
	case err == nil:
		return ResultOK
	case errors.Is(err, context.DeadlineExceeded):
		return ResultTimeout
	case errors.Is(err, context.Canceled):
		return ResultCanceled
	default:
		return ResultError
	}
}

// SpanSample records a local interval. Overlapping spans must not be added to
// estimate a critical path; Incomplete records a phase still open at finish.
type SpanSample struct {
	Stage      Stage         `json:"stage"`
	Offset     time.Duration `json:"offset"`
	Duration   time.Duration `json:"duration"`
	Result     Result        `json:"result"`
	Incomplete bool          `json:"incomplete,omitempty"`
}

// BarrierOverlayCoverage totals disjoint intersections for one operation kind
// with one observed phase. Save and Send may overlap each other; their totals
// must not be added to infer a union or time blocked on the Raft owner.
// Count counts positive-duration intersections; a cross-phase operation may count
// in each phase. Unfinished means an intersecting operation was caller-clipped.
// These totals are not continuous spans and must not be added to phase duration.
type BarrierOverlayCoverage struct {
	Duration   time.Duration `json:"duration"`
	Count      uint64        `json:"count"`
	Unfinished bool          `json:"unfinished,omitempty"`
}

// BarrierPhaseCoverage uses the same phase boundaries as the retained group.
// Observed means the phase began, including a caller-clipped incomplete phase;
// false means unknown, with zero-valued totals rather than measured zero work.
type BarrierPhaseCoverage struct {
	Stage    Stage                  `json:"stage"`
	Observed bool                   `json:"observed"`
	Save     BarrierOverlayCoverage `json:"save"`
	Send     BarrierOverlayCoverage `json:"send"`
}

// BarrierBudgetOwnerCoverage measures an owner-blocking storage fallback caused
// by a rejected bounded asynchronous reservation. DrainWait, SavePermit and
// SyncWork are disjoint subsets of Duration, not additional elapsed time.
// RejectMask is the union of closed reasons: slot count=1, node count=2,
// slot bytes=4, node bytes=8. Ordinary asynchronous Save is never this scope.
type BarrierBudgetOwnerCoverage struct {
	Duration   time.Duration `json:"duration"`
	Count      uint64        `json:"count"`
	Unfinished bool          `json:"unfinished,omitempty"`
	DrainWait  time.Duration `json:"drain_wait"`
	SavePermit time.Duration `json:"save_permit"`
	SyncWork   time.Duration `json:"sync_work"`
	RejectMask uint8         `json:"reject_mask"`
}

// BarrierOtherOwnerCoverage measures a disjoint non-budget synchronous storage
// fence on the same owner. CauseMask is closed: oversized Ready=1, snapshot=2,
// configuration=4, compaction=8, snapshot capture=16, embedding=32. The producer
// must measure the owner operation itself; asynchronous Save overlap is not proof.
type BarrierOtherOwnerCoverage struct {
	Duration   time.Duration `json:"duration"`
	Count      uint64        `json:"count"`
	Unfinished bool          `json:"unfinished,omitempty"`
	CauseMask  uint8         `json:"cause_mask"`
}

// BarrierOwnerPhase counts positive intersections of mutually exclusive owner
// scopes with the corresponding BarrierCoverage phase. A cross-phase scope may
// count in each phase. Unfinished means caller clipping, even when that earlier
// phase completed; it does not imply that the phase is incomplete.
type BarrierOwnerPhase struct {
	Budget BarrierBudgetOwnerCoverage `json:"budget"`
	Other  BarrierOtherOwnerCoverage  `json:"other"`
}

// BarrierOwnerCoverage inherits the exact five phase boundaries and Observed
// flags from its containing coverage. Unobserved entries must be zero and mean
// unknown. Presence is optional so legacy producers never imply measured zero.
type BarrierOwnerCoverage struct {
	Phases [BarrierPhaseCount]BarrierOwnerPhase `json:"phases"`
}

// BarrierCountPausedCoverage intersects the same Slot's scheduling pause with
// the selected five phases. It is independent of owner, Save and Send coverage:
// a paused Slot yields its worker while physical work may still be running.
// Unobserved phases remain unknown; Count counts positive pause intersections,
// not reservation rejections. Unfinished means caller clipping before resume.
type BarrierCountPausedCoverage struct {
	Phases [BarrierPhaseCount]BarrierOverlayCoverage `json:"phases"`
}

// BarrierCoverage is a fixed-size companion to one longest barrier group.
// The order is control, eligibility, raw quorum, Ready acceptance, durable apply.
type BarrierCoverage struct {
	Phases             [BarrierPhaseCount]BarrierPhaseCoverage `json:"phases"`
	OwnerStorage       *BarrierOwnerCoverage                   `json:"owner_storage,omitempty"`
	StorageCountPaused *BarrierCountPausedCoverage             `json:"storage_count_paused,omitempty"`
}

// clone detaches the optional fixed owner array at every publication boundary.
func (c BarrierCoverage) clone() BarrierCoverage {
	if c.OwnerStorage != nil {
		owner := *c.OwnerStorage
		c.OwnerStorage = &owner
	}
	if c.StorageCountPaused != nil {
		paused := *c.StorageCountPaused
		c.StorageCountPaused = &paused
	}
	return c
}

// Sample is an immutable endpoint observation. Matching TraceIDs join the two
// endpoints; their wall clocks cannot determine one-way network latency.
type Sample struct {
	// TraceID is connection-generation plus request identity, or a local-only key.
	TraceID string `json:"trace_id"`
	// ParentTraceID links a nested client call to its observed local server owner.
	ParentTraceID string `json:"parent_trace_id,omitempty"`
	// Unbound means negotiation or dispatch did not establish a joinable wire key.
	Unbound      bool          `json:"unbound,omitempty"`
	Side         Side          `json:"side"`
	ServiceID    uint16        `json:"service_id"`
	At           time.Time     `json:"at"`
	Duration     time.Duration `json:"duration"`
	Result       Result        `json:"result"`
	Spans        []SpanSample  `json:"spans"`
	DroppedSpans uint64        `json:"dropped_spans,omitempty"`
	// RecorderDropped is cumulative local observation admission loss, not RPC loss.
	RecorderDropped uint64 `json:"recorder_dropped,omitempty"`
	// BarrierGroups counts observed local Slot groups; Spans retains only the
	// longest caller-visible group, never a sum of parallel Slot latencies.
	BarrierGroups uint64 `json:"barrier_groups,omitempty"`
	// DroppedBarrierGroups counts fixed observer-registry overflow separately
	// from known spans lost to the per-trace span limit.
	DroppedBarrierGroups uint64 `json:"dropped_barrier_groups,omitempty"`
	// BarrierCoverage is absent when unavailable or when the whole group is dropped.
	BarrierCoverage *BarrierCoverage `json:"barrier_coverage,omitempty"`
	// ReceivePrefix precedes the existing server window; absence means unknown.
	ReceivePrefix *ReceivePrefix `json:"receive_prefix,omitempty"`
}

// Clone detaches mutable data before crossing a store/query boundary.
func (s Sample) Clone() Sample {
	s.Spans = append([]SpanSample(nil), s.Spans...)
	if s.ReceivePrefix != nil {
		prefix := *s.ReceivePrefix
		s.ReceivePrefix = &prefix
	}
	if s.BarrierCoverage != nil {
		coverage := s.BarrierCoverage.clone()
		s.BarrierCoverage = &coverage
	}
	return s
}

// Recorder bounds simultaneously retained request-local observations. Finished
// samples transfer to the caller's existing bounded observation drain.
type Recorder struct {
	// limit and active bound observations independently of business admission.
	limit    int64
	active   atomic.Int64
	dropped  atomic.Uint64
	sequence atomic.Uint64
	prefix   string
}

func NewRecorder(maxActive int) (*Recorder, error) {
	if maxActive < 1 || maxActive > 4096 {
		return nil, fmt.Errorf("rpctrace: active limit must be within 1..4096")
	}
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	return &Recorder{limit: int64(maxActive), prefix: "local/" + hex.EncodeToString(seed[:]) + "/"}, nil
}

func (r *Recorder) Active() int64 {
	if r == nil {
		return 0
	}
	return r.active.Load()
}
func (r *Recorder) Dropped() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// Start acquires observation credit only. A nil result must never reject work.
func (r *Recorder) Start(ctx context.Context, side Side, serviceID uint16) *Trace {
	if r == nil {
		return nil
	}
	if side != SideClient && side != SideServer {
		r.dropped.Add(1)
		return nil
	}
	for {
		n := r.active.Load()
		if n >= r.limit {
			r.dropped.Add(1)
			return nil
		}
		if r.active.CompareAndSwap(n, n+1) {
			break
		}
	}
	t := &Trace{owner: r, at: time.Now(), id: r.prefix + strconv.FormatUint(r.sequence.Add(1), 16), unbound: true, side: side, serviceID: serviceID}
	if parent := From(ctx); parent != nil {
		t.parentID = parent.ID()
	}
	return t
}

// Trace publishes at most MaxSpans local intervals at exactly one Finish.
// Its fixed historical candidate never expands the publication limit.
// Its mutex protects only fixed-size observation data, never business work.
type Trace struct {
	mu        sync.Mutex
	owner     *Recorder
	at        time.Time
	id        string
	parentID  string
	unbound   bool
	side      Side
	serviceID uint16
	closed    bool
	// Receive evidence is fixed-size and never changes the original trace clock.
	receivePrefix        ReceivePrefix
	hasReceivePrefix     bool
	hasResponseReceive   bool
	spans                [MaxSpans]SpanSample
	count                int
	dropped              uint64
	barrierSpans         [MaxBarrierSpans]SpanSample
	barrierCount         int
	barrierDuration      time.Duration
	barrierCoverage      BarrierCoverage
	hasBarrierCoverage   bool
	barrierGroups        uint64
	droppedBarrierGroups uint64
}

// DropBarrierGroup records observation admission loss without guessing how
// many phases the unobserved business request would have reached.
func (t *Trace) DropBarrierGroup() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.droppedBarrierGroups++
	}
}

// HistoricalSpan is a completed observation window using the Trace owner's
// local clock. Incomplete means its phase was still open when that caller left;
// its End must remain that caller's terminal time, not Trace.Finish time.
type HistoricalSpan struct {
	Stage      Stage
	Start, End time.Time
	Result     Result
	Incomplete bool
}

// RetainLongestBarrier copies fixed-size historical data. The caller must not
// hold a Slot mutex here. Only the longest complete group is retained, and
// Finish either publishes that entire group within MaxSpans or reports drops.
func (t *Trace) RetainLongestBarrier(start, end time.Time, spans [MaxBarrierSpans]HistoricalSpan, count int, dropped uint64) {
	t.retainLongestBarrier(start, end, spans, count, dropped, nil)
}

// RetainLongestBarrierWithCoverage copies coverage with its source phases.
// Invalid phase provenance rejects the candidate; a later legacy replacement
// clears coverage. The caller must release Slot locks before publication.
func (t *Trace) RetainLongestBarrierWithCoverage(start, end time.Time, spans [MaxBarrierSpans]HistoricalSpan, count int, dropped uint64, coverage BarrierCoverage) {
	t.retainLongestBarrier(start, end, spans, count, dropped, &coverage)
}

func (t *Trace) retainLongestBarrier(start, end time.Time, spans [MaxBarrierSpans]HistoricalSpan, count int, dropped uint64, coverage *BarrierCoverage) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.dropped += dropped
	if count < 1 || count > MaxBarrierSpans || start.Before(t.at) || end.Before(start) {
		t.dropped += uint64(max(1, min(count, MaxBarrierSpans)))
		return
	}
	var group [MaxBarrierSpans]SpanSample
	for i := 0; i < count; i++ {
		s := spans[i]
		if !validBarrierStage(s.Stage) || s.Start.Before(start) || s.End.After(end) || s.End.Before(s.Start) {
			t.dropped += uint64(count)
			return
		}
		group[i] = SpanSample{Stage: s.Stage, Offset: s.Start.Sub(t.at), Duration: s.End.Sub(s.Start), Result: normalizeResult(s.Result), Incomplete: s.Incomplete}
	}
	if coverage != nil && !validBarrierCoverage(start, end, spans, count, *coverage) {
		t.dropped += uint64(count)
		return
	}
	t.barrierGroups++
	if t.barrierCount == 0 || end.Sub(start) > t.barrierDuration {
		t.barrierSpans = group
		t.barrierCount = count
		t.barrierDuration = end.Sub(start)
		t.hasBarrierCoverage = coverage != nil
		t.barrierCoverage = BarrierCoverage{}
		if coverage != nil {
			t.barrierCoverage = coverage.clone()
		}
	}
}

// validBarrierCoverage checks the fixed phase prefix, not arbitrary overlay
// order. A final successful apply phase may finish before the caller returns.
func validBarrierCoverage(start, end time.Time, spans [MaxBarrierSpans]HistoricalSpan, count int, c BarrierCoverage) bool {
	stages := [BarrierPhaseCount]Stage{StageSlotControlWait, StageSlotReadEligibility, StageSlotRawQuorum, StageSlotReadyToAccept, StageSlotApplyWait}
	phaseCount := 0
	previous := start
	for i, stage := range stages {
		p := c.Phases[i]
		if p.Stage != stage {
			return false
		}
		observed := i < count && spans[i].Stage == stage
		if p.Observed != observed {
			return false
		}
		if !observed {
			if p.Save != (BarrierOverlayCoverage{}) || p.Send != (BarrierOverlayCoverage{}) {
				return false
			}
			if c.OwnerStorage != nil && c.OwnerStorage.Phases[i] != (BarrierOwnerPhase{}) {
				return false
			}
			if c.StorageCountPaused != nil && c.StorageCountPaused.Phases[i] != (BarrierOverlayCoverage{}) {
				return false
			}
			continue
		}
		// A missing phase can never be followed by an observed one.
		if phaseCount != i {
			return false
		}
		span := spans[i]
		if !span.Start.Equal(previous) || (span.Incomplete && !span.End.Equal(end)) {
			return false
		}
		if i > 0 && spans[i-1].Incomplete {
			return false
		}
		duration := span.End.Sub(span.Start)
		if !validOverlayCoverage(p.Save, duration) || !validOverlayCoverage(p.Send, duration) {
			return false
		}
		if c.OwnerStorage != nil && !validOwnerPhase(c.OwnerStorage.Phases[i], duration) {
			return false
		}
		if c.StorageCountPaused != nil && !validOverlayCoverage(c.StorageCountPaused.Phases[i], duration) {
			return false
		}
		previous = span.End
		phaseCount++
	}
	if phaseCount == 0 || (phaseCount < BarrierPhaseCount && !spans[phaseCount-1].Incomplete) {
		return false
	}
	// Remaining historical records must be overlays, never hidden extra phases.
	for i := phaseCount; i < count; i++ {
		switch spans[i].Stage {
		case StageSlotSaveEntries, StageSlotSaveHardState, StageSlotSaveSnapshot, StageSlotSaveMixed, StageSlotRaftSend:
		default:
			return false
		}
	}
	return true
}

func validOverlayCoverage(c BarrierOverlayCoverage, limit time.Duration) bool {
	if c.Duration < 0 || c.Duration > limit || (c.Duration == 0) != (c.Count == 0) {
		return false
	}
	return (!c.Unfinished || c.Count > 0) && c.Count <= uint64(c.Duration)
}

// validOwnerPhase validates disjoint scopes and their subsets without summing
// durations, which could overflow before a bounds check. Provenance that a
// scope really blocked the owner belongs to the producer, not this validator.
func validOwnerPhase(p BarrierOwnerPhase, limit time.Duration) bool {
	b, o := p.Budget, p.Other
	if !validOverlayCoverage(BarrierOverlayCoverage{Duration: b.Duration, Count: b.Count, Unfinished: b.Unfinished}, limit) ||
		!validOverlayCoverage(BarrierOverlayCoverage{Duration: o.Duration, Count: o.Count, Unfinished: o.Unfinished}, limit) ||
		o.Duration > limit-b.Duration {
		return false
	}
	if (b.Duration > 0 && (b.RejectMask == 0 || b.RejectMask & ^uint8(15) != 0)) ||
		(b.Duration == 0 && b.RejectMask != 0) ||
		(o.Duration > 0 && (o.CauseMask == 0 || o.CauseMask & ^uint8(63) != 0)) ||
		(o.Duration == 0 && o.CauseMask != 0) {
		return false
	}
	remaining := b.Duration
	for _, child := range [3]time.Duration{b.DrainWait, b.SavePermit, b.SyncWork} {
		if child < 0 || child > remaining {
			return false
		}
		remaining -= child
	}
	return true
}

// Bind assigns the negotiated wire identity before dispatching traced work.
// Rebinding and empty identities are rejected without changing the sample.
func (t *Trace) Bind(token [16]byte, requestID uint64) bool {
	if t == nil || token == [16]byte{} || requestID == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || !t.unbound {
		return false
	}
	t.id = hex.EncodeToString(token[:]) + "/" + strconv.FormatUint(requestID, 16)
	t.unbound = false
	return true
}

func (t *Trace) ID() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.id
}

// Span is a small handle to one timing interval. Zero handles are disabled.
type Span struct {
	trace *Trace
	index int
}

func (t *Trace) Begin(stage Stage) Span {
	if t == nil {
		return Span{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return Span{}
	}
	if !validStage(stage) || t.count == MaxSpans {
		t.dropped++
		return Span{}
	}
	i := t.count
	t.count++
	t.spans[i] = SpanSample{Stage: stage, Offset: time.Since(t.at), Result: ResultUnfinished, Incomplete: true}
	return Span{trace: t, index: i}
}

// End closes an interval once. Late completion cannot mutate a published sample.
func (s Span) End(result Result) {
	if s.trace == nil {
		return
	}
	t := s.trace
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || !t.spans[s.index].Incomplete {
		return
	}
	p := &t.spans[s.index]
	p.Duration = time.Since(t.at) - p.Offset
	p.Result = normalizeResult(result)
	p.Incomplete = false
}

// Finish returns one detached snapshot, including unfinished intervals, and
// releases recorder credit. The caller must send it through a bounded sink.
func (t *Trace) Finish(result Result) *Sample {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	elapsed := time.Since(t.at)
	spans := make([]SpanSample, t.count)
	copy(spans, t.spans[:t.count])
	for i := range spans {
		if spans[i].Incomplete {
			spans[i].Duration = elapsed - spans[i].Offset
		}
	}
	var coverage *BarrierCoverage
	if t.count+t.barrierCount <= MaxSpans {
		spans = append(spans, t.barrierSpans[:t.barrierCount]...)
		if t.hasBarrierCoverage {
			copy := t.barrierCoverage.clone()
			coverage = &copy
		}
	} else {
		t.dropped += uint64(t.barrierCount)
	}
	s := &Sample{TraceID: t.id, ParentTraceID: t.parentID, Unbound: t.unbound, Side: t.side, ServiceID: t.serviceID, At: t.at, Duration: elapsed, Result: normalizeResult(result), Spans: spans, DroppedSpans: t.dropped, RecorderDropped: t.owner.dropped.Load(), BarrierGroups: t.barrierGroups, DroppedBarrierGroups: t.droppedBarrierGroups, BarrierCoverage: coverage}
	if t.hasReceivePrefix {
		prefix := t.receivePrefix
		s.ReceivePrefix = &prefix
	}
	t.owner.active.Add(-1)
	return s
}

type contextKey struct{}

func From(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(contextKey{}).(*Trace)
	return t
}
func With(ctx context.Context, t *Trace) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, t)
}

// Copy propagates observation ownership without inheriting source cancellation,
// deadlines, or unrelated values into independently executing mutations.
func Copy(dst, src context.Context) context.Context { return With(dst, From(src)) }
func Begin(ctx context.Context, stage Stage) Span   { return From(ctx).Begin(stage) }

func validStage(stage Stage) bool {
	switch stage {
	case StagePeerAcquire, StageCapability, StageClientQueue, StageRequestWrite, StageResponseWait,
		StageServiceQueue, StageHandler, StageResponseEncode, StageResponseQueue, StageResponseWrite,
		StageResponseReceiveObserver, StageResponseDispatch, StageResponseResume,
		StageRecoveryAdmission, StageRecoveryFlight, StageRecoveryOwners, StageRecoveryOwner, StageRecoveryInstall,
		StagePermissionAdmission, StagePermissionBarrier, StagePermissionSnapshot, StagePermissionAuthority,
		StageCommitAdmission, StageCommitQueue, StageCommitCollect, StageCommitBuild, StageCommitDurable, StageCommitPublish,
		StageChannelSubmit, StageChannelFutureWait, StageChannelPrepareWait, StageChannelPrepare,
		StageChannelFirstAppendPending, StageChannelFirstAppendQueue, StageChannelFirstAppend, StageChannelAfterFirstAppend:
		return true
	default:
		return validBarrierStage(stage) || validAppendPhase(stage)
	}
}

func validBarrierStage(stage Stage) bool {
	switch stage {
	case StageSlotControlWait, StageSlotReadEligibility, StageSlotRawQuorum, StageSlotReadyToAccept, StageSlotApplyWait,
		StageSlotSaveEntries, StageSlotSaveHardState, StageSlotSaveSnapshot, StageSlotSaveMixed, StageSlotRaftSend:
		return true
	default:
		return false
	}
}

func normalizeResult(r Result) Result {
	switch r {
	case ResultOK, ResultError, ResultTimeout, ResultCanceled, ResultBusy, ResultStopped, ResultUnfinished:
		return r
	default:
		return ResultError
	}
}
