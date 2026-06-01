# Internalv2 Presence Touch Keepalive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace internalv2 presence authority rehydrate with owner-side ping touch batching and authority-side TTL expiry.

**Architecture:** Gateway PING marks the owner-local session dirty. A single app worker batches dirty owner routes, sends `TouchRoutes` to the current hash-slot leader, updates local authority epochs from route-authority events, and expires untouched authority routes by TTL. The worker does not scan active sessions on authority changes and does not run per-hash-slot replay jobs.

**Tech Stack:** Go, internalv2 presence usecase/runtime/access layers, clusterv2 node RPC, existing `go test` unit suites.

---

## Source Spec

- `docs/superpowers/specs/2026-06-01-internalv2-presence-touch-design.md`

## File Structure

- Modify `internalv2/runtime/online/types.go`
  - Add `LastActivityUnix` to `OnlineConn`.
  - Reword hash slot comments away from rehydrate.
- Modify `internalv2/runtime/online/registry.go`
  - Replace hash-slot active scan indexes with per-shard dirty touch indexes.
  - Add `MarkTouched`, `DrainTouched`, and `RequeueTouched`.
- Modify `internalv2/runtime/online/FLOW.md`
  - Replace rehydrate scan wording with dirty touch batching.
- Modify `internalv2/runtime/presence/types.go`
  - Add `LastSeenUnix` to `Route`.
  - Remove `RehydrateResult`.
- Modify `internalv2/runtime/presence/directory.go`
  - Remove `RehydrateRoutes`.
  - Add `TouchRoutes` and `ExpireRoutes`.
  - Add explicit unregister tombstones so same-sequence touches cannot resurrect disconnected routes.
  - Keep register, unregister, and conflict behavior intact.
- Modify `internalv2/runtime/presence/FLOW.md`
  - Document touch and TTL semantics.
- Modify `internalv2/usecase/presence/types.go`
  - Add `TouchCommand`.
  - Remove `RehydrateResult` alias.
- Modify `internalv2/usecase/presence/ports.go`
  - Add `MarkTouched` to the local registry port.
- Create `internalv2/usecase/presence/touch.go`
  - Implement entry-agnostic `Touch`.
- Modify `internalv2/usecase/presence/app.go`
  - Set `LastActivityUnix` and `LastSeenUnix` from activation data.
- Modify `internalv2/usecase/presence/FLOW.md`
  - Add the touch flow.
- Modify `internalv2/access/gateway/handler.go`
  - Add `Touch` to `PresenceUsecase`.
  - Call it best-effort before writing PONG.
- Modify `internalv2/access/gateway/FLOW.md`
  - Update PING flow.
- Modify `internalv2/access/node/presence_rpc.go`
  - Replace rehydrate authority RPC with `TouchRoutes`.
- Modify `internalv2/access/node/presence_codec.go`
  - Add `LastSeenUnix` route field.
  - Replace op id `rehydrate_routes` with `touch_routes`.
  - Remove rehydrate response payload encoding.
- Modify `internalv2/access/node/FLOW.md`
  - Update supported RPC calls.
- Modify `internalv2/infra/cluster/presence.go`
  - Add `TouchRoutesTo`.
  - Remove rehydrate-specific methods.
- Modify `internalv2/infra/cluster/FLOW.md`
  - Document touch routing.
- Delete `internalv2/app/presence_rehydrate.go`
- Create `internalv2/app/presence_touch.go`
  - Add the combined authority watcher, dirty touch flusher, and TTL sweeper.
  - Keep the owner-action local registry interface that was previously colocated with the rehydrate worker.
- Modify `internalv2/app/app.go`
  - Wire the new worker and expose `Touch` through activation timeout wrapper.
- Modify `internalv2/app/config.go`
  - Replace rehydrate config with touch config.
- Modify `internalv2/app/lifecycle.go`
  - Lifecycle names remain generic; only tests and comments change.
- Modify `internalv2/app/FLOW.md`
  - Replace rehydrate flow with touch worker flow.
- Modify `cmd/wukongimv2/config.go`
  - Parse new `WK_PRESENCE_TOUCH_FLUSH_INTERVAL`, `WK_PRESENCE_TOUCH_BATCH_SIZE`, and `WK_PRESENCE_ROUTE_TTL`.
  - Remove old rehydrate keys from supported parsing.
- Modify config examples:
  - `wukongim.conf.example`
  - `cmd/wukongimv2/wukongimv2.conf.example`
  - `cmd/wukongimv2/wukongimv2-node1.conf.example`
  - `cmd/wukongimv2/wukongimv2-node2.conf.example`
  - `cmd/wukongimv2/wukongimv2-node3.conf.example`
  - `scripts/wukongimv2/wukongimv2.conf`
  - `scripts/wukongimv2/wukongimv2-node1.conf`
  - `scripts/wukongimv2/wukongimv2-node2.conf`
  - `scripts/wukongimv2/wukongimv2-node3.conf`
- Modify `AGENTS.md`
  - Keep the directory summary aligned if wording still mentions rehydrate.

## Route Semantics

`RegisterRoute` remains the authoritative connect path and may return conflict actions.

`TouchRoutes` is a keepalive path:

```go
func (d *Directory) TouchRoutes(target RouteTarget, routes []Route) error
```

- Empty input returns nil after target validation.
- Existing same-identity route is refreshed when incoming `OwnerSeq` is at least the active route sequence.
- Explicit `UnregisterRoute` records a tombstone sequence for the exact route identity.
- Missing same-identity route is recreated only when incoming `OwnerSeq` is greater than the explicit unregister tombstone and the route does not conflict with current active routes.
- Conflicting missing touch routes are ignored without owner actions.
- Stale owner sequence touches are ignored without changing active routes.

`ExpireRoutes` removes active authority routes that have not been touched within TTL:

```go
func (d *Directory) ExpireRoutes(now time.Time, ttl time.Duration) int
```

It never creates tombstones, so the next same-sequence touch can rebuild a route removed only by TTL. The real disconnect fence is explicit unregister.

## Task 1: Owner Registry Dirty Touch State

**Files:**
- Modify `internalv2/runtime/online/types.go`
- Modify `internalv2/runtime/online/registry.go`
- Test `internalv2/runtime/online/registry_test.go`
- Modify `internalv2/runtime/online/FLOW.md`

- [ ] **Step 1: Read package flow**

Run: `sed -n '1,220p' internalv2/runtime/online/FLOW.md`

Expected: output describes owner-local sessions and still mentions rehydrate before this task.

- [ ] **Step 2: Add failing registry tests**

Delete the old `TestVisitActiveByHashSlotPagesWithoutSortingWholeBucket` and `TestVisitActiveByHashSlotSkipsPendingAndOtherSlots` tests because the touch design no longer pages active sessions by hash slot. Add tests to `internalv2/runtime/online/registry_test.go`:

