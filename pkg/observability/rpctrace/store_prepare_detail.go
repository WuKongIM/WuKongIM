package rpctrace

import (
	"math"
	"sync"
	"time"
)

// StorePrepareDetailSample contains two disjoint subsets of shared group wall
// time. It is not per-item CPU, pure read wait, or physical commit latency.
// KnownMask bit0 qualifies frontier totals; bit1 qualifies item preparation.
// An unattempted, unfinished or invalid category stays unknown with zero time.
type StorePrepareDetailSample struct {
	KnownMask        uint8         `json:"known_mask"`
	FrontierWall     time.Duration `json:"frontier_wall"`
	FrontierCalls    uint64        `json:"frontier_calls"`
	ItemPrepareWall  time.Duration `json:"item_prepare_wall"`
	ItemPrepareCalls uint64        `json:"item_prepare_calls"`
}

// StorePrepareDetail is an opaque, optional group-local accumulator. Its zero
// value adds no diagnostic clock reads or allocations to storage preparation.
type StorePrepareDetail struct{ recorder *storePrepareDetailRecorder }

// StorePrepareInterval is a value handle for one admitted category attempt.
// Only the matching active token may finish it; repeated or late End is inert.
type StorePrepareInterval struct {
	recorder *storePrepareDetailRecorder
	token    uint64
}

// storePrepareDetailRecorder owns its clock and fixed numeric state. It never
// retains caller contexts, records, keys or a new observation identity. The
// preparation owner constructs it once; interval/closure transitions serialize
// independently of Trace.Finish, which never reaches into this mutable state.
type storePrepareDetailRecorder struct {
	mu             sync.Mutex
	parent         CommitSpan
	now            func() time.Time
	sample         StorePrepareDetailSample
	invalid        uint8
	closed         bool
	nextToken      uint64
	activeToken    uint64
	activeCategory uint8
	activeStart    time.Time
}

const (
	storePrepareFrontier uint8 = 1
	storePrepareItem     uint8 = 2
)

// StorePrepareDetail selects only existing, open preparation parent slots. It
// must be constructed by the preparation owner before handing off its parent.
// The default path does not read a clock until a live interval is admitted.
func (s *CommitSpan) StorePrepareDetail() StorePrepareDetail {
	return s.storePrepareDetailWithClock(time.Now)
}

// storePrepareDetailWithClock is the narrow package test seam; production uses
// only time.Now. No clock or recorder is admitted for nil/full/finished parents.
func (s *CommitSpan) storePrepareDetailWithClock(now func() time.Time) StorePrepareDetail {
	if s == nil || now == nil || !s.hasOpenStorePrepareParent() {
		return StorePrepareDetail{}
	}
	if s.prepareDetail == nil {
		s.prepareDetail = &storePrepareDetailRecorder{parent: *s, now: now}
	}
	return StorePrepareDetail{recorder: s.prepareDetail}
}

func (s CommitSpan) hasOpenStorePrepareParent() bool {
	if s.owners == nil {
		return false
	}
	for i := 0; i < int(s.owners.count); i++ {
		index := s.slots[i]
		if index == 0 {
			continue
		}
		trace := s.owners.traces[i]
		trace.mu.Lock()
		open := !trace.closed && int(index) <= trace.count && trace.spans[index-1].Stage == StageStoreAppendPrepare && trace.spans[index-1].Incomplete
		trace.mu.Unlock()
		if open {
			return true
		}
	}
	return false
}

// BeginFrontier brackets one existing outer loadLEO call, including its cached
// path or recovery. The category remains shared group wall, not a read metric.
func (d StorePrepareDetail) BeginFrontier() StorePrepareInterval {
	return d.begin(storePrepareFrontier)
}

// BeginItemPrepare brackets one existing item preparation call and all of its
// decoding, proof and validation work without changing that call's ownership.
func (d StorePrepareDetail) BeginItemPrepare() StorePrepareInterval { return d.begin(storePrepareItem) }

