package conversationactive

import (
	"container/heap"
	"context"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type conversationKey struct {
	kind        metadb.ConversationKind
	channelID   string
	channelType uint8
}

type cacheAddress struct {
	uid string
	key conversationKey
}

type cacheEntry struct {
	// patch is the latest coalesced active projection for this cached row.
	patch ActivePatch
	// version changes whenever patch content changes, fencing stale flush snapshots.
	version uint64
	// dirty reports that patch still needs to be flushed to the durable store.
	dirty bool
	// hashSlot is the UID hash slot supplied by the authority route at admission time.
	hashSlot uint16
	// hasHashSlot reports whether hashSlot is valid for target-scoped flushing.
	hasHashSlot bool
}

type flushEntry struct {
	// uid owns the dirty cached conversation row.
	uid string
	// key identifies the cached conversation row within the UID map.
	key conversationKey
	// patch is the exact active projection snapshot sent to durable storage.
	patch ActivePatch
	// version fences dirty clearing against concurrent cache updates.
	version uint64
}

// Manager owns the in-memory UID conversation active cache.
type Manager struct {
	// mu protects cache and all per-UID conversation rows.
	mu sync.RWMutex
	// nowMS supplies ActiveAtMS when an admitted batch does not provide one.
	nowMS func() int64
	// store reads and persists durable active rows for cache/store merging.
	store ActiveStore
	// activeCooldown skips receiver-only active_at flushes within this durable row age window.
	activeCooldown time.Duration
	// maxCachedRows bounds cached rows across all UIDs; zero means unbounded.
	maxCachedRows int
	// observer receives cache and flush observations.
	observer Observer
	// totalRows tracks cached active rows across all UID maps.
	totalRows int
	// dirtyRows tracks cached rows that still need durable flush.
	dirtyRows int
	// rowsByKind tracks cached active rows by conversation kind.
	rowsByKind map[metadb.ConversationKind]int
	// dirtyRowsByKind tracks dirty cached rows by conversation kind.
	dirtyRowsByKind map[metadb.ConversationKind]int
	// dirtyActiveAtCounts counts dirty rows by positive ActiveAtMS for oldest-age observation.
	dirtyActiveAtCounts map[int64]int
	// dirtyActiveAtHeap keeps candidate dirty ActiveAtMS values ordered by oldest first.
	dirtyActiveAtHeap dirtyActiveAtMinHeap
	// nextVersion allocates monotonic cache-entry versions for dirty flush fencing.
	nextVersion uint64
	// cache stores UID -> conversation key -> active projection entry.
	cache map[string]map[conversationKey]cacheEntry
	// dirtyByHashSlot indexes dirty cache rows by UID hash slot for bounded authority handoff drains.
	dirtyByHashSlot map[uint16]map[cacheAddress]struct{}
}

// NewManager creates a conversation active admission manager.
func NewManager(opts Options) *Manager {
	nowMS := opts.NowMS
	if nowMS == nil {
		nowMS = func() int64 {
			return time.Now().UnixMilli()
		}
	}
	return &Manager{
		nowMS:               nowMS,
		store:               opts.Store,
		activeCooldown:      opts.ActiveCooldown,
		maxCachedRows:       opts.MaxCachedRows,
		observer:            opts.Observer,
		rowsByKind:          make(map[metadb.ConversationKind]int),
		dirtyRowsByKind:     make(map[metadb.ConversationKind]int),
		dirtyActiveAtCounts: make(map[int64]int),
		cache:               make(map[string]map[conversationKey]cacheEntry),
		dirtyByHashSlot:     make(map[uint16]map[cacheAddress]struct{}),
	}
}

// AdmitActiveBatch admits a channelappend recipient batch into the active cache.
func (m *Manager) AdmitActiveBatch(ctx context.Context, batch ActiveBatch) error {
	return m.admitActiveBatch(ctx, 0, false, batch)
}

// AdmitActiveBatchForHashSlot admits a channelappend recipient batch for one UID hash slot.
func (m *Manager) AdmitActiveBatchForHashSlot(ctx context.Context, hashSlot uint16, batch ActiveBatch) error {
	return m.admitActiveBatch(ctx, hashSlot, true, batch)
}

func (m *Manager) admitActiveBatch(ctx context.Context, hashSlot uint16, hasHashSlot bool, batch ActiveBatch) error {
	activeAtMS := batch.ActiveAtMS
	if activeAtMS == 0 {
		activeAtMS = m.nowMS()
	}

	patches := make([]ActivePatch, 0, len(batch.Recipients)+1)
	if batch.SenderUID != "" {
		patches = append(patches, ActivePatch{
			UID:         batch.SenderUID,
			Kind:        batch.Kind,
			ChannelID:   batch.ChannelID,
			ChannelType: batch.ChannelType,
			ActiveAtMS:  activeAtMS,
			ReadSeq:     batch.MessageSeq,
		})
	}

	for _, recipient := range batch.Recipients {
		if recipient.UID == "" {
			continue
		}
		if batch.SenderUID != "" && recipient.UID == batch.SenderUID {
			continue
		}

		var readSeq uint64
		if recipient.IsSender {
			readSeq = batch.MessageSeq
		}

		patches = append(patches, ActivePatch{
			UID:         recipient.UID,
			Kind:        batch.Kind,
			ChannelID:   batch.ChannelID,
			ChannelType: batch.ChannelType,
			ActiveAtMS:  activeAtMS,
			ReadSeq:     readSeq,
		})
	}

	return m.markActive(ctx, hashSlot, hasHashSlot, patches)
}

// MarkActive merges active conversation patches into the UID cache.
func (m *Manager) MarkActive(ctx context.Context, patches []ActivePatch) error {
	return m.markActive(ctx, 0, false, patches)
}

// MarkActiveForHashSlot merges active conversation patches owned by one UID hash slot.
func (m *Manager) MarkActiveForHashSlot(ctx context.Context, hashSlot uint16, patches []ActivePatch) error {
	return m.markActive(ctx, hashSlot, true, patches)
}

func (m *Manager) markActive(ctx context.Context, hashSlot uint16, hasHashSlot bool, patches []ActivePatch) error {
	if len(patches) == 0 {
		return nil
	}

	for {
		m.mu.Lock()
		newRows := m.countNewRowsLocked(patches)
		if !m.cacheWouldExceedLocked(newRows) {
			for _, patch := range patches {
				if patch.UID == "" {
					continue
				}
				m.markActiveLocked(patch, hashSlot, hasHashSlot)
			}
			m.mu.Unlock()
			m.observeCache()
			return nil
		}
		if newRows > m.maxCachedRows {
			m.mu.Unlock()
			return ErrCachePressure
		}
		m.mu.Unlock()

		if err := m.spillForPressure(ctx, newRows); err != nil {
			return err
		}
	}
}

func (m *Manager) markActiveLocked(patch ActivePatch, hashSlot uint16, hasHashSlot bool) {
	key := conversationKey{kind: patch.Kind, channelID: patch.ChannelID, channelType: patch.ChannelType}
	address := cacheAddress{uid: patch.UID, key: key}
	byChannel := m.cache[patch.UID]
	if byChannel == nil {
		byChannel = make(map[conversationKey]cacheEntry)
		m.cache[patch.UID] = byChannel
	}

	current, ok := byChannel[key]
	if !ok {
		m.nextVersion++
		entry := cacheEntry{patch: patch, version: m.nextVersion, dirty: true, hashSlot: hashSlot, hasHashSlot: hasHashSlot}
		byChannel[key] = entry
		m.totalRows++
		if m.rowsByKind == nil {
			m.rowsByKind = make(map[metadb.ConversationKind]int)
		}
		m.rowsByKind[key.kind]++
		m.trackDirtyLocked(address, entry)
		return
	}

	merged := current.patch
	if patch.ActiveAtMS > merged.ActiveAtMS {
		merged.ActiveAtMS = patch.ActiveAtMS
	}
	if patch.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = patch.ReadSeq
	}
	slotChanged := current.hashSlot != hashSlot || current.hasHashSlot != hasHashSlot
	if merged == current.patch && !slotChanged {
		return
	}

	next := current
	next.patch = merged
	next.hashSlot = hashSlot
	next.hasHashSlot = hasHashSlot
	if current.dirty {
		m.moveDirtyLocked(address, current, next)
	} else {
		next.dirty = true
		m.trackDirtyLocked(address, next)
	}
	m.nextVersion++
	next.version = m.nextVersion
	next.dirty = true
	byChannel[key] = next
}

