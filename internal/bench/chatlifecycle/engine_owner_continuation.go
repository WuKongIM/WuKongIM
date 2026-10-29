package chatlifecycle

import (
	"container/heap"
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"time"
)

// engineTimeLease keeps the same time transaction locked until both the caller
// and its accepted owner job finish. Identity is immutable and the lease is
// never reused, so a canceled caller cannot release another transaction's lock.
type engineTimeLease struct {
	engine        *Engine
	generationCtx context.Context
	refs          atomic.Int32
}

// lockTimeTransaction leases the caller's full serialized time mutation. An
// accepted advance may retain its second half before the caller returns.
func (e *Engine) lockTimeTransaction(generationCtx context.Context) *engineTimeLease {
	e.stepMu.Lock()
	lease := &engineTimeLease{engine: e, generationCtx: generationCtx}
	lease.refs.Store(1)
	return lease
}

// release drops exactly one caller or owner half; the final half alone unlocks
// stepMu. Normal caller postprocessing therefore stays inside the transaction.
func (l *engineTimeLease) release() {
	remaining := l.refs.Add(-1)
	if remaining == 0 {
		l.engine.stepMu.Unlock()
	} else if remaining < 0 {
		panic("chat lifecycle time lease released twice")
	}
}

// registerLogicalJob shares Stop's admission fence. A command rejected after
// registration must roll back this one owner half and WaitGroup entry; after
// publication, only the logical job's finalizer may release them.
func (e *Engine) registerLogicalJob(lease *engineTimeLease) error {
	if lease == nil || lease.engine != e {
		return errEngineConfig
	}
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if !e.running || !e.accepting || e.generationCtx != lease.generationCtx {
		return errEngineNotRunning
	}
	if !lease.refs.CompareAndSwap(1, 2) {
		return errEngineConfig
	}
	e.logicalJobs.Add(1)
	return nil
}

// engineAdvanceJob is the generation's one private continuation. Its budget,
// context, time cut, diagnostics and reply belong to the whole logical advance,
// not to an individual owner slice. All mutable fields are owner-only.
type engineAdvanceJob struct {
	ctx      context.Context
	now      time.Time
	queuedAt time.Time
	turn     *driverTurn
	lease    *engineTimeLease
	response chan<- advanceResult
	result   advanceResult
	// Initialization, admissionError and expiry are consumed once per job.
	initialized bool
	// The original SEND/Gosched quantum crosses total-work slice boundaries.
	sentWorkSinceYield int
	// Native terminal failures suppress the original CPU-saturation check.
	generationTerminal bool
}

// startAdvanceJob preserves the original command-entry cancellation check and
// queue timing. The explicit lease prevents any second advance from replacing
// a canceled caller's still-owned continuation.
func (e *Engine) startAdvanceJob(job *engineAdvanceJob) {
	if err := job.ctx.Err(); err != nil {
		job.result.err = err
		e.finishAdvanceJob(job)
		return
	}
	if e.advanceJob != nil {
		job.result.err = errEngineConfig
		e.finishAdvanceJob(job)
		return
	}
	if job.turn != nil {
		job.turn.record.AdvanceWait += e.driverElapsed(job.turn, job.queuedAt)
	}
	e.advanceJob = job
	e.runAdvanceSlice(job)
}

func (e *Engine) runAdvanceSlice(job *engineAdvanceJob) {
	if e.advanceSlice(job) {
		// advanceSlice has already cleared the active-owner diagnostic pointer.
		e.finishAdvanceJob(job)
	}
}

// finishAdvanceJob is the sole accepted-job completion path. A buffered reply
// never waits for a canceled caller. Job ownership joins only after the slot,
// driver pointer and owner half have all been released.
func (e *Engine) finishAdvanceJob(job *engineAdvanceJob) {
	if e.advanceJob == job {
		e.advanceJob = nil
	}
	e.driverTurn = nil
	job.response <- job.result
	job.lease.release()
	e.logicalJobs.Done()
}

// advanceSlice returns after a fixed total-work share, without renewing the
// original maxWork budget or time cut. Only the initial entry checks ctx.Err;
// later slices preserve the original per-kind cancellation decisions.
func (e *Engine) advanceSlice(job *engineAdvanceJob) bool {
	turn, now := job.turn, job.now
	e.driverTurn = turn
	defer func() { e.driverTurn = nil }()
	if !job.initialized {
		// The old advance checked again after the command's queue-timing read.
		if err := job.ctx.Err(); err != nil {
			job.result.err = err
			return true
		}
		job.initialized = true
		e.beginDriverCPUOwnerTurn(turn)
		e.now = now
		verifierStarted := e.driverNow(turn)
		expired := e.verifier.expireCorrelations(now, func(clientMsgNo string) {
			e.sessions.releaseCorrelationLease(clientMsgNo)
		})
		if turn != nil {
			turn.record.VerifierExpiry += e.driverElapsed(turn, verifierStarted)
			turn.record.CorrelationsExpired += uint64(expired)
		}
		e.drainDriverCompletions(turn, driverDrainInitial)
		job.result.err = e.admissionError
	}
	for sliceProcessed := 0; sliceProcessed < completionFairnessQuantum && job.result.processed < e.maxWork; sliceProcessed++ {
		workDue := len(e.work) > 0 && !e.work[0].due.After(now)
		retryDue := e.retries != nil && e.retries.due(now)
		if !workDue && !retryDue {
			break
		}
		workStarted := e.driverNow(turn)
		processedSend := false
		var workErr error
		if retryDue && (!workDue || !e.work[0].due.Before(e.retries.entries[0].Due)) {
			retries := e.retries.PopDue(now, 1)
			if len(retries) == 1 {
				processedSend = true
				workErr = e.processAttempt(job.ctx, retries[0].Intent, retries[0].Attempt.Attempt, now)
			}
		} else {
			work := heap.Pop(&e.work).(*engineWork)
			if work.kind == engineWorkSend {
				e.queuedSends--
				processedSend = true
			}
			workErr = e.processWork(job.ctx, work, now)
		}
		if turn != nil {
			turn.record.WorkExecution += e.driverElapsed(turn, workStarted)
			turn.record.Processed++
		}
		job.result.err = errors.Join(job.result.err, workErr)
		job.result.processed++
		e.drainDriverCompletions(turn, driverDrainAfterWork)
		if runtimeFailureTerminatesGeneration(workErr) {
			job.generationTerminal = true
			break
		}
		if processedSend {
			job.sentWorkSinceYield++
		}
		if job.sentWorkSinceYield >= completionFairnessQuantum {
			if len(e.inflight) > 0 {
				yieldStarted, yieldClockOK := e.beginDriverYield(turn)
				runtime.Gosched()
				e.endDriverYield(turn, yieldStarted, yieldClockOK)
				e.drainDriverCompletions(turn, driverDrainFairness)
			}
			job.sentWorkSinceYield = 0
		}
	}
	due := (len(e.work) > 0 && !e.work[0].due.After(now)) || (e.retries != nil && e.retries.due(now))
	if !job.generationTerminal && job.result.processed == e.maxWork && due {
		job.result.err = errors.Join(job.result.err, e.recordRuntimeFailure(RuntimeFailureEngineCPUSaturated, uint64(e.maxWork)))
	}
	return job.generationTerminal || job.result.processed == e.maxWork || !due
}
