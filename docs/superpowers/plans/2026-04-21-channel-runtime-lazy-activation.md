# Channel Runtime Lazy Activation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove channel runtime full-scan prewarm, replace it with authoritative on-demand activation for business and replication paths, and ensure cold followers can self-start after leader wake-up.

**Architecture:** Keep `slot/meta` authoritative, keep only hot runtime objects locally, and split routing metadata updates from runtime materialization. Business paths continue to refresh by `ChannelID`; replication paths gain `ChannelKey` activation via an injected runtime activator plus a leader-side wake-up probe for cold followers.

**Tech Stack:** Go, Pebble-backed channel store, slot authoritative RPC, `pkg/channel/runtime`, `internal/app`, `stretchr/testify`, targeted `go test`

---

## Scope Note

The approved spec includes idle eviction as a later rollout stage. This implementation plan intentionally delivers the first shippable slice:

- remove periodic full scan
- add on-demand activation by ID/key
- split routing metadata from runtime activation
- activate replication ingress on miss
- add leader wake-up for cold followers

Do **not** add idle eviction or new user-facing config knobs in this plan. Treat those as a follow-up plan once the lazy activation path is stable under load.

## File Structure

### Existing files to modify

- `internal/app/channelmeta.go` — shrink lifecycle behavior so `Start()` no longer launches the one-second full-scan ticker; keep `RefreshChannelMeta()` as the business-facing entry point and delegate to new activation helpers.
- `internal/app/channelcluster.go` — split “update routing meta” from “ensure/remove runtime”, so non-local replicas can cache leader routing without creating runtime.
- `internal/app/build.go` — wire the new app-side activator into `channelMetaSync` and `pkg/channel/runtime.Config`.
- `internal/app/channelmeta_test.go` — replace current full-scan assumptions with activation-focused tests and add negative/non-local behavior coverage.
- `internal/app/lifecycle_test.go` — assert app start still succeeds after `channelMetaSync.Start()` becomes a no-op lifecycle hook.
- `internal/app/multinode_integration_test.go` — verify a cold follower activates after leader append + wake-up instead of startup prewarm.
- `pkg/channel/handler/key.go` — add the inverse parser for `channel/<type>/<base64raw(id)>`.
- `pkg/channel/handler/key_test.go` — cover successful and invalid key parsing cases.
- `pkg/channel/runtime/types.go` — extend runtime config with an injected activator interface and activation source enum.
- `pkg/channel/runtime/backpressure.go` — teach `ServeFetch()` and `ServeReconcileProbe()` to retry once after activation on runtime miss.
- `pkg/channel/runtime/longpoll.go` — teach long-poll lane open/membership paths to activate channels by key when needed.
- `pkg/channel/runtime/replicator.go` — add leader-side wake-up scheduling/dedup for cold followers.
- `pkg/channel/runtime/session_test.go` — add miss-then-activate tests for fetch/probe and wake-up behavior.
- `pkg/channel/runtime/longpoll_test.go` — add long-poll session activation coverage.
- `pkg/channel/FLOW.md` — update the channel flow description so it matches lazy activation and cold-follower wake-up.

### New files to create

- `internal/app/channelmeta_activate.go` — app-side `ActivateByID()` / `ActivateByKey()` orchestration, authoritative lookup, and apply sequencing.
- `internal/app/channelmeta_cache.go` — bounded in-memory positive/negative cache and `singleflight` bookkeeping for activation.
- `pkg/channel/runtime/activation.go` — runtime-local activator interface, source labels, and helper that wraps “lookup miss -> activate -> retry once”.

Keep the new files focused:

- app package owns authority lookups, caching, and apply policy
- runtime package owns the ingress retry contract and wake-up mechanics
- key parsing stays with the existing key encoder in `pkg/channel/handler`

## Task 1: Add `ChannelKey` parsing and runtime activator interfaces

**Files:**
- Create: `pkg/channel/runtime/activation.go`
- Modify: `pkg/channel/runtime/types.go`
- Modify: `pkg/channel/handler/key.go`
- Test: `pkg/channel/handler/key_test.go`

- [ ] **Step 1: Write the failing key parser tests**

