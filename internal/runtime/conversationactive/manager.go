package conversationactive

import (
	"context"
	"errors"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	pressureHighWatermarkPercent = 80
	pressureLowWatermarkPercent  = 70
	inlineActivePatchRows        = 8
	preparedBatchInitialRows     = 512
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

type preparedActivePatch struct {
	address     cacheAddress
	patch       ActivePatch
	hashSlot    uint16
	hasHashSlot bool
}

type preparedActiveBatch struct {
	rows      []preparedActivePatch
	positions map[cacheAddress]int
}

type routedActivePatch struct {
	hashSlot uint16
	patch    ActivePatch
}

type cacheEntry struct {
	// patch is the latest observed active projection exposed by the cache view and used by the next required flush.
	patch ActivePatch
	// durableActiveAtMS is the latest ActiveAt confirmed by a durable read or successful write for this baseline generation.
	durableActiveAtMS int64
	// baselineGeneration fences durable confirmations from a removed, recreated, or delete-invalidated entry.
	baselineGeneration uint64
	// deleteBarrier is the newest durable delete floor already applied to this cached entry.
	deleteBarrier uint64
	// version changes whenever patch content changes, fencing stale flush snapshots.
	version uint64
	// dirty reports that patch still needs to be flushed to the durable store.
	dirty bool
	// readSeqDirty reports that the current dirty version advances ReadSeq, rather than merely retaining a historical value.
	readSeqDirty bool
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
	// baselineGeneration fences durable confirmations against delete invalidation or row recreation.
	baselineGeneration uint64
	// confirmedActiveAtMS is filled after a durable cooldown read for skipped snapshots.
	confirmedActiveAtMS int64
	// readSeqDirty keeps receiver-only cooldown classification independent from a historical cached ReadSeq.
	readSeqDirty bool
}

type cacheMutationKind uint8

const (
	cacheMutationUnchanged cacheMutationKind = iota
	cacheMutationBecameDirty
	cacheMutationDirtyUpdated
	cacheMutationCooldownSuppressed
)

type dirtyClearResult struct {
	cleared          int
	versionConflicts int
	staleSnapshots   int
}

// Manager owns the in-memory UID conversation active cache.
type Manager struct {
	// mu protects cache and all per-UID conversation rows.
	mu sync.RWMutex
	// flushMu serializes dirty snapshots through durable completion to bound concurrent snapshot memory.
	flushMu sync.Mutex
	// nowMS supplies ActiveAtMS when an admitted batch does not provide one.
	nowMS func() int64
	// store reads and persists durable active rows for cache/store merging.
	store ActiveStore
	// activeCooldown suppresses receiver-only re-dirtying and flushes within this durable row age window.
	activeCooldown time.Duration
	// maxCachedRows bounds cached rows across all UIDs; zero means unbounded.
	maxCachedRows int
	// pressureHighRows starts proactive asynchronous flushing at this cache size.
	pressureHighRows int
	// pressureLowDirtyRows stops a pressure-triggered flush cycle while retaining clean rows as an eviction reserve.
	pressureLowDirtyRows int
	// pressureDraining reports that the asynchronous worker is draining a high-pressure cache.
	pressureDraining bool
	// pressureNotify receives nonblocking cache-pressure wakeups owned by the app flush worker.
	pressureNotify chan<- PressureSignal
	// cacheObservationInterval bounds aggregate cache snapshot work on the admission path.
	cacheObservationInterval time.Duration
	// lastCacheObservationAt is the local time of the latest emitted aggregate cache snapshot.
	lastCacheObservationAt time.Time
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
	// dirtyAge indexes only live positive dirty ActiveAtMS values and removes empty buckets eagerly.
	dirtyAge dirtyAgeIndex
	// dirtyQueue gives bounded flushes deterministic round-robin coverage across live dirty rows.
	dirtyQueue dirtyAddressQueue
	// nextVersion allocates monotonic cache-entry versions for dirty flush fencing.
	nextVersion uint64
	// nextBaselineGeneration allocates identities for durable baselines across deletion and row recreation.
	nextBaselineGeneration uint64
	// observationRevision orders cache snapshots delivered by concurrent callers.
	observationRevision uint64
	// cache stores UID -> conversation key -> active projection entry.
	cache map[string]map[conversationKey]cacheEntry
	// cleanIndex contains every clean cache address when maxCachedRows is bounded.
	// It lets admission find an eviction reserve without scanning dirty cache rows.
	cleanIndex map[cacheAddress]struct{}
	// cleanByHashSlot indexes clean cache rows with an authority hash slot for bounded handoff purges.
	cleanByHashSlot map[uint16]map[cacheAddress]struct{}
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
	pressureHighRows, pressureLowDirtyRows := pressureWatermarks(opts.MaxCachedRows)
	manager := &Manager{
		nowMS:                    nowMS,
		store:                    opts.Store,
		activeCooldown:           opts.ActiveCooldown,
		maxCachedRows:            opts.MaxCachedRows,
		pressureHighRows:         pressureHighRows,
		pressureLowDirtyRows:     pressureLowDirtyRows,
		pressureNotify:           opts.PressureNotify,
		cacheObservationInterval: opts.CacheObservationInterval,
		observer:                 opts.Observer,
		rowsByKind:               make(map[metadb.ConversationKind]int),
		dirtyRowsByKind:          make(map[metadb.ConversationKind]int),
		cache:                    make(map[string]map[conversationKey]cacheEntry),
		dirtyByHashSlot:          make(map[uint16]map[cacheAddress]struct{}),
	}
	if opts.MaxCachedRows > 0 {
		manager.cleanIndex = make(map[cacheAddress]struct{})
	}
	return manager
}

// AdmitActiveBatch admits a channelappend recipient batch into the active cache.
func (m *Manager) AdmitActiveBatch(ctx context.Context, batch ActiveBatch) error {
	return m.admitActiveBatch(ctx, 0, false, batch)
}

// AdmitActiveBatchForHashSlot admits a channelappend recipient batch for one UID hash slot.
func (m *Manager) AdmitActiveBatchForHashSlot(ctx context.Context, hashSlot uint16, batch ActiveBatch) error {
	return m.admitActiveBatch(ctx, hashSlot, true, batch)
}

// AdmitRoutedActiveBatches atomically admits multiple already-partitioned
// exact hash-slot groups under one cache lock. A cache-address hash-slot
// conflict or cache-pressure rejection leaves every group unapplied.
func (m *Manager) AdmitRoutedActiveBatches(ctx context.Context, batches []RoutedActiveBatch) error {
	var inlineRows [inlineActivePatchRows]preparedActivePatch
	prepared, err := m.prepareRoutedActiveBatches(batches, inlineRows[:0])
	if err != nil {
		return err
	}
	return m.markPreparedActive(ctx, prepared, true)
}

func (m *Manager) admitActiveBatch(ctx context.Context, hashSlot uint16, hasHashSlot bool, batch ActiveBatch) error {
	activeAtMS := batch.ActiveAtMS
	if activeAtMS == 0 {
		activeAtMS = m.nowMS()
	}

	patchCapacity := len(batch.Recipients)
	if batch.SenderUID != "" {
		patchCapacity++
	}
	var inlinePatches [inlineActivePatchRows]ActivePatch
	patches := inlinePatches[:0]
	if patchCapacity > cap(inlinePatches) {
		patches = make([]ActivePatch, 0, patchCapacity)
	}
	if batch.SenderUID != "" {
		patches = append(patches, ActivePatch{
			UID:         batch.SenderUID,
			Kind:        batch.Kind,
			ChannelID:   batch.ChannelID,
			ChannelType: batch.ChannelType,
			ActiveAtMS:  activeAtMS,
			ReadSeq:     batch.MessageSeq,
			MessageSeq:  batch.MessageSeq,
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
			MessageSeq:  batch.MessageSeq,
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

// ApplyConversationDeletes reconciles committed delete barriers with exact cached rows.
// Rows at or below the barrier, including rows with an unknown message sequence, are removed.
// Newer rows retain their latest active view and become dirty against an invalid durable baseline.
// Reapplying the same barrier re-invalidates a clean newer row after a prior uncommitted delete attempt.
func (m *Manager) ApplyConversationDeletes(deletes []metadb.ConversationDelete) {
	if m == nil || len(deletes) == 0 {
		return
	}

	m.mu.Lock()
	for _, deletion := range deletes {
		if deletion.UID == "" || deletion.ChannelID == "" || deletion.ChannelType <= 0 || deletion.ChannelType > 255 || deletion.DeletedToSeq == 0 {
			continue
		}
		key := conversationKey{kind: deletion.Kind, channelID: deletion.ChannelID, channelType: uint8(deletion.ChannelType)}
		address := cacheAddress{uid: deletion.UID, key: key}
		byChannel := m.cache[deletion.UID]
		if byChannel == nil {
			continue
		}
		current, ok := byChannel[key]
		if !ok || deletion.DeletedToSeq < current.deleteBarrier {
			continue
		}
		if current.patch.MessageSeq == 0 || current.patch.MessageSeq <= deletion.DeletedToSeq {
			m.removeCacheEntryLocked(address, current)
			continue
		}
		if deletion.DeletedToSeq == current.deleteBarrier && current.dirty {
			continue
		}

		current.deleteBarrier = deletion.DeletedToSeq
		current.durableActiveAtMS = 0
		m.nextBaselineGeneration++
		current.baselineGeneration = m.nextBaselineGeneration
		if !current.dirty {
			m.untrackCleanLocked(address, current)
			current.dirty = true
			current.readSeqDirty = false
			m.trackDirtyLocked(address, current)
		}
		m.nextVersion++
		current.version = m.nextVersion
		byChannel[key] = current
	}
	signalPressure := m.startPressureDrainLocked()
	stopPressure := false
	if m.pressureDraining && m.dirtyRows <= m.pressureLowDirtyRows {
		m.pressureDraining = false
		stopPressure = true
		signalPressure = false
	}
	m.mu.Unlock()

	if stopPressure {
		m.observePressure(PressureObservation{Event: "stop_low_watermark"})
	}
	if signalPressure {
		m.observePressure(PressureObservation{Event: "start_high_watermark"})
		m.signalPressure()
	}
	m.observeCache()
}

// InvalidateConversationDeleteAttempts conservatively fences cache baselines
// after a delete batch returns an unknown committed prefix. It never treats an
// attempted barrier as confirmed and never removes a cached row; every present
// row is forced dirty so durable state decides whether its activity survives.
func (m *Manager) InvalidateConversationDeleteAttempts(deletes []metadb.ConversationDelete) {
	if m == nil || len(deletes) == 0 {
		return
	}

	m.mu.Lock()
	for _, deletion := range deletes {
		if deletion.UID == "" || deletion.ChannelID == "" || deletion.ChannelType <= 0 || deletion.ChannelType > 255 || deletion.DeletedToSeq == 0 {
			continue
		}
		key := conversationKey{kind: deletion.Kind, channelID: deletion.ChannelID, channelType: uint8(deletion.ChannelType)}
		address := cacheAddress{uid: deletion.UID, key: key}
		byChannel := m.cache[deletion.UID]
		if byChannel == nil {
			continue
		}
		current, ok := byChannel[key]
		if !ok {
			continue
		}

		current.durableActiveAtMS = 0
		m.nextBaselineGeneration++
		current.baselineGeneration = m.nextBaselineGeneration
		if !current.dirty {
			m.untrackCleanLocked(address, current)
			current.dirty = true
			current.readSeqDirty = false
			m.trackDirtyLocked(address, current)
		}
		m.nextVersion++
		current.version = m.nextVersion
		byChannel[key] = current
	}
	signalPressure := m.startPressureDrainLocked()
	stopPressure := false
	if m.pressureDraining && m.dirtyRows <= m.pressureLowDirtyRows {
		m.pressureDraining = false
		stopPressure = true
		signalPressure = false
	}
	m.mu.Unlock()

	if stopPressure {
		m.observePressure(PressureObservation{Event: "stop_low_watermark"})
	}
	if signalPressure {
		m.observePressure(PressureObservation{Event: "start_high_watermark"})
		m.signalPressure()
	}
	m.observeCache()
}

// PurgeCleanHashSlot removes clean cache rows retained from one authority hash slot.
// Dirty rows are preserved for the existing scoped handoff drain.
func (m *Manager) PurgeCleanHashSlot(hashSlot uint16) int {
	purged := m.PurgeCleanHashSlotStateOnly(hashSlot)
	if purged > 0 {
		m.ObserveCacheState()
	}
	return purged
}

// PurgeCleanHashSlotStateOnly removes clean rows without invoking the observer.
// Callers holding an outer lifecycle lock must observe after publishing state and unlocking.
func (m *Manager) PurgeCleanHashSlotStateOnly(hashSlot uint16) int {
	if m == nil {
		return 0
	}

	m.mu.Lock()
	purged := 0
	for address := range m.cleanByHashSlot[hashSlot] {
		entry, ok := m.cache[address.uid][address.key]
		if !ok || entry.dirty || !entry.hasHashSlot || entry.hashSlot != hashSlot {
			// Discard a stale index entry defensively without falling back to a full-cache scan.
			delete(m.cleanByHashSlot[hashSlot], address)
			continue
		}
		m.removeCacheEntryLocked(address, entry)
		purged++
	}
	if len(m.cleanByHashSlot[hashSlot]) == 0 {
		delete(m.cleanByHashSlot, hashSlot)
	}
	m.mu.Unlock()
	return purged
}

// ObserveCacheState publishes one current cache snapshot outside caller lifecycle locks.
func (m *Manager) ObserveCacheState() {
	if m == nil {
		return
	}
	m.observeCache()
}

func (m *Manager) markActive(ctx context.Context, hashSlot uint16, hasHashSlot bool, patches []ActivePatch) error {
	if len(patches) == 0 {
		return nil
	}
	var inlineRows [inlineActivePatchRows]preparedActivePatch
	prepared := prepareActiveBatch(patches, inlineRows[:0])
	if len(prepared.rows) == 0 {
		return nil
	}
	for index := range prepared.rows {
		prepared.rows[index].hashSlot = hashSlot
		prepared.rows[index].hasHashSlot = hasHashSlot
	}
	return m.markPreparedActive(ctx, prepared, false)
}

func (m *Manager) markPreparedActive(_ context.Context, prepared preparedActiveBatch, rejectHashSlotConflict bool) error {
	if len(prepared.rows) == 0 {
		return nil
	}
	lockStartedAt := time.Now()
	m.mu.Lock()
	lockAcquiredAt := time.Now()
	mutation := MutationObservation{Result: "ok", LockWaitDuration: nonNegativeDuration(lockAcquiredAt.Sub(lockStartedAt))}
	if rejectHashSlotConflict && m.hashSlotConflictLocked(prepared.rows) {
		mutation.Result = "error"
		mutation.LockHoldDuration = nonNegativeDuration(time.Since(lockAcquiredAt))
		m.mu.Unlock()
		m.observeMutation(mutation)
		return ErrHashSlotConflict
	}
	newRows := m.newRowsLocked(prepared.rows)
	if m.cacheWouldExceedLocked(newRows) {
		if newRows > m.maxCachedRows {
			mutation.Result = "cache_pressure"
			observationStartedAt := time.Now()
			cacheObservation, observeCache := m.maybeCacheObservationLocked(false, observationStartedAt)
			if observeCache {
				mutation.CacheObservationDuration = nonNegativeDuration(time.Since(observationStartedAt))
			}
			mutation.LockHoldDuration = nonNegativeDuration(time.Since(lockAcquiredAt))
			m.mu.Unlock()
			m.observeMutation(mutation)
			m.observeCacheSnapshot(cacheObservation, observeCache)
			return ErrCachePressure
		}
		over := m.totalRows + newRows - m.maxCachedRows
		var inlineVictims [inlineActivePatchRows]cacheAddress
		if m.evictableCleanRowsLocked(prepared) >= over {
			victims, ok := m.planCleanEvictionsLocked(over, prepared, inlineVictims[:0])
			if ok {
				m.evictCleanVictimsLocked(victims)
			}
		}
	}
	if !m.cacheWouldExceedLocked(newRows) {
		for _, row := range prepared.rows {
			switch m.markActiveLocked(row, row.hashSlot, row.hasHashSlot) {
			case cacheMutationBecameDirty:
				mutation.BecameDirty++
			case cacheMutationDirtyUpdated:
				mutation.DirtyUpdated++
			case cacheMutationCooldownSuppressed:
				mutation.CooldownSuppressed++
			default:
				mutation.Unchanged++
			}
		}
		signalPressure := m.startPressureDrainLocked()
		observationStartedAt := time.Now()
		cacheObservation, observeCache := m.maybeCacheObservationLocked(signalPressure, observationStartedAt)
		if observeCache {
			mutation.CacheObservationDuration = nonNegativeDuration(time.Since(observationStartedAt))
		}
		mutation.LockHoldDuration = nonNegativeDuration(time.Since(lockAcquiredAt))
		m.mu.Unlock()
		if signalPressure {
			m.observePressure(PressureObservation{Event: "start_high_watermark"})
			m.signalPressure()
		}
		m.observeMutation(mutation)
		m.observeCacheSnapshot(cacheObservation, observeCache)
		return nil
	}
	signalPressure := !m.pressureDraining
	m.pressureDraining = true
	mutation.Result = "cache_pressure"
	observationStartedAt := time.Now()
	cacheObservation, observeCache := m.maybeCacheObservationLocked(signalPressure, observationStartedAt)
	if observeCache {
		mutation.CacheObservationDuration = nonNegativeDuration(time.Since(observationStartedAt))
	}
	mutation.LockHoldDuration = nonNegativeDuration(time.Since(lockAcquiredAt))
	m.mu.Unlock()
	if signalPressure {
		m.observePressure(PressureObservation{Event: "start_hard_limit"})
		m.signalPressure()
	}
	m.observeMutation(mutation)
	m.observeCacheSnapshot(cacheObservation, observeCache)
	return ErrCachePressure
}

func (m *Manager) prepareRoutedActiveBatches(batches []RoutedActiveBatch, inline []preparedActivePatch) (preparedActiveBatch, error) {
	if len(batches) == 0 {
		return preparedActiveBatch{}, nil
	}
	capacity := 0
	for _, routed := range batches {
		capacity += len(routed.Batch.Recipients)
		if routed.Batch.SenderUID != "" {
			capacity++
		}
	}
	if capacity <= cap(inline) {
		return m.prepareSmallRoutedActiveBatches(batches, inline)
	}
	return m.prepareLargeRoutedActiveBatches(batches, capacity)
}

func (m *Manager) prepareSmallRoutedActiveBatches(batches []RoutedActiveBatch, inline []preparedActivePatch) (preparedActiveBatch, error) {
	var inlinePatches [inlineActivePatchRows]routedActivePatch
	patches := inlinePatches[:0]
	for _, routed := range batches {
		batch := routed.Batch
		activeAtMS := batch.ActiveAtMS
		if activeAtMS == 0 {
			activeAtMS = m.nowMS()
		}
		if batch.SenderUID != "" {
			patches = append(patches, routedActivePatch{hashSlot: routed.HashSlot, patch: ActivePatch{
				UID:         batch.SenderUID,
				Kind:        batch.Kind,
				ChannelID:   batch.ChannelID,
				ChannelType: batch.ChannelType,
				ActiveAtMS:  activeAtMS,
				ReadSeq:     batch.MessageSeq,
				MessageSeq:  batch.MessageSeq,
			}})
		}
		for _, recipient := range batch.Recipients {
			if recipient.UID == "" || batch.SenderUID != "" && recipient.UID == batch.SenderUID {
				continue
			}
			var readSeq uint64
			if recipient.IsSender {
				readSeq = batch.MessageSeq
			}
			patches = append(patches, routedActivePatch{hashSlot: routed.HashSlot, patch: ActivePatch{
				UID:         recipient.UID,
				Kind:        batch.Kind,
				ChannelID:   batch.ChannelID,
				ChannelType: batch.ChannelType,
				ActiveAtMS:  activeAtMS,
				ReadSeq:     readSeq,
				MessageSeq:  batch.MessageSeq,
			}})
		}
	}

	rows := inline[:0]
	for _, routed := range patches {
		address := activePatchAddress(routed.patch)
		merged := false
		for index := range rows {
			current := &rows[index]
			if current.address != address {
				continue
			}
			if current.hashSlot != routed.hashSlot {
				return preparedActiveBatch{}, ErrHashSlotConflict
			}
			mergeActivePatch(&current.patch, routed.patch)
			merged = true
			break
		}
		if !merged {
			rows = append(rows, preparedActivePatch{
				address:     address,
				patch:       routed.patch,
				hashSlot:    routed.hashSlot,
				hasHashSlot: true,
			})
		}
	}
	return preparedActiveBatch{rows: rows}, nil
}

func (m *Manager) prepareLargeRoutedActiveBatches(batches []RoutedActiveBatch, capacity int) (preparedActiveBatch, error) {
	prepared := preparedActiveBatch{
		rows:      make([]preparedActivePatch, 0, capacity),
		positions: make(map[cacheAddress]int, capacity),
	}
	for _, routed := range batches {
		batch := routed.Batch
		activeAtMS := batch.ActiveAtMS
		if activeAtMS == 0 {
			activeAtMS = m.nowMS()
		}
		if batch.SenderUID != "" {
			if err := appendPreparedRoutedPatch(&prepared, routed.HashSlot, ActivePatch{
				UID:         batch.SenderUID,
				Kind:        batch.Kind,
				ChannelID:   batch.ChannelID,
				ChannelType: batch.ChannelType,
				ActiveAtMS:  activeAtMS,
				ReadSeq:     batch.MessageSeq,
				MessageSeq:  batch.MessageSeq,
			}); err != nil {
				return preparedActiveBatch{}, err
			}
		}
		for _, recipient := range batch.Recipients {
			if recipient.UID == "" || batch.SenderUID != "" && recipient.UID == batch.SenderUID {
				continue
			}
			var readSeq uint64
			if recipient.IsSender {
				readSeq = batch.MessageSeq
			}
			if err := appendPreparedRoutedPatch(&prepared, routed.HashSlot, ActivePatch{
				UID:         recipient.UID,
				Kind:        batch.Kind,
				ChannelID:   batch.ChannelID,
				ChannelType: batch.ChannelType,
				ActiveAtMS:  activeAtMS,
				ReadSeq:     readSeq,
				MessageSeq:  batch.MessageSeq,
			}); err != nil {
				return preparedActiveBatch{}, err
			}
		}
	}
	return prepared, nil
}

func appendPreparedRoutedPatch(prepared *preparedActiveBatch, hashSlot uint16, patch ActivePatch) error {
	if prepared == nil || patch.UID == "" {
		return nil
	}
	address := activePatchAddress(patch)
	if prepared.positions != nil {
		if index, ok := prepared.positions[address]; ok {
			current := &prepared.rows[index]
			if current.hasHashSlot && current.hashSlot != hashSlot {
				return ErrHashSlotConflict
			}
			mergeActivePatch(&current.patch, patch)
			return nil
		}
		prepared.positions[address] = len(prepared.rows)
	} else {
		for index := range prepared.rows {
			current := &prepared.rows[index]
			if current.address != address {
				continue
			}
			if current.hasHashSlot && current.hashSlot != hashSlot {
				return ErrHashSlotConflict
			}
			mergeActivePatch(&current.patch, patch)
			return nil
		}
	}
	prepared.rows = append(prepared.rows, preparedActivePatch{
		address:     address,
		patch:       patch,
		hashSlot:    hashSlot,
		hasHashSlot: true,
	})
	return nil
}

func (m *Manager) hashSlotConflictLocked(rows []preparedActivePatch) bool {
	for _, row := range rows {
		if !row.hasHashSlot {
			continue
		}
		byChannel := m.cache[row.address.uid]
		if byChannel == nil {
			continue
		}
		current, ok := byChannel[row.address.key]
		if ok && current.hasHashSlot && current.hashSlot != row.hashSlot {
			return true
		}
	}
	return false
}

func (m *Manager) signalPressure() {
	if m == nil || m.pressureNotify == nil {
		return
	}
	select {
	case m.pressureNotify <- PressureSignal{EnqueuedAt: time.Now()}:
		m.observePressure(PressureObservation{Event: "signal_sent"})
	default:
		m.observePressure(PressureObservation{Event: "signal_coalesced"})
	}
}

func (m *Manager) startPressureDrainLocked() bool {
	if m.pressureDraining || m.maxCachedRows <= 0 || m.pressureHighRows <= 0 || m.totalRows < m.pressureHighRows || m.dirtyRows <= m.pressureLowDirtyRows {
		return false
	}
	m.pressureDraining = true
	return true
}

func (m *Manager) continuePressureDrain(cleared int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if !m.pressureDraining {
		m.mu.Unlock()
		return
	}
	if m.dirtyRows <= m.pressureLowDirtyRows {
		m.pressureDraining = false
		m.mu.Unlock()
		m.observePressure(PressureObservation{Event: "stop_low_watermark"})
		return
	}
	m.mu.Unlock()
	if cleared <= 0 {
		m.observePressure(PressureObservation{Event: "requeue_no_progress"})
		return
	}
	m.observePressure(PressureObservation{Event: "requeue_progress"})
	m.signalPressure()
}

func pressureWatermarks(maxRows int) (int, int) {
	if maxRows <= 0 {
		return 0, 0
	}
	high := maxRows * pressureHighWatermarkPercent / 100
	if high <= 0 {
		high = 1
	}
	low := maxRows * pressureLowWatermarkPercent / 100
	if low >= high {
		low = high - 1
	}
	if low < 0 {
		low = 0
	}
	return high, low
}

func (m *Manager) markActiveLocked(row preparedActivePatch, hashSlot uint16, hasHashSlot bool) cacheMutationKind {
	patch := row.patch
	address := row.address
	key := address.key
	byChannel := m.cache[patch.UID]
	if byChannel == nil {
		byChannel = make(map[conversationKey]cacheEntry)
		m.cache[patch.UID] = byChannel
	}

	current, ok := byChannel[key]
	if !ok {
		m.nextVersion++
		m.nextBaselineGeneration++
		entry := cacheEntry{
			patch:              patch,
			version:            m.nextVersion,
			baselineGeneration: m.nextBaselineGeneration,
			dirty:              true,
			readSeqDirty:       patch.ReadSeq > 0,
			hashSlot:           hashSlot,
			hasHashSlot:        hasHashSlot,
		}
		byChannel[key] = entry
		m.totalRows++
		if m.rowsByKind == nil {
			m.rowsByKind = make(map[metadb.ConversationKind]int)
		}
		m.rowsByKind[key.kind]++
		m.trackDirtyLocked(address, entry)
		return cacheMutationBecameDirty
	}

	slotChanged := current.hashSlot != hashSlot || current.hasHashSlot != hasHashSlot
	merged := current.patch
	readSeqAdvanced := patch.ReadSeq > merged.ReadSeq
	if patch.ActiveAtMS > merged.ActiveAtMS {
		merged.ActiveAtMS = patch.ActiveAtMS
	}
	if patch.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = patch.ReadSeq
	}
	if patch.MessageSeq > merged.MessageSeq {
		merged.MessageSeq = patch.MessageSeq
	}
	if merged == current.patch && !slotChanged {
		return cacheMutationUnchanged
	}
	if m.shouldSuppressCleanReceiverActive(current, merged, readSeqAdvanced, slotChanged) {
		m.nextVersion++
		current.patch = merged
		current.version = m.nextVersion
		byChannel[key] = current
		return cacheMutationCooldownSuppressed
	}

	next := current
	next.patch = merged
	next.hashSlot = hashSlot
	next.hasHashSlot = hasHashSlot
	wasDirty := current.dirty
	if wasDirty {
		next.readSeqDirty = current.readSeqDirty || readSeqAdvanced
		m.moveDirtyLocked(address, current, next)
	} else {
		m.untrackCleanLocked(address, current)
		next.dirty = true
		next.readSeqDirty = readSeqAdvanced
		m.trackDirtyLocked(address, next)
	}
	m.nextVersion++
	next.version = m.nextVersion
	next.dirty = true
	byChannel[key] = next
	if wasDirty {
		return cacheMutationDirtyUpdated
	}
	return cacheMutationBecameDirty
}

// shouldSuppressCleanReceiverActive reports whether a receiver-only update is
// still covered by the durable ActiveAt baseline retained in a clean entry.
func (m *Manager) shouldSuppressCleanReceiverActive(current cacheEntry, merged ActivePatch, readSeqAdvanced, slotChanged bool) bool {
	if current.dirty || slotChanged || readSeqAdvanced {
		return false
	}
	cooldownMS := int64(m.activeCooldown / time.Millisecond)
	if cooldownMS <= 0 || current.durableActiveAtMS <= 0 || merged.ActiveAtMS < current.durableActiveAtMS {
		return false
	}
	return merged.ActiveAtMS-current.durableActiveAtMS < cooldownMS
}

func prepareActiveBatch(patches []ActivePatch, inline []preparedActivePatch) preparedActiveBatch {
	if len(patches) <= cap(inline) {
		rows := inline[:0]
		for _, patch := range patches {
			if patch.UID == "" {
				continue
			}
			address := activePatchAddress(patch)
			merged := false
			for index := range rows {
				if rows[index].address != address {
					continue
				}
				mergeActivePatch(&rows[index].patch, patch)
				merged = true
				break
			}
			if !merged {
				rows = append(rows, preparedActivePatch{address: address, patch: patch})
			}
		}
		return preparedActiveBatch{rows: rows}
	}

	capacity := len(patches)
	if capacity > preparedBatchInitialRows {
		capacity = preparedBatchInitialRows
	}
	rows := make([]preparedActivePatch, 0, capacity)
	positions := make(map[cacheAddress]int, capacity)
	for _, patch := range patches {
		if patch.UID == "" {
			continue
		}
		address := activePatchAddress(patch)
		if index, ok := positions[address]; ok {
			mergeActivePatch(&rows[index].patch, patch)
			continue
		}
		positions[address] = len(rows)
		rows = append(rows, preparedActivePatch{address: address, patch: patch})
	}
	return preparedActiveBatch{rows: rows, positions: positions}
}

func activePatchAddress(patch ActivePatch) cacheAddress {
	return cacheAddress{
		uid: patch.UID,
		key: conversationKey{kind: patch.Kind, channelID: patch.ChannelID, channelType: patch.ChannelType},
	}
}

func mergeActivePatch(current *ActivePatch, incoming ActivePatch) {
	if incoming.ActiveAtMS > current.ActiveAtMS {
		current.ActiveAtMS = incoming.ActiveAtMS
	}
	if incoming.ReadSeq > current.ReadSeq {
		current.ReadSeq = incoming.ReadSeq
	}
	if incoming.MessageSeq > current.MessageSeq {
		current.MessageSeq = incoming.MessageSeq
	}
}

func (b preparedActiveBatch) contains(address cacheAddress) bool {
	if b.positions != nil {
		_, ok := b.positions[address]
		return ok
	}
	for _, row := range b.rows {
		if row.address == address {
			return true
		}
	}
	return false
}

func (m *Manager) newRowsLocked(rows []preparedActivePatch) int {
	var count int
	for _, row := range rows {
		byChannel := m.cache[row.address.uid]
		if byChannel == nil {
			count++
			continue
		}
		if _, ok := byChannel[row.address.key]; !ok {
			count++
		}
	}
	return count
}

func (m *Manager) cacheWouldExceedLocked(newRows int) bool {
	return m.maxCachedRows > 0 && newRows > 0 && m.totalRows+newRows > m.maxCachedRows
}

func (m *Manager) evictableCleanRowsLocked(protected preparedActiveBatch) int {
	cleanRows := len(m.cleanIndex)
	for _, row := range protected.rows {
		if _, ok := m.cleanIndex[row.address]; ok {
			cleanRows--
		}
	}
	if cleanRows < 0 {
		return 0
	}
	return cleanRows
}

func (m *Manager) planCleanEvictionsLocked(limit int, protected preparedActiveBatch, scratch []cacheAddress) ([]cacheAddress, bool) {
	if limit <= 0 {
		return scratch[:0], true
	}
	if cap(scratch) < limit {
		scratch = make([]cacheAddress, 0, limit)
	} else {
		scratch = scratch[:0]
	}
	for address := range m.cleanIndex {
		if protected.contains(address) {
			continue
		}
		byChannel := m.cache[address.uid]
		entry, ok := byChannel[address.key]
		if !ok || entry.dirty {
			continue
		}
		scratch = append(scratch, address)
		if len(scratch) == limit {
			return scratch, true
		}
	}
	return scratch, false
}

func (m *Manager) evictCleanVictimsLocked(victims []cacheAddress) bool {
	for _, address := range victims {
		byChannel := m.cache[address.uid]
		entry, ok := byChannel[address.key]
		if !ok || entry.dirty {
			return false
		}
	}
	for _, address := range victims {
		byChannel := m.cache[address.uid]
		m.removeCacheEntryLocked(address, byChannel[address.key])
	}
	return true
}

func (m *Manager) removeCacheEntryLocked(address cacheAddress, entry cacheEntry) {
	byChannel := m.cache[address.uid]
	if byChannel == nil {
		return
	}
	if entry.dirty {
		m.untrackDirtyLocked(address, entry)
	} else {
		m.untrackCleanLocked(address, entry)
	}
	delete(byChannel, address.key)
	if m.totalRows > 0 {
		m.totalRows--
	}
	decrementKindCount(m.rowsByKind, address.key.kind)
	if len(byChannel) == 0 {
		delete(m.cache, address.uid)
	}
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
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	laneWaitDuration := time.Since(startedAt)

	selectStartedAt := time.Now()
	entries := m.dirtyFlushEntriesForHashSlot(hashSlot, limit)
	return m.flushDirtyEntries(ctx, startedAt, laneWaitDuration, time.Since(selectStartedAt), entries)
}

func (m *Manager) flushDirty(ctx context.Context, limit int) (FlushResult, error) {
	if m.store == nil {
		return FlushResult{}, ErrStoreRequired
	}
	startedAt := time.Now()
	m.flushMu.Lock()
	defer m.flushMu.Unlock()

	return m.flushDirtySerialized(ctx, startedAt, time.Since(startedAt), limit)
}

func (m *Manager) flushDirtySerialized(ctx context.Context, startedAt time.Time, laneWaitDuration time.Duration, limit int) (FlushResult, error) {
	selectStartedAt := time.Now()
	entries := m.dirtyFlushEntries(limit)
	return m.flushDirtyEntries(ctx, startedAt, laneWaitDuration, time.Since(selectStartedAt), entries)
}

func (m *Manager) flushDirtyEntries(ctx context.Context, startedAt time.Time, laneWaitDuration time.Duration, selectDuration time.Duration, entries []flushEntry) (FlushResult, error) {
	observation := FlushObservation{
		Selected:         len(entries),
		LaneWaitDuration: nonNegativeDuration(laneWaitDuration),
		SelectDuration:   nonNegativeDuration(selectDuration),
	}
	if len(entries) == 0 {
		m.continuePressureDrain(0)
		observation.Result = "no_dirty"
		observation.Duration = positiveDuration(time.Since(startedAt))
		m.observeFlush(observation)
		m.observeCache()
		return FlushResult{}, nil
	}

	filterStartedAt := time.Now()
	flushEntries, skippedEntries, deleteFenced, err := m.filterFlushEntries(ctx, entries)
	observation.FilterDuration = nonNegativeDuration(time.Since(filterStartedAt))
	if err != nil {
		observation.Result = flushErrorResult(err)
		observation.FailureStage = "filter"
		observation.Requeued = m.currentDirtySelectedCount(entries)
		observation.Duration = positiveDuration(time.Since(startedAt))
		m.observeFlush(observation)
		m.observePressurePause(err)
		m.observeCache()
		return FlushResult{Selected: len(entries), Requeued: observation.Requeued}, err
	}
	// Durable delete reconciliation already removed these rows during filtering.
	// Publish that terminal classification even if a later touch for the
	// remaining rows fails; only rows still present can be requeued.
	observation.DeleteFenced = deleteFenced
	patches := make([]metadb.ConversationActivePatch, 0, len(flushEntries))
	for _, entry := range flushEntries {
		patches = append(patches, activePatchMetaPatch(entry.patch))
	}
	if len(patches) > 0 {
		persistStartedAt := time.Now()
		if err := m.store.TouchConversationActiveAt(ctx, patches); err != nil {
			observation.PersistDuration = nonNegativeDuration(time.Since(persistStartedAt))
			observation.Result = flushErrorResult(err)
			observation.FailureStage = "persist"
			observation.Requeued = m.currentDirtySelectedCount(entries)
			observation.Duration = positiveDuration(time.Since(startedAt))
			m.observeFlush(observation)
			m.observePressurePause(err)
			m.observeCache()
			return FlushResult{Selected: len(entries), DeleteFenced: deleteFenced, Requeued: observation.Requeued}, err
		}
		observation.PersistDuration = nonNegativeDuration(time.Since(persistStartedAt))
	}
	observation.Persisted = len(flushEntries)
	observation.Skipped = len(skippedEntries) - deleteFenced
	clearStartedAt := time.Now()
	skippedClear, persistedClear, clearLockWait, clearApply := m.clearDirtyEntries(skippedEntries, flushEntries)
	observation.ClearDuration = nonNegativeDuration(time.Since(clearStartedAt))
	observation.ClearLockWaitDuration = clearLockWait
	observation.ClearApplyDuration = clearApply
	observation.Cleared = skippedClear.cleared + persistedClear.cleared
	observation.VersionConflicts = skippedClear.versionConflicts + persistedClear.versionConflicts
	observation.Superseded = skippedClear.staleSnapshots + persistedClear.staleSnapshots
	observation.Requeued = observation.VersionConflicts
	m.continuePressureDrain(observation.Cleared)
	observation.Result = "ok"
	observation.Duration = positiveDuration(time.Since(startedAt))
	m.observeFlush(observation)
	m.observeCache()
	return FlushResult{
		Selected:         observation.Selected,
		Persisted:        observation.Persisted,
		Skipped:          observation.Skipped,
		DeleteFenced:     observation.DeleteFenced,
		Cleared:          observation.Cleared,
		VersionConflicts: observation.VersionConflicts,
		Superseded:       observation.Superseded,
		Requeued:         observation.Requeued,
	}, nil
}

// currentDirtySelectedCount reports how many selected addresses currently
// retain dirty work. A delete-fenced snapshot can be removed and then become
// dirty again when a newer activity version arrives before an error returns.
func (m *Manager) currentDirtySelectedCount(entries []flushEntry) int {
	if m == nil || len(entries) == 0 {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	dirty := 0
	for _, entry := range entries {
		current, ok := m.cache[entry.uid][entry.key]
		if ok && current.dirty {
			dirty++
		}
	}
	return dirty
}

func flushErrorResult(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	return "error"
}

func pressurePauseEvent(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "pause_timeout"
	}
	return "pause_error"
}

func (m *Manager) observePressurePause(err error) {
	if m == nil {
		return
	}
	m.mu.RLock()
	draining := m.pressureDraining
	m.mu.RUnlock()
	if draining {
		m.observePressure(PressureObservation{Event: pressurePauseEvent(err)})
	}
}

func (m *Manager) filterFlushEntries(ctx context.Context, entries []flushEntry) ([]flushEntry, []flushEntry, int, error) {
	if m.activeCooldown <= 0 {
		return entries, nil, 0, nil
	}
	keys := make([]metadb.ConversationStateKey, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, metadb.ConversationStateKey{
			UID:         entry.patch.UID,
			Kind:        entry.patch.Kind,
			ChannelID:   entry.patch.ChannelID,
			ChannelType: int64(entry.patch.ChannelType),
		})
	}
	if len(keys) == 0 {
		return entries, nil, 0, nil
	}
	states, err := m.store.GetConversationStates(ctx, keys)
	if err != nil {
		return nil, nil, 0, err
	}
	cooldownMS := int64(m.activeCooldown / time.Millisecond)
	if cooldownMS <= 0 {
		return entries, nil, 0, nil
	}
	flushEntries := make([]flushEntry, 0, len(entries))
	skippedEntries := make([]flushEntry, 0)
	deleteReconciliations := make([]metadb.ConversationDelete, 0)
	deleteFenced := 0
	for _, entry := range entries {
		key := metadb.ConversationStateKey{
			UID:         entry.patch.UID,
			Kind:        entry.patch.Kind,
			ChannelID:   entry.patch.ChannelID,
			ChannelType: int64(entry.patch.ChannelType),
		}
		state, ok := states[key]
		if ok && cacheActivityFencedByDelete(entry.patch.MessageSeq, state.DeletedToSeq) {
			entry.confirmedActiveAtMS = state.ActiveAt
			skippedEntries = append(skippedEntries, entry)
			deleteReconciliations = append(deleteReconciliations, activeViewDeleteReconciliation(state))
			deleteFenced++
			continue
		}
		if entry.readSeqDirty {
			flushEntries = append(flushEntries, entry)
			continue
		}
		if !ok || state.ActiveAt <= 0 || entry.patch.ActiveAtMS-state.ActiveAt >= cooldownMS {
			flushEntries = append(flushEntries, entry)
			continue
		}
		entry.confirmedActiveAtMS = state.ActiveAt
		skippedEntries = append(skippedEntries, entry)
	}
	m.ApplyConversationDeletes(deleteReconciliations)
	return flushEntries, skippedEntries, deleteFenced, nil
}

func (m *Manager) clearSkippedDirty(entries []flushEntry) dirtyClearResult {
	skipped, _, _, _ := m.clearDirtyEntries(entries, nil)
	return skipped
}

func (m *Manager) observeCache() {
	if m.observer == nil {
		return
	}
	m.observer.ObserveConversationActiveCache(m.cacheObservation())
}

func (m *Manager) observeCacheSnapshot(obs CacheObservation, ok bool) {
	if !ok || m.observer == nil {
		return
	}
	m.observer.ObserveConversationActiveCache(obs)
}

func (m *Manager) observeMutation(obs MutationObservation) {
	if m.observer == nil {
		return
	}
	m.observer.ObserveConversationActiveMutation(obs)
}

func (m *Manager) observeFlush(obs FlushObservation) {
	if m.observer == nil {
		return
	}
	m.observer.ObserveConversationActiveFlush(obs)
}

func (m *Manager) observePressure(obs PressureObservation) {
	if m.observer == nil || obs.Event == "" {
		return
	}
	m.observer.ObserveConversationActivePressure(obs)
}

func (m *Manager) cacheObservation() CacheObservation {
	m.mu.Lock()
	m.lastCacheObservationAt = time.Now()
	observation := m.cacheObservationLocked()
	m.mu.Unlock()
	return observation
}

func (m *Manager) maybeCacheObservationLocked(force bool, now time.Time) (CacheObservation, bool) {
	if m.observer == nil {
		return CacheObservation{}, false
	}
	if !force && m.cacheObservationInterval > 0 && !m.lastCacheObservationAt.IsZero() &&
		now.After(m.lastCacheObservationAt) && now.Sub(m.lastCacheObservationAt) < m.cacheObservationInterval {
		return CacheObservation{}, false
	}
	m.lastCacheObservationAt = now
	return m.cacheObservationLocked(), true
}

func (m *Manager) cacheObservationLocked() CacheObservation {
	m.observationRevision++
	if m.observationRevision == 0 {
		m.observationRevision++
	}
	revision := m.observationRevision
	rows := m.totalRows
	dirtyRows := m.dirtyRows
	rowsByKind := cloneKindCounts(m.rowsByKind)
	dirtyRowsByKind := cloneKindCounts(m.dirtyRowsByKind)
	oldestDirtyAt := m.dirtyAge.Oldest()
	pressureDraining := m.pressureDraining
	return CacheObservation{
		Revision:         revision,
		Rows:             rows,
		DirtyRows:        dirtyRows,
		DirtyQueueRows:   m.dirtyQueue.Len(),
		DirtyAgeBuckets:  m.dirtyAge.Len(),
		RowsByKind:       rowsByKind,
		DirtyRowsByKind:  dirtyRowsByKind,
		OldestDirtyAge:   dirtyAge(m.nowMS(), oldestDirtyAt),
		PressureDraining: pressureDraining,
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

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func (m *Manager) dirtyFlushEntries(limit int) []flushEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	queueRows := m.dirtyQueue.Len()
	if queueRows == 0 {
		return nil
	}
	capacity := queueRows
	if limit > 0 && limit < capacity {
		capacity = limit
	}
	entries := make([]flushEntry, 0, capacity)
	address, hasAddress := m.dirtyQueue.Front()
	for visited := 0; hasAddress && visited < queueRows; visited++ {
		next, hasNext := m.dirtyQueue.Next(address)
		byChannel := m.cache[address.uid]
		entry, live := byChannel[address.key]
		if !live || !entry.dirty {
			m.dirtyQueue.Remove(address)
			address, hasAddress = next, hasNext
			continue
		}
		entries = append(entries, newFlushEntry(address, entry))
		m.dirtyQueue.MoveToBack(address)
		if limit > 0 && len(entries) >= limit {
			break
		}
		address, hasAddress = next, hasNext
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
			uid:                address.uid,
			key:                address.key,
			patch:              entry.patch,
			version:            entry.version,
			baselineGeneration: entry.baselineGeneration,
			readSeqDirty:       entry.readSeqDirty,
		})
		if limit > 0 && len(entries) >= limit {
			return entries
		}
	}
	return entries
}

func newFlushEntry(address cacheAddress, entry cacheEntry) flushEntry {
	return flushEntry{
		uid:                address.uid,
		key:                address.key,
		patch:              entry.patch,
		version:            entry.version,
		baselineGeneration: entry.baselineGeneration,
		readSeqDirty:       entry.readSeqDirty,
	}
}

func (m *Manager) clearFlushedDirty(entries []flushEntry) dirtyClearResult {
	_, flushed, _, _ := m.clearDirtyEntries(nil, entries)
	return flushed
}

func (m *Manager) clearDirtyEntries(skippedEntries, flushedEntries []flushEntry) (dirtyClearResult, dirtyClearResult, time.Duration, time.Duration) {
	if len(skippedEntries) == 0 && len(flushedEntries) == 0 {
		return dirtyClearResult{}, dirtyClearResult{}, 0, 0
	}
	lockStartedAt := time.Now()
	m.mu.Lock()
	lockAcquiredAt := time.Now()
	defer m.mu.Unlock()

	skipped := dirtyClearResult{}
	for _, entry := range skippedEntries {
		m.clearDirtyEntryLocked(entry, true, &skipped)
	}
	flushed := dirtyClearResult{}
	for _, entry := range flushedEntries {
		m.clearDirtyEntryLocked(entry, false, &flushed)
	}
	return skipped, flushed,
		nonNegativeDuration(lockAcquiredAt.Sub(lockStartedAt)),
		nonNegativeDuration(time.Since(lockAcquiredAt))
}

func (m *Manager) clearDirtyEntryLocked(entry flushEntry, restoreDurableActiveAt bool, result *dirtyClearResult) {
	byChannel := m.cache[entry.uid]
	if byChannel == nil {
		result.staleSnapshots++
		return
	}
	current, ok := byChannel[entry.key]
	if !ok || !current.dirty {
		result.staleSnapshots++
		return
	}
	confirmedActiveAtMS := entry.patch.ActiveAtMS
	if restoreDurableActiveAt {
		confirmedActiveAtMS = entry.confirmedActiveAtMS
	}
	if current.baselineGeneration == entry.baselineGeneration {
		current.durableActiveAtMS = confirmedActiveAtMS
	}
	if current.version != entry.version {
		if !restoreDurableActiveAt {
			// A successful persisted snapshot covers its ReadSeq even when a
			// concurrent receiver-only ActiveAt update keeps the row dirty.
			// Rebase classification so only a newer sender sequence bypasses
			// cooldown on the retry.
			current.readSeqDirty = current.patch.ReadSeq > entry.patch.ReadSeq
		}
		byChannel[entry.key] = current
		result.versionConflicts++
		return
	}
	m.untrackDirtyLocked(cacheAddress{uid: entry.uid, key: entry.key}, current)
	current.dirty = false
	current.readSeqDirty = false
	byChannel[entry.key] = current
	m.trackCleanLocked(cacheAddress{uid: entry.uid, key: entry.key}, current)
	result.cleared++
}

func (m *Manager) trackCleanLocked(address cacheAddress, entry cacheEntry) {
	if m.cleanIndex != nil {
		m.cleanIndex[address] = struct{}{}
	}
	if !entry.hasHashSlot {
		return
	}
	if m.cleanByHashSlot == nil {
		m.cleanByHashSlot = make(map[uint16]map[cacheAddress]struct{})
	}
	byAddress := m.cleanByHashSlot[entry.hashSlot]
	if byAddress == nil {
		byAddress = make(map[cacheAddress]struct{})
		m.cleanByHashSlot[entry.hashSlot] = byAddress
	}
	byAddress[address] = struct{}{}
}

func (m *Manager) untrackCleanLocked(address cacheAddress, entry cacheEntry) {
	if m.cleanIndex != nil {
		delete(m.cleanIndex, address)
	}
	if !entry.hasHashSlot {
		return
	}
	byAddress := m.cleanByHashSlot[entry.hashSlot]
	delete(byAddress, address)
	if len(byAddress) == 0 {
		delete(m.cleanByHashSlot, entry.hashSlot)
	}
}

func (m *Manager) trackDirtyLocked(address cacheAddress, entry cacheEntry) {
	m.dirtyRows++
	if m.dirtyRowsByKind == nil {
		m.dirtyRowsByKind = make(map[metadb.ConversationKind]int)
	}
	m.dirtyRowsByKind[entry.patch.Kind]++
	m.addDirtyHashSlotLocked(address, entry)
	m.dirtyAge.Add(entry.patch.ActiveAtMS)
	m.dirtyQueue.Add(address)
}

func (m *Manager) untrackDirtyLocked(address cacheAddress, entry cacheEntry) {
	if m.dirtyRows > 0 {
		m.dirtyRows--
	}
	decrementKindCount(m.dirtyRowsByKind, entry.patch.Kind)
	m.removeDirtyHashSlotLocked(address, entry)
	m.dirtyAge.Remove(entry.patch.ActiveAtMS)
	m.dirtyQueue.Remove(address)
}

func (m *Manager) moveDirtyLocked(address cacheAddress, oldEntry, newEntry cacheEntry) {
	if oldEntry.hashSlot != newEntry.hashSlot || oldEntry.hasHashSlot != newEntry.hasHashSlot {
		m.removeDirtyHashSlotLocked(address, oldEntry)
		m.addDirtyHashSlotLocked(address, newEntry)
	}
	if oldEntry.patch.ActiveAtMS != newEntry.patch.ActiveAtMS {
		m.dirtyAge.Move(oldEntry.patch.ActiveAtMS, newEntry.patch.ActiveAtMS)
	}
}

func (m *Manager) addDirtyHashSlotLocked(address cacheAddress, entry cacheEntry) {
	if !entry.hasHashSlot {
		return
	}
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

func (m *Manager) removeDirtyHashSlotLocked(address cacheAddress, entry cacheEntry) {
	if !entry.hasHashSlot || m.dirtyByHashSlot == nil {
		return
	}
	addresses := m.dirtyByHashSlot[entry.hashSlot]
	delete(addresses, address)
	if len(addresses) == 0 {
		delete(m.dirtyByHashSlot, entry.hashSlot)
	}
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
		MessageSeq:  patch.MessageSeq,
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
