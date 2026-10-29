package online

import (
	"cmp"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/RaduBerinde/btreemap"
	presence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// ErrStaleActivation rejects an old, incomplete or mismatched owner barrier.
var ErrStaleActivation = errors.New("internal/runtime/online: stale activation")

// ErrSlotPageCapacity rejects ownership that cannot be prepaid without waiting.
var ErrSlotPageCapacity = errors.New("internal/runtime/online: slot page capacity")

// ownerSlot serializes activation, close, activity and the ordered scan. Its
// lock always precedes a session shard lock; no I/O runs under either lock.
type ownerSlot struct {
	mu sync.Mutex
	// active is ordered by session ID and contains only currently active routes.
	active *btreemap.BTreeMap[uint64, OwnerRoute]
	// claim/cut retain the highest authenticated generation even after End.
	claim presence.GenerationClaim
	cut   presence.OwnerCut
	// ended permits receipt activation; aborted prevents reopening the same G.
	ended, aborted bool
	// last owns only the previous immutable page, including its budget credit.
	last *slotPageBacking
	end  presence.SlotEnd
}

// slotPageBudget accounts both the replay cache and caller-owned replies until
// their last actual reference returns. It is independent of session storage.
type slotPageBudget struct {
	mu                             sync.Mutex
	rows, bytes, maxRows, maxBytes int
}

// SlotPageBudgetStats describes charged decoded owner pages, not process RSS.
type SlotPageBudgetStats struct{ Rows, Bytes int }

// slotPageBacking is immutable after publication. The cache and each reply
// retain one reference; retiring a cache entry never frees a live reply's credit.
type slotPageBacking struct {
	page        presence.SlotPage
	request     presence.SlotPageRequest
	refs        atomic.Int64
	budget      *slotPageBudget
	rows, bytes int
}

// OwnedSlotPage keeps Page immutable until Release. Each independently returned
// handle owns one reference; Release is idempotent for that handle.
type OwnedSlotPage struct {
	Page    presence.SlotPage
	backing *slotPageBacking
	once    sync.Once
}

// Release returns this reply's credit only after its consumer stops using Page.
func (p *OwnedSlotPage) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.backing == nil {
			return
		}
		p.Page.Routes = nil
		p.backing.budget.mu.Lock()
		p.backing.budget.bytes -= int(unsafe.Sizeof(OwnedSlotPage{}))
		p.backing.budget.mu.Unlock()
		p.backing.release()
	})
}

func (p *slotPageBacking) retain() (*OwnedSlotPage, error) {
	size := int(unsafe.Sizeof(OwnedSlotPage{}))
	p.budget.mu.Lock()
	if size > p.budget.maxBytes-p.budget.bytes {
		p.budget.mu.Unlock()
		return nil, ErrSlotPageCapacity
	}
	p.budget.bytes += size
	p.budget.mu.Unlock()
	return p.retainedHandle(), nil
}

func (p *slotPageBacking) retainedHandle() *OwnedSlotPage {
	p.refs.Add(1)
	return &OwnedSlotPage{Page: p.page, backing: p}
}

func (p *slotPageBacking) release() {
	if p == nil || p.refs.Add(-1) != 0 {
		return
	}
	clear(p.page.Routes)
	p.page.Routes = nil
	p.budget.mu.Lock()
	p.budget.rows -= p.rows
	p.budget.bytes -= p.bytes
	p.budget.mu.Unlock()
}

// SlotPageBudget observes currently retained page credits.
func (r *Registry) SlotPageBudget() SlotPageBudgetStats {
	r.pageBudget.mu.Lock()
	defer r.pageBudget.mu.Unlock()
	return SlotPageBudgetStats{Rows: r.pageBudget.rows, Bytes: r.pageBudget.bytes}
}

func (r *Registry) slot(hashSlot uint16) *ownerSlot {
	if value, ok := r.slots.Load(hashSlot); ok {
		return value.(*ownerSlot)
	}
	next := &ownerSlot{active: btreemap.New[uint64, OwnerRoute](16, cmp.Compare[uint64])}
	value, _ := r.slots.LoadOrStore(hashSlot, next)
	return value.(*ownerSlot)
}

// BeginSlot installs a claim already authenticated by the usecase's cluster
// port. It never waits for pending CONNECTs and does not itself prove commitment.
func (r *Registry) BeginSlot(claim presence.GenerationClaim, nodeID, bootID uint64) (presence.OwnerCut, error) {
	if !claim.Valid() || nodeID == 0 || bootID == 0 {
		return presence.OwnerCut{}, ErrStaleActivation
	}
	slot := r.slot(claim.Target.HashSlot)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.claim.Generation != 0 {
		if claim.Generation < slot.claim.Generation {
			return presence.OwnerCut{}, ErrStaleActivation
		}
		if claim.Generation == slot.claim.Generation {
			if !claim.Equal(slot.claim) || slot.cut.NodeID != nodeID || slot.cut.BootID != bootID || slot.aborted {
				return presence.OwnerCut{}, ErrStaleActivation
			}
			return slot.cut, nil
		}
	}
	if slot.cut.Cut == math.MaxUint64 {
		return presence.OwnerCut{}, ErrStaleActivation
	}
	slot.last.release()
	slot.last = nil
	slot.claim = claim
	slot.cut = presence.OwnerCut{NodeID: nodeID, BootID: bootID, Cut: slot.cut.Cut + 1}
	slot.ended = false
	slot.aborted = false
	slot.end = presence.SlotEnd{}
	return slot.cut, nil
}