```go
func TestRegistryMarkTouchedMarksActiveRouteDirty(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 2})
	conn := OnlineConn{UID: "u1", HashSlot: 7, OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: 21, SessionID: 101, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(conn))
	require.NoError(t, reg.MarkActive(conn.SessionID))

	got, ok := reg.MarkTouched(conn.SessionID, 15)
	require.True(t, ok)
	require.Equal(t, int64(15), got.LastActivityUnix)

	batch := reg.DrainTouched(10)
	require.Len(t, batch, 1)
	require.Equal(t, conn.SessionID, batch[0].SessionID)
	require.Equal(t, int64(15), batch[0].LastActivityUnix)
	require.Empty(t, reg.DrainTouched(10))
}

func TestRegistryMarkTouchedIgnoresPendingAndMissing(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	conn := OnlineConn{UID: "u1", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 7, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(conn))

	_, ok := reg.MarkTouched(conn.SessionID, 11)
	require.False(t, ok)
	_, ok = reg.MarkTouched(999, 12)
	require.False(t, ok)
	require.Empty(t, reg.DrainTouched(10))
}

func TestRegistryDrainTouchedBatchesAndClearsDirty(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	for i := uint64(1); i <= 3; i++ {
		conn := OnlineConn{UID: fmt.Sprintf("u%d", i), HashSlot: uint16(i), OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: i, SessionID: i, ConnectedUnix: 10}
		require.NoError(t, reg.RegisterPending(conn))
		require.NoError(t, reg.MarkActive(i))
		_, ok := reg.MarkTouched(i, int64(20+i))
		require.True(t, ok)
	}

	first := reg.DrainTouched(2)
	require.Len(t, first, 2)
	second := reg.DrainTouched(2)
	require.Len(t, second, 1)
	require.Empty(t, reg.DrainTouched(2))
}

func TestRegistryRequeueTouchedSkipsRemovedOrSupersededSessions(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	original := OnlineConn{UID: "u1", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 10, SessionID: 100, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(original))
	require.NoError(t, reg.MarkActive(original.SessionID))
	_, ok := reg.MarkTouched(original.SessionID, 20)
	require.True(t, ok)
	drained := reg.DrainTouched(10)
	require.Len(t, drained, 1)

	removed, ok := reg.MarkClosingAndUnregister(original.SessionID)
	require.True(t, ok)
	reg.RequeueTouched([]OnlineConn{removed})
	require.Empty(t, reg.DrainTouched(10))

	replacement := original
	replacement.OwnerSeq = 11
	replacement.ConnectedUnix = 30
	require.NoError(t, reg.RegisterPending(replacement))
	require.NoError(t, reg.MarkActive(replacement.SessionID))
	reg.RequeueTouched(drained)
	require.Empty(t, reg.DrainTouched(10))

	_, ok = reg.MarkTouched(replacement.SessionID, 40)
	require.True(t, ok)
	batch := reg.DrainTouched(10)
	require.Len(t, batch, 1)
	require.Equal(t, uint64(11), batch[0].OwnerSeq)
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internalv2/runtime/online`

Expected: FAIL with missing `LastActivityUnix`, `MarkTouched`, `DrainTouched`, or `RequeueTouched`.

- [ ] **Step 4: Add the registry API**

Update `OnlineConn`:

```go
// HashSlot is the UID hash slot observed during activation.
HashSlot uint16
// LastActivityUnix records the latest owner-observed client activity for batched authority touch.
LastActivityUnix int64
```

Update `registryShard`:

```go
mu         sync.RWMutex
bySession  map[uint64]OnlineConn
dirtyIDs   map[uint64]struct{}
dirtyOrder []uint64
```

Remove `RouteCursor`, `routeBucket`, `byHashSlot`, `VisitActiveByHashSlot`, `collectActiveByHashSlot`, `nextActiveLocked`, `insertActiveLocked`, `removeActiveLocked`, and `bucketLocked`. Remove the `sort` import from `registry.go`. Initialize `dirtyIDs` in `NewRegistry`.

Add methods:

```go
// MarkTouched records client activity for one active session and marks it for authority touch.
func (r *Registry) MarkTouched(sessionID uint64, activityUnix int64) (OnlineConn, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	conn, ok := shard.bySession[sessionID]
	if !ok || conn.State != RouteStateActive {
		return OnlineConn{}, false
	}
	if activityUnix > conn.LastActivityUnix {
		conn.LastActivityUnix = activityUnix
	}
	if conn.LastActivityUnix == 0 {
		conn.LastActivityUnix = conn.ConnectedUnix
	}
	shard.bySession[sessionID] = conn
	shard.markDirtyLocked(sessionID)
	return conn, true
}

// DrainTouched returns up to limit active sessions that need authority touch.
func (r *Registry) DrainTouched(limit int) []OnlineConn {
	if limit <= 0 {
		return nil
	}
	out := make([]OnlineConn, 0, limit)
	for i := range r.shards {
		if len(out) == limit {
			break
		}
		shard := &r.shards[i]
		shard.mu.Lock()
		out = shard.drainDirtyLocked(out, limit-len(out))
		shard.mu.Unlock()
	}
	return out
}

// RequeueTouched marks still-active sessions dirty after a failed touch flush.
func (r *Registry) RequeueTouched(conns []OnlineConn) {
	for _, conn := range conns {
		shard := r.sessionShard(conn.SessionID)
		shard.mu.Lock()
		current, ok := shard.bySession[conn.SessionID]
		if ok && current.State == RouteStateActive &&
			current.UID == conn.UID &&
			current.OwnerNodeID == conn.OwnerNodeID &&
			current.OwnerBootID == conn.OwnerBootID &&
			current.OwnerSeq == conn.OwnerSeq {
			if conn.LastActivityUnix > current.LastActivityUnix {
				current.LastActivityUnix = conn.LastActivityUnix
				shard.bySession[conn.SessionID] = current
			}
			shard.markDirtyLocked(conn.SessionID)
		}
		shard.mu.Unlock()
	}
}
```

Add helper methods on `registryShard`:

```go
func (s *registryShard) markDirtyLocked(sessionID uint64) {
	if _, ok := s.dirtyIDs[sessionID]; ok {
		return
	}
	s.dirtyIDs[sessionID] = struct{}{}
	s.dirtyOrder = append(s.dirtyOrder, sessionID)
}

func (s *registryShard) drainDirtyLocked(out []OnlineConn, remaining int) []OnlineConn {
	if remaining <= 0 || len(s.dirtyOrder) == 0 {
		return out
	}
	write := 0
	for _, sessionID := range s.dirtyOrder {
		if _, dirty := s.dirtyIDs[sessionID]; !dirty {
			continue
		}
		if remaining > 0 {
			delete(s.dirtyIDs, sessionID)
			if conn, ok := s.bySession[sessionID]; ok && conn.State == RouteStateActive {
				out = append(out, conn)
				remaining--
				continue
			}
			continue
		}
		s.dirtyOrder[write] = sessionID
		write++
	}
	for i := write; i < len(s.dirtyOrder); i++ {
		s.dirtyOrder[i] = 0
	}
	s.dirtyOrder = s.dirtyOrder[:write]
	return out
}
```

