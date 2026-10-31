package presence

import (
	"context"
	"errors"
	"sync"
	"time"
	"unsafe"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

type slotFlightKey struct {
	hash                uint16
	slot                uint32
	leader, term, epoch uint64
}

func generationKey(t RouteTarget) slotFlightKey {
	return slotFlightKey{t.HashSlot, t.SlotID, t.LeaderNodeID, t.LeaderTerm, t.ConfigEpoch}
}

// slotRecoveryFlight owns one physical scan, including late canceled I/O. UID
// callers only own references; one canceled caller cannot cancel other users.
type slotRecoveryFlight struct {
	target RouteTarget
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	users  int
	err    error
	// Cooperative scans stop between pages; only an outer executor grants a turn.
	cooperative bool
	busy        bool
	finished    bool
	permit      chan struct{}
	turn        chan slotTurnResult
}

type slotRecheck struct{ marker byte }

// reconcile retains one complete all-owner read through actual provider return.
// It reserves the original 4096-row call bound before retaining any owner rows;
// no subset of owners can discharge a suspect identity.
func (r *SlotRecovery) reconcile(ctx context.Context, claim GenerationClaim, uids []string) (err error) {
	physical, cancel := context.WithTimeout(ctx, 2*time.Second)
	read := &slotRecheck{}
	r.mu.Lock()
	if r.closed || len(r.rechecks) >= 16 {
		r.mu.Unlock()
		cancel()
		return authority.ErrRouteNotReady
	}
	r.rechecks[read] = cancel
	r.mu.Unlock()
	defer func() { cancel(); r.mu.Lock(); delete(r.rechecks, read); r.closeDrainedLocked(); r.mu.Unlock() }()
	lease, err := r.budget.TryReserve(4096, 4096*uint64(unsafe.Sizeof(Route{})))
	if err != nil {
		return err
	}
	defer lease.Release()
	cuts, err := r.directory.CompleteGenerationOwners(claim, lease.TryGrow)
	if err != nil {
		return err
	}
	rows := make([]Route, 0, 4096)
	defer func() { clear(rows); rows = nil }()
	allowed := make(map[string]bool, len(uids))
	for _, uid := range uids {
		allowed[uid] = true
	}
	for _, cut := range cuts {
		ownerCtx, ownerCancel := context.WithTimeout(physical, time.Second)
		owned, e := r.owners.ReadGenerationUIDs(ownerCtx, claim, cut, uids, r.budget)
		ownerCancel()
		if e != nil {
			if owned != nil {
				owned.Release()
			}
			return e
		}
		if owned == nil {
			return authority.ErrRouteNotReady
		}
		if len(rows)+len(owned.Routes) > 4096 {
			owned.Release()
			return authority.ErrRouteNotReady
		}
		bytes := uint64(0)
		for _, route := range owned.Routes {
			if !allowed[route.UID] || route.OwnerNodeID != cut.NodeID || route.OwnerBootID != cut.BootID || route.SessionID == 0 || route.OwnerSeq == 0 {
				owned.Release()
				return authority.ErrStaleRoute
			}
			bytes += ownedRouteStringBytes(route)
		}
		if err = lease.TryGrow(0, bytes); err != nil {
			owned.Release()
			return err
		}
		rows = append(rows, owned.Routes...)
		owned.Release()
		if err = physical.Err(); err != nil {
			return err
		}
	}
	if err = r.source.ValidateSlotGeneration(physical, claim); err != nil {
		return err
	}
	return r.directory.ReconcileSuspect(claim, uids, rows)
}

// SlotRecovery adds one shared full-slot task beneath the existing bounded
// AuthorityRecovery admission. Use it as both Directory and Owners there so
// legacy per-UID readback markers cannot masquerade as a generation proof.
// There are at most sixteen actual scans, including canceled I/O, and no second
// background work queue. Existing caller/input admission remains unchanged.
type SlotRecovery struct {
	directory *authority.Directory
	source    SlotGenerationSource
	owners    SlotGenerationOwners
	budget    *RecoveryResultBudget
	// control bounds actual Claim/Begin providers together at the original
	// sixteen-scan I/O ceiling. Waiting workers own no provider or owner timer.
	control chan struct{}
	// admission is bound once by NewAuthorityRecovery before exposure to callers.
	admission   *AuthorityRecovery
	mu          sync.Mutex
	flights     map[slotFlightKey]*slotRecoveryFlight
	rechecks    map[*slotRecheck]context.CancelFunc
	closed      bool
	drained     chan struct{}
	drainClosed bool
}

func NewSlotRecovery(directory *authority.Directory, source SlotGenerationSource, owners SlotGenerationOwners) *SlotRecovery {
	return &SlotRecovery{directory: directory, source: source, owners: owners, budget: NewRecoveryResultBudget(RecoveryResultMaxRows, RecoveryResultMaxBytes), control: make(chan struct{}, 16), flights: make(map[slotFlightKey]*slotRecoveryFlight), drained: make(chan struct{}), rechecks: make(map[*slotRecheck]context.CancelFunc)}
}

// RecoveryResultBudget is shared by task staging, decoded pages and original
// caller results; it must not be replaced by another per-task allowance.
func (r *SlotRecovery) RecoveryResultBudget() *RecoveryResultBudget { return r.budget }
func (r *SlotRecovery) SupportsBoundedRecoveryResults() bool        { return true }

// RegisterRoute keeps CONNECT in the original admission/credit contract and
// returns only the receipt produced by the actual final authority mutation.
func (r *SlotRecovery) RegisterRoute(ctx context.Context, target RouteTarget, route Route) (RegisterResult, error) {
	if r.admission == nil {
		return RegisterResult{}, authority.ErrRouteNotReady
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.admission.Endpoints(ctx, target, []string{route.UID}); err != nil {
			return RegisterResult{}, err
		}
		claim, err := r.current(target)
		if err != nil {
			return RegisterResult{}, err
		}
		if err = r.source.ValidateSlotGeneration(ctx, claim); err != nil {
			return RegisterResult{}, err
		}
		if attempt != 0 {
			// A delayed old boot may trigger one conservative rebuild, but cannot
			// revoke its authenticated replacement or loop indefinitely. Compare
			// the same complete claim's actual cuts, never an ordering of BootIDs.
			if err = r.matchRegistrationOwner(claim, route); err != nil {
				return RegisterResult{}, err
			}
		}
		result, err := r.directory.RegisterRouteWithReceipt(claim, route)
		if attempt == 0 && errors.Is(err, authority.ErrOwnerVectorChanged) {
			// This typed rejection occurs before any registration mutation. Keep
			// the original caller deadline and share the ordinary admission path.
			continue
		}
		if err != nil {
			return result, err
		}
		if err = r.source.ValidateSlotGeneration(ctx, claim); err != nil {
			return RegisterResult{}, err
		}
		return result, nil
	}
	return RegisterResult{}, authority.ErrOwnerVectorChanged
}

func (r *SlotRecovery) matchRegistrationOwner(claim GenerationClaim, route Route) error {
	lease, err := r.budget.TryReserve(0, 0)
	if err != nil {
		return err
	}
	defer lease.Release()
	cuts, err := r.directory.CompleteGenerationOwners(claim, lease.TryGrow)
	if err != nil {
		return err
	}
	defer func() { clear(cuts); cuts = nil }()
	for _, cut := range cuts {
		if cut.NodeID == route.OwnerNodeID && cut.BootID == route.OwnerBootID {
			return nil
		}
	}
	return authority.ErrOwnerVectorChanged
}

func (r *SlotRecovery) CommitRouteWithReceipt(ctx context.Context, claim GenerationClaim, token PendingRouteToken) (ActivationReceipt, error) {
	if err := r.source.ValidateSlotGeneration(ctx, claim); err != nil {
		return ActivationReceipt{}, err
	}
	receipt, err := r.directory.CommitRouteWithReceipt(claim, token)
	if err != nil {
		return receipt, err
	}
	if err = r.source.ValidateSlotGeneration(ctx, claim); err != nil {
		return ActivationReceipt{}, err
	}
	return receipt, nil
}

func (r *SlotRecovery) AbortRouteWithClaim(ctx context.Context, claim GenerationClaim, token PendingRouteToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.directory.AbortRouteWithClaim(claim, token)
}

func (r *SlotRecovery) TouchRoutes(ctx context.Context, claim GenerationClaim, routes []Route) error {
	if err := r.source.ValidateSlotGeneration(ctx, claim); err != nil {
		return err
	}
	if err := r.directory.TouchRoutesWithClaim(claim, routes); err != nil {
		return err
	}
	return r.source.ValidateSlotGeneration(ctx, claim)
}

func (r *SlotRecovery) UnregisterRoute(ctx context.Context, claim GenerationClaim, identity RouteIdentity, seq uint64) error {
	if err := r.source.ValidateSlotGeneration(ctx, claim); err != nil {
		return err
	}
	if err := r.directory.UnregisterRouteWithClaim(claim, identity, seq); err != nil {
		return err
	}
	return r.source.ValidateSlotGeneration(ctx, claim)
}

func (r *SlotRecovery) current(target RouteTarget) (GenerationClaim, error) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return GenerationClaim{}, authority.ErrRouteNotReady
	}
	claim, complete, err := r.directory.CurrentGeneration(target)
	if err != nil {
		if errors.Is(err, authority.ErrSlotIncomplete) {
			return GenerationClaim{}, authority.ErrRouteNotReady
		}
		return GenerationClaim{}, err
	}
	if !complete {
		return GenerationClaim{}, authority.ErrRouteNotReady
	}
	if err = r.source.ValidateSlotGeneration(context.Background(), claim); err != nil {
		return GenerationClaim{}, authority.ErrRouteNotReady
	}
	return claim, nil
}