func (s *ownerSlot) matches(claim presence.GenerationClaim, cut presence.OwnerCut) bool {
	return claim.Valid() && claim.Equal(s.claim) && cut == s.cut && !s.aborted
}

// ValidateSlotCut checks the retained owner barrier before and after a bounded
// UID recheck. End preserves it; Abort and higher generations invalidate it.
func (r *Registry) ValidateSlotCut(claim presence.GenerationClaim, cut presence.OwnerCut) error {
	slot := r.slot(claim.Target.HashSlot)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.matches(claim, cut) {
		return ErrStaleActivation
	}
	return nil
}

// ReadSlotPage traverses only the ordered active index. Exact last-page replay
// returns identical rows even after activity or close; older pages are rejected.
func (r *Registry) ReadSlotPage(req presence.SlotPageRequest) (*OwnedSlotPage, error) {
	if req.Sequence == 0 || req.MaxRows <= 0 || req.MaxRows > 4096 || req.MaxBytes <= 0 || req.MaxBytes > 128<<20 {
		return nil, ErrSlotPageCapacity
	}
	slot := r.slot(req.Claim.Target.HashSlot)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.matches(req.Claim, req.Owner) || slot.ended {
		return nil, ErrStaleActivation
	}
	var rowsTotal uint64
	if last := slot.last; last != nil {
		if req.Sequence == last.request.Sequence && req.AfterSessionID == last.request.AfterSessionID && req.MaxRows == last.request.MaxRows && req.MaxBytes == last.request.MaxBytes {
			return last.retain()
		}
		if last.page.Exhausted || req.Sequence != last.page.Sequence+1 || req.AfterSessionID != last.page.NextSessionID {
			return nil, ErrStaleActivation
		}
		rowsTotal = last.page.RowsTotal
	} else if req.Sequence != 1 || req.AfterSessionID != 0 {
		return nil, ErrStaleActivation
	}

	// Count under the same slot lock before allocating. Activity/identity cannot
	// change between this pass and the copied pass. Index backing is not copied.
	count, stringBytes := 0, 0
	baseBytes := int(unsafe.Sizeof(slotPageBacking{}))
	handleBytes := int(unsafe.Sizeof(OwnedSlotPage{}))
	rowBytes := int(unsafe.Sizeof(presence.Route{}))
	if baseBytes+handleBytes > req.MaxBytes {
		return nil, ErrSlotPageCapacity
	}
	exhausted := true
	slot.active.AscendFunc(btreemap.GT(req.AfterSessionID), btreemap.Max[uint64](), func(_ uint64, route OwnerRoute) bool {
		n := len(route.UID) + len(route.DeviceID) + len(route.Listener)
		if count == req.MaxRows || baseBytes+handleBytes+(count+1)*rowBytes+stringBytes+n > req.MaxBytes {
			exhausted = false
			return false
		}
		count++
		stringBytes += n
		return true
	})
	if !exhausted && count == 0 {
		return nil, ErrSlotPageCapacity
	}
	bytes := baseBytes + count*rowBytes + stringBytes
	budget := &r.pageBudget
	budget.mu.Lock()
	if count > budget.maxRows-budget.rows || bytes+handleBytes > budget.maxBytes-budget.bytes {
		budget.mu.Unlock()
		return nil, ErrSlotPageCapacity
	}
	budget.rows += count
	budget.bytes += bytes + handleBytes
	budget.mu.Unlock()
	page := presence.SlotPage{Claim: req.Claim, Owner: req.Owner, Sequence: req.Sequence, AfterSessionID: req.AfterSessionID, NextSessionID: req.AfterSessionID, RowsTotal: rowsTotal + uint64(count), Exhausted: exhausted, Routes: make([]presence.Route, 0, count)}
	slot.active.AscendFunc(btreemap.GT(req.AfterSessionID), btreemap.Max[uint64](), func(id uint64, route OwnerRoute) bool {
		if len(page.Routes) == count {
			return false
		}
		page.Routes = append(page.Routes, ownedPresenceRoute(route))
		page.NextSessionID = id
		return true
	})
	backing := &slotPageBacking{page: page, request: req, budget: budget, rows: count, bytes: bytes}
	backing.refs.Store(1)
	prior := slot.last
	slot.last = backing
	prior.release()
	return backing.retainedHandle(), nil
}

