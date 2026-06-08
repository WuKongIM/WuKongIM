package app

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type conversationAuthorityStore interface {
	// ListUserConversationActivePage returns durable active rows in active-index order.
	ListUserConversationActivePage(context.Context, string, metadb.UserConversationActiveCursor, int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
	// GetUserConversationState returns the durable primary row used for delete-barrier checks.
	GetUserConversationState(context.Context, string, string, int64) (metadb.UserConversationState, bool, error)
	// TouchUserConversationActiveAtBatch flushes authority-cache activity hints without blind upserts.
	TouchUserConversationActiveAtBatch(context.Context, []metadb.UserConversationActivePatch) error
}

type conversationAuthorityOptions struct {
	// LocalNodeID identifies the node allowed to serve local authority targets.
	LocalNodeID uint64
	// Store persists and reads UID-owned conversation active rows.
	Store conversationAuthorityStore
	// MaxRowsPerUID bounds cached active rows for one UID.
	MaxRowsPerUID int
	// MaxRows bounds cached active rows across all UIDs.
	MaxRows int
	// ListDBWindowMax bounds DB overfetch needed to merge cache rows without dropping them.
	ListDBWindowMax int
	// AdmissionBatchRows mirrors routed-client admission config; local cache admission is currently atomic under one lock.
	AdmissionBatchRows int
	// AdmissionConcurrency mirrors routed-client admission config; local cache admission is currently atomic under one lock.
	AdmissionConcurrency int
	// HandoffTimeout is enforced by the committed sink before calling DrainAuthority.
	HandoffTimeout time.Duration
	// Observer receives low-cardinality authority cache/list/handoff observations.
	Observer conversationAuthorityObserver
}

type conversationAuthority struct {
	// mu protects cached rows, target state, and totalRows.
	mu sync.Mutex
	// localNodeID fences admissions to targets owned by this node.
	localNodeID uint64
	// store backs durable active-page reads and flushes.
	store conversationAuthorityStore
	// maxRowsByUID limits per-UID cache growth.
	maxRowsByUID int
	// maxRows limits total cache growth across UIDs.
	maxRows int
	// listWindow limits DB overfetch during active-view merge.
	listWindow int
	// totalRows tracks cached rows across all UID maps.
	totalRows int
	// byUID stores unflushed active patches keyed by UID and conversation key.
	byUID map[string]map[conversationAuthorityKey]conversationAuthorityEntry
	// targets stores fenced authority state by full route target.
	targets map[conversationAuthorityTargetKey]conversationAuthorityState
	// observer receives authority-specific cache/list/handoff observations.
	observer conversationAuthorityObserver
}

type conversationAuthorityKey struct {
	// channelID identifies the conversation channel.
	channelID string
	// channelType identifies the channel namespace.
	channelType int64
}

type conversationAuthorityEntry struct {
	// patch is the coalesced unflushed activity candidate for one conversation.
	patch conversationusecase.ActivePatch
	// target is the exact route target that admitted this cache entry.
	target conversationAuthorityTargetKey
}

type conversationAuthorityCachedRow struct {
	// row is the active-view representation of a cached patch.
	row metadb.UserConversationState
	// messageSeq fences the cached activation against durable delete barriers.
	messageSeq uint64
}

type conversationAuthorityFlushEntry struct {
	// uid owns the cached conversation row.
	uid string
	// key identifies the cached conversation row within the UID map.
	key conversationAuthorityKey
	// patch is the exact snapshot sent to durable flush.
	patch conversationusecase.ActivePatch
	// target is the exact route target that admitted this cache entry.
	target conversationAuthorityTargetKey
}

type conversationAuthorityTargetKey struct {
	// hashSlot is the logical UID hash slot.
	hashSlot uint16
	// slotID is the physical Slot that owns hashSlot.
	slotID uint32
	// leaderNodeID is the authority leader for this target.
	leaderNodeID uint64
	// routeRevision fences route-table changes.
	routeRevision uint64
	// authorityEpoch fences authority leadership changes.
	authorityEpoch uint64
}

// conversationAuthorityState describes local authority handoff state.
type conversationAuthorityState string

const (
	conversationAuthorityActive   conversationAuthorityState = "active"
	conversationAuthorityWarming  conversationAuthorityState = "warming"
	conversationAuthorityDraining conversationAuthorityState = "draining"
)

// conversationDrainResult reports the local outcome of draining authority cache rows.
type conversationDrainResult = string

const (
	conversationDrainResultDrained     conversationDrainResult = "drained"
	conversationDrainResultTransferred conversationDrainResult = "transferred"
	conversationDrainResultNoDirty     conversationDrainResult = "no_dirty"
	conversationDrainResultBusy        conversationDrainResult = "busy"
)

const (
	conversationAuthorityPhaseAdmit = "admit"
	conversationAuthorityPhaseList  = "list"
	conversationAuthorityPhaseFlush = "flush"

	conversationAuthorityResultOK            = "ok"
	conversationAuthorityResultError         = "error"
	conversationAuthorityResultIgnored       = "ignored"
	conversationAuthorityResultCachePressure = "cache_pressure"
	conversationAuthorityResultRouteNotReady = "route_not_ready"
	conversationAuthorityResultStaleRoute    = "stale_route"
	conversationAuthorityResultNotLeader     = "not_leader"
	conversationAuthorityResultTimeout       = "timeout"
	conversationAuthorityResultOther         = "other"
)

type conversationAuthorityObserver interface {
	ObserveConversationAuthorityAdmit(conversationAuthorityAdmitEvent)
	ObserveConversationAuthorityCachePressure(conversationAuthorityCachePressureEvent)
	ObserveConversationAuthorityList(conversationAuthorityListEvent)
	ObserveConversationAuthorityHandoff(conversationAuthorityHandoffEvent)
}

// conversationAuthorityAdmitEvent reports one foreground authority admission outcome.
type conversationAuthorityAdmitEvent struct {
	Result string
}

// conversationAuthorityCachePressureEvent reports local cache pressure by authority phase.
type conversationAuthorityCachePressureEvent struct {
	Phase  string
	Result string
}

// conversationAuthorityListEvent reports one authority active-view list outcome.
type conversationAuthorityListEvent struct {
	Result string
}

// conversationAuthorityHandoffEvent reports one authority drain/handoff outcome.
type conversationAuthorityHandoffEvent struct {
	Result string
}

// newConversationAuthority constructs an unwired local authority cache runtime.
func newConversationAuthority(opts conversationAuthorityOptions) *conversationAuthority {
	if opts.MaxRowsPerUID <= 0 {
		opts.MaxRowsPerUID = 4096
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = 100000
	}
	if opts.ListDBWindowMax <= 0 {
		opts.ListDBWindowMax = 1000
	}
	return &conversationAuthority{
		localNodeID:  opts.LocalNodeID,
		store:        opts.Store,
		maxRowsByUID: opts.MaxRowsPerUID,
		maxRows:      opts.MaxRows,
		listWindow:   opts.ListDBWindowMax,
		byUID:        make(map[string]map[conversationAuthorityKey]conversationAuthorityEntry),
		targets:      make(map[conversationAuthorityTargetKey]conversationAuthorityState),
		observer:     opts.Observer,
	}
}

// markActive marks a fenced target ready for local cache admission and list reads.
func (a *conversationAuthority) markActive(target conversationusecase.RouteTarget) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setTargetStateLocked(targetKey(target), conversationAuthorityActive)
}