Update `RegisterPending` to seed activity time:

```go
if conn.LastActivityUnix == 0 {
	conn.LastActivityUnix = conn.ConnectedUnix
}
```

Update `MarkActive` so it only sets `RouteStateActive` and stores the connection copy; it no longer inserts into a hash-slot bucket.

Update `MarkClosingAndUnregister` so closing sessions are removed from `dirtyIDs`; stale IDs left in `dirtyOrder` are discarded during drain.

- [ ] **Step 5: Update online flow doc**

Replace the rehydrate section with:

```markdown
## Touch Batching

`MarkTouched` records PING or other valid client activity for active sessions and marks the session dirty. `DrainTouched` returns bounded batches for the app touch worker; `RequeueTouched` restores still-active sessions after a failed authority touch call.
```

- [ ] **Step 6: Run focused tests**

Run: `go test ./internalv2/runtime/online`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/runtime/online
git commit -m "feat: track internalv2 presence dirty touches"
```

## Task 2: Authority Touch And TTL Runtime

**Files:**
- Modify `internalv2/runtime/presence/types.go`
- Modify `internalv2/runtime/presence/directory.go`
- Test `internalv2/runtime/presence/directory_test.go`
- Modify `internalv2/runtime/presence/FLOW.md`

- [ ] **Step 1: Read package flow**

Run: `sed -n '1,240p' internalv2/runtime/presence/FLOW.md`

Expected: output describes register, unregister, owner sequence fencing, and rehydrate before this task.

- [ ] **Step 2: Add failing directory tests**

Add tests:

```go
func TestDirectoryTouchRoutesRefreshesExistingRoute(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	target := testRouteTarget(7)
	d.BecomeAuthority(target)
	route := testRoute("u1", 100)
	route.LastSeenUnix = 10
	_, err := d.RegisterRoute(target, route)
	require.NoError(t, err)

	route.LastSeenUnix = 20
	require.NoError(t, d.TouchRoutes(target, []Route{route}))

	got, err := d.EndpointsByUID(target, "u1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(20), got[0].LastSeenUnix)
}

func TestDirectoryTouchRoutesRecreatesMissingRoute(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	target := testRouteTarget(7)
	d.BecomeAuthority(target)
	route := testRoute("u1", 100)
	route.LastSeenUnix = 30

	require.NoError(t, d.TouchRoutes(target, []Route{route}))

	got, err := d.EndpointsByUID(target, "u1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, route.Identity(), got[0].Identity())
	require.Equal(t, int64(30), got[0].LastSeenUnix)
}

func TestDirectoryTouchRoutesIgnoresExplicitUnregisterTombstoneAtSameOwnerSeq(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	target := testRouteTarget(7)
	d.BecomeAuthority(target)
	route := testRoute("u1", 100)
	route.OwnerSeq = 9
	route.LastSeenUnix = 10
	_, err := d.RegisterRoute(target, route)
	require.NoError(t, err)
	require.NoError(t, d.UnregisterRoute(target, route.Identity(), 9))

	route.LastSeenUnix = 20
	require.NoError(t, d.TouchRoutes(target, []Route{route}))

	got, err := d.EndpointsByUID(target, "u1")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestDirectoryTouchRoutesIgnoresConflictingMissingRoute(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	target := testRouteTarget(7)
	d.BecomeAuthority(target)
	first := testRoute("u1", 100)
	first.DeviceFlag = 1
	first.DeviceLevel = deviceLevelMaster
	first.LastSeenUnix = 10
	_, err := d.RegisterRoute(target, first)
	require.NoError(t, err)

	second := first
	second.SessionID = 200
	second.OwnerSeq = 200
	second.DeviceID = "other-device"
	second.LastSeenUnix = 20
	require.NoError(t, d.TouchRoutes(target, []Route{second}))

	got, err := d.EndpointsByUID(target, "u1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, first.SessionID, got[0].SessionID)
}

func TestDirectoryExpireRoutesRemovesUntouchedRoutes(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	target := testRouteTarget(7)
	d.BecomeAuthority(target)
	oldRoute := testRoute("old", 100)
	oldRoute.LastSeenUnix = time.Unix(10, 0).Unix()
	freshRoute := testRoute("fresh", 101)
	freshRoute.LastSeenUnix = time.Unix(95, 0).Unix()
	_, err := d.RegisterRoute(target, oldRoute)
	require.NoError(t, err)
	_, err = d.RegisterRoute(target, freshRoute)
	require.NoError(t, err)

	expired := d.ExpireRoutes(time.Unix(101, 0), 90*time.Second)
	require.Equal(t, 1, expired)

	oldEndpoints, err := d.EndpointsByUID(target, "old")
	require.NoError(t, err)
	require.Empty(t, oldEndpoints)
	freshEndpoints, err := d.EndpointsByUID(target, "fresh")
	require.NoError(t, err)
	require.Len(t, freshEndpoints, 1)
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internalv2/runtime/presence`

Expected: FAIL with missing `LastSeenUnix`, `TouchRoutes`, or `ExpireRoutes`.

- [ ] **Step 4: Add route field and remove rehydrate DTO**

In `Route`:

```go
// LastSeenUnix records the latest authority-observed owner activity for TTL expiry.
LastSeenUnix int64
```

Delete `RehydrateResult` from `types.go`.

Update `authoritySlot`:

```go
// tombstoneSeq stores explicit unregister fences for exact route identities.
tombstoneSeq map[identityKey]uint64
```

Initialize `tombstoneSeq` in `newAuthoritySlot`.

- [ ] **Step 5: Add touch and TTL implementation**

Add:

```go
// TouchRoutes refreshes or recreates owner routes without triggering conflict actions.
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
	return nil
}

// ExpireRoutes removes active routes that have not been touched within ttl.
func (d *Directory) ExpireRoutes(now time.Time, ttl time.Duration) int {
	if ttl <= 0 || now.IsZero() {
		return 0
	}
	expired := 0
	for i := range d.shards {
		shard := &d.shards[i]
		shard.mu.Lock()
		for _, slot := range shard.slots {
			for key, route := range slot.active {
				seenUnix := route.LastSeenUnix
				if seenUnix == 0 {
					seenUnix = route.ConnectedUnix
				}
				if seenUnix == 0 {
					continue
				}
				if time.Unix(seenUnix, 0).Add(ttl).Before(now) {
					slot.removeActiveLocked(key, route)
					expired++
				}
			}
		}
		shard.mu.Unlock()
	}
	return expired
}
```

Add helper:

```go
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
	if route.LastSeenUnix == 0 {
		route.LastSeenUnix = route.ConnectedUnix
	}
	if existing, ok := s.active[key]; ok {
		if route.OwnerSeq < existing.OwnerSeq {
			return
		}
		if route.LastSeenUnix == 0 {
			route.LastSeenUnix = existing.LastSeenUnix
		}
		s.ownerSeq[key] = route.OwnerSeq
		s.upsertActiveLocked(route)
		return
	}
	if len(s.conflictsLocked(route)) > 0 {
		return
	}
	s.ownerSeq[key] = route.OwnerSeq
	s.upsertActiveLocked(route)
}
```

Update `registerLocked` so zero `LastSeenUnix` is seeded from `ConnectedUnix`:

```go
if tombstone, ok := s.tombstoneSeq[key]; ok && route.OwnerSeq <= tombstone {
	return RegisterResult{}, ErrStaleRoute
}
if route.LastSeenUnix == 0 {
	route.LastSeenUnix = route.ConnectedUnix
}
```

Update `UnregisterRoute` so explicit disconnects fence equal-sequence touches:

```go
if ownerSeq > slot.tombstoneSeq[key] {
	slot.tombstoneSeq[key] = ownerSeq
}
if ownerSeq > slot.ownerSeq[key] {
	slot.ownerSeq[key] = ownerSeq
}
```

Delete `RehydrateRoutes` from `directory.go`.

- [ ] **Step 6: Update presence flow doc**

Replace the rehydrate section with:

```markdown
## Touch And TTL

`TouchRoutes` refreshes existing route identities or recreates missing non-conflicting identities from owner touch batches. It does not create pending routes or owner actions. Explicit `UnregisterRoute` records a tombstone so equal-sequence touches cannot resurrect a disconnected route. `ExpireRoutes` removes active routes without tombstones, which cleans up owner crashes and missed unregisters while still allowing the next owner touch to rebuild the route.
```

- [ ] **Step 7: Run focused tests**

Run: `go test ./internalv2/runtime/presence`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internalv2/runtime/presence
git commit -m "feat: add internalv2 presence touch ttl"
```

## Task 3: Presence Usecase Touch

**Files:**
- Modify `internalv2/usecase/presence/types.go`
- Modify `internalv2/usecase/presence/ports.go`
- Modify `internalv2/usecase/presence/app.go`
- Create `internalv2/usecase/presence/touch.go`
- Test `internalv2/usecase/presence/app_test.go`
- Modify `internalv2/usecase/presence/FLOW.md`

- [ ] **Step 1: Read package flow**

Run: `sed -n '1,220p' internalv2/usecase/presence/FLOW.md`

Expected: output describes activate, deactivate, query, and import boundaries.

- [ ] **Step 2: Add failing usecase tests**

Add tests:

```go
func TestTouchMarksLocalSessionTouched(t *testing.T) {
	local := &fakeLocalRegistry{}
	app := New(Options{Local: local})

	err := app.Touch(context.Background(), TouchCommand{SessionID: 44, ActivityUnix: 123})
	require.NoError(t, err)
	require.Equal(t, uint64(44), local.touchedSessionID)
	require.Equal(t, int64(123), local.touchedUnix)
}

func TestTouchRequiresLocalRegistry(t *testing.T) {
	app := New(Options{})
	err := app.Touch(context.Background(), TouchCommand{SessionID: 44, ActivityUnix: 123})
	require.ErrorIs(t, err, ErrLocalRegistryUnavailable)
}

func TestRouteFromConnCarriesLastSeenUnix(t *testing.T) {
	route := routeFromConn(OnlineConn{
		UID:              "u1",
		OwnerNodeID:      1,
		OwnerBootID:      2,
		OwnerSeq:         3,
		SessionID:        4,
		ConnectedUnix:    10,
		LastActivityUnix: 20,
	})
	require.Equal(t, int64(20), route.LastSeenUnix)
}
```

Extend the fake local registry:

```go
func (f *fakeLocalRegistry) MarkTouched(sessionID uint64, activityUnix int64) (OnlineConn, bool) {
	f.touchedSessionID = sessionID
	f.touchedUnix = activityUnix
	return OnlineConn{SessionID: sessionID, LastActivityUnix: activityUnix, State: RouteStateActive}, true
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internalv2/usecase/presence`

Expected: FAIL with missing `TouchCommand`, `Touch`, or `MarkTouched`.

- [ ] **Step 4: Add usecase touch API**

In `types.go`:

```go
// TouchCommand records owner-observed client activity for keepalive batching.
type TouchCommand struct {
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
	// ActivityUnix records when the owner observed a valid client packet.
	ActivityUnix int64
}
```

Remove the `RehydrateResult` alias.

In `ports.go`:

```go
MarkTouched(sessionID uint64, activityUnix int64) (OnlineConn, bool)
```

Create `touch.go`:

```go
package presence

import "context"

// Touch marks an active owner-local session as needing an authority keepalive touch.
func (a *App) Touch(ctx context.Context, cmd TouchCommand) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.local == nil {
		return ErrLocalRegistryUnavailable
	}
	a.local.MarkTouched(cmd.SessionID, cmd.ActivityUnix)
	return nil
}
```

`MarkTouched` returning false is still a nil `Touch` result; PING keepalive is best-effort and should not close a client because the local session is already gone or not active yet.

Update `onlineConn`:

```go
LastActivityUnix: cmd.ConnectedUnix,
```

Update `routeFromConn`:

```go
LastSeenUnix: conn.LastActivityUnix,
```

- [ ] **Step 5: Update usecase flow doc**

Add:

````markdown
## Touch Flow

```text
Touch(command)
  -> local.MarkTouched(sessionID, activityUnix)
```

Touch is owner-local only. Authority forwarding is performed by the app touch worker so gateway PING does not issue one RPC per client packet.
````

- [ ] **Step 6: Run focused tests**

Run: `go test ./internalv2/usecase/presence`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/usecase/presence
git commit -m "feat: add internalv2 presence touch usecase"
```

## Task 4: Node RPC TouchRoutes Contract

**Files:**
- Modify `internalv2/access/node/presence_rpc.go`
- Modify `internalv2/access/node/presence_codec.go`
- Test `internalv2/access/node/presence_codec_test.go`
- Test `internalv2/access/node/presence_rpc_test.go`
- Modify `internalv2/access/node/FLOW.md`

- [ ] **Step 1: Read package flow**

Run: `sed -n '1,220p' internalv2/access/node/FLOW.md`

Expected: output lists `RehydrateRoutes` before this task.

- [ ] **Step 2: Add failing codec and RPC tests**

Add codec test:

```go
func TestPresenceRPCCodecRoundTripTouchRoutes(t *testing.T) {
	req := presenceRPCRequest{
		Op:     presenceOpTouchRoutes,
		Target: presence.RouteTarget{HashSlot: 7, SlotID: 8, LeaderNodeID: 9, RouteRevision: 10, AuthorityEpoch: 11},
		Routes: []presence.Route{{
			UID:              "u1",
			OwnerNodeID:      1,
			OwnerBootID:      2,
			OwnerSeq:         3,
			SessionID:        4,
			DeviceID:         "d1",
			DeviceFlag:       1,
			DeviceLevel:      0,
			Listener:         "tcp",
			ConnectedUnix:    100,
			LastSeenUnix:     150,
		}},
	}

	body, err := encodePresenceRPCRequestBinary(req)
	require.NoError(t, err)
	got, err := decodePresenceRPCRequest(body)
	require.NoError(t, err)
	require.Equal(t, req.Op, got.Op)
	require.Equal(t, req.Target, got.Target)
	require.Equal(t, req.Routes, got.Routes)
}
```

Add RPC dispatch test:

```go
func TestAdapterHandlePresenceAuthorityTouchRoutes(t *testing.T) {
	authority := &recordingPresenceAuthority{}
	adapter := New(Options{Authority: authority})
	req := presenceRPCRequest{
		Op:     presenceOpTouchRoutes,
		Target: testPresenceTarget(),
		Routes: []presence.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, OwnerSeq: 3, SessionID: 4, LastSeenUnix: 50}},
	}
	body, err := encodePresenceRPCRequestBinary(req)
	require.NoError(t, err)

	respBody, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodePresenceRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, req.Target, authority.touchTarget)
	require.Equal(t, req.Routes, authority.touchRoutes)
}
```

Add client test:

```go
func TestClientTouchRoutesCallsPresenceAuthorityService(t *testing.T) {
	node := &recordingPresenceRPCNode{responseStatus: rpcStatusOK}
	client := NewClient(node)
	target := testPresenceTarget()
	routes := []presence.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, OwnerSeq: 3, SessionID: 4, LastSeenUnix: 50}}

	require.NoError(t, client.TouchRoutes(context.Background(), target, routes))

	require.Equal(t, target.LeaderNodeID, node.nodeID)
	require.Equal(t, PresenceAuthorityRPCServiceID, node.serviceID)
	req, err := decodePresenceRPCRequest(node.payload)
	require.NoError(t, err)
	require.Equal(t, presenceOpTouchRoutes, req.Op)
	require.Equal(t, routes, req.Routes)
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internalv2/access/node`

Expected: FAIL with missing `presenceOpTouchRoutes`, route field codec, or client method.

- [ ] **Step 4: Replace rehydrate RPC with touch**

In `presence_rpc.go`:

```go
presenceOpTouchRoutes = "touch_routes"
```

Update `PresenceAuthority`:

```go
TouchRoutes(context.Context, presence.RouteTarget, []presence.Route) error
```

Handle the op:

```go
case presenceOpTouchRoutes:
	err := a.authority.TouchRoutes(ctx, req.Target, req.Routes)
	return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: presenceRPCStatusForError(err)})
