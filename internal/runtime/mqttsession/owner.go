// Package mqttsession owns node-local MQTT execution, never durable authority.
package mqttsession

import (
	"container/heap"
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
)

var (
	ErrOwnerInvalid = errors.New("mqttsession: invalid owner execution request")
	ErrOwnerFenced  = errors.New("mqttsession: owner execution fenced")
	ErrOwnerUnknown = errors.New("mqttsession: owner isolation unproved")
	ErrOwnerLimit   = errors.New("mqttsession: owner execution capacity reached")
	ErrOwnerStopped = errors.New("mqttsession: owner execution stopped")
	ErrOwnerClose   = errors.New("mqttsession: transport close failed")
)

// Claim contains already authenticated identity and proposed durable generations.
// Reserve does not establish that authentication or commit Session ownership.
type Claim struct {
	Key               contract.Key
	UID               string
	SessionGeneration uint64
	OwnerGeneration   uint64
}

// OwnerOptions bounds retained owners, admitted work and local lease horizons.
type OwnerOptions struct {
	NodeID uint64
	// BootID must be unique to this process and registry lifetime.
	BootID string
	// Capacity includes pending and closing owners, not just active connections.
	Capacity int
	// MaxOperations bounds admitted scopes per owner; there is no waiting queue.
	MaxOperations int
	// PendingTimeout bounds incomplete local reservations before activation.
	PendingTimeout time.Duration
	// MaxLease bounds the accepted local execution horizon, at most one minute.
	MaxLease time.Duration
	// CloseRetry delays failed/unfinished cleanup without reopening admission.
	CloseRetry time.Duration
	// Now and all supplied deadlines use this process's monotonic time base.
	Now func() time.Time
}

// CloseTransport must seal all future socket writes and close the physical
// connection before returning nil. It must obey cancellation and must not wait
// for this registry's business cleanup or call Quiesce recursively.
type CloseTransport func(context.Context) error

type ownerStage uint8

const (
	ownerPending ownerStage = 1 + iota
	ownerActive
	ownerClosing
)

type ownerCloseAttempt struct {
	done chan struct{}
	err  error
}
type ownerEntry struct {
	owner    contract.Owner
	uid      string
	stage    ownerStage
	revision uint64
	// leaseUntil fences new work even when the app's expiry sweep is delayed.
	leaseUntil time.Time
	// deadline schedules pending expiry, lease expiry or a closing retry.
	deadline        time.Time
	heapIndex       int
	ctx             context.Context
	cancel          context.CancelCauseFunc
	closeTransport  CloseTransport
	transportClosed bool
	operations      int
	attempt         *ownerCloseAttempt
	complete        chan struct{}
}

// Owners tracks bounded owner execution. One short registry lock protects maps,
// counts and the indexed deadline heap; no callback or wait runs under it.
// The app owns bounded Sweep calls; this registry creates no worker per session.
type Owners struct {
	mu        sync.Mutex
	opts      OwnerOptions
	entries   map[uint64]*ownerEntry
	deadlines ownerDeadlines
	counts    OwnerSnapshot
	// issued advances only with registration under mu and never wraps/reuses IDs.
	issued  uint64
	stopped bool
	ctx     context.Context
	cancel  context.CancelCauseFunc
}