func (m *Manager) countNewRowsLocked(patches []ActivePatch) int {
	seen := make(map[cacheAddress]struct{}, len(patches))
	var count int
	for _, patch := range patches {
		if patch.UID == "" {
			continue
		}
		key := conversationKey{kind: patch.Kind, channelID: patch.ChannelID, channelType: patch.ChannelType}
		address := cacheAddress{uid: patch.UID, key: key}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		if byChannel := m.cache[patch.UID]; byChannel != nil {
			if _, ok := byChannel[key]; ok {
				continue
			}
		}
		count++
	}
	return count
}

func (m *Manager) cacheWouldExceedLocked(newRows int) bool {
	return m.maxCachedRows > 0 && newRows > 0 && m.totalRows+newRows > m.maxCachedRows
}

func (m *Manager) spillForPressure(ctx context.Context, newRows int) error {
	if m.store == nil {
		return ErrCachePressure
	}
	if _, err := m.flushDirty(ctx, 0); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	over := m.totalRows + newRows - m.maxCachedRows
	if over <= 0 {
		return nil
	}
	if evicted := m.evictCleanRowsLocked(over); evicted < over {
		return ErrCachePressure
	}
	return nil
}

func (m *Manager) evictCleanRowsLocked(limit int) int {
	if limit <= 0 {
		return 0
	}
	var evicted int
	for uid, byChannel := range m.cache {
		for key, entry := range byChannel {
			if entry.dirty {
				continue
			}
			delete(byChannel, key)
			m.totalRows--
			decrementKindCount(m.rowsByKind, key.kind)
			evicted++
			if evicted >= limit {
				break
			}
		}
		if len(byChannel) == 0 {
			delete(m.cache, uid)
		}
		if evicted >= limit {
			break
		}
	}
	return evicted
}

