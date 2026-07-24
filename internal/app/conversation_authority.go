package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type conversationAuthorityStore interface {
	// ListConversationActivePage returns durable active rows in active-index order.
	ListConversationActivePage(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
	// GetConversationState returns the durable primary row used for active-view hydration.
	GetConversationState(context.Context, metadb.ConversationKind, string, string, int64) (metadb.ConversationState, bool, error)
	// GetConversationStates returns durable primary rows used for flush-time active_at filtering.
	GetConversationStates(context.Context, []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error)
	// TouchConversationActiveAtBatch flushes activity hints across bounded Slot proposals.
	// An error does not prove that no earlier proposal committed, so callers must retain the full batch for idempotent retry.
	TouchConversationActiveAtBatch(context.Context, []metadb.ConversationActivePatch) error
	// HideConversationsBatch durably advances UID-owned delete barriers through Slot ownership.
	// An error does not prove that no earlier proposal committed.
	HideConversationsBatch(context.Context, []metadb.ConversationDelete) error
}

type conversationAuthorityOptions struct {
	// LocalNodeID identifies the node allowed to serve local authority targets.
	LocalNodeID uint64
	// Store persists and reads UID-owned conversation active rows.
	Store conversationAuthorityStore
	// MaxRowsPerUID is retained for config compatibility; runtime cache pressure is bounded globally.
	MaxRowsPerUID int
	// MaxRows bounds cached active rows across all UIDs.
	MaxRows int
	// ListDBWindowMax is retained for config compatibility; active-view windowing is owned by the runtime.
	ListDBWindowMax int
	// AdmissionBatchRows mirrors routed-client active admission config and is retained for config compatibility.
	AdmissionBatchRows int
	// AdmissionConcurrency mirrors routed-client admission config and is retained for config compatibility.
	AdmissionConcurrency int
	// ActiveCooldown coalesces receiver-only active_at persistence while the authority cache keeps the latest activity visible.
	ActiveCooldown time.Duration
	// FlushBatchRows bounds dirty rows per periodic, pressure-woken, and authority handoff flush attempt.
	FlushBatchRows int
	// PressureNotify receives nonblocking dirty-cache pressure wakeups for the app flush worker.
	PressureNotify chan<- conversationactive.PressureSignal
	// CurrentRouteTarget returns the locally visible authority target for one hash slot.
	CurrentRouteTarget func(uint16) (conversationusecase.RouteTarget, bool)
	// Observer receives low-cardinality authority cache/list/handoff observations.
	Observer conversationAuthorityObserver
}

type conversationAuthority struct {
	// mu protects target state.
	mu sync.Mutex
	// localNodeID fences admissions to targets owned by this node.
	localNodeID uint64
	// store backs durable active-page reads and flushes.
	store conversationAuthorityStore
	// active owns active-row cache, list merge, pressure, and dirty flush state.
	active *conversationactive.Manager
	// flushBatchRows bounds one scoped handoff flush iteration.
	flushBatchRows int
	// currentRouteTarget reads the local route table for lazy authority activation after leader moves.
	currentRouteTarget func(uint16) (conversationusecase.RouteTarget, bool)
	// targets stores fenced authority state by full route target.
	targets map[conversationAuthorityTargetKey]conversationAuthorityState
	// admissions tracks cache mutations already accepted under each exact
	// target so handoff can fence new work and wait only for that target.
	admissions map[conversationAuthorityTargetKey]conversationAuthorityAdmissionState
	// observer receives authority-specific cache/list/handoff observations.
	observer conversationAuthorityObserver
	// beforeActiveMutation is a deterministic test seam between exact-target
	// validation and the runtime cache mutation. Production wiring leaves it nil.
	beforeActiveMutation func()
}

// conversationAuthorityAdmissionState tracks in-flight cache mutations for one
// exact authority target. idle is allocated only when a drain must wait and
// closes exactly once when inFlight reaches zero.
type conversationAuthorityAdmissionState struct {
	inFlight int
	idle     chan struct{}
}

// conversationActiveStoreAdapter adapts the app store surface to the runtime active cache store.
type conversationActiveStoreAdapter struct {
	// authority supplies the current app store, including tests that install it after construction.
	authority *conversationAuthority
}

func (s conversationActiveStoreAdapter) ListConversationActivePage(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	store := s.store()
	if store == nil {
		return nil, after, false, conversationactive.ErrStoreRequired
	}
	return store.ListConversationActivePage(ctx, kind, uid, after, limit)
}

func (s conversationActiveStoreAdapter) GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	store := s.store()
	if store == nil {
		return metadb.ConversationState{}, false, conversationactive.ErrStoreRequired
	}
	return store.GetConversationState(ctx, kind, uid, channelID, channelType)
}

