package presence

import (
	"context"
	"errors"
	"sync"
	"unsafe"
)

const (
	// RecoveryResultMaxBytes reserves 1/64 of an 8 GiB target node for controlled
	// decoded ownership. It excludes raw wire, Directory, allocator overhead and RSS.
	RecoveryResultMaxBytes uint64 = 128 << 20
	// RecoveryResultMaxRows counts concurrent backing capacity, including pages
	// and old/new accumulator allocations. Every original call still caps at 4096.
	RecoveryResultMaxRows uint64 = 3 * 16 * 4096
)

// ErrRecoveryResultBudget rejects an allocation before ownership is acquired.
var ErrRecoveryResultBudget = errors.New("presence: recovery result capacity unavailable")

// RecoveryResultBudget is a nonwaiting joint ledger for one recovery generation.
// Callers reserve before allocation and drop all references before returning it.
type RecoveryResultBudget struct {
	mu                sync.Mutex
	maxRows, maxBytes uint64
	stats             RecoveryResultBudgetStats
}

// RecoveryResultBudgetStats describes controlled ownership, not total heap use.
type RecoveryResultBudgetStats struct {
	Rows, Bytes, HighWaterRows, HighWaterBytes, Rejected uint64
}

// RecoveryResultLease owns one allocation group. Its state uses the budget lock;
// release is idempotent even when shutdown and the physical owner race to join.
type RecoveryResultLease struct {
	budget      *RecoveryResultBudget
	rows, bytes uint64
	released    bool
}

// NewRecoveryResultBudget fixes limits. A zero byte limit admits no lease;
// a zero row limit still permits bounded empty proofs.
func NewRecoveryResultBudget(maxRows, maxBytes uint64) *RecoveryResultBudget {
	return &RecoveryResultBudget{maxRows: maxRows, maxBytes: maxBytes}
}

func (b *RecoveryResultBudget) reserveLocked(rows, bytes uint64) bool {
	if rows > b.maxRows-b.stats.Rows || bytes > b.maxBytes-b.stats.Bytes {
		b.stats.Rejected++
		return false
	}
	b.stats.Rows += rows
	b.stats.Bytes += bytes
	b.stats.HighWaterRows = max(b.stats.HighWaterRows, b.stats.Rows)
	b.stats.HighWaterBytes = max(b.stats.HighWaterBytes, b.stats.Bytes)
	return true
}

// TryReserve charges payload and fixed lease metadata atomically. Failure never
// allocates a lease or retains partial credits and never waits for other owners.
func (b *RecoveryResultBudget) TryReserve(rows, bytes uint64) (*RecoveryResultLease, error) {
	if b == nil {
		return nil, ErrRecoveryResultBudget
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	metadata := uint64(unsafe.Sizeof(RecoveryResultLease{}))
	if bytes > ^uint64(0)-metadata {
		b.stats.Rejected++
		return nil, ErrRecoveryResultBudget
	}
	if !b.reserveLocked(rows, bytes+metadata) {
		return nil, ErrRecoveryResultBudget
	}
	return &RecoveryResultLease{budget: b, rows: rows, bytes: bytes}, nil
}

// TryGrow reserves additional live backing before copying or decoding into it.
func (l *RecoveryResultLease) TryGrow(rows, bytes uint64) error {
	if l == nil || l.budget == nil {
		return ErrRecoveryResultBudget
	}
	b := l.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.released {
		return ErrRecoveryResultBudget
	}
	if !b.reserveLocked(rows, bytes) {
		return ErrRecoveryResultBudget
	}
	l.rows += rows
	l.bytes += bytes
	return nil
}

// Reduce returns an exact dropped allocation; invalid amounts cannot underflow
// or silently clamp. The fixed lease metadata remains charged until Release.
func (l *RecoveryResultLease) Reduce(rows, bytes uint64) bool {
	if l == nil || l.budget == nil {
		return false
	}
	b := l.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.released || rows > l.rows || bytes > l.bytes {
		return false
	}
	l.rows -= rows
	l.bytes -= bytes
	b.stats.Rows -= rows
	b.stats.Bytes -= bytes
	return true
}

// Release returns credits only after the owner has dropped its allocations.
func (l *RecoveryResultLease) Release() {
	if l == nil || l.budget == nil {
		return
	}
	b := l.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.released {
		return
	}
	l.released = true
	b.stats.Rows -= l.rows
	b.stats.Bytes -= l.bytes + uint64(unsafe.Sizeof(RecoveryResultLease{}))
	l.rows, l.bytes = 0, 0
}

// Stats returns a fixed aggregate without retaining requests or route identities.
func (b *RecoveryResultBudget) Stats() RecoveryResultBudgetStats {
	if b == nil {
		return RecoveryResultBudgetStats{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats
}

// OwnedRecoveryResults keeps decoded ownership through fenced installation.
// No provider or scratch aliases may survive Release. Published Directory state
// has its own lifetime and is explicitly outside this ledger.
type OwnedRecoveryResults struct {
	Results [][]EndpointLookupResult
	Lease   *RecoveryResultLease
}

// Release drops the owned outer backing before making capacity reusable.
func (o *OwnedRecoveryResults) Release() {
	if o == nil {
		return
	}
	clear(o.Results[:cap(o.Results)])
	o.Results = nil
	o.Lease.Release()
	o.Lease = nil
}

// OwnedOwnerRouteCallPage includes every decoded allocation in a received page.
type OwnedOwnerRouteCallPage struct {
	Page  OwnerRouteCallPage
	Lease *RecoveryResultLease
}

// Release drops the page before returning its backing and string reservations.
func (o *OwnedOwnerRouteCallPage) Release() {
	if o == nil {
		return
	}
	clear(o.Page.Results[:cap(o.Page.Results)])
	o.Page = OwnerRouteCallPage{}
	o.Lease.Release()
	o.Lease = nil
}

// RecoveryCallBatchOwnersBounded opts in only when local, remote and protocol
// compatibility paths all reserve before allocation. Legacy ports retain their
// original sixteen-call result guard. Any error invalidates the complete round.
type RecoveryCallBatchOwnersBounded interface {
	SupportsBoundedRecoveryResults() bool
	RecoverRouteCallsBounded(context.Context, []RecoveryCall, *RecoveryResultBudget, func(RecoveryResultUsage)) (OwnedRecoveryResults, error)
}

// OwnerRouteCallPageReaderBounded returns prepaid complete calls and preserves
// the same fence, page and per-call limits as OwnerRouteCallPageReader.
type OwnerRouteCallPageReaderBounded interface {
	ReadOwnerRouteCallsBounded(context.Context, []RecoveryCall, int, *RecoveryResultBudget) (OwnedOwnerRouteCallPage, error)
}