// Flush persists dirty active rows and clears only unchanged dirty markers.
func (m *Manager) Flush(ctx context.Context, limit int) (FlushResult, error) {
	return m.flushDirty(ctx, limit)
}

// FlushHashSlot persists dirty active rows for one UID hash slot.
func (m *Manager) FlushHashSlot(ctx context.Context, hashSlot uint16, limit int) (FlushResult, error) {
	if m.store == nil {
		return FlushResult{}, ErrStoreRequired
	}
	startedAt := time.Now()
	return m.flushDirtyEntries(ctx, startedAt, m.dirtyFlushEntriesForHashSlot(hashSlot, limit))
}

func (m *Manager) flushDirty(ctx context.Context, limit int) (FlushResult, error) {
	if m.store == nil {
		return FlushResult{}, ErrStoreRequired
	}
	startedAt := time.Now()

	return m.flushDirtyEntries(ctx, startedAt, m.dirtyFlushEntries(limit))
}

func (m *Manager) flushDirtyEntries(ctx context.Context, startedAt time.Time, entries []flushEntry) (FlushResult, error) {
	if len(entries) == 0 {
		m.observeFlush(FlushObservation{Result: "no_dirty", Duration: positiveDuration(time.Since(startedAt))})
		m.observeCache()
		return FlushResult{}, nil
	}

	flushEntries, skippedEntries, err := m.filterFlushEntries(ctx, entries)
	if err != nil {
		m.observeFlush(FlushObservation{Result: "error", Selected: len(entries), Duration: positiveDuration(time.Since(startedAt))})
		m.observeCache()
		return FlushResult{Selected: len(entries)}, err
	}

	patches := make([]metadb.ConversationActivePatch, 0, len(flushEntries))
	for _, entry := range flushEntries {
		patches = append(patches, activePatchMetaPatch(entry.patch))
	}
	if len(patches) > 0 {
		if err := m.store.TouchConversationActiveAt(ctx, patches); err != nil {
			m.observeFlush(FlushObservation{Result: "error", Selected: len(entries), Duration: positiveDuration(time.Since(startedAt))})
			m.observeCache()
			return FlushResult{Selected: len(entries)}, err
		}
	}
	m.clearSkippedDirty(skippedEntries)
	m.clearFlushedDirty(flushEntries)
	m.observeFlush(FlushObservation{Result: "ok", Selected: len(entries), Flushed: len(flushEntries), Duration: positiveDuration(time.Since(startedAt))})
	m.observeCache()
	return FlushResult{Selected: len(entries), Flushed: len(flushEntries)}, nil
}

