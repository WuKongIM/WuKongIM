package conversationactive

import (
	"context"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type conversationKey struct {
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
	// maxCachedRows bounds cached rows across all UIDs; zero means unbounded.
	maxCachedRows int
	// observer receives cache and flush observations.
	observer Observer
	// totalRows tracks cached active rows across all UID maps.
	totalRows int
	// nextVersion allocates monotonic cache-entry versions for dirty flush fencing.
	nextVersion uint64
	// cache stores UID -> conversation key -> active projection entry.
	cache map[string]map[conversationKey]cacheEntry
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
		nowMS:         nowMS,
		store:         opts.Store,
		maxCachedRows: opts.MaxCachedRows,
		observer:      opts.Observer,
		cache:         make(map[string]map[conversationKey]cacheEntry),
	}
}

// AdmitActiveBatch admits a channelwrite recipient batch into the active cache.
func (m *Manager) AdmitActiveBatch(ctx context.Context, batch ActiveBatch) error {
	activeAtMS := batch.ActiveAtMS
	if activeAtMS == 0 {
		activeAtMS = m.nowMS()
	}

	patches := make([]ActivePatch, 0, len(batch.Recipients)+1)
	if batch.SenderUID != "" {
		patches = append(patches, ActivePatch{
			UID:         batch.SenderUID,
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
			ChannelID:   batch.ChannelID,
			ChannelType: batch.ChannelType,
			ActiveAtMS:  activeAtMS,
			ReadSeq:     readSeq,
		})
	}

	return m.MarkActive(ctx, patches)
}

// MarkActive merges active conversation patches into the UID cache.
func (m *Manager) MarkActive(ctx context.Context, patches []ActivePatch) error {
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
				m.markActiveLocked(patch)
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

func (m *Manager) markActiveLocked(patch ActivePatch) {
	key := conversationKey{channelID: patch.ChannelID, channelType: patch.ChannelType}
	byChannel := m.cache[patch.UID]
	if byChannel == nil {
		byChannel = make(map[conversationKey]cacheEntry)
		m.cache[patch.UID] = byChannel
	}

	current, ok := byChannel[key]
	if !ok {
		m.nextVersion++
		byChannel[key] = cacheEntry{patch: patch, version: m.nextVersion, dirty: true}
		m.totalRows++
		return
	}

	merged := current.patch
	if patch.ActiveAtMS > merged.ActiveAtMS {
		merged.ActiveAtMS = patch.ActiveAtMS
	}
	if patch.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = patch.ReadSeq
	}
	if merged == current.patch {
		return
	}

	m.nextVersion++
	current.patch = merged
	current.version = m.nextVersion
	current.dirty = true
	byChannel[key] = current
}

func (m *Manager) countNewRowsLocked(patches []ActivePatch) int {
	seen := make(map[cacheAddress]struct{}, len(patches))
	var count int
	for _, patch := range patches {
		if patch.UID == "" {
			continue
		}
		key := conversationKey{channelID: patch.ChannelID, channelType: patch.ChannelType}
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

func (m *Manager) flushDirty(ctx context.Context, limit int) (FlushResult, error) {
	if m.store == nil {
		return FlushResult{}, ErrStoreRequired
	}
	startedAt := time.Now()

	entries := m.dirtyFlushEntries(limit)
	if len(entries) == 0 {
		m.observeFlush(FlushObservation{Result: "no_dirty", Duration: positiveDuration(time.Since(startedAt))})
		m.observeCache()
		return FlushResult{}, nil
	}

	patches := make([]metadb.UserConversationActivePatch, 0, len(entries))
	for _, entry := range entries {
		patches = append(patches, activePatchMetaPatch(entry.patch))
	}
	if err := m.store.TouchUserConversationActiveAt(ctx, patches); err != nil {
		m.observeFlush(FlushObservation{Result: "error", Selected: len(entries), Duration: positiveDuration(time.Since(startedAt))})
		m.observeCache()
		return FlushResult{Selected: len(entries)}, err
	}
	m.clearFlushedDirty(entries)
	m.observeFlush(FlushObservation{Result: "ok", Selected: len(entries), Flushed: len(entries), Duration: positiveDuration(time.Since(startedAt))})
	m.observeCache()
	return FlushResult{Selected: len(entries), Flushed: len(entries)}, nil
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dirtyRows int
	var oldestDirtyAt int64
	for _, byChannel := range m.cache {
		for _, entry := range byChannel {
			if !entry.dirty {
				continue
			}
			dirtyRows++
			activeAt := entry.patch.ActiveAtMS
			if activeAt <= 0 {
				continue
			}
			if oldestDirtyAt == 0 || activeAt < oldestDirtyAt {
				oldestDirtyAt = activeAt
			}
		}
	}
	return CacheObservation{
		Rows:           m.totalRows,
		DirtyRows:      dirtyRows,
		OldestDirtyAge: dirtyAge(m.nowMS(), oldestDirtyAt),
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
	}
}

func activePatchMetaPatch(patch ActivePatch) metadb.UserConversationActivePatch {
	return metadb.UserConversationActivePatch{
		UID:         patch.UID,
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

	var count int
	for _, byChannel := range m.cache {
		for _, entry := range byChannel {
			if entry.dirty {
				count++
			}
		}
	}
	return count
}

// EntryForTest returns a cached active row for tests.
func (m *Manager) EntryForTest(uid, channelID string, channelType uint8) (ActivePatch, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byChannel := m.cache[uid]
	if byChannel == nil {
		return ActivePatch{}, false
	}
	entry, ok := byChannel[conversationKey{channelID: channelID, channelType: channelType}]
	return entry.patch, ok
}