```

Add client method:

```go
// TouchRoutes refreshes owner routes on the target authority node.
func (c *Client) TouchRoutes(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpTouchRoutes, Target: target, Routes: routes})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}
```

Delete `RehydrateRoutes` client and interface methods.

In `presence_codec.go`:

```go
presenceOpTouchRoutesID
```

Map op id to `presenceOpTouchRoutes`.

Append and read `LastSeenUnix` immediately after `ConnectedUnix`:

```go
dst = appendVarint(dst, route.LastSeenUnix)
```

```go
if route.LastSeenUnix, offset, err = readVarint(body, offset); err != nil {
	return presence.Route{}, offset, err
}
```

Remove `Rehydrate` from `presenceRPCResponse` and remove `appendPresenceRehydrateResults` / `readPresenceRehydrateResults`.

- [ ] **Step 5: Update node flow doc**

Replace the supported call list item:

```markdown
- `TouchRoutes(RouteTarget, []Route)`
```

Remove rehydrate wording from codec responsibility.

- [ ] **Step 6: Run focused tests**

Run: `go test ./internalv2/access/node`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/access/node
git commit -m "feat: add internalv2 presence touch rpc"
```

## Task 5: Cluster Presence Touch Adapter

**Files:**
- Modify `internalv2/infra/cluster/presence.go`
- Test `internalv2/infra/cluster/presence_test.go`
- Modify `internalv2/infra/cluster/FLOW.md`