func (m *Manager) filterFlushEntries(ctx context.Context, entries []flushEntry) ([]flushEntry, []flushEntry, error) {
	if m.activeCooldown <= 0 {
		return entries, nil, nil
	}
	keys := make([]metadb.ConversationStateKey, 0, len(entries))
	for _, entry := range entries {
		if entry.patch.ReadSeq > 0 {
			continue
		}
		keys = append(keys, metadb.ConversationStateKey{
			UID:         entry.patch.UID,
			Kind:        entry.patch.Kind,
			ChannelID:   entry.patch.ChannelID,
			ChannelType: int64(entry.patch.ChannelType),
		})
	}
	if len(keys) == 0 {
		return entries, nil, nil
	}
	states, err := m.store.GetConversationStates(ctx, keys)
	if err != nil {
		return nil, nil, err
	}
	cooldownMS := int64(m.activeCooldown / time.Millisecond)
	if cooldownMS <= 0 {
		return entries, nil, nil
	}
	flushEntries := make([]flushEntry, 0, len(entries))
	skippedEntries := make([]flushEntry, 0)
	for _, entry := range entries {
		if entry.patch.ReadSeq > 0 {
			flushEntries = append(flushEntries, entry)
			continue
		}
		key := metadb.ConversationStateKey{
			UID:         entry.patch.UID,
			Kind:        entry.patch.Kind,
			ChannelID:   entry.patch.ChannelID,
			ChannelType: int64(entry.patch.ChannelType),
		}
		state, ok := states[key]
		if !ok || state.ActiveAt <= 0 || entry.patch.ActiveAtMS-state.ActiveAt >= cooldownMS {
			flushEntries = append(flushEntries, entry)
			continue
		}
		entry.patch.ActiveAtMS = state.ActiveAt
		skippedEntries = append(skippedEntries, entry)
	}
	return flushEntries, skippedEntries, nil
}

func (m *Manager) clearSkippedDirty(entries []flushEntry) {
	if len(entries) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, skipped := range entries {
		byChannel := m.cache[skipped.uid]
		if byChannel == nil {
			continue
		}
		current, ok := byChannel[skipped.key]
		if !ok || current.version != skipped.version || !current.dirty {
			continue
		}
		m.untrackDirtyLocked(cacheAddress{uid: skipped.uid, key: skipped.key}, current)
		current.patch.ActiveAtMS = skipped.patch.ActiveAtMS
		current.dirty = false
		m.nextVersion++
		current.version = m.nextVersion
		byChannel[skipped.key] = current
	}
}

func (m *Manager) observeCache() {
	if m.observer == nil {
		return
	}
	m.observer.ObserveConversationActiveCache(m.cacheObservation())
}

func (m *Manager) observeFlush(obs FlushObservation) {
	if m.observer == nil {
		return
	}
	m.observer.ObserveConversationActiveFlush(obs)
}

