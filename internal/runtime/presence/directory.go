package presence

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultShardCount = 32
	deviceLevelSlave  = 0
	deviceLevelMaster = 1
)

// Directory stores authoritative presence routes for locally led hash slots.
type Directory struct {
	// localNodeID optionally verifies that incoming RouteTarget values point here.
	localNodeID uint64
	// shards spreads hash-slot authority state across independent locks.
	shards []directoryShard
	// touchRoutesTotal counts route touch entries accepted by the target fence.
	touchRoutesTotal atomic.Uint64
	// expiredRoutesTotal counts routes removed by TTL expiry.
	expiredRoutesTotal atomic.Uint64
}

type directoryShard struct {
	// mu protects all authority slots assigned to this shard.
	mu sync.RWMutex
	// slots holds per-hash-slot authority identities installed on this node.
	slots map[uint16]*authoritySlot
}

type authoritySlot struct {
	// target is the exact fencing token accepted by this authority identity.
	target RouteTarget
	// active contains committed routes by exact owner route identity.
	active map[identityKey]Route
	// byUID indexes active route identities for UID endpoint lookups.
	byUID map[string]map[identityKey]struct{}
	// pending contains conflict candidates waiting for action commit or abort.
	pending map[PendingRouteToken]pendingRoute
	// ownerSeq stores the latest owner sequence seen for each route identity.
	ownerSeq map[identityKey]uint64
	// tombstoneSeq stores explicit unregister fences for exact route identities.
	tombstoneSeq map[identityKey]uint64
	// expiryHeap orders non-empty activity-second buckets by oldest activity.
	expiryHeap expiryBucketHeap
	// expiryBySeen locates the unique bucket for each indexed activity second.
	expiryBySeen map[int64]*expiryBucket
	// expiryByKey locates the exact bucket membership for each indexed route.
	expiryByKey map[identityKey]*expiryBucket
	// nextID allocates shard-local pending route tokens.
	nextID uint64
}

type pendingRoute struct {
	// route is the candidate that will become active on commit.
	route Route
	// conflicts are active route identities acknowledged by the caller.
	conflicts []identityKey
}

type identityKey struct {
	// uid is part of the exact route identity carried by authority RPCs.
	uid string
	// ownerNodeID is the gateway node that owns the route identity.
	ownerNodeID uint64
	// ownerBootID identifies the owner's process generation.
	ownerBootID uint64
	// sessionID is unique within one owner boot generation.
	sessionID uint64
}

// NewDirectory creates a sharded in-memory authority directory.
func NewDirectory(opts DirectoryOptions) *Directory {
	shardCount := opts.ShardCount
	if shardCount <= 0 {
		shardCount = defaultShardCount
	}
	d := &Directory{
		localNodeID: opts.LocalNodeID,
		shards:      make([]directoryShard, shardCount),
	}
	for i := range d.shards {
		d.shards[i].slots = make(map[uint16]*authoritySlot)
	}
	return d
}

// BecomeAuthority installs a fresh authority identity for one hash slot.
func (d *Directory) BecomeAuthority(target RouteTarget) {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	current := shard.slots[target.HashSlot]
	if current != nil {
		if sameAuthorityIdentity(current.target, target) {
			if target.RouteRevision >= current.target.RouteRevision {
				current.target = target
			}
			return
		}
	}
	shard.slots[target.HashSlot] = newAuthoritySlot(target)
}

// LoseAuthority clears authority state for one hash slot.
func (d *Directory) LoseAuthority(hashSlot uint16) {
	shard := d.shard(hashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	delete(shard.slots, hashSlot)
}

// RegisterRoute registers a route or stores it as pending when conflicts exist.
func (d *Directory) RegisterRoute(target RouteTarget, route Route) (RegisterResult, error) {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return RegisterResult{}, err
	}
	return slot.registerLocked(route)
}

// CommitRoute promotes a pending conflict candidate and removes acknowledged conflicts.
func (d *Directory) CommitRoute(target RouteTarget, token PendingRouteToken) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	return slot.commitRouteLocked(token)
}

// AbortRoute drops a pending conflict candidate without touching active routes.
func (d *Directory) AbortRoute(target RouteTarget, token PendingRouteToken) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	if _, ok := slot.pending[token]; !ok {
		return ErrRouteNotReady
	}
	delete(slot.pending, token)
	return nil
}