```go
func TestParseChannelKey(t *testing.T) {
    id, err := ParseChannelKey(channel.ChannelKey("channel/2/ZzFAeTI"))
    require.NoError(t, err)
    require.Equal(t, channel.ChannelID{ID: "g1@y2", Type: 2}, id)
}

func TestParseChannelKeyRejectsInvalidFormat(t *testing.T) {
    _, err := ParseChannelKey(channel.ChannelKey("group-21"))
    require.ErrorIs(t, err, channel.ErrInvalidMeta)
}
```

- [ ] **Step 2: Run the parser tests and verify they fail**

Run: `go test ./pkg/channel/handler -run 'TestParseChannelKey|TestParseChannelKeyRejectsInvalidFormat' -count=1`
Expected: FAIL because `ParseChannelKey` does not exist yet.

- [ ] **Step 3: Add `ParseChannelKey` next to `KeyFromChannelID`**

```go
func ParseChannelKey(key channel.ChannelKey) (channel.ChannelID, error) {
    parts := strings.SplitN(string(key), "/", 3)
    if len(parts) != 3 || parts[0] != strings.TrimSuffix(keyPrefix, "/") {
        return channel.ChannelID{}, channel.ErrInvalidMeta
    }
    channelType, err := strconv.ParseUint(parts[1], 10, 8)
    if err != nil {
        return channel.ChannelID{}, channel.ErrInvalidMeta
    }
    rawID, err := base64.RawURLEncoding.DecodeString(parts[2])
    if err != nil {
        return channel.ChannelID{}, channel.ErrInvalidMeta
    }
    return channel.ChannelID{ID: string(rawID), Type: uint8(channelType)}, nil
}
```

- [ ] **Step 4: Add the runtime activator contract**

Create `pkg/channel/runtime/activation.go` with a narrow interface so runtime can ask the app layer to activate by key without importing `internal/app`:

```go
type ActivationSource string

const (
    ActivationSourceBusiness ActivationSource = "business"
    ActivationSourceFetch    ActivationSource = "fetch"
    ActivationSourceProbe    ActivationSource = "probe"
    ActivationSourceLaneOpen ActivationSource = "lane_open"
)

type Activator interface {
    ActivateByKey(ctx context.Context, key core.ChannelKey, source ActivationSource) (core.Meta, error)
}
```

Then add `Activator Activator` to `pkg/channel/runtime/types.go:Config`.

- [ ] **Step 5: Re-run the focused tests**

Run: `go test ./pkg/channel/handler ./pkg/channel/runtime -run 'TestParseChannelKey|TestSessionServeFetchReturnsReplicaFetchResult' -count=1`
Expected: PASS for the parser tests, PASS for the runtime smoke test, proving the new interface wiring is compile-safe.

- [ ] **Step 6: Commit the interface/parser slice**

```bash
git add pkg/channel/handler/key.go pkg/channel/handler/key_test.go pkg/channel/runtime/activation.go pkg/channel/runtime/types.go
git commit -m "refactor: add channel key activation interfaces"
```

## Task 2: Refactor app channel meta into an on-demand activator and remove periodic full scan

**Files:**
- Create: `internal/app/channelmeta_activate.go`
- Create: `internal/app/channelmeta_cache.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/channelcluster.go`
- Modify: `internal/app/build.go`
- Test: `internal/app/channelmeta_test.go`
- Test: `internal/app/lifecycle_test.go`

- [ ] **Step 1: Write the failing activation tests before touching behavior**

Add tests that lock the new contract:

```go
func TestChannelMetaSyncRefreshCachesRemoteRoutingMetaWithoutRuntime(t *testing.T) {
    // authoritative meta exists, local node is not in Replicas
    // expect: no error, routing meta stored, runtime not ensured
}

func TestChannelMetaSyncStartDoesNotScanAuthoritativeMetas(t *testing.T) {
    // Start should not call ListChannelRuntimeMeta anymore
}

func TestChannelMetaSyncActivateByKeyUsesAuthoritativeLookupAndSingleflight(t *testing.T) {
    // concurrent ActivateByKey calls share one authoritative fetch
}
```

- [ ] **Step 2: Run the app tests to verify they fail on the old behavior**