func (r *SlotRecovery) EndpointsByUIDs(target RouteTarget, uids []string) ([]Route, error) {
	claim, err := r.current(target)
	if err != nil {
		return nil, err
	}
	return r.directory.EndpointsByGeneration(claim, uids)
}
func (r *SlotRecovery) RecoveryUIDs(target RouteTarget, uids []string) ([]string, error) {
	if len(uids) > authority.RecoveryBatchSize {
		return nil, authority.ErrRouteNotReady
	}
	if _, err := r.current(target); err != nil {
		if !errors.Is(err, authority.ErrRouteNotReady) {
			return nil, err
		}
		return append([]string(nil), uids...), nil
	}
	return r.directory.RecoveryUIDs(target, uids)
}
func (r *SlotRecovery) InstallRecoveredRoutes(target RouteTarget, uids []string, _ []Route) error {
	if _, err := r.current(target); err != nil {
		return err
	}
	return r.directory.InstallRecoveredRoutes(target, uids, nil)
}

func (r *SlotRecovery) ensure(ctx context.Context, target RouteTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return authority.ErrRouteNotReady
	}
	if _, err := r.current(target); err == nil {
		return nil
	}
	key := generationKey(target)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return authority.ErrRouteNotReady
	}
	f := r.flights[key]
	if f == nil {
		if len(r.flights)+len(r.rechecks) >= 16 {
			r.mu.Unlock()
			return authority.ErrRouteNotReady
		}
		physical, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		f = &slotRecoveryFlight{target: target, ctx: physical, cancel: cancel, done: make(chan struct{})}
		r.flights[key] = f
		goroutine.SafeGo(nil, goroutine.TaskPresenceRecoveryScheduler, func() { r.run(key, f) })
	}
	f.users++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		f.users--
		if f.users == 0 {
			f.cancel()
		}
		r.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return f.err
	}
}

