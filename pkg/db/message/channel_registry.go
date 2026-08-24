package message

import (
	"container/list"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

const defaultChannelWarmCacheEntries = 8192

// channelWarmState is the bounded, lease-independent append state retained
// after a canonical entry reaches zero references. It never retains a usable
// ChannelLog or a database operation admission.
type channelWarmState struct {
	key                         ChannelKey
	id                          ChannelID
	leo                         uint64
	loaded                      bool
	idempotencyMembership       idempotencyMembershipFilter
	idempotencyMembershipLoaded bool
}

type channelWarmCacheEntry struct {
	key   ChannelKey
	state channelWarmState
}

// channelRegistry owns one canonical mutable entry per active channel key.
type channelRegistry struct {
	// mu protects the entry map, reference counters, and close condition.
	mu sync.Mutex
	// cond wakes database close when active operations or pins drain.
	cond *sync.Cond
	// closed is the mutex-protected terminal admission state.
	closed bool
	// closing provides a lock-free fast rejection path for new operations.
	closing atomic.Bool
	// entries contains the one canonical mutable entry for each active key.
	entries map[ChannelKey]*channelEntry
	// warmEntries retains bounded append state without retaining a live lease.
	warmEntries map[ChannelKey]*list.Element
	// warmOrder orders retained state from least to most recently reclaimed.
	warmOrder *list.List
	// maxWarmEntries bounds retained state independently from channel count.
	maxWarmEntries int

	// activeOps counts database operations admitted before close.
	activeOps atomic.Int64

	// outstandingLeases counts caller-owned channel lease references.
	outstandingLeases uint64
	// backgroundPins counts commit-owned canonical entry references.
	backgroundPins uint64
	// acquireTotal counts successful lease acquisitions.
	acquireTotal uint64
	// releaseTotal counts terminal lease releases.
	releaseTotal uint64
	// reclaimTotal counts canonical entries removed at zero references.
	reclaimTotal uint64
}

type channelRegistrySnapshot struct {
	// activeEntries is the number of currently canonicalized channel keys.
	activeEntries int
	// outstandingLeases is the current caller-owned lease count.
	outstandingLeases uint64
	// backgroundPins is the current commit-owned pin count.
	backgroundPins uint64
	// acquireTotal is the cumulative successful acquisition count.
	acquireTotal uint64
	// releaseTotal is the cumulative terminal release count.
	releaseTotal uint64
	// reclaimTotal is the cumulative canonical reclaim count.
	reclaimTotal uint64
}

func newChannelRegistry() *channelRegistry {
	r := &channelRegistry{
		entries:        make(map[ChannelKey]*channelEntry),
		warmEntries:    make(map[ChannelKey]*list.Element),
		warmOrder:      list.New(),
		maxWarmEntries: defaultChannelWarmCacheEntries,
	}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *channelRegistry) acquire(db *MessageDB, key ChannelKey, id ChannelID) (*ChannelLog, error) {
	if r == nil || db == nil || key == "" {
		return nil, dberrors.ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.closing.Load() || db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	entry := r.entries[key]
	if entry != nil {
		if entry.id != id {
			return nil, dberrors.ErrConflict
		}
	} else {
		warm, err := r.takeWarmLocked(key, id)
		if err != nil {
			return nil, err
		}
		entry = &channelEntry{
			db:             db,
			key:            key,
			id:             id,
			appendKeyCache: newAppendKeyCache(key, id),
		}
		if warm != nil {
			entry.leo.Store(warm.leo)
			entry.loaded.Store(warm.loaded)
			entry.idempotencyMembership = warm.idempotencyMembership
			entry.idempotencyMembershipLoaded = warm.idempotencyMembershipLoaded
		}
		r.entries[key] = entry
	}
	entry.refs++
	r.outstandingLeases++
	r.acquireTotal++
	lease := &ChannelLog{channelEntry: entry, registry: r}
	lease.useCond.L = &lease.useMu
	return lease, nil
}

func (r *channelRegistry) releaseLease(entry *channelEntry) {
	if r == nil || entry == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry.refs == 0 {
		return
	}
	entry.refs--
	if r.outstandingLeases > 0 {
		r.outstandingLeases--
	}
	r.releaseTotal++
	r.reclaimLocked(entry)
}

func (r *channelRegistry) retainPin(entry *channelEntry) error {
	if r == nil || entry == nil {
		return dberrors.ErrClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.closing.Load() || entry.detached.Load() || r.entries[entry.key] != entry {
		return dberrors.ErrClosed
	}
	entry.refs++
	r.backgroundPins++
	return nil
}

func (r *channelRegistry) releasePin(entry *channelEntry) {
	if r == nil || entry == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry.refs == 0 || r.backgroundPins == 0 {
		return
	}
	entry.refs--
	r.backgroundPins--
	r.reclaimLocked(entry)
	if r.backgroundPins == 0 {
		r.cond.Broadcast()
	}
}

func (r *channelRegistry) reclaimLocked(entry *channelEntry) {
	if entry.refs != 0 || r.entries[entry.key] != entry {
		return
	}
	delete(r.entries, entry.key)
	entry.detached.Store(true)
	r.retainWarmLocked(entry)
	r.reclaimTotal++
}

func (r *channelRegistry) takeWarmLocked(key ChannelKey, id ChannelID) (*channelWarmState, error) {
	element := r.warmEntries[key]
	if element == nil {
		return nil, nil
	}
	cached := element.Value.(channelWarmCacheEntry)
	if cached.state.id != id {
		// A key reused with another durable identity must not inherit append
		// state from the previous zero-reference generation.
		delete(r.warmEntries, key)
		r.warmOrder.Remove(element)
		return nil, nil
	}
	delete(r.warmEntries, key)
	r.warmOrder.Remove(element)
	state := cached.state
	return &state, nil
}

func (r *channelRegistry) retainWarmLocked(entry *channelEntry) {
	if r.maxWarmEntries <= 0 {
		return
	}
	if existing := r.warmEntries[entry.key]; existing != nil {
		delete(r.warmEntries, entry.key)
		r.warmOrder.Remove(existing)
	}
	state := channelWarmState{
		key:                         entry.key,
		id:                          entry.id,
		leo:                         entry.leo.Load(),
		loaded:                      entry.loaded.Load(),
		idempotencyMembership:       entry.idempotencyMembership,
		idempotencyMembershipLoaded: entry.idempotencyMembershipLoaded,
	}
	entry.idempotencyMembership = idempotencyMembershipFilter{}
	entry.idempotencyMembershipLoaded = false
	element := r.warmOrder.PushBack(channelWarmCacheEntry{key: entry.key, state: state})
	r.warmEntries[entry.key] = element
	for len(r.warmEntries) > r.maxWarmEntries {
		oldest := r.warmOrder.Front()
		if oldest == nil {
			break
		}
		cached := oldest.Value.(channelWarmCacheEntry)
		delete(r.warmEntries, cached.key)
		r.warmOrder.Remove(oldest)
	}
}

func (r *channelRegistry) invalidateWarm(key ChannelKey) {
	if r == nil || key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	element := r.warmEntries[key]
	if element == nil {
		return
	}
	delete(r.warmEntries, key)
	r.warmOrder.Remove(element)
}

func (r *channelRegistry) beginClose() {
	if r == nil {
		return
	}
	r.closing.Store(true)
	r.mu.Lock()
	r.closed = true
	r.cond.Broadcast()
	r.mu.Unlock()
}

func (r *channelRegistry) beginOperation() bool {
	if r == nil || r.closing.Load() {
		return false
	}
	r.activeOps.Add(1)
	if r.closing.Load() {
		r.endOperation()
		return false
	}
	return true
}

func (r *channelRegistry) endOperation() {
	if r == nil {
		return
	}
	if r.activeOps.Add(-1) == 0 && r.closing.Load() {
		r.mu.Lock()
		r.cond.Broadcast()
		r.mu.Unlock()
	}
}

func (r *channelRegistry) waitForDrain() {
	if r == nil {
		return
	}
	r.mu.Lock()
	for r.activeOps.Load() != 0 || r.backgroundPins != 0 {
		r.cond.Wait()
	}
	r.mu.Unlock()
}

func (r *channelRegistry) detachEntries() {
	if r == nil {
		return
	}
	r.mu.Lock()
	for _, entry := range r.entries {
		entry.detached.Store(true)
	}
	r.entries = make(map[ChannelKey]*channelEntry)
	r.warmEntries = make(map[ChannelKey]*list.Element)
	r.warmOrder.Init()
	r.mu.Unlock()
}

func (r *channelRegistry) snapshot() channelRegistrySnapshot {
	if r == nil {
		return channelRegistrySnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return channelRegistrySnapshot{
		activeEntries:     len(r.entries),
		outstandingLeases: r.outstandingLeases,
		backgroundPins:    r.backgroundPins,
		acquireTotal:      r.acquireTotal,
		releaseTotal:      r.releaseTotal,
		reclaimTotal:      r.reclaimTotal,
	}
}

func (r *channelRegistry) activeEntry(key ChannelKey) *channelEntry {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[key]
}
