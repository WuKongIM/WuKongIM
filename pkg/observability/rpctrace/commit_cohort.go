package rpctrace

import (
	"math"
	"time"
)

const (
	// MaxCommitCohortLinks bounds complete request-attempt records per Trace.
	MaxCommitCohortLinks = 2
	// MaxCommitObservationOwners bounds sampled-owner fanout independently of
	// business batches. Overflow loses observation, never durability or admission.
	MaxCommitObservationOwners = 64
)

// CommitCohortRef identifies one actual physical build attempt. Instance is an
// opaque recorder namespace; it is not a caller, Channel or durable identity.
type CommitCohortRef struct {
	Instance string `json:"instance"`
	Attempt  uint64 `json:"attempt"`
}

// CommitInterval is a local Trace-relative observation. Missing or incomplete
// intervals are unknown and overlapping intervals must never be added.
type CommitInterval struct {
	Observed bool          `json:"observed"`
	Complete bool          `json:"complete"`
	Offset   time.Duration `json:"offset"`
	Duration time.Duration `json:"duration"`
}

// CommitNativeFacts copies an existing physical engine observation without
// adding timers. CommitWaitDuration includes publication and WAL completion.
type CommitNativeFacts struct {
	Available                   bool          `json:"available"`
	TotalDuration               time.Duration `json:"total_duration"`
	SemaphoreWaitDuration       time.Duration `json:"semaphore_wait_duration"`
	WALQueueWaitDuration        time.Duration `json:"wal_queue_wait_duration"`
	MemTableWriteStallDuration  time.Duration `json:"mem_table_write_stall_duration"`
	L0ReadAmpWriteStallDuration time.Duration `json:"l0_read_amp_write_stall_duration"`
	WALRotationDuration         time.Duration `json:"wal_rotation_duration"`
	CommitWaitDuration          time.Duration `json:"commit_wait_duration"`
}

// CommitCohortLink separates one request's terminal work from its shared
// physical build. TimedSum excludes residual gaps and is not a wall clock.
type CommitCohortLink struct {
	CohortRef      CommitCohortRef  `json:"cohort_ref"`
	RequestOrdinal uint32           `json:"request_ordinal"`
	RebuildOf      *CommitCohortRef `json:"rebuild_of,omitempty"`
	// PhysicalWindow spans actual Build entry through the last physical phase.
	PhysicalWindow CommitInterval `json:"physical_window"`
	// SubmitWindow includes the synchronous request-observer tail.
	SubmitWindow CommitInterval `json:"submit_window"`
	Build        CommitInterval `json:"build"`
	Durable      CommitInterval `json:"durable"`
	Publish      CommitInterval `json:"publish"`
	// TerminalWait includes prior finalizers/observers without guessing a cause.
	TerminalWait CommitInterval `json:"terminal_wait"`
	// Finalize brackets the original resource-release callback, including
	// selected observation overhead before the callback starts.
	Finalize CommitInterval    `json:"finalize"`
	TimedSum time.Duration     `json:"timed_sum"`
	Native   CommitNativeFacts `json:"native"`
	Outcome  string            `json:"outcome"`
}

// CommitCohortSample is detached at Finish and Clone. OwnersDropped records
// bounded owner-admission loss, separately from whole attempt links dropped.
type CommitCohortSample struct {
	Links         []CommitCohortLink `json:"links"`
	Dropped       uint64             `json:"dropped"`
	OwnersDropped uint64             `json:"owners_dropped,omitempty"`
}

type commitCohortState struct {
	links         [MaxCommitCohortLinks]CommitCohortLink
	count         uint8
	dropped       uint64
	ownersDropped uint64
}

type commitObservationOwners struct {
	traces [MaxCommitObservationOwners]*Trace
	count  uint8
}

// CommitObservation carries at most 64 sampled Trace owners and no caller
// context, Future, item or payload. The zero value is disabled.
type CommitObservation struct{ owners *commitObservationOwners }

// Empty reports whether this handle carries any admitted owner references.
func (o CommitObservation) Empty() bool { return o.owners == nil || o.owners.count == 0 }

func commitTraceOpen(t *Trace) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed
}

func incrementCommitDrop(n *uint64) {
	if *n != math.MaxUint64 {
		(*n)++
	}
}
func (t *Trace) dropCommitOwner() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		incrementCommitDrop(&t.cohorts.ownersDropped)
	}
}

// NewCommitObservation admits a bounded immutable owner set. It allocates
// nothing for nil/finished owners and deduplicates without an unbounded map.
func NewCommitObservation(traces ...*Trace) CommitObservation {
	var owners *commitObservationOwners
	for _, trace := range traces {
		if !commitTraceOpen(trace) {
			continue
		}
		duplicate := false
		if owners != nil {
			for i := 0; i < int(owners.count); i++ {
				if owners.traces[i] == trace {
					duplicate = true
					break
				}
			}
		}
		if duplicate {
			continue
		}
		if owners == nil {
			owners = &commitObservationOwners{}
		}
		if int(owners.count) == MaxCommitObservationOwners {
			trace.dropCommitOwner()
			continue
		}
		owners.traces[owners.count] = trace
		owners.count++
	}
	return CommitObservation{owners: owners}
}