func (m *Manager) cacheObservation() CacheObservation {
	m.mu.Lock()
	rows := m.totalRows
	dirtyRows := m.dirtyRows
	rowsByKind := cloneKindCounts(m.rowsByKind)
	dirtyRowsByKind := cloneKindCounts(m.dirtyRowsByKind)
	oldestDirtyAt := m.oldestDirtyAtLocked()
	m.mu.Unlock()
	return CacheObservation{
		Rows:            rows,
		DirtyRows:       dirtyRows,
		RowsByKind:      rowsByKind,
		DirtyRowsByKind: dirtyRowsByKind,
		OldestDirtyAge:  dirtyAge(m.nowMS(), oldestDirtyAt),
	}
}

func dirtyAge(nowMS int64, oldestDirtyAtMS int64) time.Duration {
	if nowMS <= 0 || oldestDirtyAtMS <= 0 || nowMS <= oldestDirtyAtMS {
		return 0
	}
	return time.Duration(nowMS-oldestDirtyAtMS) * time.Millisecond
}

func positiveDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Nanosecond
	}
	return d
}

func (m *Manager) dirtyFlushEntries(limit int) []flushEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := make([]flushEntry, 0)
	for uid, byChannel := range m.cache {
		for key, entry := range byChannel {
			if !entry.dirty {
				continue
			}
			entries = append(entries, flushEntry{
				uid:     uid,
				key:     key,
				patch:   entry.patch,
				version: entry.version,
			})
			if limit > 0 && len(entries) >= limit {
				return entries
			}
		}
	}
	return entries
}

func (m *Manager) dirtyFlushEntriesForHashSlot(hashSlot uint16, limit int) []flushEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addresses := m.dirtyByHashSlot[hashSlot]
	if len(addresses) == 0 {
		return nil
	}
	entries := make([]flushEntry, 0)
	for address := range addresses {
		byChannel := m.cache[address.uid]
		if byChannel == nil {
			continue
		}
		entry, ok := byChannel[address.key]
		if !ok || !entry.dirty || !entry.hasHashSlot || entry.hashSlot != hashSlot {
			continue
		}
		entries = append(entries, flushEntry{
			uid:     address.uid,
			key:     address.key,
			patch:   entry.patch,
			version: entry.version,
		})
		if limit > 0 && len(entries) >= limit {
			return entries
		}
	}
	return entries
}

func (m *Manager) clearFlushedDirty(entries []flushEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, flushed := range entries {
		byChannel := m.cache[flushed.uid]
		if byChannel == nil {
			continue
		}
		current, ok := byChannel[flushed.key]
		if !ok || current.version != flushed.version || !current.dirty {
			continue
		}
		current.dirty = false
		byChannel[flushed.key] = current
		m.untrackDirtyLocked(cacheAddress{uid: flushed.uid, key: flushed.key}, current)
	}
}

func (m *Manager) trackDirtyLocked(address cacheAddress, entry cacheEntry) {
	m.dirtyRows++
	if m.dirtyRowsByKind == nil {
		m.dirtyRowsByKind = make(map[metadb.ConversationKind]int)
	}
	m.dirtyRowsByKind[entry.patch.Kind]++
	if entry.hasHashSlot {
		if m.dirtyByHashSlot == nil {
			m.dirtyByHashSlot = make(map[uint16]map[cacheAddress]struct{})
		}
		addresses := m.dirtyByHashSlot[entry.hashSlot]
		if addresses == nil {
			addresses = make(map[cacheAddress]struct{})
			m.dirtyByHashSlot[entry.hashSlot] = addresses
		}
		addresses[address] = struct{}{}
	}
	activeAtMS := entry.patch.ActiveAtMS
	if activeAtMS <= 0 {
		return
	}
	if m.dirtyActiveAtCounts == nil {
		m.dirtyActiveAtCounts = make(map[int64]int)
	}
	if m.dirtyActiveAtCounts[activeAtMS] == 0 {
		heap.Push(&m.dirtyActiveAtHeap, activeAtMS)
	}
	m.dirtyActiveAtCounts[activeAtMS]++
}

