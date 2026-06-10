package app

import (
	"context"
	"errors"
	"sync"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type conversationAuthorityStore interface {
	// ListUserConversationActivePage returns durable active rows in active-index order.
	ListUserConversationActivePage(context.Context, string, metadb.UserConversationActiveCursor, int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
	// GetUserConversationState returns the durable primary row used for active-view hydration.
	GetUserConversationState(context.Context, string, string, int64) (metadb.UserConversationState, bool, error)
	// TouchUserConversationActiveAtBatch atomically flushes activity hints.
	TouchUserConversationActiveAtBatch(context.Context, []metadb.UserConversationActivePatch) error
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
	// AdmissionBatchRows mirrors routed-client admission config and is retained for config compatibility.
	AdmissionBatchRows int
	// AdmissionConcurrency mirrors routed-client admission config and is retained for config compatibility.
	AdmissionConcurrency int
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

func (s conversationActiveStoreAdapter) ListUserConversationActivePage(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	store := s.store()
	if store == nil {
		return nil, after, false, conversationactive.ErrStoreRequired
	}
	return store.ListUserConversationActivePage(ctx, uid, after, limit)
}

func (s conversationActiveStoreAdapter) GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	store := s.store()
	if store == nil {
		return metadb.UserConversationState{}, false, conversationactive.ErrStoreRequired
	}
	return store.GetUserConversationState(ctx, uid, channelID, channelType)
}

func (s conversationActiveStoreAdapter) TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error {
	store := s.store()
	if store == nil {
		return conversationactive.ErrStoreRequired
	}
	return store.TouchUserConversationActiveAtBatch(ctx, patches)
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
	authority := &conversationAuthority{
		localNodeID: opts.LocalNodeID,
		store:       opts.Store,
		targets:     make(map[conversationAuthorityTargetKey]conversationAuthorityState),
		observer:    opts.Observer,
	}
	authority.active = conversationactive.NewManager(conversationactive.Options{
		Store:         conversationActiveStoreAdapter{authority: authority},
		MaxCachedRows: opts.MaxRows,
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
	a.mu.Lock()
	err = a.ensureTargetLocked(target)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	activePatches := conversationActivePatches(patches)
	if len(activePatches) == 0 {
		return nil
	}
	err = mapConversationActiveError(a.active.MarkActive(ctx, activePatches))
	if errors.Is(err, conversationusecase.ErrCachePressure) {
		a.observeCachePressure(conversationAuthorityPhaseAdmit, err)
	}
	return err
}

// AdmitActiveBatch validates the route target and delegates channelwrite active admission to the runtime cache.
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
	a.mu.Lock()
	err = a.ensureTargetLocked(target)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	err = mapConversationActiveError(a.active.AdmitActiveBatch(ctx, batch))
	if errors.Is(err, conversationusecase.ErrCachePressure) {
		a.observeCachePressure(conversationAuthorityPhaseAdmit, err)
	}
	return err
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
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := a.active.Flush(ctx, 0)
	return mapConversationActiveError(err)
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

	flush, flushErr := a.active.Flush(ctx, 0)
	if flushErr != nil {
		result = conversationDrainResultBusy
		err = mapConversationActiveError(flushErr)
		return result, err
	}
	if flush.Selected == 0 {
		return result, nil
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
	if ctx == nil {
		ctx = context.Background()
	}
	page, err := a.active.ListActiveView(ctx, uid, after, limit)
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

func conversationActivePatches(patches []conversationusecase.ActivePatch) []conversationactive.ActivePatch {
	activePatches := make([]conversationactive.ActivePatch, 0, len(patches))
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType <= 0 || patch.ChannelType > 255 || patch.ActiveAt <= 0 {
			continue
		}
		activePatches = append(activePatches, conversationactive.ActivePatch{
			UID:         patch.UID,
			ChannelID:   patch.ChannelID,
			ChannelType: uint8(patch.ChannelType),
			ActiveAtMS:  patch.ActiveAt,
			ReadSeq:     patch.ReadSeq,
		})
	}
	return activePatches
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
