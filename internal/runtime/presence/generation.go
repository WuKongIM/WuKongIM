package presence

import (
	"strings"
	"sync"
	"unsafe"
)

const (
	generationScanning = iota + 1
	generationComplete
	generationClosed
	// These are fail-closed metadata bounds, not a heap/RSS limit.
	maxGenerationOwners   = 256
	maxGenerationPageRows = 4096
	maxSuspectRows        = 8192
	maxSuspectBytes       = 128 << 20
)

type generationOwner struct {
	cut                  OwnerCut
	sequence, last, rows uint64
	exhausted, ended     bool
}

// slotGeneration is protected by its Directory shard, including publication,
// activation receipts and TTL uncertainty. No network operation holds that lock.
type slotGeneration struct {
	claim GenerationClaim
	state int
	// published survives revocation so Abort cannot treat former Complete rows
	// as unpublished staging when both states have become closed.
	published   bool
	owners      map[uint64]*generationOwner
	suspects    map[identityKey]Route
	suspectUIDs map[string]int
}

// suspectLedger is acquired only while a shard is held, never in reverse order.
type suspectLedger struct {
	mu          sync.Mutex
	rows, bytes uint64
}

func suspectBytes(r Route) uint64 {
	return uint64(256 + len(r.UID) + len(r.DeviceID) + len(r.Listener))
}

// BeginGeneration installs a previously validated committed claim and exact
// complete owner vector. The cluster adapter, not this runtime, proves commit.
// A failed or removed generation may only be retried with a strictly larger G.
func (d *Directory) BeginGeneration(claim GenerationClaim, owners []OwnerCut) error {
	if !claim.Valid() || len(owners) == 0 || len(owners) > maxGenerationOwners {
		return ErrStaleGeneration
	}
	vector := make(map[uint64]*generationOwner, len(owners))
	for _, owner := range owners {
		if !owner.Valid() || vector[owner.NodeID] != nil {
			return ErrStaleGeneration
		}
		vector[owner.NodeID] = &generationOwner{cut: owner}
	}
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if _, err := d.validateTargetLocked(shard, claim.Target); err != nil {
		return err
	}
	old := shard.highWater[claim.Target.HashSlot]
	if old.Generation >= claim.Generation {
		return ErrStaleGeneration
	}
	if old.Generation != 0 && claim.PreviousGeneration < old.Generation {
		return ErrStaleGeneration
	}
	previous := shard.slots[claim.Target.HashSlot]
	slot := newAuthoritySlot(claim.Target)
	// Preserve observed exact close/sequence fences across the owner Begin to
	// Directory installation interval. No active rows or UID proofs are reused.
	if previous != nil {
		slot.ownerSeq = previous.ownerSeq
		slot.tombstoneSeq = previous.tombstoneSeq
	}
	slot.generation = &slotGeneration{claim: claim, state: generationScanning, owners: vector, suspects: make(map[identityKey]Route), suspectUIDs: make(map[string]int)}
	d.dropGenerationLocked(shard.slots[claim.Target.HashSlot])
	shard.highWater[claim.Target.HashSlot] = claim
	shard.slots[claim.Target.HashSlot] = slot
	return nil
}

func (d *Directory) generationLocked(shard *directoryShard, claim GenerationClaim) (*authoritySlot, *slotGeneration, error) {
	slot, err := d.validateTargetLocked(shard, claim.Target)
	if err != nil {
		return nil, nil, err
	}
	if !claim.Valid() || slot.generation == nil || !slot.generation.claim.Equal(claim) || slot.generation.state == generationClosed {
		return nil, nil, ErrStaleGeneration
	}
	return slot, slot.generation, nil
}

// CurrentGeneration is a read-only local gate, not a cluster validation result.
func (d *Directory) CurrentGeneration(target RouteTarget) (GenerationClaim, bool, error) {
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return GenerationClaim{}, false, err
	}
	if slot.generation == nil || slot.generation.state == generationClosed {
		return GenerationClaim{}, false, ErrSlotIncomplete
	}
	return slot.generation.claim, slot.generation.state == generationComplete, nil
}

// CompleteGenerationOwners copies the exact published owner vector under the
// same claim lock. Its caller reserves this bounded output before allocation.
func (d *Directory) CompleteGenerationOwners(claim GenerationClaim, reserve func(uint64, uint64) error) ([]OwnerCut, error) {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	_, g, err := d.generationLocked(shard, claim)
	if err != nil {
		return nil, err
	}
	if g.state != generationComplete {
		return nil, ErrSlotIncomplete
	}
	if reserve != nil {
		if err = reserve(0, uint64(len(g.owners))*uint64(unsafe.Sizeof(OwnerCut{}))); err != nil {
			return nil, err
		}
	}
	owners := make([]OwnerCut, 0, len(g.owners))
	for _, owner := range g.owners {
		owners = append(owners, owner.cut)
	}
	return owners, nil
}

