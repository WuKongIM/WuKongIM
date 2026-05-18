package replica

import "sync"

// appendRequestPool recycles append request envelopes after successful commits.
var appendRequestPool = sync.Pool{
	New: func() any { return new(appendRequest) },
}

// appendWaiterPool recycles waiter channels without carrying stale completions.
var appendWaiterPool = sync.Pool{
	New: func() any { return &appendWaiter{ch: make(chan appendCompletion, 1)} },
}

func acquireAppendRequest() *appendRequest {
	return appendRequestPool.Get().(*appendRequest)
}

func releaseAppendRequest(req *appendRequest) {
	if req == nil {
		return
	}
	waiter := req.waiter
	*req = appendRequest{}
	appendRequestPool.Put(req)
	releaseAppendWaiter(waiter)
}

func acquireAppendWaiter() *appendWaiter {
	waiter := appendWaiterPool.Get().(*appendWaiter)
	if waiter.ch == nil {
		waiter.ch = make(chan appendCompletion, 1)
	}
	return waiter
}

func releaseAppendWaiter(waiter *appendWaiter) {
	if waiter == nil {
		return
	}
	ch := waiter.ch
	for {
		select {
		case <-ch:
		default:
			*waiter = appendWaiter{ch: ch}
			appendWaiterPool.Put(waiter)
			return
		}
	}
}