func NewOwners(opts OwnerOptions) (*Owners, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NodeID == 0 || !contract.ValidIdentity(opts.BootID, 128) || opts.Capacity < 1 || opts.Capacity > 1_000_000 || opts.MaxOperations < 1 || opts.MaxOperations > 1024 || opts.PendingTimeout <= 0 || opts.PendingTimeout > time.Minute || opts.MaxLease <= 0 || opts.MaxLease > time.Minute || opts.CloseRetry <= 0 || opts.CloseRetry > time.Minute {
		return nil, ErrOwnerInvalid
	}
	if now := opts.Now(); now == now.Round(0) {
		return nil, ErrOwnerInvalid
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Owners{opts: opts, entries: make(map[uint64]*ownerEntry), ctx: ctx, cancel: cancel}, nil
}

// Reserve publishes a pending identity before it can enter durable metadata.
// Different ClientIDs and concurrent candidates do not invoke WK device kicks.
func (m *Owners) Reserve(claim Claim, closeTransport CloseTransport) (contract.Owner, error) {
	if m == nil || claim.Key.Validate() != nil || !contract.ValidIdentity(claim.UID, 1024) || claim.SessionGeneration == 0 || claim.OwnerGeneration == 0 || closeTransport == nil {
		return contract.Owner{}, ErrOwnerInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return contract.Owner{}, ErrOwnerStopped
	}
	if len(m.entries) >= m.opts.Capacity || m.issued == math.MaxUint64 {
		return contract.Owner{}, ErrOwnerLimit
	}
	m.issued++
	o := contract.Owner{Key: claim.Key, SessionGeneration: claim.SessionGeneration, OwnerGeneration: claim.OwnerGeneration, NodeID: m.opts.NodeID, BootID: m.opts.BootID, ConnectionID: m.issued}
	ctx, cancel := context.WithCancelCause(m.ctx)
	e := &ownerEntry{owner: o, uid: claim.UID, stage: ownerPending, heapIndex: -1, ctx: ctx, cancel: cancel, closeTransport: closeTransport, complete: make(chan struct{})}
	m.entries[o.ConnectionID] = e
	m.counts.Pending++
	m.scheduleLocked(e, m.opts.Now().Add(m.opts.PendingTimeout))
	return o, nil
}

// Activate opens execution only after the caller proves this exact committed
// owner and derives a conservative deadline. Local time is never remote proof.
func (m *Owners) Activate(owner contract.Owner, revision uint64, until time.Time) error {
	return m.installLease(owner, revision, until, false)
}

// Renew accepts a newer committed receipt only while the existing lease is live.
// Neither a delayed renewal nor an exact retry can resurrect a fenced owner.
func (m *Owners) Renew(owner contract.Owner, revision uint64, until time.Time) error {
	return m.installLease(owner, revision, until, true)
}

func (m *Owners) installLease(owner contract.Owner, revision uint64, until time.Time, renew bool) error {
	if m == nil {
		return ErrOwnerInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrOwnerStopped
	}
	e, err := m.entryLocked(owner)
	if err != nil {
		return err
	}
	if e == nil {
		return ErrOwnerFenced
	}
	now := m.opts.Now()
	if e.stage == ownerClosing || !now.Before(e.deadline) {
		m.fenceLocked(e)
		return ErrOwnerFenced
	}
	if revision == 0 || until == until.Round(0) || !until.After(now) || until.Sub(now) > m.opts.MaxLease {
		return ErrOwnerInvalid
	}
	if e.stage == ownerActive && e.revision == revision && e.leaseUntil.Equal(until) {
		return nil
	}
	if renew {
		if e.stage != ownerActive || revision <= e.revision {
			return ErrOwnerFenced
		}
	} else if e.stage != ownerPending {
		return ErrOwnerFenced
	}
	if e.stage == ownerPending {
		m.counts.Pending--
		m.counts.Active++
	}
	e.stage, e.revision, e.leaseUntil = ownerActive, revision, until
	m.scheduleLocked(e, until)
	return nil
}

// Operation covers every effect of one admitted command or delivery. Done must
// run after all effects finish; cancellation alone never releases its ownership.
type Operation struct {
	owners     *Owners
	entry      *ownerEntry
	ctx        context.Context
	cancel     context.CancelCauseFunc
	stopParent func() bool
	done       atomic.Bool
}

func (o *Operation) Context() context.Context { return o.ctx }

// Begin is the admission ordering point shared with Quiesce and lease expiry.
func (m *Owners) Begin(ctx context.Context, owner contract.Owner) (*Operation, error) {
	if m == nil || ctx == nil {
		return nil, ErrOwnerInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil, ErrOwnerStopped
	}
	e, err := m.entryLocked(owner)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if e == nil || e.stage != ownerActive {
		m.mu.Unlock()
		return nil, ErrOwnerFenced
	}
	if !m.opts.Now().Before(e.leaseUntil) {
		m.fenceLocked(e)
		m.mu.Unlock()
		return nil, ErrOwnerFenced
	}
	if e.operations >= m.opts.MaxOperations {
		m.mu.Unlock()
		return nil, ErrOwnerLimit
	}
	e.operations++
	m.counts.Operations++
	opCtx, cancel := context.WithCancelCause(e.ctx)
	op := &Operation{owners: m, entry: e, ctx: opCtx, cancel: cancel}
	m.mu.Unlock()
	if ctx.Done() != nil {
		op.stopParent = context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	}
	return op, nil
}

// Done is idempotent and may run concurrently with transport close or expiry.
func (o *Operation) Done() {
	if o == nil || o.owners == nil || !o.done.CompareAndSwap(false, true) {
		return
	}
	if o.stopParent != nil {
		o.stopParent()
	}
	o.cancel(context.Canceled)
	m := o.owners
	m.mu.Lock()
	defer m.mu.Unlock()
	o.entry.operations--
	m.counts.Operations--
	m.retireLocked(o.entry)
}

// Quiesce proves exact-owner isolation only after transport closure and operation
// drain. Failed/timed-out attempts stay fenced and retain their capacity.
func (m *Owners) Quiesce(ctx context.Context, owner contract.Owner) error {
	if m == nil || ctx == nil {
		return ErrOwnerInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	e, err := m.entryLocked(owner)
	if err != nil || e == nil {
		m.mu.Unlock()
		return err
	}
	m.fenceLocked(e)
	if e.transportClosed {
		done := e.complete
		m.retireLocked(e)
		m.mu.Unlock()
		return awaitOwner(ctx, done)
	}
	if attempt := e.attempt; attempt != nil {
		m.mu.Unlock()
		if err := awaitOwner(ctx, attempt.done); err != nil {
			return err
		}
		if attempt.err != nil {
			return attempt.err
		}
		return awaitOwner(ctx, e.complete)
	}
	attempt := &ownerCloseAttempt{done: make(chan struct{})}
	e.attempt = attempt
	m.mu.Unlock()
	err = callOwnerClose(ctx, e.closeTransport)
	m.mu.Lock()
	e.attempt = nil
	attempt.err = err
	if err == nil {
		e.transportClosed = true
		m.retireLocked(e)
	} else {
		m.scheduleLocked(e, m.opts.Now().Add(m.opts.CloseRetry))
	}
	close(attempt.done)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return awaitOwner(ctx, e.complete)
}

func (m *Owners) entryLocked(owner contract.Owner) (*ownerEntry, error) {
	if owner.Validate() != nil {
		return nil, ErrOwnerInvalid
	}
	if owner.NodeID != m.opts.NodeID || owner.BootID != m.opts.BootID || owner.ConnectionID > m.issued {
		return nil, ErrOwnerUnknown
	}
	e := m.entries[owner.ConnectionID]
	if e != nil && e.owner != owner {
		return nil, ErrOwnerUnknown
	}
	// Absence is safe only because allocation/publication and retirement share
	// this lock, IDs are never caller-provided, and the process identity matches.
	return e, nil
}

func (m *Owners) fenceLocked(e *ownerEntry) {
	if e.stage == ownerClosing {
		return
	}
	if e.stage == ownerPending {
		m.counts.Pending--
	} else {
		m.counts.Active--
	}
	m.counts.Closing++
	e.stage = ownerClosing
	e.cancel(ErrOwnerFenced)
	m.scheduleLocked(e, m.opts.Now().Add(m.opts.CloseRetry))
}

func (m *Owners) retireLocked(e *ownerEntry) {
	if e.stage != ownerClosing || !e.transportClosed || e.operations != 0 || m.entries[e.owner.ConnectionID] != e {
		return
	}
	delete(m.entries, e.owner.ConnectionID)
	m.counts.Closing--
	if e.heapIndex >= 0 {
		heap.Remove(&m.deadlines, e.heapIndex)
	}
	close(e.complete)
}

func callOwnerClose(ctx context.Context, fn CloseTransport) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrOwnerClose
		}
	}()
	if err = fn(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return ErrOwnerClose
	}
	return nil
}

func awaitOwner(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