// markWarming marks a target as not yet safe to serve active-view reads.
func (a *conversationAuthority) markWarming(target conversationusecase.RouteTarget) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setTargetStateLocked(targetKey(target), conversationAuthorityWarming)
}

// AdmitPatches coalesces activity hints into the local authority cache.
func (a *conversationAuthority) AdmitPatches(_ context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	if a == nil {
		return nil
	}
	observePressure := false
	a.mu.Lock()
	defer func() {
		a.mu.Unlock()
		if observePressure {
			a.observeCachePressure(conversationAuthorityPhaseAdmit, conversationusecase.ErrCachePressure)
		}
	}()
	if err := a.ensureTargetLocked(target); err != nil {
		return err
	}
	routeTarget := targetKey(target)
	stagedByUID := make(map[string]map[conversationAuthorityKey]conversationAuthorityEntry)
	stagedTotalRows := a.totalRows
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 || patch.ActiveAt <= 0 {
			continue
		}
		rows := stagedByUID[patch.UID]
		if rows == nil {
			rows = cloneConversationAuthorityRows(a.byUID[patch.UID])
			stagedByUID[patch.UID] = rows
		}
		key := conversationAuthorityKey{channelID: patch.ChannelID, channelType: patch.ChannelType}
		existing, exists := rows[key]
		if !exists && (len(rows) >= a.maxRowsByUID || stagedTotalRows >= a.maxRows) {
			observePressure = true
			return conversationusecase.ErrCachePressure
		}
		if !exists {
			stagedTotalRows++
			if stagedTotalRows > a.maxRows {
				observePressure = true
				return conversationusecase.ErrCachePressure
			}
		}
		rows[key] = conversationAuthorityEntry{patch: mergeConversationActivePatch(existing.patch, patch), target: routeTarget}
	}
	for uid, rows := range stagedByUID {
		a.byUID[uid] = rows
	}
	a.totalRows = stagedTotalRows
	return nil
}

