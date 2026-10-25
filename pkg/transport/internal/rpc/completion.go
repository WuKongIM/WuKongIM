package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// Completion owns one RPC's terminal decision independently of waiter scheduling.
// Payload copying and context methods run outside mu; locked decisions only assign
// bounded scalar state. A response remains provisional until Send succeeds.
type Completion struct {
	// mu serializes candidate, cancellation, shutdown, and the Send prerequisite.
	mu sync.Mutex
	// ctx is consulted only outside mu; deadline retains a monotonic reference.
	ctx         context.Context
	deadline    time.Time
	hasDeadline bool
	// done closes exactly once when a decided result also has a known Send result.
	done      chan struct{}
	decided   bool
	sendKnown bool
	notified  bool
	// result remains provisional until sendKnown, then immutable once decided.
	result CompletionResult
}

// CompletionResult distinguishes caller cancellation from a remote typed error.
// LocalContext permits only the winning caller cancellation to send a cancel frame.
type CompletionResult struct {
	Response     Response
	LocalContext bool
}

// NewCompletion captures the caller deadline on a monotonic reference. It does
// not add a timer or goroutine; the caller's existing context provides wakeups.
func NewCompletion(ctx context.Context) *Completion {
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		deadline = normalizeCompletionDeadline(deadline, time.Now())
	}
	return &Completion{ctx: ctx, deadline: deadline, hasDeadline: hasDeadline, done: make(chan struct{})}
}

func normalizeCompletionDeadline(deadline, now time.Time) time.Time {
	return now.Add(deadline.Sub(now))
}

// Done wakes the waiter only after both Send and a terminal decision are known.
func (c *Completion) Done() <-chan struct{} { return c.done }

// Publish commits a fully copied response if it precedes the captured deadline.
// The timestamp is acquired while owning the decision, so lock wait counts toward
// the budget. A trace's Notified boundary is this commit, not a channel send.
func (c *Completion) Publish(response Response) bool {
	contextErr := c.ctx.Err()
	c.mu.Lock()
	defer c.mu.Unlock()
	var at time.Time
	if c.hasDeadline || response.ReceiveTiming != nil {
		at = time.Now()
	}
	return c.publishLocked(response, at, contextErr)
}

func (c *Completion) publishLocked(response Response, at time.Time, contextErr error) bool {
	if c.decided {
		return false
	}
	if contextErr != nil {
		c.decideContextLocked(contextErr)
		return false
	}
	if c.hasDeadline && !at.Before(c.deadline) {
		c.decideContextLocked(context.DeadlineExceeded)
		return false
	}
	if response.ReceiveTiming != nil {
		response.ReceiveTiming.Notified = at
	}
	c.result = CompletionResult{Response: response}
	c.decided = true
	c.notifyLocked()
	return true
}

// SendResult fixes the send prerequisite once. An original send error overrides
// a provisional response or cancellation; a later duplicate cannot revise it.
func (c *Completion) SendResult(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sendKnown {
		return
	}
	c.sendKnown = true
	if err != nil {
		c.result = CompletionResult{Response: Response{Err: err}}
		c.decided = true
	}
	c.notifyLocked()
}

// Resolve arbitrates every wakeup against the same immutable owner decision.
// Context methods are deliberately evaluated before acquiring the owner lock.
func (c *Completion) Resolve(connectionErr error) (CompletionResult, bool) {
	contextErr := c.ctx.Err()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resolveLocked(contextErr, connectionErr)
}

func (c *Completion) resolveLocked(contextErr, connectionErr error) (CompletionResult, bool) {
	if !c.decided {
		if contextErr != nil {
			c.decideContextLocked(contextErr)
		} else if connectionErr != nil {
			c.result = CompletionResult{Response: Response{Err: connectionErr}}
			c.decided = true
			c.notifyLocked()
		}
	}
	return c.result, c.sendKnown && c.decided
}

// Fail settles an undecided owner after it has been detached from index locks.
// A response already committed before connection shutdown retains precedence.
func (c *Completion) Fail(err error) {
	if err == nil {
		err = core.ErrStopped
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.decided {
		return
	}
	c.result = CompletionResult{Response: Response{Err: err}}
	c.decided = true
	c.notifyLocked()
}

func (c *Completion) decideContextLocked(err error) {
	if err == context.Canceled {
		err = core.ErrCanceled
	}
	c.result = CompletionResult{Response: Response{Err: err}, LocalContext: true}
	c.decided = true
	c.notifyLocked()
}

func (c *Completion) notifyLocked() {
	if c.sendKnown && c.decided && !c.notified {
		c.notified = true
		close(c.done)
	}
}