func (s conversationActiveStoreAdapter) GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	store := s.store()
	if store == nil {
		return nil, conversationactive.ErrStoreRequired
	}
	return store.GetConversationStates(ctx, keys)
}

func (s conversationActiveStoreAdapter) TouchConversationActiveAt(ctx context.Context, patches []metadb.ConversationActivePatch) error {
	store := s.store()
	if store == nil {
		return conversationactive.ErrStoreRequired
	}
	return store.TouchConversationActiveAtBatch(ctx, patches)
}

func (s conversationActiveStoreAdapter) store() conversationAuthorityStore {
	if s.authority == nil {
		return nil
	}
	return s.authority.store
}

type conversationAuthorityTargetKey struct {
	// hashSlot is the physical UID hash slot.
	hashSlot uint16
	// slotID is the logical Slot Raft Group that owns hashSlot.
	slotID uint32
	// leaderNodeID is the authority leader for this target.
	leaderNodeID uint64
	// leaderTerm is the Slot Raft term observed for leaderNodeID.
	leaderTerm uint64
	// configEpoch is the control-plane Slot config epoch.
	configEpoch uint64
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

// conversationAuthorityAdmitEvent reports one authority cache admission outcome.
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

// newConversationAuthority constructs an unwired local authority route facade.
func newConversationAuthority(opts conversationAuthorityOptions) *conversationAuthority {
	if opts.MaxRows <= 0 {
		opts.MaxRows = 100000
	}
	if opts.FlushBatchRows <= 0 {
		opts.FlushBatchRows = defaultConversationAuthorityFlushBatchRows
	}
	authority := &conversationAuthority{
		localNodeID:        opts.LocalNodeID,
		store:              opts.Store,
		flushBatchRows:     opts.FlushBatchRows,
		currentRouteTarget: opts.CurrentRouteTarget,
		targets:            make(map[conversationAuthorityTargetKey]conversationAuthorityState),
		admissions:         make(map[conversationAuthorityTargetKey]conversationAuthorityAdmissionState),
		observer:           opts.Observer,
	}
	var activeObserver conversationactive.Observer
	if observer, ok := opts.Observer.(conversationactive.Observer); ok {
		activeObserver = observer
	}
	authority.active = conversationactive.NewManager(conversationactive.Options{
		Store:                    conversationActiveStoreAdapter{authority: authority},
		ActiveCooldown:           opts.ActiveCooldown,
		MaxCachedRows:            opts.MaxRows,
		PressureNotify:           opts.PressureNotify,
		CacheObservationInterval: 100 * time.Millisecond,
		Observer:                 activeObserver,
	})
	return authority
}

// markActive marks a fenced target ready for local cache admission and list reads.
func (a *conversationAuthority) markActive(target conversationusecase.RouteTarget) {
	if a == nil {
		return
	}
	key := targetKey(target)
	a.mu.Lock()
	alreadyActive := a.targets[key] == conversationAuthorityActive
	// Purge stale clean rows before publishing the target as active. Publishing
	// first would allow a concurrent cooldown-suppressed admission to refresh a
	// clean row that the subsequent purge could then discard.
	purged := 0
	if !alreadyActive && a.active != nil {
		purged = a.active.PurgeCleanHashSlotStateOnly(target.HashSlot)
	}
	a.setTargetStateLocked(key, conversationAuthorityActive)
	a.mu.Unlock()
	if purged > 0 {
		a.active.ObserveCacheState()
	}
}

// markWarming marks a target as not yet safe to serve active-view reads.
func (a *conversationAuthority) markWarming(target conversationusecase.RouteTarget) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setTargetStateLocked(targetKey(target), conversationAuthorityWarming)
}