// UnregisterRoute tombstones and removes one exact route identity.
func (d *Directory) UnregisterRoute(target RouteTarget, identity RouteIdentity, ownerSeq uint64) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	key := makeIdentityKey(identity)
	if ownerSeq > slot.tombstoneSeq[key] {
		slot.tombstoneSeq[key] = ownerSeq
	}
	if ownerSeq > slot.ownerSeq[key] {
		slot.ownerSeq[key] = ownerSeq
	}
	if existing, ok := slot.active[key]; ok && existing.OwnerSeq <= ownerSeq {
		slot.removeActiveLocked(key, existing)
	}
	for token, pending := range slot.pending {
		if makeRouteIdentityKey(pending.route) == key && pending.route.OwnerSeq <= ownerSeq {
			delete(slot.pending, token)
		}
	}
	return nil
}

// TouchRoutes refreshes active owner activity and recreates non-conflicting missing routes.
func (d *Directory) TouchRoutes(target RouteTarget, routes []Route) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	for _, route := range routes {
		slot.touchLocked(route)
	}
	d.touchRoutesTotal.Add(uint64(len(routes)))
	return nil
}

// ExpireRoutesDetailed removes due active routes and reports bounded index diagnostics.
func (d *Directory) ExpireRoutesDetailed(now time.Time, ttl time.Duration) ExpireResult {
	result := ExpireResult{}
	for i := range d.shards {
		shard := &d.shards[i]
		shard.mu.Lock()
		for _, slot := range shard.slots {
			slotResult := slot.expireLocked(now, ttl)
			result.Expired += slotResult.Expired
			result.DueBuckets += slotResult.DueBuckets
			result.Examined += slotResult.Examined
			result.IndexRoutes += slotResult.IndexRoutes
			result.IndexBuckets += slotResult.IndexBuckets
		}
		shard.mu.Unlock()
	}
	if result.Expired > 0 {
		d.expiredRoutesTotal.Add(uint64(result.Expired))
	}
	return result
}

// ExpireRoutes removes active routes whose last observed activity is older than ttl.
func (d *Directory) ExpireRoutes(now time.Time, ttl time.Duration) int {
	return d.ExpireRoutesDetailed(now, ttl).Expired
}

// Snapshot returns aggregate authority route counts for bench diagnostics.
func (d *Directory) Snapshot() Snapshot {
	if d == nil {
		return Snapshot{ByHashSlot: map[uint16]int{}}
	}
	snap := Snapshot{
		ByHashSlot:         make(map[uint16]int),
		TouchRoutesTotal:   d.touchRoutesTotal.Load(),
		ExpiredRoutesTotal: d.expiredRoutesTotal.Load(),
	}
	for i := range d.shards {
		shard := &d.shards[i]
		shard.mu.RLock()
		for hashSlot, slot := range shard.slots {
			count := len(slot.active)
			if count > 0 {
				snap.ByHashSlot[hashSlot] = count
				snap.Active += count
			}
			snap.ExpiryIndexRoutes += len(slot.expiryByKey)
			snap.ExpiryIndexBuckets += len(slot.expiryHeap)
		}
		shard.mu.RUnlock()
	}
	return snap
}

// EndpointsByUID returns active authoritative routes for one UID.
func (d *Directory) EndpointsByUID(target RouteTarget, uid string) ([]Route, error) {
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return nil, err
	}
	keys := slot.byUID[uid]
	if len(keys) == 0 {
		return nil, nil
	}
	routes := make([]Route, 0, len(keys))
	for key := range keys {
		if route, ok := slot.active[key]; ok {
			routes = append(routes, route)
		}
	}
	sortRoutes(routes)
	return routes, nil
}

// Identity returns the immutable identity fields for this route.
func (r Route) Identity() RouteIdentity {
	return RouteIdentity{
		UID:         r.UID,
		OwnerNodeID: r.OwnerNodeID,
		OwnerBootID: r.OwnerBootID,
		SessionID:   r.SessionID,
	}
}

func (d *Directory) validateTarget(target RouteTarget) error {
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	_, err := d.validateTargetLocked(shard, target)
	return err
}

