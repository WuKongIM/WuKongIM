package rpctrace

// CommitSpan joins one bounded interval on the original sampled storage owners.
// It retains no caller context or mutation. Slots are encoded as index+1 so a
// disabled, finished, excluded or full owner can never end an unrelated span.
type CommitSpan struct {
	owners *commitObservationOwners
	slots  [MaxCommitObservationOwners]uint8
}

// BeginStage uses the existing per-Trace span limit and relative clock. excluded
// is an optional Trace already timed by the direct caller; it avoids duplicate
// intervals without changing physical cohort membership or owner admission.
// The zero observation does not read a clock, allocate, or generate an identity.
func (o CommitObservation) BeginStage(stage Stage, excluded *Trace) CommitSpan {
	var phase CommitSpan
	if o.Empty() {
		return phase
	}
	phase.owners = o.owners
	for i := 0; i < int(o.owners.count); i++ {
		trace := o.owners.traces[i]
		if trace == excluded {
			continue
		}
		span := trace.Begin(stage)
		if span.trace != nil {
			phase.slots[i] = uint8(span.index + 1)
		}
	}
	return phase
}

// End closes only admitted slots. Trace.Finish remains the immutable boundary;
// late physical work cannot reopen or mutate a published request sample.
func (s CommitSpan) End(result Result) {
	if s.owners == nil {
		return
	}
	for i := 0; i < int(s.owners.count); i++ {
		if index := s.slots[i]; index != 0 {
			(Span{trace: s.owners.traces[i], index: int(index) - 1}).End(result)
		}
	}
}
