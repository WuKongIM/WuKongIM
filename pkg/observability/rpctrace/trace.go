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

// Side identifies the endpoint that measured a sample with its own clock.
type Side string

const (
	SideClient Side = "client"
	SideServer Side = "server"
)

// Stage is a closed timing vocabulary, never a user or payload identifier.
type Stage string

const (
	StagePeerAcquire         Stage = "peer_acquire"
	StageCapability          Stage = "capability"
	StageClientQueue         Stage = "client_queue"
	StageRequestWrite        Stage = "request_write"
	StageResponseWait        Stage = "response_wait"
	StageServiceQueue        Stage = "service_queue"
	StageHandler             Stage = "handler"
	StageResponseEncode      Stage = "response_encode"
	StageResponseQueue       Stage = "response_queue"
	StageResponseWrite       Stage = "response_write"
	StageRecoveryAdmission   Stage = "recovery_admission"
	StageRecoveryFlight      Stage = "recovery_flight_wait"
	StageRecoveryOwners      Stage = "recovery_owners_admission"
	StageRecoveryOwner       Stage = "recovery_owner_read"
	StageRecoveryInstall     Stage = "recovery_install"
	StagePermissionAdmission Stage = "permission_admission"
	StagePermissionBarrier   Stage = "permission_barrier"
	StagePermissionSnapshot  Stage = "permission_snapshot"
	StagePermissionAuthority Stage = "permission_authority"
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
}

// Clone detaches the only mutable backing slice before crossing a store/query boundary.
func (s Sample) Clone() Sample {
	s.Spans = append([]SpanSample(nil), s.Spans...)
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

// Trace owns at most MaxSpans local intervals until exactly one Finish.
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
	spans     [MaxSpans]SpanSample
	count     int
	dropped   uint64
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
	s := &Sample{TraceID: t.id, ParentTraceID: t.parentID, Unbound: t.unbound, Side: t.side, ServiceID: t.serviceID, At: t.at, Duration: elapsed, Result: normalizeResult(result), Spans: spans, DroppedSpans: t.dropped, RecorderDropped: t.owner.dropped.Load()}
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
		StageRecoveryAdmission, StageRecoveryFlight, StageRecoveryOwners, StageRecoveryOwner, StageRecoveryInstall,
		StagePermissionAdmission, StagePermissionBarrier, StagePermissionSnapshot, StagePermissionAuthority:
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
