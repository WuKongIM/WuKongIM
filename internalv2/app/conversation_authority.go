package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type conversationAuthorityStore interface {
	// ListConversationActivePage returns durable active rows in active-index order.
	ListConversationActivePage(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
	// GetConversationState returns the durable primary row used for active-view hydration.
	GetConversationState(context.Context, metadb.ConversationKind, string, string, int64) (metadb.ConversationState, bool, error)
	// GetConversationStates returns durable primary rows used for flush-time active_at filtering.
	GetConversationStates(context.Context, []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error)
	// TouchConversationActiveAtBatch atomically flushes activity hints.
	TouchConversationActiveAtBatch(context.Context, []metadb.ConversationActivePatch) error
}

type conversationActiveAdmitter interface {
	AdmitActiveBatch(context.Context, conversationactive.ActiveBatch) error
}

type normalConversationActiveAdmitter struct {
	next conversationActiveAdmitter
}

func (a normalConversationActiveAdmitter) AdmitActiveBatch(ctx context.Context, batch conversationactive.ActiveBatch) error {
	if a.next == nil {
		return nil
	}
	batch.Kind = metadb.ConversationKindNormal
	return a.next.AdmitActiveBatch(ctx, batch)
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
	// ActiveCooldown skips receiver-only active_at flushes newer than the durable row by less than this duration.
	ActiveCooldown time.Duration
	// FlushBatchRows bounds dirty rows flushed per authority handoff iteration.
	FlushBatchRows int
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
	// observer receives authority-specific cache/list/handoff observations.
	observer conversationAuthorityObserver
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
	// hashSlot is the logical UID hash slot.
	hashSlot uint16
	// slotID is the physical Slot that owns hashSlot.
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
		opts.FlushBatchRows = 128
	}
	authority := &conversationAuthority{
		localNodeID:        opts.LocalNodeID,
		store:              opts.Store,
		flushBatchRows:     opts.FlushBatchRows,
		currentRouteTarget: opts.CurrentRouteTarget,
		targets:            make(map[conversationAuthorityTargetKey]conversationAuthorityState),
		observer:           opts.Observer,
	}
	var activeObserver conversationactive.Observer
	if observer, ok := opts.Observer.(conversationactive.Observer); ok {
		activeObserver = observer
	}
	authority.active = conversationactive.NewManager(conversationactive.Options{
		Store:          conversationActiveStoreAdapter{authority: authority},
		ActiveCooldown: opts.ActiveCooldown,
		MaxCachedRows:  opts.MaxRows,
		Observer:       activeObserver,
	})
	return authority
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
	err = a.ensureTarget(target)
	if err != nil {
		return err
	}
	activePatches, err := conversationActivePatches(patches)
	if err != nil {
		return err
	}
	if len(activePatches) == 0 {
		return nil
	}
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
	err = a.ensureTarget(target)
	if err != nil {
		return err
	}
	err = mapConversationActiveError(a.active.AdmitActiveBatchForHashSlot(ctx, target.HashSlot, batch))
	if errors.Is(err, conversationusecase.ErrCachePressure) {
		a.observeCachePressure(conversationAuthorityPhaseAdmit, err)
	}
	return err
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
	current, ok := a.currentRouteTarget(target.HashSlot)
	if !ok || targetKey(current) != key {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureTargetKeyLocked(key); err == nil {
		return nil
	}
	a.setTargetStateLocked(key, conversationAuthorityActive)
	return nil
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
	for existing := range a.targets {
		if existing != target && a.sameLocalAuthorityTarget(existing, target) {
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
	return a.flushDrainingAuthority(ctx, target.HashSlot)
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
	return a.flushDrainingAuthority(ctx, target.HashSlot)
}

func (a *conversationAuthority) flushDrainingAuthority(ctx context.Context, hashSlot uint16) (conversationDrainResult, error) {
	selected := 0
	for {
		if err := ctx.Err(); err != nil {
			return conversationDrainResultBusy, err
		}
		flush, flushErr := a.active.FlushHashSlot(ctx, hashSlot, a.flushBatchRows)
		if flushErr != nil {
			return conversationDrainResultBusy, mapConversationActiveError(flushErr)
		}
		if flush.Selected == 0 {
			if selected == 0 {
				return conversationDrainResultNoDirty, nil
			}
			return conversationDrainResultDrained, nil
		}
		selected += flush.Selected
	}
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
			return nil, fmt.Errorf("internalv2/app: invalid conversation kind %d", patch.Kind)
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
