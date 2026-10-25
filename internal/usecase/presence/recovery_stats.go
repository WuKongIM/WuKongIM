package presence

// RecoveryStats is a fixed-size, identity-free snapshot of actual recovery
// ownership. UID admission bytes exclude response memory and process RSS.
type RecoveryStats struct {
	// Disabled result census zeros mean unobserved, not empty result ownership.
	ResultCensusEnabled bool
	// Current ownership includes canceled creators until their actual work joins.
	QueuedCalls, ActiveBatches, ResultCalls, Members, FencedUIDs, OwnedUIDBytes int
	// These flags require a nonempty FIFO; both may be true at the same cut.
	ResultLimited, ExecutionLimited bool
	// Dispatch totals count original work before the fresh Directory recheck,
	// not wire RPCs or successful proof publication.
	BatchesTotal, BatchCallsTotal, BatchUIDsTotal uint64
	// Batch high-water values describe one dispatched physical batch.
	BatchCallsHighWater, BatchUIDsHighWater int
	// Decoded structure is separate from UID admission; exclusions are defined
	// by MeasureRecoveryResults.
	ResultRows, ResultDecodedBytes, PageRows, PageDecodedBytes uint64
	// Result high-water values span concurrently owned batches in this runtime
	// generation, not a per-request history or heap limit.
	ResultRowsHighWater, ResultDecodedBytesHighWater, PageRowsHighWater, PageDecodedBytesHighWater, DecodedBytesHighWater uint64
	// Enforced result ownership is always counted, independent of sampled census.
	ResultBudgetEnabled                                                                                          bool
	OwnedResultRows, OwnedResultBytes, OwnedResultRowsHighWater, OwnedResultBytesHighWater, ResultBudgetRejected uint64
}

// Stats returns one immutable census without retaining callers or results.
func (r *AuthorityRecovery) Stats() RecoveryStats {
	if r == nil {
		return RecoveryStats{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.census
	s.ResultCensusEnabled = r.resultCensus
	s.QueuedCalls, s.ActiveBatches, s.ResultCalls = len(r.queue), len(r.active), r.resultCalls
	s.Members, s.FencedUIDs, s.OwnedUIDBytes = len(r.members), len(r.inFlight), r.ownedUIDBytes
	s.ResultLimited = len(r.queue) > 0 && r.boundedOwners == nil && r.resultCalls == recoveryMaxResultCalls
	s.ExecutionLimited = len(r.queue) > 0 && len(r.active) == recoveryMaxCohorts
	if r.resultBudget != nil {
		b := r.resultBudget.Stats()
		s.ResultBudgetEnabled = true
		s.OwnedResultRows, s.OwnedResultBytes = b.Rows, b.Bytes
		s.OwnedResultRowsHighWater, s.OwnedResultBytesHighWater = b.HighWaterRows, b.HighWaterBytes
		s.ResultBudgetRejected = b.Rejected
	}
	return s
}

func (r *AuthorityRecovery) observeResultUsage(e *recoveryExecution, usage RecoveryResultUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observeResultUsageLocked(e, usage)
}

func (r *AuthorityRecovery) observeResultUsageLocked(e *recoveryExecution, usage RecoveryResultUsage) {
	s := &r.census
	s.ResultRows = s.ResultRows - e.usage.ResultRows + usage.ResultRows
	s.ResultDecodedBytes = s.ResultDecodedBytes - e.usage.ResultDecodedBytes + usage.ResultDecodedBytes
	s.PageRows = s.PageRows - e.usage.PageRows + usage.PageRows
	s.PageDecodedBytes = s.PageDecodedBytes - e.usage.PageDecodedBytes + usage.PageDecodedBytes
	e.usage = usage
	s.ResultRowsHighWater = max(s.ResultRowsHighWater, s.ResultRows)
	s.ResultDecodedBytesHighWater = max(s.ResultDecodedBytesHighWater, s.ResultDecodedBytes)
	s.PageRowsHighWater = max(s.PageRowsHighWater, s.PageRows)
	s.PageDecodedBytesHighWater = max(s.PageDecodedBytesHighWater, s.PageDecodedBytes)
	s.DecodedBytesHighWater = max(s.DecodedBytesHighWater, s.ResultDecodedBytes+s.PageDecodedBytes)
}
