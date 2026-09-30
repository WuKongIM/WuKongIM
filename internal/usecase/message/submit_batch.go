package message

import (
	"errors"
	"sync"
	"time"
)

// BatchAdmission atomically accepts prepared items or returns an error without
// invoking complete. On success complete runs exactly once, possibly before
// Submit returns. Implementations copy descriptors, retain immutable payloads,
// bound all accepted ownership and preserve overlapping canonical Channel order.
type BatchAdmission interface {
	Submit([]SendBatchItem, func([]SendBatchItemResult)) error
}

// SubmitBatchEach joins permission, directory and hooks before returning, while
// accepted append work completes asynchronously. Callers must serialize preparation
// for a session, retain input payloads/capacity until complete returns, and order
// publication across batches. Within this batch emit is serialized and session-
// ordered. Its first error suppresses further emits but never skips completion.
// Nil error transfers exactly one complete call, which may run before return.
// Item rejections are results; a returned error transfers no ownership.
func (a *App) SubmitBatchEach(items []SendBatchItem, emit func(int, SendBatchItemResult) error, complete func(error)) error {
	if emit == nil || complete == nil {
		return ErrSendBatchEmitterRequired
	}
	if a == nil || a.batchAdmission == nil {
		return ErrRouteNotReady
	}
	_ = a.sendBatchEach(items, emit, a.batchAdmission, complete)
	return nil
}

// sendBatchPublication owns only result publication. Preparation must never read
// its concurrently changing terminal flags to decide subsequent admission.
type sendBatchPublication struct {
	mu       sync.Mutex
	results  []SendBatchItemResult
	terminal []bool
	next     []int
	keys     []sendBatchSessionKey
	heads    map[sendBatchSessionKey]int
	pending  int
	prepared bool
	finished bool
	err      error
	emit     func(int, SendBatchItemResult) error
	cancel   func()
	complete func(error)
}

func newSendBatchPublication(items []SendBatchItem, emit func(int, SendBatchItemResult) error, cancel func(), complete func(error)) *sendBatchPublication {
	p := &sendBatchPublication{results: make([]SendBatchItemResult, len(items)), terminal: make([]bool, len(items)), next: make([]int, len(items)), keys: make([]sendBatchSessionKey, len(items)), heads: make(map[sendBatchSessionKey]int), pending: len(items), emit: emit, cancel: cancel, complete: complete}
	tails := make(map[sendBatchSessionKey]int)
	for i, item := range items {
		p.next[i] = -1
		key := sendBatchSessionKeyFor(i, item.Command)
		p.keys[i] = key
		if tail, ok := tails[key]; ok {
			p.next[tail] = i
		} else {
			p.heads[key] = i
		}
		tails[key] = i
	}
	return p
}

func (p *sendBatchPublication) finishItem(index int, result SendBatchItemResult) {
	p.mu.Lock()
	if index < 0 || index >= len(p.results) || p.terminal[index] {
		p.err = errors.Join(p.err, ErrSendBatchEmissionMismatch)
		p.mu.Unlock()
		return
	}
	p.results[index] = result
	p.terminal[index] = true
	p.pending--
	key := p.keys[index]
	for head := p.heads[key]; head >= 0 && p.terminal[head]; head = p.heads[key] {
		if p.err == nil {
			p.err = p.emit(head, p.results[head])
		}
		p.heads[key] = p.next[head]
	}
	complete, err := p.finishLocked()
	p.mu.Unlock()
	if complete != nil {
		complete(err)
	}
}

// finishPreparation is the second half of the join: even inline results may not
// cancel contexts or notify the caller while directory/hook preparation is active.
func (p *sendBatchPublication) finishPreparation(preparationErr error) error {
	p.mu.Lock()
	p.prepared = true
	p.err = errors.Join(p.err, preparationErr)
	complete, err := p.finishLocked()
	p.mu.Unlock()
	if complete != nil {
		complete(err)
	}
	return err
}

func (p *sendBatchPublication) finishLocked() (func(error), error) {
	if !p.prepared || p.pending != 0 || p.finished {
		return nil, p.err
	}
	p.finished = true
	p.cancel()
	return p.complete, p.err
}

func (a *App) admitSendBatchLane(admission BatchAdmission, items []SendBatchItem, indexes []int, permission, preAppend time.Duration, started time.Time, finalize func(int, SendBatchItemResult)) {
	complete := func(results []SendBatchItemResult) {
		duration := time.Since(started)
		stageResult := sendBatchStageResultOK
		if len(results) != len(items) {
			results = make([]SendBatchItemResult, len(items))
			for i := range results {
				results[i] = SendBatchItemResult{Result: SendResult{Reason: ReasonSystemError}, Err: ErrSendBatchEmissionMismatch}
			}
		}
		for i := range results {
			if results[i].Err != nil {
				stageResult = sendBatchStageResultErr
			}
			results[i].Err = annotateSendBatchTimeout(results[i].Err, SendBatchFailureDiagnostics{FailedStage: sendBatchStageSubmitter, Permission: permission, PreAppend: preAppend, Submitter: duration, DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(items[i].Deadline, started)})
		}
		// Observe before final publication so complete joins this lane's diagnostics.
		a.observeSendBatchStage(sendBatchStageSubmitter, stageResult, len(items), duration)
		for i, result := range results {
			finalize(indexes[i], result)
		}
	}
	if err := admission.Submit(items, complete); err != nil {
		results := make([]SendBatchItemResult, len(items))
		for i := range results {
			results[i].Err = err
		}
		complete(results)
	}
}