func (a *conversationAuthority) ensureTargetLocked(target conversationusecase.RouteTarget) error {
	return a.ensureTargetKeyLocked(targetKey(target))
}

func (a *conversationAuthority) ensureTargetKeyLocked(target conversationAuthorityTargetKey) error {
	if target.leaderNodeID != 0 && target.leaderNodeID != a.localNodeID {
		return conversationusecase.ErrNotLeader
	}
	switch a.targets[target] {
	case conversationAuthorityActive:
		return nil
	case conversationAuthorityWarming:
		return conversationusecase.ErrRouteNotReady
	default:
		return conversationusecase.ErrStaleRoute
	}
}

func (a *conversationAuthority) setTargetStateLocked(target conversationAuthorityTargetKey, state conversationAuthorityState) {
	for existing := range a.targets {
		if existing != target && a.sameLocalAuthorityTarget(existing, target) {
			a.retagEntriesLocked(existing, target)
			delete(a.targets, existing)
		}
	}
	a.targets[target] = state
}

// sameLocalAuthorityTarget reports whether two target keys represent the same local UID authority.
func (a *conversationAuthority) sameLocalAuthorityTarget(existing, next conversationAuthorityTargetKey) bool {
	return existing.hashSlot == next.hashSlot && a.localAuthorityTarget(existing) && a.localAuthorityTarget(next)
}

// localAuthorityTarget reports whether the target can be served by this local runtime.
func (a *conversationAuthority) localAuthorityTarget(target conversationAuthorityTargetKey) bool {
	return target.leaderNodeID == 0 || target.leaderNodeID == a.localNodeID
}

// retagEntriesLocked moves dirty rows forward when a local authority target is superseded.
func (a *conversationAuthority) retagEntriesLocked(from, to conversationAuthorityTargetKey) {
	for uid, rows := range a.byUID {
		for key, entry := range rows {
			if entry.target != from {
				continue
			}
			entry.target = to
			rows[key] = entry
		}
		a.byUID[uid] = rows
	}
}

// Flush persists the current cache snapshot using active-touch patch semantics.
func (a *conversationAuthority) Flush(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if a.store == nil {
		return conversationusecase.ErrRouteNotReady
	}
	entries := a.flushEntries()
	if len(entries) == 0 {
		return nil
	}
	return a.flushEntriesToStore(ctx, entries)
}