func (d StorePrepareDetail) begin(category uint8) StorePrepareInterval {
	r := d.recorder
	if r == nil {
		return StorePrepareInterval{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.parent.hasOpenStorePrepareParent() {
		return StorePrepareInterval{}
	}
	count := &r.sample.FrontierCalls
	if category == storePrepareItem {
		count = &r.sample.ItemPrepareCalls
	}
	if *count == math.MaxUint64 {
		r.invalidate(category)
		return StorePrepareInterval{}
	}
	*count++
	if r.activeToken != 0 {
		// Overlapping categories cannot be disjoint subsets of the parent.
		r.invalidate(r.activeCategory)
		r.invalidate(category)
		r.activeToken = 0
		return StorePrepareInterval{}
	}
	if r.invalid&category != 0 {
		return StorePrepareInterval{}
	}
	if r.nextToken == math.MaxUint64 {
		r.invalidate(category)
		return StorePrepareInterval{}
	}
	r.nextToken++
	r.activeToken, r.activeCategory = r.nextToken, category
	r.activeStart = r.now()
	if r.activeStart.IsZero() {
		r.invalidate(category)
		r.activeToken = 0
		return StorePrepareInterval{}
	}
	return StorePrepareInterval{recorder: r, token: r.activeToken}
}

// End closes an admitted interval at most once. A read admitted while a parent
// was open may race a later Finish; closed parents never receive its result.
func (i StorePrepareInterval) End() {
	r := i.recorder
	if r == nil || i.token == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.activeToken != i.token {
		return
	}
	category, start := r.activeCategory, r.activeStart
	r.activeToken = 0
	if !r.parent.hasOpenStorePrepareParent() {
		r.invalidate(category)
		return
	}
	end := r.now()
	if end.IsZero() || end.Before(start) {
		r.invalidate(category)
		return
	}
	duration := end.Sub(start)
	if duration < 0 || !start.Add(duration).Equal(end) {
		r.invalidate(category)
		return
	}
	total := &r.sample.FrontierWall
	if category == storePrepareItem {
		total = &r.sample.ItemPrepareWall
	}
	if duration > time.Duration(math.MaxInt64)-*total {
		r.invalidate(category)
		return
	}
	*total += duration
	r.sample.KnownMask |= category
}

func (r *storePrepareDetailRecorder) invalidate(category uint8) {
	r.invalid |= category
	r.sample.KnownMask &^= category
	if category&storePrepareFrontier != 0 {
		r.sample.FrontierWall = 0
	}
	if category&storePrepareItem != 0 {
		r.sample.ItemPrepareWall = 0
	}
}

// seal preserves attempted counts but never manufactures an end for an open
// interval. Publication receives an immutable value; the mutable clock/owners
// are released before any parent trace is locked for its original End.
func (r *storePrepareDetailRecorder) seal() StorePrepareDetailSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		if r.activeToken != 0 {
			r.invalidate(r.activeCategory)
			r.activeToken = 0
		}
		r.closed = true
		r.now = nil
		r.parent = CommitSpan{}
	}
	return r.sample
}

// endStorePrepare keeps the original parent End clock and immutable-Finish
// boundary. Only still-open admitted parents allocate a detached numeric copy.
func (s Span) endStorePrepare(result Result, sample StorePrepareDetailSample) {
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
	// The two valid categories are disjoint subsets, never a duration larger
	// than the retained parent. Unexpected clocks remain unknown, not clipped.
	if sample.FrontierWall > p.Duration || sample.ItemPrepareWall > p.Duration-sample.FrontierWall {
		sample.KnownMask = 0
		sample.FrontierWall, sample.ItemPrepareWall = 0, 0
	}
	copy := sample
	p.StorePrepareDetail = &copy
}

func (s SpanSample) cloneStorePrepareDetail() SpanSample {
	if s.StorePrepareDetail != nil {
		copy := *s.StorePrepareDetail
		s.StorePrepareDetail = &copy
	}
	return s
}