Run: `go test ./internal/app -run 'TestChannelMetaSyncRefreshCachesRemoteRoutingMetaWithoutRuntime|TestChannelMetaSyncStartDoesNotScanAuthoritativeMetas|TestChannelMetaSyncActivateByKeyUsesAuthoritativeLookupAndSingleflight' -count=1`
Expected: FAIL because refresh still rejects non-local replicas, `Start()` still scans, and `ActivateByKey` does not exist.

- [ ] **Step 3: Move activation logic into focused files**

Create `internal/app/channelmeta_activate.go` with the orchestration methods:

```go
func (s *channelMetaSync) ActivateByID(ctx context.Context, id channel.ChannelID, source channelruntime.ActivationSource) (channel.Meta, error)
func (s *channelMetaSync) ActivateByKey(ctx context.Context, key channel.ChannelKey, source channelruntime.ActivationSource) (channel.Meta, error)
```

`ActivateByKey` should:

1. parse the key via `channelhandler.ParseChannelKey`
2. check positive/negative cache
3. use `singleflight` around authoritative lookup + reconcile
4. update routing meta locally
5. ensure runtime only when local node belongs to `Replicas`
6. remove stale local runtime when local node no longer belongs to `Replicas`

- [ ] **Step 4: Add a tiny cache helper instead of growing `channelmeta.go`**

Create `internal/app/channelmeta_cache.go` with positive/negative entries and TTL checks:

```go
type channelActivationCache struct {
    mu       sync.Mutex
    positive map[channel.ChannelKey]cachedMeta
    negative map[channel.ChannelKey]cachedNegative
}
```

Keep TTLs as internal constants in this slice. Do **not** add config plumbing yet.

- [ ] **Step 5: Change `channelMetaSync.Start()` into a no-op lifecycle hook**

In `internal/app/channelmeta.go`:

- delete the startup `syncOnce()` call
- delete the ticker goroutine
- keep `Start()` / `Stop()` concurrency-safe so existing app lifecycle tests still pass
- keep `RefreshChannelMeta()` as the business-facing alias that calls `ActivateByID(ctx, id, channelruntime.ActivationSourceBusiness)`

- [ ] **Step 6: Split routing apply from runtime materialization in `appChannelCluster`**

Change `internal/app/channelcluster.go` so app-side activation can do both of these intentionally:

```go
func (c *appChannelCluster) ApplyRoutingMeta(meta channel.Meta) error
func (c *appChannelCluster) EnsureLocalRuntime(meta channel.Meta) error
func (c *appChannelCluster) RemoveLocalRuntime(key channel.ChannelKey) error
```

Use those helpers inside activation instead of forcing every authoritative refresh through the old “always upsert runtime” path.

- [ ] **Step 7: Wire the activator into the app build**

In `internal/app/build.go`:

- initialize the new cache / `singleflight` support on `app.channelMetaSync`
- pass `app.channelMetaSync` into `channelruntime.Config{Activator: ...}`
- keep `access/node` on `RefreshChannelMeta()` for business refreshes

- [ ] **Step 8: Re-run the focused app tests and then the broader app package**

Run:
- `go test ./internal/app -run 'TestChannelMetaSyncRefreshCachesRemoteRoutingMetaWithoutRuntime|TestChannelMetaSyncStartDoesNotScanAuthoritativeMetas|TestChannelMetaSyncActivateByKeyUsesAuthoritativeLookupAndSingleflight' -count=1`
- `go test ./internal/app -run 'TestChannelMetaSync|TestStart|TestStop' -count=1`

Expected: PASS. `Start()` should no longer list all metas, and remote-only refresh should retain routing info without creating runtime.

- [ ] **Step 9: Commit the app activation slice**

```bash
git add internal/app/channelmeta.go internal/app/channelmeta_activate.go internal/app/channelmeta_cache.go internal/app/channelcluster.go internal/app/build.go internal/app/channelmeta_test.go internal/app/lifecycle_test.go
git commit -m "refactor: make channel meta activation demand-driven"
```

## Task 3: Teach replication ingress to activate on runtime miss

**Files:**
- Modify: `pkg/channel/runtime/backpressure.go`
- Modify: `pkg/channel/runtime/longpoll.go`
- Test: `pkg/channel/runtime/session_test.go`
- Test: `pkg/channel/runtime/longpoll_test.go`