func (m *Manager) untrackDirtyLocked(address cacheAddress, entry cacheEntry) {
	if m.dirtyRows > 0 {
		m.dirtyRows--
	}
	decrementKindCount(m.dirtyRowsByKind, entry.patch.Kind)
	if entry.hasHashSlot && m.dirtyByHashSlot != nil {
		addresses := m.dirtyByHashSlot[entry.hashSlot]
		delete(addresses, address)
		if len(addresses) == 0 {
			delete(m.dirtyByHashSlot, entry.hashSlot)
		}
	}
	activeAtMS := entry.patch.ActiveAtMS
	if activeAtMS <= 0 || m.dirtyActiveAtCounts == nil {
		return
	}
	count := m.dirtyActiveAtCounts[activeAtMS]
	if count <= 1 {
		delete(m.dirtyActiveAtCounts, activeAtMS)
		return
	}
	m.dirtyActiveAtCounts[activeAtMS] = count - 1
}

func (m *Manager) moveDirtyLocked(address cacheAddress, oldEntry, newEntry cacheEntry) {
	if oldEntry.patch.ActiveAtMS == newEntry.patch.ActiveAtMS &&
		oldEntry.hashSlot == newEntry.hashSlot &&
		oldEntry.hasHashSlot == newEntry.hasHashSlot {
		return
	}
	m.untrackDirtyLocked(address, oldEntry)
	m.trackDirtyLocked(address, newEntry)
}

func (m *Manager) oldestDirtyAtLocked() int64 {
	for m.dirtyActiveAtHeap.Len() > 0 {
		oldest := m.dirtyActiveAtHeap[0]
		if m.dirtyActiveAtCounts[oldest] > 0 {
			return oldest
		}
		heap.Pop(&m.dirtyActiveAtHeap)
	}
	return 0
}

func cloneKindCounts(in map[metadb.ConversationKind]int) map[metadb.ConversationKind]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[metadb.ConversationKind]int, len(in))
	for kind, count := range in {
		if count > 0 {
			out[kind] = count
		}
	}
	return out
}

func decrementKindCount(counts map[metadb.ConversationKind]int, kind metadb.ConversationKind) {
	if len(counts) == 0 {
		return
	}
	count := counts[kind]
	if count <= 1 {
		delete(counts, kind)
		return
	}
	counts[kind] = count - 1
}

func activePatchMetaPatch(patch ActivePatch) metadb.ConversationActivePatch {
	return metadb.ConversationActivePatch{
		UID:         patch.UID,
		Kind:        patch.Kind,
		ChannelID:   patch.ChannelID,
		ChannelType: int64(patch.ChannelType),
		ReadSeq:     patch.ReadSeq,
		ActiveAt:    patch.ActiveAtMS,
		UpdatedAt:   patch.ActiveAtMS,
	}
}

// DirtyCountForTest returns the number of unflushed cached rows for tests.
func (m *Manager) DirtyCountForTest() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.dirtyRows
}

// EntryForTest returns a cached active row for tests.
func (m *Manager) EntryForTest(kind metadb.ConversationKind, uid, channelID string, channelType uint8) (ActivePatch, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byChannel := m.cache[uid]
	if byChannel == nil {
		return ActivePatch{}, false
	}
	entry, ok := byChannel[conversationKey{kind: kind, channelID: channelID, channelType: channelType}]
	return entry.patch, ok
}

type dirtyActiveAtMinHeap []int64

func (h dirtyActiveAtMinHeap) Len() int {
	return len(h)
}

func (h dirtyActiveAtMinHeap) Less(i, j int) bool {
	return h[i] < h[j]
}

func (h dirtyActiveAtMinHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *dirtyActiveAtMinHeap) Push(x any) {
	*h = append(*h, x.(int64))
}

func (h *dirtyActiveAtMinHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