- [ ] **Step 1: Read package flow**

Run: `sed -n '1,220p' internalv2/infra/cluster/FLOW.md`

Expected: output mentions `RehydrateRoutesTo` before this task.

- [ ] **Step 2: Add failing adapter tests**

Add tests:

```go
func TestPresenceAuthorityClientTouchRoutesToLocal(t *testing.T) {
	node := newFakePresenceNode(1)
	local := &recordingPresenceAuthority{}
	client := NewPresenceAuthorityClient(node, local)
	target := presence.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	routes := []presence.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, OwnerSeq: 3, SessionID: 4, LastSeenUnix: 50}}

	require.NoError(t, client.TouchRoutesTo(context.Background(), target, routes))
	require.Equal(t, target, local.touchTarget)
	require.Equal(t, routes, local.touchRoutes)
}

func TestPresenceAuthorityClientTouchRoutesToRemote(t *testing.T) {
	node := newFakePresenceNode(1)
	node.rpcResponseStatus = "ok"
	client := NewPresenceAuthorityClient(node, &recordingPresenceAuthority{})
	target := presence.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 3, AuthorityEpoch: 4}
	routes := []presence.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, OwnerSeq: 3, SessionID: 4, LastSeenUnix: 50}}

	require.NoError(t, client.TouchRoutesTo(context.Background(), target, routes))
	require.Equal(t, uint64(2), node.rpcNodeID)
	require.Equal(t, accessnode.PresenceAuthorityRPCServiceID, node.rpcServiceID)
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internalv2/infra/cluster`

Expected: FAIL with missing `TouchRoutesTo`.

- [ ] **Step 4: Add TouchRoutesTo and remove rehydrate adapter methods**

In `PresenceAuthorityClient`:

```go
// TouchRoutesTo refreshes owner routes in a specific observed authority epoch.
func (c *PresenceAuthorityClient) TouchRoutesTo(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return err
	}
	return authority.TouchRoutes(ctx, target, routes)
}
```

Delete `RehydrateRoutesTo`, `CommitRehydratedRoute`, and `AbortRehydratedRoute`.

Update test fakes to implement `TouchRoutes`.

- [ ] **Step 5: Update infra flow doc**

Replace the rehydrate paragraph with:

```markdown
Touch batching uses `TouchRoutesTo(target, routes)` because the app worker groups dirty owner sessions by the exact authority target observed during flush. The adapter sends the batch locally when the target leader is this node and uses access/node RPC for remote leaders.
```

- [ ] **Step 6: Run focused tests**

Run: `go test ./internalv2/infra/cluster`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/infra/cluster
git commit -m "feat: route internalv2 presence touch batches"
```

## Task 6: Gateway Ping Marks Touch

**Files:**
- Modify `internalv2/access/gateway/handler.go`
- Test `internalv2/access/gateway/handler_test.go`
- Modify `internalv2/access/gateway/FLOW.md`

- [ ] **Step 1: Read package flow**

Run: `sed -n '1,220p' internalv2/access/gateway/FLOW.md`

Expected: output says PING writes PONG directly before this task.

- [ ] **Step 2: Add failing gateway test**

Add test:

```go
func TestHandlerPingTouchesPresenceBeforePong(t *testing.T) {
	presence := &recordingPresenceUsecase{}
	handler := New(Options{Presence: presence})
	ctx := testGatewayContext(t, "u1", 99)

	require.NoError(t, handler.OnFrame(ctx, &frame.PingPacket{}))

	require.Equal(t, uint64(99), presence.touch.SessionID)
	require.Greater(t, presence.touch.ActivityUnix, int64(0))
	require.IsType(t, &frame.PongPacket{}, ctx.writtenFrame)
}