func (a *conversationAuthority) flushEntriesToStore(ctx context.Context, entries []conversationAuthorityFlushEntry) error {
	patches := make([]metadb.UserConversationActivePatch, 0, len(entries))
	for _, entry := range entries {
		patches = append(patches, entry.patch.ToMetaPatch())
	}
	if err := a.store.TouchUserConversationActiveAtBatch(ctx, patches); err != nil {
		return err
	}
	a.clearFlushed(entries)
	return nil
}

// DrainAuthority marks the target draining and flushes dirty rows for handoff.
func (a *conversationAuthority) DrainAuthority(ctx context.Context, target conversationusecase.RouteTarget) (result conversationDrainResult, err error) {
	if a == nil {
		return conversationDrainResultNoDirty, nil
	}
	result = conversationDrainResultNoDirty
	defer func() {
		a.observeHandoff(result, err)
	}()
	if a.store == nil {
		result = conversationDrainResultBusy
		err = conversationusecase.ErrRouteNotReady
		return result, err
	}
	routeTarget := targetKey(target)
	a.mu.Lock()
	if target.LeaderNodeID != 0 && target.LeaderNodeID != a.localNodeID {
		a.mu.Unlock()
		result = conversationDrainResultBusy
		err = conversationusecase.ErrNotLeader
		return result, err
	}
	if _, ok := a.targets[routeTarget]; !ok {
		a.mu.Unlock()
		err = conversationusecase.ErrStaleRoute
		return result, err
	}
	a.setTargetStateLocked(routeTarget, conversationAuthorityDraining)
	a.mu.Unlock()
	entries := a.flushEntriesForTarget(routeTarget)
	if len(entries) == 0 {
		return result, nil
	}
	if err = a.flushEntriesToStore(ctx, entries); err != nil {
		result = conversationDrainResultBusy
		return result, err
	}
	result = conversationDrainResultDrained
	return result, nil
}

// ListUserConversationActiveView is a conservative test/backward adapter for unscoped reads.
// Future local and RPC callers should use ListUserConversationActiveViewForTarget.
func (a *conversationAuthority) ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (page conversationusecase.ActiveViewPage, err error) {
	if a == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	defer func() {
		a.observeList(err)
	}()
	if a.store == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	target, err := a.unscopedListTarget()
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return a.listUserConversationActiveView(ctx, &target, true, uid, after, limit)
}

// ListUserConversationActiveViewForTarget serves an active view after validating the exact local route target.
func (a *conversationAuthority) ListUserConversationActiveViewForTarget(ctx context.Context, target conversationusecase.RouteTarget, uid string, after metadb.UserConversationActiveCursor, limit int) (page conversationusecase.ActiveViewPage, err error) {
	if a == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	defer func() {
		a.observeList(err)
	}()
	if a.store == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	targetKey := targetKey(target)
	a.mu.Lock()
	err = a.ensureTargetLocked(target)
	a.mu.Unlock()
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return a.listUserConversationActiveView(ctx, &targetKey, false, uid, after, limit)
}

func (a *conversationAuthority) listUserConversationActiveView(ctx context.Context, target *conversationAuthorityTargetKey, requireSingleActive bool, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	cacheRows, overflow := a.cacheRowsAfter(uid, after, target, a.listWindow)
	if overflow {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrCachePressure
	}
	// The DB read needs one extra row to prove whether the returned page has more.
	dbLimit := limit + serviceCacheRowCount(len(cacheRows), limit) + 1
	if dbLimit > a.listWindow {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrCachePressure
	}
	dbRows, _, done, err := a.store.ListUserConversationActivePage(ctx, uid, after, dbLimit)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	merged, err := a.mergeRows(ctx, uid, dbRows, cacheRows)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	sortConversationRows(merged)
	if len(merged) > limit {
		merged = merged[:limit]
		done = false
	}
	if target != nil {
		if requireSingleActive {
			currentTarget, err := a.unscopedListTarget()
			if err != nil {
				return conversationusecase.ActiveViewPage{}, err
			}
			if currentTarget != *target {
				return conversationusecase.ActiveViewPage{}, conversationusecase.ErrStaleRoute
			}
		} else {
			a.mu.Lock()
			err := a.ensureTargetKeyLocked(*target)
			a.mu.Unlock()
			if err != nil {
				return conversationusecase.ActiveViewPage{}, err
			}
		}
	}
	return conversationusecase.ActiveViewPage{Rows: merged, Cursor: conversationRowsCursor(merged, after), Done: done}, nil
}

