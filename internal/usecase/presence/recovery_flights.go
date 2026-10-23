package presence

import (
	"context"
	"errors"
	"strings"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

const (
	recoveryMaxCohorts   = 16
	recoveryMaxKeys      = 8192
	recoveryMaxMembers   = 512
	recoveryPageUIDBytes = 32 << 20
	// This ledger covers owned UID clones and conservative scheduler metadata,
	// not Directory, transport response heap, allocator overhead or process RSS.
	recoveryMaxUIDBytes = 128 << 20
	// One result responsibility retains at most one original call's 4096 routes.
	recoveryMaxResultCalls = 16
)

// recoveryUIDKey includes the distributed authority fence, excluding node-local
// revisions so matching proofs coalesce while every original target is rechecked.
type recoveryUIDKey struct {
	hashSlot                              uint16
	slotID                                uint32
	leaderNodeID, leaderTerm, configEpoch uint64
	uid                                   string
}

func recoveryKey(t RouteTarget, uid string) recoveryUIDKey {
	return recoveryUIDKey{t.HashSlot, t.SlotID, t.LeaderNodeID, t.LeaderTerm, t.ConfigEpoch, uid}
}

// Flights share status only. Every successful caller rereads fenced Directory.
type recoveryUIDFlight struct {
	done chan struct{}
	err  error
	key  recoveryUIDKey
	work *recoveryWork
	// users is mu-protected live proof ownership; its historical peak is shrunk
	// as members leave and all references are dropped before key bytes return.
	users    map[*recoveryMember]struct{}
	userPeak int
}

// recoveryMember owns one accepted caller's index budget. Its context controls
// only that caller; flights keep explicit live references for shared execution.
type recoveryMember struct {
	ctx      context.Context
	stop     chan struct{}
	flights  []*recoveryUIDFlight
	work     *recoveryWork
	bytes    int
	returned bool
}

// recoveryWork owns the newly missing subset of one original call. Workers
// publish it before callers join old flights, so overlapping calls cannot cycle.
type recoveryWork struct {
	creator *recoveryMember
	groups  []EndpointLookupGroup
	flights []*recoveryUIDFlight
	bytes   int
	// started closes at dispatch or queued termination, not provider return.
	started chan struct{}
	// queuedWait records FIFO/capacity waiting, excluding an idle dispatch turn.
	queuedWait bool
	// execution is mu-protected real ownership, independent of creator lifetime.
	execution *recoveryExecution
	// finished transfers minimal status to caller references after active release.
	finished bool
}

// recoveryExecution owns a whole batch's result credits until the provider,
// installation and response references finish. Only all-member cancellation or
// Close cancels its root; no member supplies a shared execution deadline.
type recoveryExecution struct {
	ctx    context.Context
	cancel context.CancelFunc
	works  []*recoveryWork
	// usage is a mu-protected observation, never an admission reservation.
	usage RecoveryResultUsage
}

// Close rejects new recovery, cancels accepted work, and joins actual provider
// completion. A timed-out Close retains ownership and can be joined again.
func (r *AuthorityRecovery) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		for m := range r.members {
			close(m.stop)
		}
		for len(r.queue) > 0 {
			r.finishWorkLocked(r.queue[0], nil)
		}
		r.queue = nil
		for e := range r.active {
			e.cancel()
		}
		r.maybeDrainedLocked()
	}
	done := r.drained
	r.mu.Unlock()
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *AuthorityRecovery) recoverPage(ctx context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
	results := make([]EndpointLookupResult, len(groups))
	if r == nil || r.directory == nil || r.owners == nil {
		for i := range results {
			results[i].Err = authority.ErrRouteNotReady
		}
		return results
	}
	results, m, waits := r.admitRecoveryPage(ctx, groups)
	if m == nil {
		return results
	}
	defer r.returnMember(m)
	if m.work != nil {
		var span rpctrace.Span
		observing := false
		select {
		case <-m.work.started:
		default:
			if m.work.queuedWait {
				span = rpctrace.Begin(ctx, rpctrace.StageRecoveryAdmission)
				observing = true
			}
		}
		var err error
		select {
		case <-m.work.started:
		case <-ctx.Done():
			err = ctx.Err()
		case <-m.stop:
			err = authority.ErrRouteNotReady
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if observing {
			span.End(rpctrace.ResultFor(err))
		}
		if err != nil {
			for i := range results {
				if errors.Is(results[i].Err, authority.ErrRouteNotReady) {
					results[i] = EndpointLookupResult{Err: err}
				}
			}
			return results
		}
	}
	for i, g := range groups {
		if !errors.Is(results[i].Err, authority.ErrRouteNotReady) {
			continue
		}
		var err error
		var span rpctrace.Span
		observing := false
		for _, f := range waits[i] {
			if f.work != m.work && !observing {
				select {
				case <-f.done:
				default:
					span = rpctrace.Begin(ctx, rpctrace.StageRecoveryFlight)
					observing = true
				}
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-m.stop:
				err = authority.ErrRouteNotReady
			case <-f.done:
				err = f.err
			}
			if err != nil {
				break
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if observing {
			span.End(rpctrace.ResultFor(err))
		}
		if err != nil {
			results[i] = EndpointLookupResult{Err: err}
			continue
		}
		results[i].Routes, results[i].Err = r.directory.EndpointsByUIDs(g.Target, g.UIDs)
	}
	return results
}

// Admission owns a bounded caller and its indices even when it only follows an
// existing flight. All dimensions are checked before cloning or publishing.
func (r *AuthorityRecovery) admitRecoveryPage(ctx context.Context, groups []EndpointLookupGroup) ([]EndpointLookupResult, *recoveryMember, [][]*recoveryUIDFlight) {
	results := make([]EndpointLookupResult, len(groups))
	waits := make([][]*recoveryUIDFlight, len(groups))
	missing := make([]EndpointLookupGroup, 0, len(groups))
	indexes := make([]int, 0, len(groups))
	refs := 0
	for i, g := range groups {
		if err := ctx.Err(); err != nil {
			results[i].Err = err
			continue
		}
		results[i].Routes, results[i].Err = r.directory.EndpointsByUIDs(g.Target, g.UIDs)
		if !errors.Is(results[i].Err, authority.ErrRouteNotReady) {
			continue
		}
		uids, err := r.directory.RecoveryUIDs(g.Target, g.UIDs)
		if err != nil {
			results[i].Err = err
			continue
		}
		if len(uids) > 0 {
			missing = append(missing, EndpointLookupGroup{Target: g.Target, UIDs: uids})
			indexes = append(indexes, i)
			refs += len(uids)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		for i := range results {
			results[i] = EndpointLookupResult{Err: authority.ErrRouteNotReady}
		}
		return results, nil, waits
	}
	if len(missing) == 0 {
		return results, nil, waits
	}
	newKeys := make(map[recoveryUIDKey]recoveryUIDKey)
	cloneBytes := 0
	for _, g := range missing {
		for _, uid := range g.UIDs {
			k := recoveryKey(g.Target, uid)
			if r.inFlight[k] != nil {
				continue
			}
			if _, ok := newKeys[k]; ok {
				continue
			}
			if len(uid) > recoveryPageUIDBytes-cloneBytes {
				return results, nil, waits
			}
			cloneBytes += len(uid)
			newKeys[k] = k
		}
	}
	// A reference can retain a distinct retired flight/done channel AND its
	// finished work/started channel from another creator. Charge all four plus
	// caller indices conservatively; none depend on that creator remaining live.
	// This logical ledger excludes allocator overhead and process RSS.
	memberBytes := 512 + len(groups)*128 + refs*512
	workBytes := 0
	if len(newKeys) > 0 {
		workBytes = 256 + len(missing)*128 + len(newKeys)*256 + cloneBytes
	}
	if len(r.members) >= recoveryMaxMembers || len(newKeys) > recoveryMaxKeys-len(r.inFlight) || memberBytes+workBytes > recoveryMaxUIDBytes-r.ownedUIDBytes {
		return results, nil, waits
	}
	m := &recoveryMember{ctx: ctx, stop: make(chan struct{}), bytes: memberBytes}
	r.members[m] = struct{}{}
	r.ownedUIDBytes += memberBytes
	if len(newKeys) > 0 {
		w := &recoveryWork{creator: m, bytes: workBytes, started: make(chan struct{})}
		m.work = w
		r.ownedUIDBytes += workBytes
		for k := range newKeys {
			owned := k
			owned.uid = strings.Clone(k.uid)
			newKeys[k] = owned
			f := &recoveryUIDFlight{done: make(chan struct{}), key: owned, work: w, users: make(map[*recoveryMember]struct{})}
			r.inFlight[owned] = f
			w.flights = append(w.flights, f)
		}
	}
	unique := make(map[*recoveryUIDFlight]struct{})
	for j, g := range missing {
		own := EndpointLookupGroup{Target: g.Target}
		for _, uid := range g.UIDs {
			k := recoveryKey(g.Target, uid)
			f := r.inFlight[k]
			waits[indexes[j]] = append(waits[indexes[j]], f)
			if _, ok := unique[f]; !ok {
				unique[f] = struct{}{}
				m.flights = append(m.flights, f)
				if f.users == nil {
					// The last member may have left while its canceled provider
					// still owns the flight. A late subscriber joins only status;
					// it cannot resurrect that execution or create a retry.
					f.users = make(map[*recoveryMember]struct{})
				}
				f.users[m] = struct{}{}
				if len(f.users) > f.userPeak {
					f.userPeak = len(f.users)
				}
			}
			if owned, ok := newKeys[k]; ok {
				own.UIDs = append(own.UIDs, owned.uid)
				delete(newKeys, k)
			}
		}
		if len(own.UIDs) > 0 {
			m.work.groups = append(m.work.groups, own)
		}
	}
	if m.work != nil {
		m.work.queuedWait = len(r.queue) > 0 || r.resultCalls == recoveryMaxResultCalls
		r.queue = append(r.queue, m.work)
	}
	r.kickLocked()
	return results, m, waits
}

// returnMember detaches one caller once, then checks each affected execution
// once. Cancellation cannot amplify shared-UID scans or release active credits.
func (r *AuthorityRecovery) returnMember(m *recoveryMember) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m.returned = true
	works := make(map[*recoveryWork]struct{})
	for _, f := range m.flights {
		delete(f.users, m)
		// Historical follower peaks must not retain an uncharged large map after
		// their caller metadata leaves the ledger. Keep capacity within 2x live use.
		if len(f.users) == 0 {
			f.users = nil
			f.userPeak = 0
		} else if len(f.users)*2 < f.userPeak {
			users := make(map[*recoveryMember]struct{}, len(f.users))
			for user := range f.users {
				users[user] = struct{}{}
			}
			f.users = users
			f.userPeak = len(users)
		}
		works[f.work] = struct{}{}
	}
	states := make(map[*recoveryMember]bool)
	executions := make(map[*recoveryExecution]struct{})
	for w := range works {
		if w.finished {
			continue
		}
		if w.execution == nil {
			if !r.workHasUsersLocked(w, states) {
				r.finishWorkLocked(w, nil)
			}
		} else {
			executions[w.execution] = struct{}{}
		}
	}
	for e := range executions {
		live := false
		for _, w := range e.works {
			if r.workHasUsersLocked(w, states) {
				live = true
				break
			}
		}
		if !live {
			e.cancel()
		}
	}
	r.releaseMemberLocked(m)
	r.kickLocked()
	r.maybeDrainedLocked()
}
func (r *AuthorityRecovery) releaseMemberLocked(m *recoveryMember) {
	if m.returned && (m.work == nil || m.work.finished) {
		if _, ok := r.members[m]; ok {
			delete(r.members, m)
			r.ownedUIDBytes -= m.bytes
			m.ctx = nil
			m.flights = nil
			m.work = nil
		}
	}
}
func recoveryMemberLive(m *recoveryMember, states map[*recoveryMember]bool) bool {
	if live, ok := states[m]; ok {
		return live
	}
	live := !m.returned && m.ctx.Err() == nil
	states[m] = live
	return live
}
func (r *AuthorityRecovery) workHasUsersLocked(w *recoveryWork, states map[*recoveryMember]bool) bool {
	for _, f := range w.flights {
		for m := range f.users {
			if recoveryMemberLive(m, states) {
				return true
			}
		}
	}
	return false
}

// liveRecoveryUIDs snapshots only still-owned proof requests. Shared owner I/O
// may finish after one member cancels, but cannot publish that member's unused
// proof. A live UID follower retains the right to install its current fence.
func (r *AuthorityRecovery) liveRecoveryUIDs(w *recoveryWork, g EndpointLookupGroup) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	states := make(map[*recoveryMember]bool)
	uids := make([]string, 0, len(g.UIDs))
	for _, uid := range g.UIDs {
		f := r.inFlight[recoveryKey(g.Target, uid)]
		if f == nil || f.work != w {
			continue
		}
		for m := range f.users {
			if recoveryMemberLive(m, states) {
				uids = append(uids, uid)
				break
			}
		}
	}
	return uids
}

// kickLocked gives already-ready admissions and completions one dispatch
// owner. It adds no collection timer and exits after taking currently ready work.
func (r *AuthorityRecovery) kickLocked() {
	if r.closed || r.dispatchPending {
		return
	}
	r.dispatchPending = true
	goroutine.SafeGo(nil, goroutine.TaskPresenceRecoveryScheduler, func() {
		r.mu.Lock()
		r.dispatchPending = false
		r.dispatchLocked()
		r.maybeDrainedLocked()
		r.mu.Unlock()
	})
}

// FIFO dispatch never holds partial result credits. Available credits cover
// every original call in a batch before its provider starts.
func (r *AuthorityRecovery) dispatchLocked() {
	for !r.closed && len(r.queue) > 0 && len(r.active) < recoveryMaxCohorts && r.resultCalls < recoveryMaxResultCalls {
		works := make([]*recoveryWork, 0, 16)
		groups, uids := 0, 0
		for len(r.queue) > 0 && len(works) < 16 && len(works) < recoveryMaxResultCalls-r.resultCalls {
			w := r.queue[0]
			if w.finished {
				r.queue[0] = nil
				r.queue = r.queue[1:]
				continue
			}
			wc := 0
			for _, g := range w.groups {
				wc += len(g.UIDs)
			}
			if groups+len(w.groups) > 256 || uids+wc > authority.RecoveryBatchSize {
				break
			}
			r.queue[0] = nil
			r.queue = r.queue[1:]
			works = append(works, w)
			groups += len(w.groups)
			uids += wc
		}
		if len(works) == 0 {
			break
		}
		executionCtx, cancel := context.WithCancel(context.Background())
		// A one-call execution may retain that call's observation. A shared batch
		// deliberately has no invented first-caller trace parent or cancellation.
		if len(works) == 1 {
			executionCtx = rpctrace.Copy(executionCtx, works[0].creator.ctx)
		}
		e := &recoveryExecution{ctx: executionCtx, cancel: cancel, works: works}
		for _, w := range works {
			w.execution = e
			close(w.started)
		}
		r.active[e] = struct{}{}
		r.resultCalls += len(works)
		r.census.BatchesTotal++
		r.census.BatchCallsTotal += uint64(len(works))
		r.census.BatchUIDsTotal += uint64(uids)
		r.census.BatchCallsHighWater = max(r.census.BatchCallsHighWater, len(works))
		r.census.BatchUIDsHighWater = max(r.census.BatchUIDsHighWater, uids)
		goroutine.SafeGo(nil, goroutine.TaskPresenceRecoveryScheduler, func() { r.execute(e) })
	}
}

// execute owns complete original-call responses through fenced installation,
// then drops provider references before returning the batch result credits.
func (r *AuthorityRecovery) execute(e *recoveryExecution) {
	outcomes := make([]map[recoveryUIDKey]error, len(e.works))
	calls := make([]RecoveryCall, len(e.works))
	for i, w := range e.works {
		outcomes[i] = make(map[recoveryUIDKey]error, len(w.flights))
		for _, f := range w.flights {
			outcomes[i][f.key] = authority.ErrRouteNotReady
		}
		for _, g := range w.groups {
			uids, err := r.directory.RecoveryUIDs(g.Target, g.UIDs)
			for _, uid := range g.UIDs {
				outcomes[i][recoveryKey(g.Target, uid)] = err
			}
			if err == nil && len(uids) > 0 {
				calls[i].Groups = append(calls[i].Groups, EndpointLookupGroup{Target: g.Target, UIDs: uids})
			}
		}
	}
	// Empty proofs are not sent to ports whose contract requires nonempty calls.
	activeCalls := make([]RecoveryCall, 0, len(calls))
	indexes := make([]int, 0, len(calls))
	for i, c := range calls {
		if len(c.Groups) > 0 {
			activeCalls = append(activeCalls, c)
			indexes = append(indexes, i)
		}
	}
	var recovered [][]EndpointLookupResult
	if len(activeCalls) > 0 {
		if observed, ok := r.owners.(RecoveryCallBatchOwnersObserved); ok && r.resultCensus {
			recovered = observed.RecoverRouteCallsObserved(e.ctx, activeCalls, func(usage RecoveryResultUsage) { r.observeResultUsage(e, usage) })
		} else if batch, ok := r.owners.(RecoveryCallBatchOwners); ok {
			recovered = batch.RecoverRouteCalls(e.ctx, activeCalls)
		} else {
			recovered = make([][]EndpointLookupResult, len(activeCalls))
			for i, c := range activeCalls {
				if batch, ok := r.owners.(RecoveryBatchOwners); ok {
					recovered[i] = batch.RecoverRoutesByTargets(e.ctx, c.Groups)
				} else {
					recovered[i] = make([]EndpointLookupResult, len(c.Groups))
					for j, g := range c.Groups {
						recovered[i][j].Routes, recovered[i][j].Err = r.owners.RecoverRoutes(e.ctx, g.Target, g.UIDs)
					}
				}
			}
		}
	}
	if r.resultCensus {
		r.observeResultUsage(e, MeasureRecoveryResults(recovered))
	}
	for j, c := range activeCalls {
		i := indexes[j]
		routeCount := 0
		if len(recovered) == len(activeCalls) {
			for _, result := range recovered[j] {
				routeCount += len(result.Routes)
			}
		}
		for k, g := range c.Groups {
			err := e.ctx.Err()
			if err == nil && (len(recovered) != len(activeCalls) || len(recovered[j]) != len(c.Groups) || routeCount > 4096) {
				err = authority.ErrRouteNotReady
			}
			if err == nil {
				err = recovered[j][k].Err
			}
			if err == nil {
				// Validate the original provider boundary before removing any
				// canceled UID. Filtering must not hide a malformed owner row
				// that the Directory would have rejected before this scheduler.
				allowed := make(map[string]struct{}, len(g.UIDs))
				for _, uid := range g.UIDs {
					allowed[uid] = struct{}{}
				}
				for _, route := range recovered[j][k].Routes {
					if _, ok := allowed[route.UID]; !ok || route.OwnerNodeID == 0 || route.OwnerBootID == 0 || route.SessionID == 0 {
						err = authority.ErrStaleRoute
						break
					}
				}
			}
			live := r.liveRecoveryUIDs(e.works[i], g)
			wanted := make(map[string]struct{}, len(live))
			for _, uid := range live {
				wanted[uid] = struct{}{}
			}
			if err == nil && len(live) == 0 {
				err = authority.ErrRouteNotReady
			}
			if err == nil {
				routes := recovered[j][k].Routes
				if len(live) != len(g.UIDs) {
					filtered := make([]Route, 0, len(routes))
					for _, route := range routes {
						if _, ok := wanted[route.UID]; ok {
							filtered = append(filtered, route)
						}
					}
					routes = filtered
				}
				span := rpctrace.Begin(e.ctx, rpctrace.StageRecoveryInstall)
				err = r.directory.InstallRecoveredRoutes(g.Target, live, routes)
				span.End(rpctrace.ResultFor(err))
			}
			for _, uid := range g.UIDs {
				outcome := err
				if _, ok := wanted[uid]; !ok {
					outcome = authority.ErrRouteNotReady
				}
				outcomes[i][recoveryKey(g.Target, uid)] = outcome
			}
		}
	}
	// Release response references before returning result responsibility credits.
	recovered = nil
	activeCalls = nil
	calls = nil
	e.cancel()
	r.mu.Lock()
	if r.resultCensus {
		r.observeResultUsageLocked(e, RecoveryResultUsage{})
	}
	count := len(e.works)
	for i, w := range e.works {
		r.finishWorkLocked(w, outcomes[i])
		e.works[i] = nil
	}
	e.works = nil
	delete(r.active, e)
	r.resultCalls -= count
	r.kickLocked()
	r.maybeDrainedLocked()
	r.mu.Unlock()
}

// finishWorkLocked publishes status, removes all active ownership references,
// and transfers only the small retired flight/channel to charged caller indices.
func (r *AuthorityRecovery) finishWorkLocked(w *recoveryWork, outcomes map[recoveryUIDKey]error) {
	if w.finished {
		return
	}
	w.finished = true
	if w.execution == nil {
		// Canceled queued work must leave the physical queue before its ledger
		// credits return, even while every execution/result credit is occupied.
		for i, queued := range r.queue {
			if queued == w {
				copy(r.queue[i:], r.queue[i+1:])
				r.queue[len(r.queue)-1] = nil
				r.queue = r.queue[:len(r.queue)-1]
				break
			}
		}
		close(w.started)
	}
	for _, f := range w.flights {
		key := f.key
		err := authority.ErrRouteNotReady
		if outcomes != nil {
			err = outcomes[key]
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			err = authority.ErrRouteNotReady
		}
		f.err = err
		delete(r.inFlight, key)
		delete(outcomes, key)
		f.key = recoveryUIDKey{}
		f.users = nil
		f.userPeak = 0
		close(f.done)
	}
	w.groups = nil
	w.flights = nil
	creator := w.creator
	w.creator = nil
	w.execution = nil
	r.ownedUIDBytes -= w.bytes
	r.releaseMemberLocked(creator)
}
func (r *AuthorityRecovery) maybeDrainedLocked() {
	if r.closed && !r.drainClosed && len(r.active) == 0 && len(r.members) == 0 && !r.dispatchPending {
		close(r.drained)
		r.drainClosed = true
	}
}