// InstallSlotPage verifies exact owner, sequence, cursor and cumulative count
// before any row changes. Partial pages never make the generation Complete.
// The caller retains staging credits until Complete or Abort has returned.
func (d *Directory) InstallSlotPage(page SlotPage) error {
	if len(page.Routes) > maxGenerationPageRows {
		return ErrRouteNotReady
	}
	shard := d.shard(page.Claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, g, err := d.generationLocked(shard, page.Claim)
	if err != nil {
		return err
	}
	owner := g.owners[page.Owner.NodeID]
	if g.state != generationScanning || owner == nil || owner.cut != page.Owner || owner.ended || owner.exhausted || page.Sequence != owner.sequence+1 || page.AfterSessionID != owner.last || page.RowsTotal < owner.rows || page.RowsTotal-owner.rows != uint64(len(page.Routes)) {
		return ErrSlotIncomplete
	}
	last := owner.last
	for _, route := range page.Routes {
		if route.UID == "" || route.OwnerNodeID != page.Owner.NodeID || route.OwnerBootID != page.Owner.BootID || route.OwnerSeq == 0 || route.SessionID <= last {
			return ErrStaleRoute
		}
		last = route.SessionID
	}
	if page.NextSessionID != last || (!page.Exhausted && last == owner.last) {
		return ErrSlotIncomplete
	}
	for _, route := range page.Routes {
		// Published Directory ownership does not alias a releasable page's strings.
		route.UID = strings.Clone(route.UID)
		route.DeviceID = strings.Clone(route.DeviceID)
		route.Listener = strings.Clone(route.Listener)
		slot.touchLocked(route)
	}
	owner.sequence, owner.last, owner.rows, owner.exhausted = page.Sequence, last, page.RowsTotal, page.Exhausted
	return nil
}

// EndGenerationOwner accepts only the terminal echo of the installed page.
func (d *Directory) EndGenerationOwner(end SlotEnd) error {
	shard := d.shard(end.Claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, g, err := d.generationLocked(shard, end.Claim)
	if err != nil {
		return err
	}
	owner := g.owners[end.Owner.NodeID]
	if g.state != generationScanning || owner == nil || owner.cut != end.Owner || !owner.exhausted || end.Sequence != owner.sequence || end.LastSessionID != owner.last || end.RowsTotal != owner.rows {
		return ErrSlotIncomplete
	}
	owner.ended = true
	return nil
}

// CompleteGeneration is the single publication point. The caller revalidates
// current applied claim and membership immediately before invoking it.
func (d *Directory) CompleteGeneration(claim GenerationClaim) error {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, g, err := d.generationLocked(shard, claim)
	if err != nil {
		return err
	}
	for _, owner := range g.owners {
		if !owner.ended {
			return ErrSlotIncomplete
		}
	}
	g.state = generationComplete
	g.published = true
	return nil
}

// AbortGeneration revokes only this unfinished generation. A late cancellation
// cannot erase an already published certificate or a successor generation.
func (d *Directory) AbortGeneration(claim GenerationClaim) error {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	// Failed scans may already be closed by TTL uncertainty overflow. Validate
	// the exact claim without admitting any other closed-generation operation.
	slot, err := d.validateTargetLocked(shard, claim.Target)
	if err != nil {
		return err
	}
	g := slot.generation
	if !claim.Valid() || g == nil || !g.claim.Equal(claim) {
		return ErrStaleGeneration
	}
	if g.published {
		return ErrSlotIncomplete
	}
	g.state = generationClosed
	d.dropGenerationLocked(slot)
	// Drop partial row/index backing while retaining the closed gate and high water.
	replacement := newAuthoritySlot(slot.target)
	replacement.generation = g
	shard.slots[claim.Target.HashSlot] = replacement
	return nil
}

// EndpointsByGeneration never accepts a same-target, different-generation proof.
func (d *Directory) EndpointsByGeneration(claim GenerationClaim, uids []string) ([]Route, error) {
	return d.EndpointsByGenerationReserved(claim, uids, nil)
}

// CountGenerationRoutes checks one completed provider group's result size under
// the same proof lock, without allocating a route snapshot or charging a final
// caller-owned lookup to decoded provider credits. The usecase retains its
// original-call cumulative bound across independently yielded groups.
func (d *Directory) CountGenerationRoutes(claim GenerationClaim, uids []string) (int, error) {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	slot, _, err := d.generationLocked(shard, claim)
	if err != nil {
		return 0, err
	}
	if len(uids) > RecoveryBatchSize || !slot.recoveryReady(uids) {
		return 0, ErrRouteNotReady
	}
	count := 0
	for _, uid := range uids {
		count += len(slot.byUID[uid])
		if count > 4096 {
			return 0, ErrRouteNotReady
		}
	}
	return count, nil
}

// EndpointsByGenerationReserved reserves exact result backing and borrowed
// immutable strings before allocating under the same generation read lock.
// reserve must be nonblocking and must never reenter this Directory.
func (d *Directory) EndpointsByGenerationReserved(claim GenerationClaim, uids []string, reserve func(uint64, uint64) error) ([]Route, error) {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	slot, _, err := d.generationLocked(shard, claim)
	if err != nil {
		return nil, err
	}
	if !slot.recoveryReady(uids) {
		return nil, ErrRouteNotReady
	}
	if len(uids) > RecoveryBatchSize {
		return nil, ErrRouteNotReady
	}
	count := 0
	bytes := uint64(0)
	for _, uid := range uids {
		count += len(slot.byUID[uid])
		if count > 4096 {
			return nil, ErrRouteNotReady
		}
		for key := range slot.byUID[uid] {
			route := slot.active[key]
			bytes += uint64(len(route.UID) + len(route.DeviceID) + len(route.Listener))
		}
	}
	if reserve != nil {
		if err := reserve(uint64(count), uint64(count)*uint64(unsafe.Sizeof(Route{}))+bytes); err != nil {
			return nil, err
		}
	}
	return slot.endpointsByUIDsLocked(uids), nil
}

func receiptFor(g *slotGeneration, route Route) (ActivationReceipt, error) {
	if g.state != generationComplete || route.UID == "" || route.SessionID == 0 || route.OwnerSeq == 0 || route.OwnerNodeID == 0 || route.OwnerBootID == 0 {
		return ActivationReceipt{}, ErrSlotIncomplete
	}
	owner := g.owners[route.OwnerNodeID]
	if owner == nil || owner.cut.BootID != route.OwnerBootID {
		return ActivationReceipt{}, ErrOwnerVectorChanged
	}
	return ActivationReceipt{Claim: g.claim, Owner: owner.cut, Identity: route.Identity(), OwnerSeq: route.OwnerSeq}, nil
}

// RegisterRouteWithReceipt binds an immediate success to the actual generation
// under the same lock as row installation; a pending conflict gets no receipt.
func (d *Directory) RegisterRouteWithReceipt(claim GenerationClaim, route Route) (RegisterResult, error) {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, g, err := d.generationLocked(shard, claim)
	if err != nil {
		return RegisterResult{}, err
	}
	receipt, err := receiptFor(g, route)
	if err != nil {
		if err == ErrOwnerVectorChanged {
			// The request proves mismatch, not a newer boot or a new owner set.
			// Revoke absence atomically; only a fresh authenticated full scan can
			// establish the replacement vector. No registration occurred here.
			g.state = generationClosed
			d.dropGenerationLocked(slot)
		}
		return RegisterResult{}, err
	}
	if g.suspectUIDs[route.UID] != 0 {
		return RegisterResult{}, ErrRouteNotReady
	}
	result, err := slot.registerLocked(route)
	if err != nil {
		return result, err
	}
	if result.PendingToken == "" {
		result.Receipt = &receipt
	} else {
		claimCopy := g.claim
		result.PendingClaim = &claimCopy
	}
	return result, nil
}

// AbortRouteWithClaim removes only the candidate in its original generation.
func (d *Directory) AbortRouteWithClaim(claim GenerationClaim, token PendingRouteToken) error {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, _, err := d.generationLocked(shard, claim)
	if err != nil {
		return err
	}
	if _, ok := slot.pending[token]; !ok {
		return ErrRouteNotReady
	}
	delete(slot.pending, token)
	return nil
}

// TouchRoutesWithClaim never lets a delayed old protocol page recreate rows in
// a later Full generation. Only the current vector's exact owner boots qualify.
func (d *Directory) TouchRoutesWithClaim(claim GenerationClaim, routes []Route) error {
	if len(routes) > 4096 {
		return ErrRouteNotReady
	}
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, g, err := d.generationLocked(shard, claim)
	if err != nil {
		return err
	}
	for _, route := range routes {
		owner := g.owners[route.OwnerNodeID]
		if owner == nil || owner.cut.BootID != route.OwnerBootID || route.SessionID == 0 || route.OwnerSeq == 0 {
			return ErrStaleRoute
		}
	}
	d.touchLocked(slot, routes)
	return nil
}

// UnregisterRouteWithClaim combines the current gate with an exact tombstone.
func (d *Directory) UnregisterRouteWithClaim(claim GenerationClaim, identity RouteIdentity, seq uint64) error {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, g, err := d.generationLocked(shard, claim)
	if err != nil {
		return err
	}
	owner := g.owners[identity.OwnerNodeID]
	if owner == nil || owner.cut.BootID != identity.OwnerBootID || identity.SessionID == 0 || seq == 0 {
		return ErrStaleRoute
	}
	return d.unregisterLocked(slot, identity, seq)
}

// CommitRouteWithReceipt signs only the final successful conflict candidate.
func (d *Directory) CommitRouteWithReceipt(claim GenerationClaim, token PendingRouteToken) (ActivationReceipt, error) {
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, g, err := d.generationLocked(shard, claim)
	if err != nil {
		return ActivationReceipt{}, err
	}
	pending, ok := slot.pending[token]
	if !ok {
		return ActivationReceipt{}, ErrRouteNotReady
	}
	receipt, err := receiptFor(g, pending.route)
	if err != nil {
		return ActivationReceipt{}, err
	}
	if g.suspectUIDs[pending.route.UID] != 0 {
		return ActivationReceipt{}, ErrRouteNotReady
	}
	if err = slot.commitRouteLocked(token); err != nil {
		return ActivationReceipt{}, err
	}
	return receipt, nil
}

func (d *Directory) removeSuspectLocked(g *slotGeneration, key identityKey) {
	route, ok := g.suspects[key]
	if !ok {
		return
	}
	delete(g.suspects, key)
	g.suspectUIDs[key.uid]--
	if g.suspectUIDs[key.uid] == 0 {
		delete(g.suspectUIDs, key.uid)
	}
	d.suspects.mu.Lock()
	d.suspects.rows--
	d.suspects.bytes -= suspectBytes(route)
	d.suspects.mu.Unlock()
}

func (d *Directory) dropGenerationLocked(slot *authoritySlot) {
	if slot == nil || slot.generation == nil {
		return
	}
	g := slot.generation
	for key := range g.suspects {
		d.removeSuspectLocked(g, key)
	}
}

func (d *Directory) expireGenerationLocked(slot *authoritySlot, route Route) {
	g := slot.generation
	if g == nil {
		// A legacy UID proof also becomes unknown when known authority activity
		// expires; it cannot justify a successful empty response.
		delete(slot.recovered, route.UID)
		slot.recoveryGeneration = &recoveryGeneration{}
		return
	}
	if g.state == generationClosed {
		return
	}
	key := makeRouteIdentityKey(route)
	if _, exists := g.suspects[key]; exists {
		return
	}
	bytes := suspectBytes(route)
	d.suspects.mu.Lock()
	if d.suspects.rows >= maxSuspectRows || bytes > maxSuspectBytes-d.suspects.bytes {
		d.suspects.mu.Unlock()
		g.state = generationClosed
		d.dropGenerationLocked(slot)
		return
	}
	d.suspects.rows++
	d.suspects.bytes += bytes
	d.suspects.mu.Unlock()
	g.suspects[key] = route
	g.suspectUIDs[route.UID]++
}

// ReconcileSuspect consumes one complete all-owner UID proof, never a partial
// owner reply. Equal old activity remains suspect; no synthetic renewal occurs.
func (d *Directory) ReconcileSuspect(claim GenerationClaim, uids []string, routes []Route) error {
	if len(uids) > RecoveryBatchSize || len(routes) > 4096 {
		return ErrRouteNotReady
	}
	allowed := make(map[string]bool, len(uids))
	for _, uid := range uids {
		allowed[uid] = true
	}
	shard := d.shard(claim.Target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, g, err := d.generationLocked(shard, claim)
	if err != nil {
		return err
	}
	if g.state != generationComplete {
		return ErrSlotIncomplete
	}
	seen := make(map[identityKey]Route, len(routes))
	for _, route := range routes {
		owner := g.owners[route.OwnerNodeID]
		if !allowed[route.UID] || owner == nil || owner.cut.BootID != route.OwnerBootID || route.SessionID == 0 || route.OwnerSeq == 0 {
			return ErrStaleRoute
		}
		seen[makeRouteIdentityKey(route)] = route
	}
	for key, old := range g.suspects {
		if !allowed[key.uid] {
			continue
		}
		current, exists := seen[key]
		if !exists {
			d.removeSuspectLocked(g, key)
			continue
		}
		if current.OwnerSeq >= old.OwnerSeq && current.LastSeenUnix > old.LastSeenUnix {
			d.removeSuspectLocked(g, key)
			slot.touchLocked(current)
		}
	}
	return nil
}