func (a *conversationAuthority) ensureListReady() error {
	_, err := a.unscopedListTarget()
	return err
}

func (a *conversationAuthority) unscopedListTarget() (conversationAuthorityTargetKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	activeCount := 0
	var activeTarget conversationAuthorityTargetKey
	var hasWarming bool
	var hasDraining bool
	for key, state := range a.targets {
		if key.leaderNodeID != 0 && key.leaderNodeID != a.localNodeID {
			continue
		}
		switch state {
		case conversationAuthorityActive:
			activeCount++
			activeTarget = key
		case conversationAuthorityWarming:
			hasWarming = true
		case conversationAuthorityDraining:
			hasDraining = true
		}
	}
	if hasWarming {
		return conversationAuthorityTargetKey{}, conversationusecase.ErrRouteNotReady
	}
	if hasDraining {
		return conversationAuthorityTargetKey{}, conversationusecase.ErrStaleRoute
	}
	if activeCount == 1 {
		return activeTarget, nil
	}
	return conversationAuthorityTargetKey{}, conversationusecase.ErrStaleRoute
}

func (a *conversationAuthority) flushEntries() []conversationAuthorityFlushEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.totalRows == 0 {
		return nil
	}
	entries := make([]conversationAuthorityFlushEntry, 0, a.totalRows)
	for uid, rows := range a.byUID {
		for key, entry := range rows {
			entries = append(entries, conversationAuthorityFlushEntry{
				uid:    uid,
				key:    key,
				patch:  entry.patch,
				target: entry.target,
			})
		}
	}
	return entries
}

func (a *conversationAuthority) flushEntriesForTarget(target conversationAuthorityTargetKey) []conversationAuthorityFlushEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.totalRows == 0 {
		return nil
	}
	var entries []conversationAuthorityFlushEntry
	for uid, rows := range a.byUID {
		for key, entry := range rows {
			if entry.target != target {
				continue
			}
			entries = append(entries, conversationAuthorityFlushEntry{
				uid:    uid,
				key:    key,
				patch:  entry.patch,
				target: entry.target,
			})
		}
	}
	return entries
}

func (a *conversationAuthority) clearFlushed(entries []conversationAuthorityFlushEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, entry := range entries {
		rows := a.byUID[entry.uid]
		current, ok := rows[entry.key]
		if !ok || current.patch != entry.patch || current.target != entry.target {
			continue
		}
		delete(rows, entry.key)
		a.totalRows--
		if len(rows) == 0 {
			delete(a.byUID, entry.uid)
		}
	}
}

func cloneConversationAuthorityRows(rows map[conversationAuthorityKey]conversationAuthorityEntry) map[conversationAuthorityKey]conversationAuthorityEntry {
	cloned := make(map[conversationAuthorityKey]conversationAuthorityEntry, len(rows))
	for key, entry := range rows {
		cloned[key] = entry
	}
	return cloned
}

func (a *conversationAuthority) cacheRowsAfter(uid string, after metadb.UserConversationActiveCursor, target *conversationAuthorityTargetKey, maxRows int) ([]conversationAuthorityCachedRow, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entries := a.byUID[uid]
	if len(entries) == 0 {
		return nil, false
	}
	rows := make([]conversationAuthorityCachedRow, 0, len(entries))
	for _, entry := range entries {
		if target != nil && entry.target != *target {
			continue
		}
		row := conversationPatchState(entry.patch)
		if conversationRowAfter(row, after) {
			rows = append(rows, conversationAuthorityCachedRow{row: row, messageSeq: entry.patch.MessageSeq})
			if len(rows) > maxRows {
				return nil, true
			}
		}
	}
	sortCachedConversationRows(rows)
	return rows, false
}