func (d *Directory) validateTargetLocked(shard *directoryShard, target RouteTarget) (*authoritySlot, error) {
	if d.localNodeID != 0 && target.LeaderNodeID != d.localNodeID {
		return nil, ErrNotLeader
	}
	slot := shard.slots[target.HashSlot]
	if slot == nil || !sameAuthorityIdentity(slot.target, target) {
		return nil, ErrNotLeader
	}
	return slot, nil
}

func (d *Directory) shard(hashSlot uint16) *directoryShard {
	return &d.shards[int(hashSlot)%len(d.shards)]
}

func newAuthoritySlot(target RouteTarget) *authoritySlot {
	return &authoritySlot{
		target:       target,
		active:       make(map[identityKey]Route),
		byUID:        make(map[string]map[identityKey]struct{}),
		pending:      make(map[PendingRouteToken]pendingRoute),
		ownerSeq:     make(map[identityKey]uint64),
		tombstoneSeq: make(map[identityKey]uint64),
		expiryHeap:   make(expiryBucketHeap, 0),
		expiryBySeen: make(map[int64]*expiryBucket),
		expiryByKey:  make(map[identityKey]*expiryBucket),
	}
}

func (s *authoritySlot) registerLocked(route Route) (RegisterResult, error) {
	key := makeRouteIdentityKey(route)
	if tombstone, ok := s.tombstoneSeq[key]; ok && route.OwnerSeq <= tombstone {
		return RegisterResult{}, ErrStaleRoute
	}
	if route.OwnerSeq < s.ownerSeq[key] {
		return RegisterResult{}, ErrStaleRoute
	}
	s.ownerSeq[key] = route.OwnerSeq
	route = normalizeRouteSeen(route)

	conflictKeys := s.conflictsLocked(route)
	if len(conflictKeys) == 0 {
		s.upsertActiveLocked(route)
		return RegisterResult{}, nil
	}

	token := s.nextPendingToken()
	s.pending[token] = pendingRoute{
		route:     route,
		conflicts: conflictKeys,
	}
	actions := make([]RouteAction, 0, len(conflictKeys))
	for _, conflictKey := range conflictKeys {
		actions = append(actions, actionForReplacement(route, s.active[conflictKey]))
	}
	return RegisterResult{
		PendingToken: token,
		Actions:      actions,
	}, nil
}

func (s *authoritySlot) commitRouteLocked(token PendingRouteToken) error {
	pending, ok := s.pending[token]
	if !ok {
		return ErrRouteNotReady
	}
	key := makeRouteIdentityKey(pending.route)
	if tombstone, ok := s.tombstoneSeq[key]; ok && pending.route.OwnerSeq <= tombstone {
		delete(s.pending, token)
		return ErrStaleRoute
	}
	if pending.route.OwnerSeq < s.ownerSeq[key] {
		delete(s.pending, token)
		return ErrStaleRoute
	}
	acknowledged := make(map[identityKey]struct{}, len(pending.conflicts))
	for _, key := range pending.conflicts {
		acknowledged[key] = struct{}{}
	}
	for _, key := range s.conflictsLocked(pending.route) {
		if _, ok := acknowledged[key]; !ok {
			return ErrRouteNotReady
		}
	}
	for _, key := range pending.conflicts {
		if existing, ok := s.active[key]; ok {
			s.removeActiveLocked(key, existing)
		}
	}
	s.upsertActiveLocked(pending.route)
	delete(s.pending, token)
	return nil
}

func (s *authoritySlot) touchLocked(route Route) {
	if route.UID == "" {
		return
	}
	key := makeRouteIdentityKey(route)
	if tombstone, ok := s.tombstoneSeq[key]; ok && route.OwnerSeq <= tombstone {
		return
	}
	if route.OwnerSeq < s.ownerSeq[key] {
		return
	}
	s.ownerSeq[key] = route.OwnerSeq
	route = normalizeRouteSeen(route)
	if existing, ok := s.active[key]; ok {
		if route.LastSeenUnix < existing.LastSeenUnix {
			route.LastSeenUnix = existing.LastSeenUnix
		}
		s.upsertActiveLocked(route)
		return
	}
	if len(s.conflictsLocked(route)) != 0 {
		return
	}
	s.upsertActiveLocked(route)
}