func (r *SlotRecovery) run(key slotFlightKey, f *slotRecoveryFlight) {
	if f.cooperative {
		<-f.permit
	}
	err := r.scan(f)
	r.mu.Lock()
	f.err = err
	f.finished = true
	delete(r.flights, key)
	close(f.done)
	f.cancel()
	r.closeDrainedLocked()
	r.mu.Unlock()
	if f.cooperative {
		f.turn <- slotTurnResult{err: err}
	}
}

// beginSlotOwners overlaps independent freshness checks with at most four
// actual providers per physical scan (sixteen scans already bound admission).
// Results retain owner-vector order. Every provider joins before return, even
// after cancellation; valid late cuts remain exact cleanup obligations.
func (r *SlotRecovery) beginSlotOwners(ctx context.Context, claim GenerationClaim, ids []uint64) ([]OwnerCut, []context.Context, []context.CancelFunc, error) {
	seen := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			return nil, nil, nil, authority.ErrStaleGeneration
		}
		seen[id] = true
	}
	cuts := make([]OwnerCut, len(ids))
	contexts := make([]context.Context, len(ids))
	cancels := make([]context.CancelFunc, len(ids))
	var mu sync.Mutex
	var workers sync.WaitGroup
	next := 0
	var firstErr error
	failed := make(chan struct{})
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
			close(failed)
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
		}
	}
	width := min(4, len(ids))
	workers.Add(width)
	for range width {
		goroutine.SafeGo(nil, goroutine.TaskPresenceRecoveryScheduler, func() {
			defer workers.Done()
			for {
				mu.Lock()
				if err := ctx.Err(); err != nil {
					fail(err)
				}
				if firstErr != nil || next == len(ids) {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()
				select {
				case r.control <- struct{}{}:
				case <-failed:
					return
				case <-ctx.Done():
					mu.Lock()
					fail(ctx.Err())
					mu.Unlock()
					return
				}
				mu.Lock()
				if err := ctx.Err(); err != nil {
					fail(err)
				}
				if firstErr != nil {
					<-r.control
					mu.Unlock()
					return
				}
				// The same fixed budget spans Begin, every page and End. It is
				// never refreshed after waiting for another owner or FIFO turn.
				ownerCtx, cancel := context.WithTimeout(ctx, time.Second)
				contexts[i], cancels[i] = ownerCtx, cancel
				mu.Unlock()
				cut, err := r.owners.BeginSlot(ownerCtx, claim, ids[i])
				if err == nil && (!cut.Valid() || cut.NodeID != ids[i]) {
					err = authority.ErrStaleGeneration
				}
				mu.Lock()
				if err == nil {
					cuts[i] = cut
				} else {
					fail(err)
				}
				<-r.control
				mu.Unlock()
			}
		})
	}
	workers.Wait()
	if firstErr != nil {
		// Failed scans need no aligned page contexts, but cleanup must include
		// every successful provider, including those returning after failure.
		valid := cuts[:0]
		for _, cut := range cuts {
			if cut.Valid() {
				valid = append(valid, cut)
			}
		}
		cuts = valid
	}
	return cuts, contexts, cancels, firstErr
}