// CombineCommitObservations keeps the same 64-owner cap across DB preparation
// and never retains an input handle list. Existing immutable sets are reused.
func CombineCommitObservations(a, b CommitObservation) CommitObservation {
	if a.Empty() {
		return b
	}
	if b.Empty() {
		return a
	}
	owners := a.owners
	copied := false
	for i := 0; i < int(b.owners.count); i++ {
		trace := b.owners.traces[i]
		if !commitTraceOpen(trace) {
			continue
		}
		duplicate := false
		for j := 0; j < int(owners.count); j++ {
			if owners.traces[j] == trace {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		if int(owners.count) == MaxCommitObservationOwners {
			trace.dropCommitOwner()
			continue
		}
		if !copied {
			next := *owners
			owners = &next
			copied = true
		}
		owners.traces[owners.count] = trace
		owners.count++
	}
	return CommitObservation{owners: owners}
}

// NewCommitCohortRef is called once centrally at a real Build entry. It reuses
// a recorder's existing namespace and saturates rather than wrapping its own
// sequence. Disabled or exhausted observations cannot affect a business write.
func NewCommitCohortRef(o CommitObservation) CommitCohortRef {
	if o.Empty() {
		return CommitCohortRef{}
	}
	for i := 0; i < int(o.owners.count); i++ {
		trace := o.owners.traces[i]
		if !commitTraceOpen(trace) || trace.owner == nil {
			continue
		}
		r := trace.owner
		for {
			previous := r.cohortSequence.Load()
			if previous == math.MaxUint64 {
				break
			}
			if r.cohortSequence.CompareAndSwap(previous, previous+1) {
				return CommitCohortRef{Instance: r.prefix, Attempt: previous + 1}
			}
		}
	}
	return CommitCohortRef{}
}

// DropCommitCohort records a known physical attempt lost to identity exhaustion.
func (o CommitObservation) DropCommitCohort() {
	if o.Empty() {
		return
	}
	for i := 0; i < int(o.owners.count); i++ {
		t := o.owners.traces[i]
		t.mu.Lock()
		if !t.closed {
			incrementCommitDrop(&t.cohorts.dropped)
		}
		t.mu.Unlock()
	}
}

// CommitPhase is the closed physical callback vocabulary.
type CommitPhase uint8

const (
	CommitPhaseBuild CommitPhase = iota + 1
	CommitPhaseDurable
	CommitPhasePublish
)

// CommitAttempt retains only a bounded owner set plus the slots admitted at
// actual Build entry. Copies observe the same fixed Trace storage.
type CommitAttempt struct {
	owners *commitObservationOwners
	slots  [MaxCommitObservationOwners]int8
}

// BeginCommitCohort binds a physical reference to each original sampled owner.
// A full Trace loses one whole link; partial references are never published.
func (o CommitObservation) BeginCommitCohort(ref CommitCohortRef, ordinal uint32, previous *CommitCohortRef, at time.Time) CommitAttempt {
	var attempt CommitAttempt
	if o.Empty() || ref.Instance == "" || ref.Attempt == 0 {
		return attempt
	}
	attempt.owners = o.owners
	for i := 0; i < int(o.owners.count); i++ {
		attempt.slots[i] = -1
		t := o.owners.traces[i]
		t.mu.Lock()
		if !t.closed {
			if int(t.cohorts.count) == MaxCommitCohortLinks {
				incrementCommitDrop(&t.cohorts.dropped)
			} else {
				index := t.cohorts.count
				t.cohorts.count++
				attempt.slots[i] = int8(index)
				link := CommitCohortLink{CohortRef: ref, RequestOrdinal: ordinal, Outcome: "unknown", PhysicalWindow: CommitInterval{Observed: true, Offset: at.Sub(t.at)}}
				if previous != nil {
					copy := *previous
					link.RebuildOf = &copy
				}
				t.cohorts.links[index] = link
			}
		}
		t.mu.Unlock()
	}
	return attempt
}

func (a CommitAttempt) update(fn func(*Trace, *CommitCohortLink)) {
	if a.owners == nil {
		return
	}
	for i := 0; i < int(a.owners.count); i++ {
		if a.slots[i] < 0 {
			continue
		}
		t := a.owners.traces[i]
		t.mu.Lock()
		if !t.closed {
			fn(t, &t.cohorts.links[a.slots[i]])
		}
		t.mu.Unlock()
	}
}
func commitPhaseInterval(link *CommitCohortLink, phase CommitPhase) *CommitInterval {
	switch phase {
	case CommitPhaseBuild:
		return &link.Build
	case CommitPhaseDurable:
		return &link.Durable
	case CommitPhasePublish:
		return &link.Publish
	default:
		return nil
	}
}
func closeCommitInterval(t *Trace, p *CommitInterval, at time.Time) {
	if p.Observed && !p.Complete {
		p.Duration = at.Sub(t.at) - p.Offset
		if p.Duration < 0 {
			p.Duration = 0
		}
		p.Complete = true
	}
}

// StartPhase uses the caller's existing measured boundary, without a new clock.
func (a CommitAttempt) StartPhase(phase CommitPhase, at time.Time) {
	a.update(func(t *Trace, l *CommitCohortLink) {
		if p := commitPhaseInterval(l, phase); p != nil && !p.Observed {
			*p = CommitInterval{Observed: true, Offset: at.Sub(t.at)}
		}
	})
}

// EndPhase closes only the actual measured callback interval.
func (a CommitAttempt) EndPhase(phase CommitPhase, at time.Time) {
	a.update(func(t *Trace, l *CommitCohortLink) {
		if p := commitPhaseInterval(l, phase); p != nil {
			closeCommitInterval(t, p, at)
		}
	})
}

// EndPhysical retains native facts independently of the classified outcome.
func (a CommitAttempt) EndPhysical(at time.Time, sum time.Duration, native CommitNativeFacts, outcome string, terminal bool) {
	a.update(func(t *Trace, l *CommitCohortLink) {
		closeCommitInterval(t, &l.PhysicalWindow, at)
		l.TimedSum = sum
		l.Native = native
		l.Outcome = commitOutcome(outcome)
		if terminal {
			l.TerminalWait = CommitInterval{Observed: true, Offset: at.Sub(t.at)}
		}
	})
}

// StartFinalize preserves the existing cleanup order, including prior wait.
func (a CommitAttempt) StartFinalize(at time.Time) {
	a.update(func(t *Trace, l *CommitCohortLink) {
		closeCommitInterval(t, &l.TerminalWait, at)
		l.Finalize = CommitInterval{Observed: true, Offset: at.Sub(t.at)}
	})
}

// EndFinalize observes successful callback return without releasing resources.
func (a CommitAttempt) EndFinalize(at time.Time) {
	a.update(func(t *Trace, l *CommitCohortLink) { closeCommitInterval(t, &l.Finalize, at) })
}

// EndTerminalWithoutFinalize closes terminal wait without inventing a callback.
func (a CommitAttempt) EndTerminalWithoutFinalize(at time.Time) {
	a.update(func(t *Trace, l *CommitCohortLink) { closeCommitInterval(t, &l.TerminalWait, at) })
}

// EndSubmit runs after the original synchronous request observer returns.
func (a CommitAttempt) EndSubmit(start, end time.Time) {
	a.update(func(t *Trace, l *CommitCohortLink) {
		l.SubmitWindow = CommitInterval{Observed: true, Complete: true, Offset: start.Sub(t.at), Duration: end.Sub(start)}
	})
}
func commitOutcome(value string) string {
	switch value {
	case "committed", "definitely_not_committed":
		return value
	default:
		return "unknown"
	}
}

func cloneCommitLink(link CommitCohortLink) CommitCohortLink {
	if link.RebuildOf != nil {
		copy := *link.RebuildOf
		link.RebuildOf = &copy
	}
	return link
}
func (s *CommitCohortSample) clone() *CommitCohortSample {
	if s == nil {
		return nil
	}
	copy := *s
	copy.Links = append([]CommitCohortLink(nil), s.Links...)
	for i := range copy.Links {
		copy.Links[i] = cloneCommitLink(copy.Links[i])
	}
	return &copy
}
func (s *commitCohortState) snapshot(elapsed time.Duration) *CommitCohortSample {
	if s.count == 0 && s.dropped == 0 && s.ownersDropped == 0 {
		return nil
	}
	out := &CommitCohortSample{Links: make([]CommitCohortLink, int(s.count)), Dropped: s.dropped, OwnersDropped: s.ownersDropped}
	for i := range out.Links {
		link := cloneCommitLink(s.links[i])
		for _, p := range []*CommitInterval{&link.PhysicalWindow, &link.SubmitWindow, &link.Build, &link.Durable, &link.Publish, &link.TerminalWait, &link.Finalize} {
			if p.Observed && !p.Complete {
				p.Duration = elapsed - p.Offset
				if p.Duration < 0 {
					p.Duration = 0
				}
			}
		}
		out.Links[i] = link
	}
	return out
}

// AppendCommitObserver is an optional extension of shared append observation.
// It selects the same first-effect owners and cannot carry business authority.
type AppendCommitObserver interface{ CommitObservation() CommitObservation }

// CommitObservationForAppend extracts the optional typed owner handle only.
func CommitObservationForAppend(observer AppendPhaseObserver) CommitObservation {
	if observer == nil {
		return CommitObservation{}
	}
	if commit, ok := observer.(AppendCommitObserver); ok {
		return commit.CommitObservation()
	}
	return CommitObservation{}
}