// EndSlot acknowledges exactly one exhausted page and releases its cached
// backing. The O(1) activation barrier remains until a higher committed claim.
func (r *Registry) EndSlot(req presence.SlotEndRequest) (presence.SlotEnd, error) {
	slot := r.slot(req.Claim.Target.HashSlot)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.matches(req.Claim, req.Owner) {
		return presence.SlotEnd{}, ErrStaleActivation
	}
	if slot.ended {
		if req.Sequence == slot.end.Sequence && req.LastSessionID == slot.end.LastSessionID && req.RowsTotal == slot.end.RowsTotal {
			return slot.end, nil
		}
		return presence.SlotEnd{}, ErrStaleActivation
	}
	last := slot.last
	if last == nil || !last.page.Exhausted || req.Sequence != last.page.Sequence || req.LastSessionID != last.page.NextSessionID || req.RowsTotal != last.page.RowsTotal {
		return presence.SlotEnd{}, ErrStaleActivation
	}
	slot.end = presence.SlotEnd{Claim: req.Claim, Owner: req.Owner, Sequence: req.Sequence, LastSessionID: req.LastSessionID, RowsTotal: req.RowsTotal}
	slot.ended = true
	slot.last = nil
	last.release()
	return slot.end, nil
}

// AbortSlot retires only this unfinished cut. It cannot reopen the same G,
// cancel a successor, or revoke an End that may already back Complete.
func (r *Registry) AbortSlot(claim presence.GenerationClaim, cut presence.OwnerCut) error {
	slot := r.slot(claim.Target.HashSlot)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.matches(claim, cut) || slot.ended {
		return ErrStaleActivation
	}
	slot.aborted = true
	slot.last.release()
	slot.last = nil
	return nil
}

// MarkActiveWithReceipt compares the receipt and promotes the exact pending
// identity under the same slot lock as BeginSlot; there is no check/promote gap.
func (r *Registry) MarkActiveWithReceipt(id uint64, receipt presence.ActivationReceipt) error {
	slot, shard, session, ok := r.lockSessionSlot(id)
	if !ok {
		return ErrConnectionNotFound
	}
	defer slot.mu.Unlock()
	defer shard.mu.Unlock()
	route := session.Route
	if !receipt.Valid() || !slot.matches(receipt.Claim, receipt.Owner) || !slot.ended ||
		receipt.Claim.Target.HashSlot != route.HashSlot || receipt.Identity.UID != route.UID || receipt.Identity.SessionID != id ||
		receipt.Identity.OwnerNodeID != route.OwnerNodeID || receipt.Identity.OwnerBootID != route.OwnerBootID || receipt.OwnerSeq != route.OwnerSeq {
		return ErrStaleActivation
	}
	r.activateLocked(slot, shard, session)
	return nil
}

func ownedPresenceRoute(route OwnerRoute) presence.Route {
	return presence.Route{UID: strings.Clone(route.UID), OwnerNodeID: route.OwnerNodeID, OwnerBootID: route.OwnerBootID, OwnerSeq: route.OwnerSeq, SessionID: route.SessionID, DeviceID: strings.Clone(route.DeviceID), DeviceFlag: route.DeviceFlag, DeviceLevel: route.DeviceLevel, Listener: strings.Clone(route.Listener), ConnectedUnix: route.ConnectedUnix, LastSeenUnix: route.LastActivityUnix}
}

// lockSessionSlot samples the slot without retaining a shard lock, then
// validates it after taking locks in canonical order. Replacement never inverts
// this order and causes only a retry of the local identity lookup.
func (r *Registry) lockSessionSlot(id uint64) (*ownerSlot, *registryShard, LocalSession, bool) {
	shard := r.sessionShard(id)
	for {
		shard.mu.RLock()
		sample, ok := shard.bySession[id]
		shard.mu.RUnlock()
		if !ok {
			return nil, nil, LocalSession{}, false
		}
		slot := r.slot(sample.Route.HashSlot)
		slot.mu.Lock()
		shard.mu.Lock()
		current, ok := shard.bySession[id]
		if ok && current.Route.HashSlot == sample.Route.HashSlot {
			return slot, shard, current, true
		}
		shard.mu.Unlock()
		slot.mu.Unlock()
		if !ok {
			return nil, nil, LocalSession{}, false
		}
	}
}

func (r *Registry) activateLocked(slot *ownerSlot, shard *registryShard, session LocalSession) {
	session.State = RouteStateActive
	if shard.activeByUID[session.Route.UID] == nil {
		shard.activeByUID[session.Route.UID] = make(map[uint64]struct{})
	}
	shard.activeByUID[session.Route.UID][session.Route.SessionID] = struct{}{}
	shard.bySession[session.Route.SessionID] = session
	slot.active.ReplaceOrInsert(session.Route.SessionID, session.Route)
}