func (r *SlotRecovery) scan(f *slotRecoveryFlight) (err error) {
	if err = f.ctx.Err(); err != nil {
		return err
	}
	if _, err = r.current(f.target); err == nil {
		return nil
	}
	// Fixed maximum owner/cursor metadata is prepaid before obtaining the owner
	// vector. Staging rows/strings grow atomically and remain charged until publish.
	staging, err := r.budget.TryReserve(0, 256*256)
	if err != nil {
		return err
	}
	defer staging.Release()
	select {
	case r.control <- struct{}{}:
	case <-f.ctx.Done():
		return f.ctx.Err()
	}
	if err = f.ctx.Err(); err != nil {
		<-r.control
		return err
	}
	claim, ids, err := r.source.ClaimSlotGeneration(f.ctx, f.target)
	<-r.control
	if err != nil {
		return err
	}
	if !claim.Valid() || generationKey(claim.Target) != generationKey(f.target) || len(ids) == 0 || len(ids) > 256 {
		return authority.ErrStaleGeneration
	}
	var cuts []OwnerCut
	var ownerContexts []context.Context
	var ownerCancels []context.CancelFunc
	defer func() {
		for _, cancel := range ownerCancels {
			if cancel != nil {
				cancel()
			}
		}
	}()
	installed, complete := false, false
	defer func() {
		if complete {
			return
		}
		if installed {
			_ = r.directory.AbortGeneration(claim)
		}
		// Cleanup has one bounded total allowance, separate from workload deadline;
		// it never retries pages or restarts a failed generation.
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, cut := range cuts {
			_ = r.owners.AbortSlot(cleanup, claim, cut)
		}
	}()
	cuts, ownerContexts, ownerCancels, err = r.beginSlotOwners(f.ctx, claim, ids)
	if err != nil {
		return err
	}
	if err = r.source.ValidateSlotGeneration(f.ctx, claim); err != nil {
		return err
	}
	if err = r.directory.BeginGeneration(claim, cuts); err != nil {
		return err
	}
	installed = true
	requests := make([]SlotPageRequest, len(cuts))
	done := make([]bool, len(cuts))
	for i, cut := range cuts {
		requests[i] = SlotPageRequest{Claim: claim, Owner: cut, Sequence: 1, MaxRows: 4096, MaxBytes: 4 << 20}
	}
	remaining := len(cuts)
	turnPages, turnRows := 0, 0
	var turnBytes uint64
	for remaining > 0 {
		// One page per owner per round; no owner monopolizes a slot's progress.
		for i := range requests {
			if done[i] {
				continue
			}
			if err = f.ctx.Err(); err != nil {
				return err
			}
			owned, e := r.owners.ReadSlotPage(ownerContexts[i], requests[i], r.budget)
			if e != nil {
				if owned != nil {
					owned.Release()
				}
				return e
			}
			if owned == nil {
				return authority.ErrSlotIncomplete
			}
			page := owned.Page
			if !page.Claim.Equal(claim) || page.Owner != requests[i].Owner || page.Sequence != requests[i].Sequence || page.AfterSessionID != requests[i].AfterSessionID || len(page.Routes) > requests[i].MaxRows {
				owned.Release()
				return authority.ErrSlotIncomplete
			}
			// Capture validated work before releasing the owned page. Invalid row
			// counts fail before the bounded string-size walk, as they did before.
			pageRows, pageBytes := len(page.Routes), slotRouteBytes(page.Routes)
			if pageBytes > uint64(requests[i].MaxBytes) {
				owned.Release()
				return authority.ErrSlotIncomplete
			}
			if err = f.ctx.Err(); err == nil {
				err = staging.TryGrow(uint64(pageRows), pageBytes)
			}
			if err == nil {
				err = r.directory.InstallSlotPage(page)
			}
			exhausted, sequence, last, total := page.Exhausted, page.Sequence, page.NextSessionID, page.RowsTotal
			owned.Release()
			page = SlotPage{}
			if err != nil {
				return err
			}
			if exhausted {
				end, e := r.owners.EndSlot(ownerContexts[i], SlotEndRequest{Claim: claim, Owner: cuts[i], Sequence: sequence, LastSessionID: last, RowsTotal: total})
				if e != nil {
					return e
				}
				if err = r.directory.EndGenerationOwner(end); err != nil {
					return err
				}
				done[i] = true
				remaining--
			} else {
				requests[i].Sequence++
				requests[i].AfterSessionID = last
			}
			turnPages++
			turnRows += pageRows
			turnBytes += pageBytes
			if remaining > 0 && f.cooperative && (turnPages >= slotTurnMaxPages || turnRows >= slotTurnMaxRows || turnBytes >= slotTurnMaxBytes) {
				// Ownership and the original chain/owner deadlines survive a yield.
				// Cleanup also waits for an actual outer executor permit.
				f.turn <- slotTurnResult{pending: true}
				<-f.permit
				// Only work accounting restarts; original chain/owner deadlines and
				// all ownership credits continue aging through the FIFO wait.
				turnPages, turnRows, turnBytes = 0, 0, 0
			}
		}
	}
	if err = r.source.ValidateSlotGeneration(f.ctx, claim); err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed || f.users == 0 || f.ctx.Err() != nil {
		err = context.Canceled
	} else {
		err = r.directory.CompleteGeneration(claim)
		complete = err == nil
	}
	r.mu.Unlock()
	return err
}