// AdmitPatches validates the route target and delegates active admission to the runtime cache.
func (a *conversationAuthority) AdmitPatches(ctx context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) (err error) {
	if a == nil {
		return nil
	}
	defer func() {
		a.observeAdmit(err)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	activePatches, err := conversationActivePatches(patches)
	if err != nil {
		return err
	}
	if len(activePatches) == 0 {
		return a.ensureTarget(target)
	}
	reserved, err := a.reserveAdmissionTarget(target)
	if err != nil {
		return err
	}
	defer a.releaseAdmissionTarget(reserved)
	a.runBeforeActiveMutationHook()
	err = mapConversationActiveError(a.active.MarkActiveForHashSlot(ctx, target.HashSlot, activePatches))
	if errors.Is(err, conversationusecase.ErrCachePressure) {
		a.observeCachePressure(conversationAuthorityPhaseAdmit, err)
	}
	return err
}

// AdmitActiveBatch validates the route target and delegates channelappend active admission to the runtime cache.
func (a *conversationAuthority) AdmitActiveBatch(ctx context.Context, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) (err error) {
	if a == nil {
		return nil
	}
	defer func() {
		a.observeAdmit(err)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	if !validConversationKind(batch.Kind) {
		return fmt.Errorf("internal/app: invalid conversation kind %d", batch.Kind)
	}
	reserved, err := a.reserveAdmissionTarget(target)
	if err != nil {
		return err
	}
	defer a.releaseAdmissionTarget(reserved)
	a.runBeforeActiveMutationHook()
	err = mapConversationActiveError(a.active.AdmitActiveBatchForHashSlot(ctx, target.HashSlot, batch))
	if errors.Is(err, conversationusecase.ErrCachePressure) {
		a.observeCachePressure(conversationAuthorityPhaseAdmit, err)
	}
	return err
}

// AdmitActiveBatches validates aligned exact-target groups and atomically
// admits every valid sibling through one routed cache mutation. A stale group
// retains its own result and does not discard independently valid groups.
func (a *conversationAuthority) AdmitActiveBatches(ctx context.Context, groups []accessnode.ConversationActiveBatchGroup) []accessnode.ConversationActiveBatchResult {
	results := make([]accessnode.ConversationActiveBatchResult, len(groups))
	if a == nil || len(groups) == 0 {
		return results
	}
	if ctx == nil {
		ctx = context.Background()
	}

	eligible := make([]bool, len(groups))
	for index, group := range groups {
		if !validConversationKind(group.Batch.Kind) {
			results[index].Err = fmt.Errorf("internal/app: invalid conversation kind %d", group.Batch.Kind)
			continue
		}
		eligible[index] = true
	}

	// The common active-target path takes the authority mutex once for exact
	// validation and unique-target reservation. Lazy activation is retried only
	// for stale local groups and ends in the same unified revalidation+reserve
	// critical section. The mutex is never held across the cache mutation below.
	lazyIndexes := make([]int, 0)
	lazyEligible := make([]bool, len(groups))
	reservedTargets := make([]conversationAuthorityTargetKey, 0, len(groups))
	a.mu.Lock()
	for index, group := range groups {
		if !eligible[index] {
			continue
		}
		err := a.ensureTargetKeyLocked(targetKey(group.Target))
		if errors.Is(err, conversationusecase.ErrStaleRoute) && a.canLazyActivateTarget(group.Target) {
			lazyIndexes = append(lazyIndexes, index)
			lazyEligible[index] = true
			results[index].Err = err
			continue
		}
		results[index].Err = err
	}
	if len(lazyIndexes) == 0 {
		reservedTargets = a.reserveValidAdmissionTargetsLocked(groups, eligible, results, reservedTargets)
	}
	a.mu.Unlock()
	for _, index := range lazyIndexes {
		results[index].Err = a.ensureTarget(groups[index].Target)
	}
	if len(lazyIndexes) > 0 {
		// Lazy activation replaces any prior local target for the same hash slot.
		// Revalidate and reserve every eligible sibling together so a target that
		// changed during activation cannot reach the cache transaction.
		a.mu.Lock()
		for index, group := range groups {
			if eligible[index] && (results[index].Err == nil || lazyEligible[index]) {
				results[index].Err = a.ensureTargetKeyLocked(targetKey(group.Target))
			}
		}
		reservedTargets = a.reserveValidAdmissionTargetsLocked(groups, eligible, results, reservedTargets)
		a.mu.Unlock()
	}

	routed := make([]conversationactive.RoutedActiveBatch, 0, len(groups))
	validIndexes := make([]int, 0, len(groups))
	for index, group := range groups {
		if results[index].Err != nil {
			continue
		}
		validIndexes = append(validIndexes, index)
		routed = append(routed, conversationactive.RoutedActiveBatch{HashSlot: group.Target.HashSlot, Batch: group.Batch})
	}
	if len(routed) > 0 {
		a.runBeforeActiveMutationHook()
		err := mapConversationActiveError(a.active.AdmitRoutedActiveBatches(ctx, routed))
		if err != nil {
			for _, index := range validIndexes {
				results[index].Err = err
			}
		}
	}
	a.releaseAdmissionTargets(reservedTargets)
	for _, result := range results {
		a.observeAdmit(result.Err)
		if errors.Is(result.Err, conversationusecase.ErrCachePressure) {
			a.observeCachePressure(conversationAuthorityPhaseAdmit, result.Err)
		}
	}
	return results
}

func (a *conversationAuthority) runBeforeActiveMutationHook() {
	if a != nil && a.beforeActiveMutation != nil {
		a.beforeActiveMutation()
	}
}

// HideConversationsForTarget persists delete barriers through the exact local
// authority and then reconciles the corresponding cache rows before success.
func (a *conversationAuthority) HideConversationsForTarget(ctx context.Context, target conversationusecase.RouteTarget, deletes []metadb.ConversationDelete) (err error) {
	if a == nil {
		return conversationusecase.ErrRouteNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err = validateConversationDeletes(deletes); err != nil {
		return err
	}
	if len(deletes) == 0 {
		return nil
	}
	if a.store == nil {
		return conversationusecase.ErrRouteNotReady
	}
	if err = a.ensureTarget(target); err != nil {
		return err
	}

	deletes = append([]metadb.ConversationDelete(nil), deletes...)
	err = mapConversationActiveError(a.store.HideConversationsBatch(ctx, deletes))
	if err != nil {
		// A bounded multi-proposal error can have an unknown committed prefix.
		// Invalidate every requested baseline without treating an unconfirmed
		// tail as deleted; durable hydration decides which barriers committed.
		a.active.InvalidateConversationDeleteAttempts(deletes)
		return err
	}
	a.active.ApplyConversationDeletes(deletes)
	a.mu.Lock()
	err = a.ensureTargetKeyLocked(targetKey(target))
	a.mu.Unlock()
	return err
}

func validateConversationDeletes(deletes []metadb.ConversationDelete) error {
	var uid string
	for _, req := range deletes {
		if req.UID == "" || req.ChannelID == "" || req.ChannelType <= 0 || req.ChannelType > 255 || req.DeletedToSeq == 0 || !validConversationKind(req.Kind) {
			return fmt.Errorf("internal/app: invalid conversation delete")
		}
		if uid == "" {
			uid = req.UID
			continue
		}
		if req.UID != uid {
			return fmt.Errorf("internal/app: conversation deletes span multiple UIDs")
		}
	}
	return nil
}

func (a *conversationAuthority) ensureTargetLocked(target conversationusecase.RouteTarget) error {
	return a.ensureTargetKeyLocked(targetKey(target))
}

func (a *conversationAuthority) ensureTarget(target conversationusecase.RouteTarget) error {
	if a == nil {
		return conversationusecase.ErrRouteNotReady
	}
	key := targetKey(target)
	a.mu.Lock()
	err := a.ensureTargetKeyLocked(key)
	a.mu.Unlock()
	if err == nil {
		return nil
	}
	if !errors.Is(err, conversationusecase.ErrStaleRoute) || !a.canLazyActivateTarget(target) {
		return err
	}
	a.mu.Lock()
	if err := a.ensureTargetKeyLocked(key); err == nil {
		a.mu.Unlock()
		return nil
	}
	// Re-read the authoritative route only after serializing with lifecycle
	// marks. Otherwise a newer markActive can win between the route lookup and
	// this lazy activation, only to be overwritten by the stale target.
	current, ok := a.currentRouteTarget(target.HashSlot)
	if !ok || targetKey(current) != key {
		a.mu.Unlock()
		return err
	}
	purged := 0
	if a.active != nil {
		purged = a.active.PurgeCleanHashSlotStateOnly(target.HashSlot)
	}
	a.setTargetStateLocked(key, conversationAuthorityActive)
	a.mu.Unlock()
	if purged > 0 {
		a.active.ObserveCacheState()
	}
	return nil
}

// reserveAdmissionTarget validates and reserves one exact target atomically on
// the active fast path. Lazy activation always ends with the same locked
// revalidation and reservation before the runtime cache can be mutated.
func (a *conversationAuthority) reserveAdmissionTarget(target conversationusecase.RouteTarget) (conversationAuthorityTargetKey, error) {
	if a == nil {
		return conversationAuthorityTargetKey{}, conversationusecase.ErrRouteNotReady
	}
	key := targetKey(target)
	a.mu.Lock()
	err := a.ensureTargetKeyLocked(key)
	if err == nil {
		a.reserveAdmissionTargetKeyLocked(key)
		a.mu.Unlock()
		return key, nil
	}
	a.mu.Unlock()
	if !errors.Is(err, conversationusecase.ErrStaleRoute) || !a.canLazyActivateTarget(target) {
		return conversationAuthorityTargetKey{}, err
	}
	if err := a.ensureTarget(target); err != nil {
		return conversationAuthorityTargetKey{}, err
	}
	a.mu.Lock()
	err = a.ensureTargetKeyLocked(key)
	if err == nil {
		a.reserveAdmissionTargetKeyLocked(key)
	}
	a.mu.Unlock()
	if err != nil {
		return conversationAuthorityTargetKey{}, err
	}
	return key, nil
}

func (a *conversationAuthority) reserveValidAdmissionTargetsLocked(groups []accessnode.ConversationActiveBatchGroup, eligible []bool, results []accessnode.ConversationActiveBatchResult, reserved []conversationAuthorityTargetKey) []conversationAuthorityTargetKey {
	for index, group := range groups {
		if index >= len(eligible) || index >= len(results) || !eligible[index] || results[index].Err != nil {
			continue
		}
		key := targetKey(group.Target)
		if containsConversationAuthorityTargetKey(reserved, key) {
			continue
		}
		a.reserveAdmissionTargetKeyLocked(key)
		reserved = append(reserved, key)
	}
	return reserved
}

func (a *conversationAuthority) reserveAdmissionTargetKeyLocked(target conversationAuthorityTargetKey) {
	state := a.admissions[target]
	state.inFlight++
	a.admissions[target] = state
}

func (a *conversationAuthority) releaseAdmissionTarget(target conversationAuthorityTargetKey) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.releaseAdmissionTargetKeyLocked(target)
	a.mu.Unlock()
}

func (a *conversationAuthority) releaseAdmissionTargets(targets []conversationAuthorityTargetKey) {
	if a == nil || len(targets) == 0 {
		return
	}
	a.mu.Lock()
	for _, target := range targets {
		a.releaseAdmissionTargetKeyLocked(target)
	}
	a.mu.Unlock()
}

func (a *conversationAuthority) releaseAdmissionTargetKeyLocked(target conversationAuthorityTargetKey) {
	state, ok := a.admissions[target]
	if !ok || state.inFlight <= 0 {
		return
	}
	state.inFlight--
	if state.inFlight > 0 {
		a.admissions[target] = state
		return
	}
	delete(a.admissions, target)
	if state.idle != nil {
		close(state.idle)
	}
}

func containsConversationAuthorityTargetKey(targets []conversationAuthorityTargetKey, target conversationAuthorityTargetKey) bool {
	for _, existing := range targets {
		if existing == target {
			return true
		}
	}
	return false
}

func (a *conversationAuthority) canLazyActivateTarget(target conversationusecase.RouteTarget) bool {
	return a != nil &&
		a.currentRouteTarget != nil &&
		target.LeaderNodeID != 0 &&
		target.LeaderNodeID == a.localNodeID
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
	for existing, existingState := range a.targets {
		if existing != target && a.sameLocalAuthorityTarget(existing, target) {
			// A warming or draining successor must not discard an older drain
			// that still owns accepted admission reservations. A newly active
			// local tenure can safely take over those late cache mutations.
			if existingState == conversationAuthorityDraining && state != conversationAuthorityActive {
				continue
			}
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

// Flush persists dirty active rows through the runtime cache.
func (a *conversationAuthority) Flush(ctx context.Context) error {
	_, err := a.FlushActiveRows(ctx, 0)
	return err
}

// FlushActiveRows persists dirty active rows through the runtime cache.
func (a *conversationAuthority) FlushActiveRows(ctx context.Context, limit int) (conversationactive.FlushResult, error) {
	if a == nil {
		return conversationactive.FlushResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := a.active.Flush(ctx, limit)
	return result, mapConversationActiveError(err)
}

// DrainAuthority marks the target draining and flushes runtime dirty rows for handoff.
func (a *conversationAuthority) DrainAuthority(ctx context.Context, target conversationusecase.RouteTarget) (result conversationDrainResult, err error) {
	if a == nil {
		return conversationDrainResultNoDirty, nil
	}
	result = conversationDrainResultNoDirty
	defer func() {
		a.observeHandoff(result, err)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	result, err = a.beginDrainAuthority(target)
	if err != nil {
		return result, err
	}
	return a.flushDrainingAuthority(ctx, target)
}

func (a *conversationAuthority) beginDrainAuthority(target conversationusecase.RouteTarget) (conversationDrainResult, error) {
	routeTarget := targetKey(target)
	a.mu.Lock()
	defer a.mu.Unlock()
	if target.LeaderNodeID != 0 && target.LeaderNodeID != a.localNodeID {
		return conversationDrainResultBusy, conversationusecase.ErrNotLeader
	}
	if _, ok := a.targets[routeTarget]; !ok {
		return conversationDrainResultNoDirty, conversationusecase.ErrStaleRoute
	}
	a.setTargetStateLocked(routeTarget, conversationAuthorityDraining)
	return conversationDrainResultNoDirty, nil
}

func (a *conversationAuthority) finishDrainingAuthority(ctx context.Context, target conversationusecase.RouteTarget) (result conversationDrainResult, err error) {
	if a == nil {
		return conversationDrainResultNoDirty, nil
	}
	result = conversationDrainResultNoDirty
	defer func() {
		a.observeHandoff(result, err)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	return a.flushDrainingAuthority(ctx, target)
}

func (a *conversationAuthority) flushDrainingAuthority(ctx context.Context, target conversationusecase.RouteTarget) (conversationDrainResult, error) {
	targetKey := targetKey(target)
	if err := a.waitForDrainingAdmissions(ctx, targetKey); err != nil {
		if errors.Is(err, conversationusecase.ErrStaleRoute) {
			return conversationDrainResultTransferred, nil
		}
		return conversationDrainResultBusy, err
	}
	selected := 0
	for {
		if err := ctx.Err(); err != nil {
			return conversationDrainResultBusy, err
		}
		if err := a.ensureDrainingTarget(targetKey); err != nil {
			if errors.Is(err, conversationusecase.ErrStaleRoute) {
				return conversationDrainResultTransferred, nil
			}
			return conversationDrainResultBusy, err
		}
		flush, flushErr := a.active.FlushHashSlot(ctx, target.HashSlot, a.flushBatchRows)
		if flushErr != nil {
			return conversationDrainResultBusy, mapConversationActiveError(flushErr)
		}
		if flush.Selected == 0 {
			// Keep the exact draining-target check and clean purge atomic with
			// respect to markActive. An obsolete drain must never purge rows
			// admitted by a newer local authority tenure for the same hash slot.
			a.mu.Lock()
			if err := a.ensureDrainingTargetKeyLocked(targetKey); err != nil {
				a.mu.Unlock()
				if errors.Is(err, conversationusecase.ErrStaleRoute) {
					return conversationDrainResultTransferred, nil
				}
				return conversationDrainResultBusy, err
			}
			purged := a.active.PurgeCleanHashSlotStateOnly(target.HashSlot)
			// The exact target is terminally drained. Retiring it in the same
			// critical section prevents completed handoffs from accumulating
			// across repeated unknown/remote route churn.
			delete(a.targets, targetKey)
			a.mu.Unlock()
			if purged > 0 {
				a.active.ObserveCacheState()
			}
			if selected == 0 {
				return conversationDrainResultNoDirty, nil
			}
			return conversationDrainResultDrained, nil
		}
		selected += flush.Selected
	}
}

func (a *conversationAuthority) waitForDrainingAdmissions(ctx context.Context, target conversationAuthorityTargetKey) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		a.mu.Lock()
		if err := a.ensureDrainingTargetKeyLocked(target); err != nil {
			a.mu.Unlock()
			return err
		}
		state, ok := a.admissions[target]
		if !ok || state.inFlight == 0 {
			a.mu.Unlock()
			return nil
		}
		if state.idle == nil {
			state.idle = make(chan struct{})
			a.admissions[target] = state
		}
		idle := state.idle
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle:
		}
	}
}

func (a *conversationAuthority) ensureDrainingTarget(target conversationAuthorityTargetKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ensureDrainingTargetKeyLocked(target)
}

func (a *conversationAuthority) ensureDrainingTargetKeyLocked(target conversationAuthorityTargetKey) error {
	if target.leaderNodeID != 0 && target.leaderNodeID != a.localNodeID {
		return conversationusecase.ErrNotLeader
	}
	if a.targets[target] != conversationAuthorityDraining {
		return conversationusecase.ErrStaleRoute
	}
	return nil
}

// ListConversationActiveView is a conservative test/backward adapter for unscoped reads.
// Future local and RPC callers should use ListConversationActiveViewForTarget.
func (a *conversationAuthority) ListConversationActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (page conversationusecase.ActiveViewPage, err error) {
	if a == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	defer func() {
		a.observeList(err)
	}()
	target, err := a.unscopedListTarget()
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return a.listConversationActiveView(ctx, &target, true, kind, uid, after, limit)
}

// ListConversationActiveViewForTarget serves an active view after validating the exact local route target.
func (a *conversationAuthority) ListConversationActiveViewForTarget(ctx context.Context, target conversationusecase.RouteTarget, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (page conversationusecase.ActiveViewPage, err error) {
	if a == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	defer func() {
		a.observeList(err)
	}()
	targetKey := targetKey(target)
	err = a.ensureTarget(target)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return a.listConversationActiveView(ctx, &targetKey, false, kind, uid, after, limit)
}

func (a *conversationAuthority) listConversationActiveView(ctx context.Context, target *conversationAuthorityTargetKey, requireSingleActive bool, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	page, err := a.active.ListActiveView(ctx, kind, uid, after, limit)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, mapConversationActiveError(err)
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
	return conversationusecase.ActiveViewPage{Rows: page.Rows, Cursor: page.Cursor, Done: page.Done}, nil
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

func conversationActivePatches(patches []conversationusecase.ActivePatch) ([]conversationactive.ActivePatch, error) {
	activePatches := make([]conversationactive.ActivePatch, 0, len(patches))
	for _, patch := range patches {
		if !validConversationKind(patch.Kind) {
			return nil, fmt.Errorf("internal/app: invalid conversation kind %d", patch.Kind)
		}
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType <= 0 || patch.ChannelType > 255 || patch.ActiveAt <= 0 {
			continue
		}
		activePatches = append(activePatches, conversationactive.ActivePatch{
			UID:         patch.UID,
			Kind:        patch.Kind,
			ChannelID:   patch.ChannelID,
			ChannelType: uint8(patch.ChannelType),
			ActiveAtMS:  patch.ActiveAt,
			ReadSeq:     patch.ReadSeq,
			MessageSeq:  patch.MessageSeq,
		})
	}
	return activePatches, nil
}

func validConversationKind(kind metadb.ConversationKind) bool {
	switch kind {
	case metadb.ConversationKindNormal, metadb.ConversationKindCMD:
		return true
	default:
		return false
	}
}

func mapConversationActiveError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, clusterpkg.ErrNotLeader), errors.Is(err, propose.ErrNotLeader):
		return fmt.Errorf("%w: %w", conversationusecase.ErrNotLeader, err)
	case errors.Is(err, clusterpkg.ErrRouteNotReady), errors.Is(err, clusterpkg.ErrNoSlotLeader),
		errors.Is(err, propose.ErrProposalBackpressure), errors.Is(err, propose.ErrBackgroundProposalThrottled):
		return fmt.Errorf("%w: %w", conversationusecase.ErrRouteNotReady, err)
	case errors.Is(err, conversationactive.ErrCachePressure):
		return conversationusecase.ErrCachePressure
	case errors.Is(err, conversationactive.ErrStoreRequired):
		return conversationusecase.ErrRouteNotReady
	default:
		return err
	}
}

func targetKey(target conversationusecase.RouteTarget) conversationAuthorityTargetKey {
	return conversationAuthorityTargetKey{
		hashSlot:     target.HashSlot,
		slotID:       target.SlotID,
		leaderNodeID: target.LeaderNodeID,
		leaderTerm:   target.LeaderTerm,
		configEpoch:  target.ConfigEpoch,
	}
}

func (a *conversationAuthority) observeCachePressure(phase string, err error) {
	if a == nil || a.observer == nil {
		return
	}
	result := conversationAuthorityResultFromError(err, conversationAuthorityResultCachePressure)
	a.observer.ObserveConversationAuthorityCachePressure(conversationAuthorityCachePressureEvent{Phase: phase, Result: result})
}

func (a *conversationAuthority) observeAdmit(err error) {
	if a == nil || a.observer == nil {
		return
	}
	result := conversationAuthorityResultFromError(err, conversationAuthorityResultOK)
	a.observer.ObserveConversationAuthorityAdmit(conversationAuthorityAdmitEvent{Result: result})
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