func serviceCacheRowCount(cacheRows, limit int) int {
	serviceRows := limit + 1
	if serviceRows < 0 {
		serviceRows = 0
	}
	if cacheRows < serviceRows {
		return cacheRows
	}
	return serviceRows
}

func (a *conversationAuthority) mergeRows(ctx context.Context, uid string, dbRows []metadb.UserConversationState, cacheRows []conversationAuthorityCachedRow) ([]metadb.UserConversationState, error) {
	merged := make([]metadb.UserConversationState, 0, len(dbRows)+len(cacheRows))
	index := make(map[conversationAuthorityKey]int, len(dbRows))
	for _, row := range dbRows {
		key := conversationAuthorityKey{channelID: row.ChannelID, channelType: row.ChannelType}
		index[key] = len(merged)
		merged = append(merged, row)
	}
	for _, cached := range cacheRows {
		cacheRow := cached.row
		key := conversationAuthorityKey{channelID: cacheRow.ChannelID, channelType: cacheRow.ChannelType}
		if cacheRow.DeletedToSeq >= cached.messageSeq {
			continue
		}
		if offset, ok := index[key]; ok {
			if merged[offset].DeletedToSeq >= cached.messageSeq {
				continue
			}
			merged[offset] = mergeConversationState(merged[offset], cacheRow)
			continue
		}
		primary, ok, err := a.store.GetUserConversationState(ctx, uid, cacheRow.ChannelID, cacheRow.ChannelType)
		if err != nil {
			return nil, err
		}
		if ok {
			if primary.DeletedToSeq >= cached.messageSeq {
				continue
			}
			cacheRow = mergeConversationState(primary, cacheRow)
		}
		cacheRow.UID = uid
		index[key] = len(merged)
		merged = append(merged, cacheRow)
	}
	return merged, nil
}

func mergeConversationActivePatch(existing, next conversationusecase.ActivePatch) conversationusecase.ActivePatch {
	if existing.UID == "" {
		return next
	}
	merged := existing
	if next.ActiveAt > merged.ActiveAt {
		merged.ActiveAt = next.ActiveAt
		merged.MessageSeq = next.MessageSeq
		merged.SparseActive = next.SparseActive
	} else if next.ActiveAt == merged.ActiveAt && next.MessageSeq > merged.MessageSeq {
		merged.MessageSeq = next.MessageSeq
		merged.SparseActive = next.SparseActive
	}
	if next.UpdatedAt > merged.UpdatedAt {
		merged.UpdatedAt = next.UpdatedAt
	}
	if next.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = next.ReadSeq
	}
	if next.DeletedToSeq > merged.DeletedToSeq {
		merged.DeletedToSeq = next.DeletedToSeq
	}
	return merged
}

func mergeConversationState(existing, next metadb.UserConversationState) metadb.UserConversationState {
	merged := existing
	if next.ActiveAt > merged.ActiveAt {
		merged.ActiveAt = next.ActiveAt
		merged.SparseActive = next.SparseActive
	} else if next.ActiveAt == merged.ActiveAt && next.UpdatedAt > merged.UpdatedAt {
		merged.SparseActive = next.SparseActive
	}
	if next.UpdatedAt > merged.UpdatedAt {
		merged.UpdatedAt = next.UpdatedAt
	}
	if next.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = next.ReadSeq
	}
	if next.DeletedToSeq > merged.DeletedToSeq {
		merged.DeletedToSeq = next.DeletedToSeq
	}
	return merged
}

func conversationPatchState(patch conversationusecase.ActivePatch) metadb.UserConversationState {
	return metadb.UserConversationState{
		UID:          patch.UID,
		ChannelID:    patch.ChannelID,
		ChannelType:  patch.ChannelType,
		ReadSeq:      patch.ReadSeq,
		DeletedToSeq: patch.DeletedToSeq,
		ActiveAt:     patch.ActiveAt,
		UpdatedAt:    patch.UpdatedAt,
		SparseActive: patch.SparseActive,
	}
}