func (r *SlotRecovery) recoverGroup(ctx context.Context, g EndpointLookupGroup, lease *RecoveryResultLease) ([]Route, error) {
	if err := r.ensure(ctx, g.Target); err != nil {
		return nil, err
	}
	claim, err := r.current(g.Target)
	if err != nil {
		return nil, err
	}
	if missing, e := r.directory.RecoveryUIDs(g.Target, g.UIDs); e != nil {
		return nil, e
	} else if len(missing) > 0 {
		if err = r.reconcile(ctx, claim, missing); err != nil {
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return r.directory.EndpointsByGenerationReserved(claim, g.UIDs, lease.TryGrow)
}

// RecoverRouteCallsBounded retains independent 4096-row original-call caps and
// all result allocations under the same ledger as shared slot recovery.
func (r *SlotRecovery) RecoverRouteCallsBounded(ctx context.Context, calls []RecoveryCall, budget *RecoveryResultBudget, _ func(RecoveryResultUsage)) (owned OwnedRecoveryResults, err error) {
	if budget != r.budget || !ValidOwnerRecoveryCalls(calls, 0) {
		return owned, authority.ErrRouteNotReady
	}
	lease, err := budget.TryReserve(0, uint64(len(calls))*uint64(unsafe.Sizeof([]EndpointLookupResult{})))
	if err != nil {
		return owned, err
	}
	owned.Lease = lease
	owned.Results = make([][]EndpointLookupResult, len(calls))
	for i, call := range calls {
		if err = lease.TryGrow(0, uint64(len(call.Groups))*uint64(unsafe.Sizeof(EndpointLookupResult{}))); err != nil {
			owned.Release()
			return OwnedRecoveryResults{}, err
		}
		owned.Results[i] = make([]EndpointLookupResult, len(call.Groups))
		rows := 0
		for j, g := range call.Groups {
			routes, e := r.recoverGroup(ctx, g, lease)
			rows += len(routes)
			if rows > 4096 {
				routes = nil
				e = authority.ErrRouteNotReady
			}
			owned.Results[i][j] = EndpointLookupResult{Routes: routes, Err: e}
		}
	}
	return owned, nil
}

// RecoverRoutes is the small compatibility interface required by the outer
// scheduler. Production selects the bounded capability above at construction.
func (r *SlotRecovery) RecoverRoutes(ctx context.Context, target RouteTarget, uids []string) ([]Route, error) {
	if r.admission != nil {
		return r.admission.Endpoints(ctx, target, uids)
	}
	owned, err := r.RecoverRouteCallsBounded(ctx, []RecoveryCall{{Groups: []EndpointLookupGroup{{Target: target, UIDs: uids}}}}, r.budget, nil)
	if err != nil {
		return nil, err
	}
	defer owned.Release()
	result := owned.Results[0][0]
	return append([]Route(nil), result.Routes...), result.Err
}

func (r *SlotRecovery) closeDrainedLocked() {
	if r.closed && len(r.flights) == 0 && len(r.rechecks) == 0 && !r.drainClosed {
		r.drainClosed = true
		close(r.drained)
	}
}

// Close stops admission and joins actual providers; cancellation alone is not
// join. A timed-out Close may be called again without losing the cleanup owner.
func (r *SlotRecovery) Close(ctx context.Context) error {
	r.stop()
	r.mu.Lock()
	drained := r.drained
	r.mu.Unlock()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *SlotRecovery) stop() {
	r.mu.Lock()
	r.closed = true
	for _, f := range r.flights {
		f.cancel()
	}
	for _, cancel := range r.rechecks {
		cancel()
	}
	r.closeDrainedLocked()
	r.mu.Unlock()
}