func (s *authoritySlot) conflictsLocked(route Route) []identityKey {
	keys := s.byUID[route.UID]
	if len(keys) == 0 {
		return nil
	}
	incomingKey := makeRouteIdentityKey(route)
	conflictKeys := make([]identityKey, 0, len(keys))
	for key := range keys {
		if key == incomingKey {
			continue
		}
		existing := s.active[key]
		if conflicts(route, existing) {
			conflictKeys = append(conflictKeys, key)
		}
	}
	sort.Slice(conflictKeys, func(i, j int) bool {
		return lessIdentityKey(conflictKeys[i], conflictKeys[j])
	})
	return conflictKeys
}

func (s *authoritySlot) upsertActiveLocked(route Route) {
	route = normalizeRouteSeen(route)
	key := makeRouteIdentityKey(route)
	if existing, ok := s.active[key]; ok {
		s.removeActiveLocked(key, existing)
	}
	s.active[key] = route
	if s.byUID[route.UID] == nil {
		s.byUID[route.UID] = make(map[identityKey]struct{})
	}
	s.byUID[route.UID][key] = struct{}{}
	s.scheduleExpiryLocked(key, route)
}

func (s *authoritySlot) removeActiveLocked(key identityKey, route Route) {
	s.unscheduleExpiryLocked(key)
	delete(s.active, key)
	if routes := s.byUID[route.UID]; routes != nil {
		delete(routes, key)
		if len(routes) == 0 {
			delete(s.byUID, route.UID)
		}
	}
}

func (s *authoritySlot) nextPendingToken() PendingRouteToken {
	s.nextID++
	return PendingRouteToken(fmt.Sprintf("%d", s.nextID))
}

func sameAuthorityIdentity(left, right RouteTarget) bool {
	return left.HashSlot == right.HashSlot &&
		left.SlotID == right.SlotID &&
		left.LeaderNodeID == right.LeaderNodeID &&
		left.LeaderTerm == right.LeaderTerm &&
		left.ConfigEpoch == right.ConfigEpoch
}

func conflicts(incoming, existing Route) bool {
	if incoming.UID != existing.UID || incoming.DeviceFlag != existing.DeviceFlag {
		return false
	}
	switch incoming.DeviceLevel {
	case deviceLevelMaster:
		return true
	case deviceLevelSlave:
		return incoming.DeviceID == existing.DeviceID
	default:
		return false
	}
}

func actionForReplacement(incoming, existing Route) RouteAction {
	kind := "close"
	if incoming.DeviceLevel == deviceLevelMaster && incoming.DeviceID != existing.DeviceID {
		kind = "kick_then_close"
	}
	return RouteAction{
		UID:         existing.UID,
		OwnerNodeID: existing.OwnerNodeID,
		OwnerBootID: existing.OwnerBootID,
		SessionID:   existing.SessionID,
		Kind:        kind,
		Reason:      "presence_conflict",
	}
}

func normalizeRouteSeen(route Route) Route {
	if route.LastSeenUnix == 0 {
		route.LastSeenUnix = route.ConnectedUnix
	}
	return route
}

func makeRouteIdentityKey(route Route) identityKey {
	return identityKey{
		uid:         route.UID,
		ownerNodeID: route.OwnerNodeID,
		ownerBootID: route.OwnerBootID,
		sessionID:   route.SessionID,
	}
}

func makeIdentityKey(identity RouteIdentity) identityKey {
	return identityKey{
		uid:         identity.UID,
		ownerNodeID: identity.OwnerNodeID,
		ownerBootID: identity.OwnerBootID,
		sessionID:   identity.SessionID,
	}
}

func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		left := makeRouteIdentityKey(routes[i])
		right := makeRouteIdentityKey(routes[j])
		return lessIdentityKey(left, right)
	})
}

func lessIdentityKey(left, right identityKey) bool {
	if left.uid != right.uid {
		return left.uid < right.uid
	}
	if left.sessionID != right.sessionID {
		return left.sessionID < right.sessionID
	}
	if left.ownerNodeID != right.ownerNodeID {
		return left.ownerNodeID < right.ownerNodeID
	}
	if left.ownerBootID != right.ownerBootID {
		return left.ownerBootID < right.ownerBootID
	}
	return false
}