func conversationRowAfter(row metadb.UserConversationState, after metadb.UserConversationActiveCursor) bool {
	if after == (metadb.UserConversationActiveCursor{}) {
		return true
	}
	if row.ActiveAt != after.ActiveAt {
		return row.ActiveAt < after.ActiveAt
	}
	if row.ChannelID != after.ChannelID {
		return row.ChannelID > after.ChannelID
	}
	return row.ChannelType > after.ChannelType
}

func conversationRowsCursor(rows []metadb.UserConversationState, fallback metadb.UserConversationActiveCursor) metadb.UserConversationActiveCursor {
	if len(rows) == 0 {
		return fallback
	}
	last := rows[len(rows)-1]
	return metadb.UserConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
}

func sortConversationRows(rows []metadb.UserConversationState) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ActiveAt != rows[j].ActiveAt {
			return rows[i].ActiveAt > rows[j].ActiveAt
		}
		if rows[i].ChannelID != rows[j].ChannelID {
			return rows[i].ChannelID < rows[j].ChannelID
		}
		return rows[i].ChannelType < rows[j].ChannelType
	})
}

func sortCachedConversationRows(rows []conversationAuthorityCachedRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].row.ActiveAt != rows[j].row.ActiveAt {
			return rows[i].row.ActiveAt > rows[j].row.ActiveAt
		}
		if rows[i].row.ChannelID != rows[j].row.ChannelID {
			return rows[i].row.ChannelID < rows[j].row.ChannelID
		}
		return rows[i].row.ChannelType < rows[j].row.ChannelType
	})
}

func targetKey(target conversationusecase.RouteTarget) conversationAuthorityTargetKey {
	return conversationAuthorityTargetKey{
		hashSlot:       target.HashSlot,
		slotID:         target.SlotID,
		leaderNodeID:   target.LeaderNodeID,
		routeRevision:  target.RouteRevision,
		authorityEpoch: target.AuthorityEpoch,
	}
}

func (a *conversationAuthority) observeCachePressure(phase string, err error) {
	if a == nil || a.observer == nil {
		return
	}
	result := conversationAuthorityResultFromError(err, conversationAuthorityResultCachePressure)
	a.observer.ObserveConversationAuthorityCachePressure(conversationAuthorityCachePressureEvent{Phase: phase, Result: result})
}

func (a *conversationAuthority) observeList(err error) {
	if a == nil || a.observer == nil {
		return
	}
	result := conversationAuthorityResultFromError(err, conversationAuthorityResultOK)
	a.observer.ObserveConversationAuthorityList(conversationAuthorityListEvent{Result: result})
	if errors.Is(err, conversationusecase.ErrCachePressure) {
		a.observeCachePressure(conversationAuthorityPhaseList, err)
	}
}

func (a *conversationAuthority) observeHandoff(result conversationDrainResult, err error) {
	if a == nil || a.observer == nil {
		return
	}
	observed := string(result)
	if err != nil {
		observed = conversationAuthorityResultFromError(err, observed)
	}
	a.observer.ObserveConversationAuthorityHandoff(conversationAuthorityHandoffEvent{Result: observed})
}

func conversationAuthorityResultFromError(err error, okResult string) string {
	if err == nil {
		if okResult == "" {
			return conversationAuthorityResultOK
		}
		return okResult
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return conversationAuthorityResultTimeout
	case errors.Is(err, conversationusecase.ErrCachePressure):
		return conversationAuthorityResultCachePressure
	case errors.Is(err, conversationusecase.ErrRouteNotReady):
		return conversationAuthorityResultRouteNotReady
	case errors.Is(err, conversationusecase.ErrStaleRoute):
		return conversationAuthorityResultStaleRoute
	case errors.Is(err, conversationusecase.ErrNotLeader):
		return conversationAuthorityResultNotLeader
	default:
		return conversationAuthorityResultError
	}
}