func TestHandlerPingStillWritesPongWhenTouchFails(t *testing.T) {
	presence := &recordingPresenceUsecase{touchErr: presence.ErrLocalRegistryUnavailable}
	handler := New(Options{Presence: presence})
	ctx := testGatewayContext(t, "u1", 99)

	require.NoError(t, handler.OnFrame(ctx, &frame.PingPacket{}))

	require.IsType(t, &frame.PongPacket{}, ctx.writtenFrame)
}
```

Extend the fake:

```go
func (r *recordingPresenceUsecase) Touch(_ context.Context, cmd presence.TouchCommand) error {
	r.touch = cmd
	return r.touchErr
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internalv2/access/gateway`

Expected: FAIL with missing `Touch` on `PresenceUsecase` or test fake.

- [ ] **Step 4: Add best-effort ping touch**

Update interface:

```go
Touch(context.Context, presence.TouchCommand) error
```

Change PING handling:

```go
case *frame.PingPacket:
	h.touchPresence(&ctx, time.Now())
	return ctx.WriteFrame(&frame.PongPacket{})
```

Add helper:

```go
func (h *Handler) touchPresence(ctx *coregateway.Context, now time.Time) {
	if h == nil || h.presence == nil || ctx == nil {
		return
	}
	if ctx.Session == nil {
		return
	}
	sessionID := ctx.Session.ID()
	if sessionID == 0 {
		return
	}
	_ = h.presence.Touch(requestContextFromContext(ctx), presence.TouchCommand{
		SessionID:    sessionID,
		ActivityUnix: now.Unix(),
	})
}
```

Use the existing context mapping helpers already used by activation and send code.

- [ ] **Step 5: Update gateway flow doc**

Replace PING flow:

```text
OnFrame(PingPacket)
  -> best-effort presence.Touch(sessionID, now)
  -> write PongPacket on the same gateway session
```

- [ ] **Step 6: Run focused tests**

Run: `go test ./internalv2/access/gateway`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/access/gateway
git commit -m "feat: mark presence touch from gateway ping"
```

## Task 7: App Touch Worker And Config

**Files:**
- Delete `internalv2/app/presence_rehydrate.go`
- Create `internalv2/app/presence_touch.go`
- Modify `internalv2/app/app.go`
- Modify `internalv2/app/config.go`
- Modify `internalv2/app/lifecycle.go`
- Test `internalv2/app/app_test.go`
- Modify `internalv2/app/FLOW.md`

- [ ] **Step 1: Read package flow**

Run: `sed -n '1,220p' internalv2/app/FLOW.md`

Expected: output says lifecycle starts the presence rehydrate worker before this task.

- [ ] **Step 2: Add failing app config and worker tests**

Replace rehydrate worker tests with:

```go
func TestDefaultPresenceConfigUsesTouchDefaults(t *testing.T) {
	cfg := defaultPresenceConfig(PresenceConfig{})
	require.Equal(t, 3*time.Second, cfg.ActivationTimeout)
	require.Equal(t, time.Second, cfg.TouchFlushInterval)
	require.Equal(t, 512, cfg.TouchBatchSize)
	require.Equal(t, 90*time.Second, cfg.RouteTTL)
	require.NoError(t, validatePresenceConfig(cfg))
}

func TestValidatePresenceConfigRejectsInvalidTouchValues(t *testing.T) {
	require.ErrorIs(t, validatePresenceConfig(PresenceConfig{ActivationTimeout: time.Second, TouchFlushInterval: -time.Second, TouchBatchSize: 1, RouteTTL: time.Second}), ErrInvalidConfig)
	require.ErrorIs(t, validatePresenceConfig(PresenceConfig{ActivationTimeout: time.Second, TouchFlushInterval: time.Second, TouchBatchSize: -1, RouteTTL: time.Second}), ErrInvalidConfig)
	require.ErrorIs(t, validatePresenceConfig(PresenceConfig{ActivationTimeout: time.Second, TouchFlushInterval: time.Second, TouchBatchSize: 1, RouteTTL: -time.Second}), ErrInvalidConfig)
}

func TestPresenceTouchWorkerFlushesDirtyRoutesByTarget(t *testing.T) {
	local := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conn := online.OnlineConn{UID: "u1", HashSlot: 7, OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: 21, SessionID: 101, ConnectedUnix: 10, LastActivityUnix: 20}
	require.NoError(t, local.RegisterPending(conn))
	require.NoError(t, local.MarkActive(conn.SessionID))
	_, ok := local.MarkTouched(conn.SessionID, 30)
	require.True(t, ok)
	authority := &recordingTouchAuthority{
		targets: map[string]presence.RouteTarget{
			"u1": {HashSlot: 7, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		},
		done: make(chan struct{}),
	}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:         local,
		Authority:     authority,
		FlushInterval: time.Hour,
		BatchSize:     512,
	})

	worker.flushOnce(context.Background(), time.Unix(40, 0))

	require.Len(t, authority.batches, 1)
	require.Equal(t, authority.targets["u1"], authority.batches[0].target)
	require.Len(t, authority.batches[0].routes, 1)
	require.Equal(t, int64(30), authority.batches[0].routes[0].LastSeenUnix)
}

func TestPresenceTouchWorkerRequeuesFailedFlush(t *testing.T) {
	local := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conn := online.OnlineConn{UID: "u1", HashSlot: 7, OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: 21, SessionID: 101, ConnectedUnix: 10, LastActivityUnix: 20}
	require.NoError(t, local.RegisterPending(conn))
	require.NoError(t, local.MarkActive(conn.SessionID))
	_, ok := local.MarkTouched(conn.SessionID, 30)
	require.True(t, ok)
	authority := &recordingTouchAuthority{
		targets: map[string]presence.RouteTarget{
			"u1": {HashSlot: 7, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		},
		err: authoritypresence.ErrRouteNotReady,
	}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{Local: local, Authority: authority, FlushInterval: time.Hour, BatchSize: 512})

	worker.flushOnce(context.Background(), time.Unix(40, 0))

	require.Len(t, local.DrainTouched(10), 1)
}

func TestPresenceTouchWorkerUpdatesAuthorityDirectoryFromEvents(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent, 1)
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Events:    events,
		Directory: directory,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, worker.Start(ctx))
	events <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{{
		HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4,
	}}}
	require.Eventually(t, func() bool {
		return len(directory.become) == 1 && directory.become[0].HashSlot == 7
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, worker.Stop(context.Background()))
}
```

- [ ] **Step 3: Run app tests to verify failure**

Run: `go test ./internalv2/app`

Expected: FAIL with old rehydrate config fields or missing touch worker.

- [ ] **Step 4: Replace presence config**

Use:

```go
// PresenceConfig contains connection presence touch and authority TTL settings.
type PresenceConfig struct {
	// ActivationTimeout bounds one gateway session activation against the UID authority.
	ActivationTimeout time.Duration
	// TouchFlushInterval controls how often owner-local dirty routes are batched to UID authorities.
	TouchFlushInterval time.Duration
	// TouchBatchSize limits owner-local dirty routes sent by one touch flush pass.
	TouchBatchSize int
	// RouteTTL removes authority routes that have not received owner activity within this duration.
	RouteTTL time.Duration
}
```

Default only zero values, so explicit negative values survive for validation:

```go
if cfg.ActivationTimeout == 0 {
	cfg.ActivationTimeout = 3 * time.Second
}
if cfg.TouchFlushInterval == 0 {
	cfg.TouchFlushInterval = time.Second
}
if cfg.TouchBatchSize == 0 {
	cfg.TouchBatchSize = 512
}
if cfg.RouteTTL == 0 {
	cfg.RouteTTL = 90 * time.Second
}
```

Validation:

```go
if cfg.ActivationTimeout < 0 { return fmt.Errorf("%w: presence activation timeout must be >= 0", ErrInvalidConfig) }
if cfg.TouchFlushInterval < 0 { return fmt.Errorf("%w: presence touch flush interval must be >= 0", ErrInvalidConfig) }
if cfg.TouchBatchSize < 0 { return fmt.Errorf("%w: presence touch batch size must be >= 0", ErrInvalidConfig) }
if cfg.RouteTTL < 0 { return fmt.Errorf("%w: presence route ttl must be >= 0", ErrInvalidConfig) }
```

After defaults, all three touch values are positive.

- [ ] **Step 5: Create touch worker**

Create `presence_touch.go` with:

```go
type presenceTouchLocalRegistry interface {
	DrainTouched(limit int) []online.OnlineConn
	RequeueTouched([]online.OnlineConn)
}

type presenceOwnerLocalRegistry interface {
	Connection(sessionID uint64) (online.OnlineConn, bool)
	MarkClosingAndUnregister(sessionID uint64) (online.OnlineConn, bool)
}

type presenceTouchAuthority interface {
	ResolveRouteTarget(uid string) (presence.RouteTarget, error)
	TouchRoutesTo(context.Context, presence.RouteTarget, []presence.Route) error
}

type presenceTouchDirectory interface {
	BecomeAuthority(presence.RouteTarget)
	LoseAuthority(uint16)
	ExpireRoutes(time.Time, time.Duration) int
}

type presenceTouchWorkerOptions struct {
	NodeID        uint64
	Events        <-chan clusterv2.RouteAuthorityEvent
	Watch         func() <-chan clusterv2.RouteAuthorityEvent
	Initial       func() []clusterv2.RouteAuthority
	Local         presenceTouchLocalRegistry
	Authority     presenceTouchAuthority
	Directory     presenceTouchDirectory
	FlushInterval time.Duration
	BatchSize     int
	RouteTTL      time.Duration
}
```

Implement `Start`, `Stop`, event watch, `handleAuthority`, `flushOnce`, and `routeFromOnlineConn`. Keep one watch goroutine and one ticker goroutine. No per-hash-slot workers are created.

Core flush behavior:

```go
func (w *presenceTouchWorker) flushOnce(ctx context.Context, now time.Time) {
	if w.opts.Directory != nil && w.opts.RouteTTL > 0 {
		w.opts.Directory.ExpireRoutes(now, w.opts.RouteTTL)
	}
	if w.opts.Local == nil || w.opts.Authority == nil {
		return
	}
	batch := w.opts.Local.DrainTouched(w.opts.BatchSize)
	if len(batch) == 0 {
		return
	}
	groups := make(map[presence.RouteTarget][]presence.Route)
	requeue := make([]online.OnlineConn, 0)
	for _, conn := range batch {
		target, err := w.opts.Authority.ResolveRouteTarget(conn.UID)
		if err != nil {
			requeue = append(requeue, conn)
			continue
		}
		groups[target] = append(groups[target], routeFromOnlineConn(conn))
	}
	for target, routes := range groups {
		if err := w.opts.Authority.TouchRoutesTo(ctx, target, routes); err != nil {
			for _, route := range routes {
				requeue = append(requeue, onlineConnFromRoute(route))
			}
		}
	}
	if len(requeue) > 0 {
		w.opts.Local.RequeueTouched(requeue)
	}
}
```

`onlineConnFromRoute` must fill UID, owner identity, session ID, connected time, and last activity so `RequeueTouched` can verify the active route identity.

- [ ] **Step 6: Wire app to touch worker**

In `New`, replace `newPresenceRehydrateWorker` with:

```go
app.presenceWorker = newPresenceTouchWorker(presenceTouchWorkerOptions{
	NodeID:        presenceNode.NodeID(),
	Watch:         presenceNode.WatchRouteAuthorities,
	Initial:       app.currentPresenceAuthorities,
	Local:         app.online,
	Authority:     client,
	Directory:     directory,
	FlushInterval: app.cfg.Presence.TouchFlushInterval,
	BatchSize:     app.cfg.Presence.TouchBatchSize,
	RouteTTL:      app.cfg.Presence.RouteTTL,
})
```

Update `presenceDirectoryAuthority`:

```go
func (a presenceDirectoryAuthority) TouchRoutes(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.TouchRoutes(target, routes)
}
```

Update `presenceOwnerActions` so its `local` field uses the owner-action interface that remains in `presence_touch.go`:

```go
type presenceOwnerActions struct {
	// local stores real sessions owned by this node.
	local presenceOwnerLocalRegistry
}
```

Update `activationTimeoutPresence`:

```go
func (p activationTimeoutPresence) Touch(ctx context.Context, cmd presence.TouchCommand) error {
	if p.next == nil {
		return nil
	}
	return p.next.Touch(ctx, cmd)
}
```

- [ ] **Step 7: Update app flow doc**

Replace lifecycle and rehydrate sections with:

```text
Start(ctx)
  -> cluster.Start(ctx)
  -> wait for clusterv2 write routing when the cluster runtime exposes route snapshots
  -> presence touch worker Start(ctx)
  -> api.Start()
  -> gateway.Start()
```

```markdown
## Presence Touch Worker

The worker watches route-authority events only to install or clear local `runtime/presence.Directory` authority epochs. It also drains owner-local dirty touches in bounded batches, groups them by current UID authority target, calls `TouchRoutesTo`, requeues failed batches for still-active sessions, and expires untouched local authority routes by TTL. It never scans all owner sessions during authority changes.
```

- [ ] **Step 8: Run app tests**

Run: `go test ./internalv2/app`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internalv2/app
git rm internalv2/app/presence_rehydrate.go
git commit -m "feat: wire internalv2 presence touch worker"
```

## Task 8: Config Parser And Examples

**Files:**
- Modify `cmd/wukongimv2/config.go`
- Modify `cmd/wukongimv2/config_test.go`
- Modify listed config example files
- Modify `AGENTS.md` only if descriptions mention rehydrate

- [ ] **Step 1: Add failing config parser tests**

In `cmd/wukongimv2/config_test.go`, replace rehydrate assertions with:

```go
require.Equal(t, 2*time.Second, cfg.Presence.TouchFlushInterval)
require.Equal(t, 1024, cfg.Presence.TouchBatchSize)
require.Equal(t, 2*time.Minute, cfg.Presence.RouteTTL)
```

Use config input:

```text
WK_PRESENCE_TOUCH_FLUSH_INTERVAL=2s
WK_PRESENCE_TOUCH_BATCH_SIZE=1024
WK_PRESENCE_ROUTE_TTL=2m
```

Add negative value tests:

```go
{"WK_PRESENCE_TOUCH_FLUSH_INTERVAL=-1s", "WK_PRESENCE_TOUCH_FLUSH_INTERVAL"},
{"WK_PRESENCE_TOUCH_BATCH_SIZE=-1", "WK_PRESENCE_TOUCH_BATCH_SIZE"},
{"WK_PRESENCE_ROUTE_TTL=-1s", "WK_PRESENCE_ROUTE_TTL"},
```

- [ ] **Step 2: Run config tests to verify failure**

Run: `go test ./cmd/wukongimv2`

Expected: FAIL with missing new config parsing or old rehydrate assertions.

- [ ] **Step 3: Parse new config keys**

In `cmd/wukongimv2/config.go`, remove old rehydrate parsing blocks and add:

```go
if raw := configValue(values, "WK_PRESENCE_TOUCH_FLUSH_INTERVAL"); raw != "" {
	interval, err := parseDuration("WK_PRESENCE_TOUCH_FLUSH_INTERVAL", raw)
	if err != nil {
		return app.Config{}, err
	}
	if interval < 0 {
		return app.Config{}, fmt.Errorf("parse WK_PRESENCE_TOUCH_FLUSH_INTERVAL: value must be >= 0")
	}
	cfg.Presence.TouchFlushInterval = interval
}
if raw := configValue(values, "WK_PRESENCE_TOUCH_BATCH_SIZE"); raw != "" {
	batchSize, err := parseInt("WK_PRESENCE_TOUCH_BATCH_SIZE", raw)
	if err != nil {
		return app.Config{}, err
	}
	if batchSize < 0 {
		return app.Config{}, fmt.Errorf("parse WK_PRESENCE_TOUCH_BATCH_SIZE: value must be >= 0")
	}
	cfg.Presence.TouchBatchSize = batchSize
}
if raw := configValue(values, "WK_PRESENCE_ROUTE_TTL"); raw != "" {
	ttl, err := parseDuration("WK_PRESENCE_ROUTE_TTL", raw)
	if err != nil {
		return app.Config{}, err
	}
	if ttl < 0 {
		return app.Config{}, fmt.Errorf("parse WK_PRESENCE_ROUTE_TTL: value must be >= 0")
	}
	cfg.Presence.RouteTTL = ttl
}
```

- [ ] **Step 4: Update examples**

Replace the rehydrate block with:

```text
# Presence route activation, owner touch batching, and authority TTL.
WK_PRESENCE_ACTIVATION_TIMEOUT=3s
WK_PRESENCE_TOUCH_FLUSH_INTERVAL=1s
WK_PRESENCE_TOUCH_BATCH_SIZE=512
WK_PRESENCE_ROUTE_TTL=90s
```

Apply the same block in all listed config example files and script configs.

- [ ] **Step 5: Run config tests**

Run: `go test ./cmd/wukongimv2`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wukongimv2 wukongim.conf.example scripts/wukongimv2 AGENTS.md
git commit -m "chore: update internalv2 presence touch config"
```

## Task 9: Remove Rehydrate References And Cross-Package Compile Fixes

**Files:**
- Search and modify all files returned by `rg -n "Rehydrate|rehydrate|VisitActiveByHashSlot|RouteCursor|byHashSlot" internalv2 cmd/wukongimv2 scripts wukongim.conf.example AGENTS.md`
- Modify tests that still implement old interfaces
- Modify FLOW docs returned by the search

- [ ] **Step 1: Search remaining references**

Run: `rg -n "Rehydrate|rehydrate|REHYDRATE|VisitActiveByHashSlot|RouteCursor|byHashSlot" internalv2 cmd/wukongimv2 scripts wukongim.conf.example AGENTS.md`

Expected before cleanup: only files not yet touched by previous tasks appear. Expected after cleanup: no output.

- [ ] **Step 2: Remove stale interface methods from tests and fakes**

When a fake authority still has:

```go
RehydrateRoutes(...)
CommitRehydratedRoute(...)
AbortRehydratedRoute(...)
```

replace it with:

```go
TouchRoutes(context.Context, presence.RouteTarget, []presence.Route) error
```

or:

```go
TouchRoutesTo(context.Context, presence.RouteTarget, []presence.Route) error
```

matching the interface under test.

- [ ] **Step 3: Run targeted package tests**

Run:

```bash
go test ./internalv2/runtime/online ./internalv2/runtime/presence ./internalv2/usecase/presence ./internalv2/access/gateway ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app ./cmd/wukongimv2
```

Expected: PASS.

- [ ] **Step 4: Commit cleanup if files changed**

```bash
git add internalv2 cmd/wukongimv2 scripts wukongim.conf.example AGENTS.md
git commit -m "chore: remove internalv2 presence rehydrate references"
```

If Step 1 already has no output and Step 3 passes without file changes, skip this commit.

## Task 10: Final Verification And Review

**Files:**
- No planned file edits.

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
go test ./internalv2/... ./cmd/wukongimv2
```

Expected: PASS.

- [ ] **Step 2: Run broader related tests**

Run:

```bash
go test ./pkg/clusterv2/... ./pkg/gateway/... ./internalv2/...
```

Expected: PASS.

- [ ] **Step 3: Inspect git diff**

Run: `git diff --stat HEAD`

Expected: no unexpected unrelated files.

Run: `git diff HEAD -- internalv2/runtime/presence internalv2/runtime/online internalv2/usecase/presence internalv2/access/gateway internalv2/access/node internalv2/infra/cluster internalv2/app cmd/wukongimv2 wukongim.conf.example scripts/wukongimv2 AGENTS.md`

Expected: diff shows touch batching and TTL replacement, with no rehydrate code path.

- [ ] **Step 4: Request subagent code review**

Use a review-focused subagent to inspect:

- owner dirty touch correctness under disconnect and replacement
- authority `TouchRoutes` conflict behavior
- app worker retry and TTL behavior
- stale rehydrate references

Expected: no P0/P1 issues before final response.

- [ ] **Step 5: Commit final review fixes if needed**

```bash
git add <reviewed-files>
git commit -m "fix: harden internalv2 presence touch routing"
```

Only create this commit when review fixes changed files.