- [ ] **Step 1: Write the failing runtime ingress tests**

Add tests like:

```go
func TestSessionServeFetchActivatesMissingChannelOnce(t *testing.T) {
    // runtime miss -> activator called -> channel ensured -> fetch retried once
}

func TestSessionServeReconcileProbeActivatesMissingChannelOnce(t *testing.T) {
    // runtime miss -> activator called -> probe retried once
}

func TestLongPollOpenActivatesTrackedMissingChannels(t *testing.T) {
    // lane open with membership for a cold local replica should activate it
}
```

Use the existing session test env and add a fake activator that records calls and optionally ensures channels into the runtime.

- [ ] **Step 2: Run the focused runtime tests and verify failure**

Run: `go test ./pkg/channel/runtime -run 'TestSessionServeFetchActivatesMissingChannelOnce|TestSessionServeReconcileProbeActivatesMissingChannelOnce|TestLongPollOpenActivatesTrackedMissingChannels' -count=1`
Expected: FAIL because current ingress returns `ErrChannelNotFound` on miss.

- [ ] **Step 3: Add a single helper for “lookup miss -> activate -> retry once”**

In `pkg/channel/runtime/activation.go` add a helper like:

```go
func (r *runtime) ensureChannelForIngress(ctx context.Context, key core.ChannelKey, source ActivationSource) (*channel, bool, error) {
    if ch, ok := r.lookupChannel(key); ok {
        return ch, false, nil
    }
    if r.cfg.Activator == nil {
        return nil, false, ErrChannelNotFound
    }
    if _, err := r.cfg.Activator.ActivateByKey(ctx, key, source); err != nil {
        return nil, false, err
    }
    ch, ok := r.lookupChannel(key)
    if !ok {
        return nil, false, ErrChannelNotFound
    }
    return ch, true, nil
}
```

- [ ] **Step 4: Use the helper in fetch/probe/long-poll ingress**

Apply the helper in:

- `pkg/channel/runtime/backpressure.go:ServeFetch()`
- `pkg/channel/runtime/backpressure.go:ServeReconcileProbe()`
- `pkg/channel/runtime/longpoll.go:ServeLanePoll()` during lane open / membership adoption

Rules:

- retry at most once after activation
- preserve the original epoch/generation checks after the channel exists
- if activator says “not local replica” or `not found`, return the existing miss/error path

- [ ] **Step 5: Re-run the runtime ingress tests and package regression suite**

Run:
- `go test ./pkg/channel/runtime -run 'TestSessionServeFetchActivatesMissingChannelOnce|TestSessionServeReconcileProbeActivatesMissingChannelOnce|TestLongPollOpenActivatesTrackedMissingChannels' -count=1`
- `go test ./pkg/channel/runtime -run 'TestSessionServeFetch|TestRuntimeLongPoll' -count=1`

Expected: PASS, with existing fetch/long-poll behavior unchanged once the channel is present.

- [ ] **Step 6: Commit the ingress activation slice**

```bash
git add pkg/channel/runtime/activation.go pkg/channel/runtime/backpressure.go pkg/channel/runtime/longpoll.go pkg/channel/runtime/session_test.go pkg/channel/runtime/longpoll_test.go
git commit -m "feat: activate cold channel runtime on replication ingress"
```

## Task 4: Add leader wake-up for cold followers

**Files:**
- Modify: `pkg/channel/runtime/replicator.go`
- Modify: `pkg/channel/runtime/channel.go`
- Test: `pkg/channel/runtime/session_test.go`
- Test: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Write the failing wake-up tests**

Add one unit test and one integration test:

```go
func TestRuntimeLeaderSchedulesWakeUpProbeForColdFollower(t *testing.T) {
    // leader append on a newly activated channel should enqueue one probe to the follower
}

func TestMultiNodeColdFollowerActivatesAfterLeaderAppend(t *testing.T) {
    // start a multi-node app, keep follower cold, append on leader, expect follower runtime to appear only after wake-up/probe
}
```

- [ ] **Step 2: Run the targeted tests and verify they fail**

Run:
- `go test ./pkg/channel/runtime -run 'TestRuntimeLeaderSchedulesWakeUpProbeForColdFollower' -count=1`
- `go test ./internal/app -run 'TestMultiNodeColdFollowerActivatesAfterLeaderAppend' -count=1`

Expected: FAIL because the leader does not currently send an explicit wake-up for a cold follower.

- [ ] **Step 3: Add deduped wake-up state in runtime**

Add a small per `(channelKey, peer)` wake-up dedupe table near the existing retry state in `pkg/channel/runtime/replicator.go`.

Keep the first implementation simple:

```go
type wakeUpState struct {
    pending bool
    lastSent time.Time
}
```

Rules:

- send at most one outstanding wake-up per `(channel, follower)`
- clear/delay retries on success/failure using the existing follower retry interval as the minimum backoff

- [ ] **Step 4: Schedule a probe from the leader when a channel becomes hot**

Use the existing leader-side append hook path (`onChannelAppend` / replication scheduling) to enqueue a `MessageKindReconcileProbeRequest` to followers that should replicate but have no active progress / lane session.

Do **not** add a new wire RPC. Reuse the existing probe request envelope.

- [ ] **Step 5: Re-run the wake-up tests and a broader multi-node regression**

Run:
- `go test ./pkg/channel/runtime -run 'TestRuntimeLeaderSchedulesWakeUpProbeForColdFollower' -count=1`
- `go test ./internal/app -run 'TestMultiNodeColdFollowerActivatesAfterLeaderAppend|TestConversationSync' -count=1`

Expected: PASS. The follower should not be prewarmed at startup, but it should appear after the leader sends the wake-up probe and the follower activates on ingress.

- [ ] **Step 6: Commit the wake-up slice**

```bash
git add pkg/channel/runtime/replicator.go pkg/channel/runtime/channel.go pkg/channel/runtime/session_test.go internal/app/multinode_integration_test.go
git commit -m "feat: wake cold followers on leader append"
```

## Task 5: Update flow docs and run the final targeted verification set

**Files:**
- Modify: `pkg/channel/FLOW.md`
- Modify: `docs/superpowers/specs/2026-04-21-channel-runtime-lazy-activation-design.md` (only if implementation forces a design clarification)

- [ ] **Step 1: Update `pkg/channel/FLOW.md` to describe lazy activation**

Document these changes:

- startup no longer prewarms every local channel
- business refresh activates by `ChannelID`
- replication ingress activates by `ChannelKey`
- leader wake-up probe is how cold followers join the follower-pull loop

- [ ] **Step 2: Run the exact verification commands used by this rollout**

Run:

```bash
go test ./pkg/channel/handler -run 'TestParseChannelKey|TestParseChannelKeyRejectsInvalidFormat' -count=1
go test ./internal/app -run 'TestChannelMetaSync|TestStart|TestStop|TestMultiNodeColdFollowerActivatesAfterLeaderAppend' -count=1
go test ./pkg/channel/runtime -run 'TestSessionServeFetch|TestSessionServeReconcileProbe|TestRuntimeLeaderSchedulesWakeUpProbeForColdFollower|TestRuntimeLongPoll' -count=1
```

Expected: all PASS.

- [ ] **Step 3: Run one wider safety pass before handoff**

Run: `go test ./internal/app ./pkg/channel/... -count=1`
Expected: PASS. If this is too slow locally, finish the three targeted commands above first, then run the broader pass before merging.

- [ ] **Step 4: Commit the docs + verification slice**

```bash
git add pkg/channel/FLOW.md docs/superpowers/specs/2026-04-21-channel-runtime-lazy-activation-design.md
git commit -m "docs: document lazy channel runtime activation"
```

## Notes for the Implementer

- Preserve the existing runtime-meta bootstrap behavior for business send refreshes. Replication ingress should never auto-bootstrap missing authoritative metadata.
- Keep all new caches in-memory only.
- Avoid importing `internal/app` from `pkg/channel/runtime`; the new activator interface is the boundary.
- Keep error handling fail-closed: when authoritative lookup fails, do not create speculative runtime state.
- Keep retries explicit and bounded: one activation retry in ingress paths, no unbounded wake-up loops.
- If implementation changes the flow materially, update both `pkg/channel/FLOW.md` and the design spec in the same branch.
