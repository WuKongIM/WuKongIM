# Channel Plane Multiple Reactors Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move durable send routing into a channel-centric reactor plane so `message.App` no longer knows how to resolve slot leaders, channel leaders, or remote append forwarding.

**Architecture:** `internal/runtime/channelplane` exposes a synchronous append facade backed by multiple channel reactors. Each channel hashes to one reactor, each `ChannelCell` serializes same-channel append effects, and peer lanes batch remote channel-leader writes across channels. `message.App` remains responsible for auth, permissions, hooks, request-scoped subscribers, and committed side effects; the plane owns route refresh, retry, local owner append, remote append, typed backpressure, and route invalidation.

**Tech Stack:** Go, existing `internal/runtime/channelmeta`, `internal/usecase/message`, `internal/app`, `internal/access/node`, `pkg/channel`, `pkg/slot`, `pkg/cluster`, `go test`, integration tests behind `-tags=integration`.

---

## Reference Docs

- Design: `docs/superpowers/specs/2026-05-22-channel-plane-reactors-design.md`
- Internal flow: `internal/FLOW.md`
- Channel metadata flow: `internal/runtime/channelmeta/FLOW.md`
- Channel execution flow: `pkg/channel/FLOW.md`
- Cluster flow: `pkg/cluster/FLOW.md`
- Slot metadata flow: `pkg/slot/FLOW.md`
- Current send path: `internal/usecase/message/send.go`, `internal/usecase/message/retry.go`
- Current local/remote append path: `internal/app/channelcluster.go`, `internal/access/node/channel_append_rpc.go`

## Scope

Implement now:

- Authoritative `RouteGeneration` plumbing from `pkg/slot/meta.ChannelRuntimeMeta` into `pkg/channel.Meta` and channelplane RPC epochs.
- New `internal/runtime/channelplane` package with reactor shards, channel cells, futures, route resolver, local append effect, compatibility remote bridge, peer reactor, typed errors, and trace/metrics hooks.
- `message.App` refactor to an append-only `ChannelAppender` dependency and removal of the old `sendBatchWithEnsuredMeta` helper.
- `appChannelCluster.AppendLocalBatch` as the plane-owned local append path while legacy `AppendBatch` remains only until peer RPC is wired.
- `AppendBatches` node RPC and `PeerReactor` batching, then removal of the legacy single-channel append RPC.
- A small `ChannelPlaneConfig` surface: `ReactorCount`, `PeerLaneCount`, and `PeerBatchMaxWait`. Keep other queue and byte budgets as code defaults in Phase 1, all with typed errors and metrics.
- FLOW documentation and config example updates.

Do not implement now:

- Moving `Fetch` or `Status` into the plane.
- Event-subscriber committed side effects.
- Mixed-version node RPC compatibility.
- A generic event bus outside the channel plane.
- Same-channel concurrent append pipelining; `ChannelMaxInflightAppends` stays fixed to `1`.

## File Structure

| Path | Responsibility |
| --- | --- |
| `pkg/channel/types.go` | Add `Meta.RouteGeneration` so local append, channelmeta, and node RPC use the same authoritative route identity. |
| `pkg/slot/meta/channel_runtime_meta.go` | Persist, normalize, and monotonic-merge `ChannelRuntimeMeta.RouteGeneration`. |
| `pkg/slot/meta/codec.go` and `pkg/slot/meta/*_test.go` | Encode/decode route generation and preserve legacy records. |
| `pkg/slot/fsm/command.go` and tests | Carry route generation through runtime-meta Raft commands and inspection. |
| `pkg/slot/proxy/runtime_meta_codec.go`, `runtime_meta_rpc.go`, tests | Carry route generation through authoritative slot RPCs. |
| `internal/runtime/channelmeta/*` | Project authoritative route generation into `channel.Meta` and cache invalidation. |
| `internal/runtime/channelplane/plane.go` | Public facade, lifecycle, append entrypoint. |
| `internal/runtime/channelplane/options.go` | Dependencies, defaults, and validation. |
| `internal/runtime/channelplane/command.go` | Append request/result DTOs and internal command shape. |
| `internal/runtime/channelplane/event.go` | Reactor event and effect completion types. |
| `internal/runtime/channelplane/future.go` | Request future, context cancellation, and shutdown completion helpers. |
| `internal/runtime/channelplane/reactor.go` | Reactor event loop, inbox, active-cell scheduling. |
| `internal/runtime/channelplane/channel_cell.go` | Per-channel state machine, pending queue, retry policy, same-channel ordering. |
| `internal/runtime/channelplane/scheduler.go` | Fair per-reactor active-channel scheduler. |
| `internal/runtime/channelplane/route.go` | `ChannelRoute`, `RouteEpoch`, validation, invalidation rules. |
| `internal/runtime/channelplane/resolver.go` | Route resolve effect and singleflight around channelmeta refresh. |
| `internal/runtime/channelplane/owner.go` | Local owner append effect interface and compatibility remote bridge. |
| `internal/runtime/channelplane/peer_reactor.go` | Per-peer lanes, batch windows, remote append aggregation. |
| `internal/runtime/channelplane/peer_rpc.go` | Neutral `AppendBatches` DTOs used by access/node adapters. |
| `internal/runtime/channelplane/errors.go` | Typed overload, stale route, not-leader, closed, and invalid request errors. |
| `internal/runtime/channelplane/metrics.go` | Observer interfaces for queue depths, retry counts, latencies, and result codes. |
| `internal/runtime/channelplane/tracing.go` | `sendtrace`/diagnostics stage helpers. |
| `internal/usecase/message/deps.go` | Replace routing dependencies with narrow `ChannelAppender`. |
| `internal/usecase/message/app.go` | Store `ChannelAppender`; remove `MetaRefresher` and `RemoteAppender` fields. |
| `internal/usecase/message/send.go` | Call plane append directly; keep auth, permission, hooks, subscribers, and committed side effects. |
| `internal/usecase/message/retry.go` | Delete after retry ownership moves into `channelplane`. |
| `internal/app/channelcluster.go` | Add `AppendLocalBatch`; keep legacy forwarding `AppendBatch` only until Task 5. |
| `internal/app/app.go`, `build.go`, `lifecycle.go`, `lifecycle_components.go` | Wire and lifecycle-manage `channelPlane`; pass it to `message.New`. |
| `internal/access/node/channel_plane_rpc.go` | New `AppendBatches` node RPC adapter. |
| `internal/access/node/channel_plane_codec.go` | Binary codec for append batch envelopes and typed results. |
| `internal/access/node/options.go`, `client.go` | Register new RPC and expose a peer appender client interface. |
| `internal/access/node/channel_append_rpc.go` | Delete or retire once `PeerReactor` is the only remote append path. |
| `internal/app/config.go`, `cmd/wukongim/config.go`, `wukongim.conf.example` | Add `WK_CHANNEL_PLANE_*` config defaults and examples. |
| `internal/FLOW.md`, `internal/runtime/channelmeta/FLOW.md`, `internal/runtime/channelplane/FLOW.md`, `pkg/channel/FLOW.md`, `pkg/cluster/FLOW.md`, `pkg/slot/FLOW.md` | Document the new send-routing ownership boundary. |

---

### Task 1: Add Authoritative Route Generation

**Files:**
- Modify: `pkg/channel/types.go`
- Modify: `pkg/slot/meta/channel_runtime_meta.go`
- Modify: `pkg/slot/meta/codec.go`
- Test: `pkg/slot/meta/channel_runtime_meta_test.go`
- Test: `pkg/slot/meta/codec_test.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Test: `pkg/slot/fsm/state_machine_test.go`
- Test: `pkg/slot/fsm/command_inspection_test.go`
- Modify: `pkg/slot/proxy/runtime_meta_codec.go`
- Modify: `pkg/slot/proxy/runtime_meta_rpc.go`
- Test: `pkg/slot/proxy/binary_rpc_test.go`
- Test: `pkg/slot/proxy/authoritative_rpc_test.go`
- Modify: `internal/runtime/channelmeta/resolver.go`
- Modify: `internal/runtime/channelmeta/cache.go`
- Test: `internal/runtime/channelmeta/resolver_test.go`
- Test: `internal/runtime/channelmeta/cache_test.go`

- [ ] **Step 1: Write failing route-generation metadata tests**

Add focused tests for slot meta and projection behavior:

```go
func TestChannelRuntimeMetaRouteGenerationRoundTrip(t *testing.T) {
    meta := testChannelRuntimeMeta("g-route", 2)
    meta.RouteGeneration = 42

    db := openTestDB(t)
    require.NoError(t, db.ForSlot(11).UpsertChannelRuntimeMeta(context.Background(), meta))

    got, err := db.ForSlot(11).GetChannelRuntimeMeta(context.Background(), meta.ChannelID, meta.ChannelType)
    require.NoError(t, err)
    require.Equal(t, uint64(42), got.RouteGeneration)
}
```

Add a resolver test that a refreshed authoritative record returns `channel.Meta.RouteGeneration`.

- [ ] **Step 2: Run RED route-generation tests**

Run:

```bash
GOWORK=off go test ./pkg/channel ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy ./internal/runtime/channelmeta -run 'RouteGeneration|RuntimeMeta' -count=1
```

Expected: FAIL because `RouteGeneration` does not exist or is not encoded/projected.

- [ ] **Step 3: Add route generation to durable metadata**

Add English comments to both structs:

```go
// RouteGeneration is the authoritative version of the channel routing record.
RouteGeneration uint64
```

Implement these rules in `pkg/slot/meta`:

- Legacy records with `RouteGeneration == 0` normalize to a non-zero value derived from `max(ChannelEpoch, LeaderEpoch, WriteFenceVersion, 1)`.
- If a candidate changes write-routing fields but does not carry a greater route generation, bump to `existing.RouteGeneration + 1` before writing.
- Lower route generations must not overwrite a newer record.

Routing fields include leader, channel epoch, leader epoch, replicas, ISR, MinISR, status, lease, retention boundary, and write-fence fields.

- [ ] **Step 4: Carry route generation through FSM and proxy codecs**

Update runtime-meta command encoding/decoding and authoritative proxy RPC encoding/decoding. Existing legacy decode tests must default missing route generation to the normalized non-zero value.

- [ ] **Step 5: Project route generation into channelmeta cache and resolver results**

Update `internal/runtime/channelmeta` projection from `metadb.ChannelRuntimeMeta` to `channel.Meta` and ensure cached positive views preserve `RouteGeneration`. Cache invalidation should treat changed route generation as a different write route.

- [ ] **Step 6: Run package tests**

Run:

```bash
GOWORK=off go test ./pkg/channel ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy ./internal/runtime/channelmeta -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channel pkg/slot/meta pkg/slot/fsm pkg/slot/proxy internal/runtime/channelmeta
git commit -m "feat: add authoritative channel route generation"
```

---

### Task 2: Introduce Channel Plane Core

**Files:**
- Create: `internal/runtime/channelplane/plane.go`
- Create: `internal/runtime/channelplane/options.go`
- Create: `internal/runtime/channelplane/command.go`
- Create: `internal/runtime/channelplane/event.go`
- Create: `internal/runtime/channelplane/future.go`
- Create: `internal/runtime/channelplane/reactor.go`
- Create: `internal/runtime/channelplane/channel_cell.go`
- Create: `internal/runtime/channelplane/scheduler.go`
- Create: `internal/runtime/channelplane/route.go`
- Create: `internal/runtime/channelplane/resolver.go`
- Create: `internal/runtime/channelplane/owner.go`
- Create: `internal/runtime/channelplane/errors.go`
- Create: `internal/runtime/channelplane/metrics.go`
- Create: `internal/runtime/channelplane/tracing.go`
- Test: `internal/runtime/channelplane/plane_test.go`
- Test: `internal/runtime/channelplane/reactor_test.go`
- Test: `internal/runtime/channelplane/channel_cell_test.go`
- Test: `internal/runtime/channelplane/future_test.go`
- Test: `internal/runtime/channelplane/resolver_test.go`

- [ ] **Step 1: Write failing plane-core tests**

Cover these behaviors first:

```go
func TestChannelPlaneMapsSameChannelToSameReactor(t *testing.T) { /* same key -> same reactor */ }
func TestChannelCellSerializesSameChannelAppends(t *testing.T) { /* second append waits until first effect completes */ }
func TestChannelPlaneCompletesFuturesInRequestOrder(t *testing.T) { /* aligned append results */ }
func TestRouteResolverSingleflightCoalescesConcurrentMisses(t *testing.T) { /* one authoritative refresh */ }
func TestChannelPlaneCancelsQueuedFutureOnContextDone(t *testing.T) { /* no future leak */ }
func TestChannelPlaneUsesCompatibilityRemoteAppenderForRemoteLeader(t *testing.T) { /* temporary bridge */ }
```

Use fake route resolver, fake local owner, fake compatibility remote appender, and a controllable clock. Do not import `internal/app` or `internal/access` from this package.

- [ ] **Step 2: Run RED plane tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane -run 'Test(ChannelPlane|ChannelCell|RouteResolver)' -count=1
```

Expected: FAIL because the package and types do not exist.

- [ ] **Step 3: Implement the minimal plane facade and lifecycle**

Implement:

```go
type Plane struct { /* reactors, resolver, owner, remote bridge, observer */ }
func New(opts Options) (*Plane, error)
func (p *Plane) Start() error
func (p *Plane) Stop(ctx context.Context) error
func (p *Plane) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error)
```

Use `channelplane.AppendBatchRequest` as an alias-compatible DTO over `pkg/channel.AppendBatchRequest` if possible. Keep message/business-specific fields out of the plane request.

- [ ] **Step 4: Implement reactor, cell, future, and route resolution**

Implement bounded reactor inboxes, per-channel pending queues, route validation, and effect completion events. Same-channel append effects must be serialized with `Inflight` limited to `1`. Cross-channel work should proceed through independent active cells.

The first remote path is a compatibility bridge that calls the old single-channel remote append method behind an interface. Mark it clearly as temporary in code comments.

- [ ] **Step 5: Run plane tests and benchmark smoke**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane -count=1
GOWORK=off go test ./internal/runtime/channelplane -run '^$' -bench BenchmarkChannelPlane -benchtime=1s -count=1
```

Expected: unit tests PASS. Benchmark command may report no benchmarks until a later task; that is acceptable only if no benchmark file has been added yet.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/channelplane
git commit -m "feat: add channel plane reactor core"
```

---

### Task 3: Move Message Durable Send To ChannelAppender

**Files:**
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send.go`
- Delete: `internal/usecase/message/retry.go`
- Modify: `internal/usecase/message/send_test.go`
- Delete or rewrite: `internal/usecase/message/retry_test.go`
- Modify: `internal/usecase/message/logging_test.go`
- Modify: `internal/usecase/message/send_benchmark_test.go`
- Modify: `internal/app/committed_events_test.go`

- [ ] **Step 1: Write failing message-boundary tests**

Add or update tests so durable send requires only an append interface:

```go
func TestSendDurableUsesChannelAppender(t *testing.T) {
    appender := &recordingChannelAppender{result: appendBatchResultForSeqs(10)}
    app := New(Options{Now: fixedNowFn, ChannelAppender: appender, CommittedDispatcher: &recordingCommittedDispatcher{}})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})

    require.NoError(t, err)
    require.Equal(t, frame.ReasonSuccess, result.Reason)
    require.Len(t, appender.calls, 1)
}
```

Add a second test that request-scoped realtime still bypasses the appender and keeps `RequestSubscribers` in `message.App`.

- [ ] **Step 2: Run RED message tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendDurableUsesChannelAppender|TestSendRequestScoped' -count=1
```

Expected: FAIL because `ChannelAppender` is not yet wired and old `Cluster` / `MetaRefresher` dependencies are still required.

- [ ] **Step 3: Replace routing dependencies in message app**

In `internal/usecase/message/deps.go`, add:

```go
type ChannelAppender interface {
    AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error)
}
```

In `Options` and `App`, replace durable-send use of `Cluster`, `MetaRefresher`, and `RemoteAppender` with `ChannelAppender`. Keep `ChannelCluster` only if another message read/status path still needs it; do not use it for durable append.

- [ ] **Step 4: Simplify `sendDurableSegment`**

Build messages exactly as today, then call `a.channelAppender.AppendBatch`. Keep:

- send permission and plugin hook flow
- request-scoped subscriber normalization
- `MessageScopedUIDs` committed side effect fields
- outer `sendtrace.StageMessageSendDurable` recording
- append result alignment and mismatch handling

Delete `sendBatchWithEnsuredMeta`, `appendBatchLocal`, `appendBatchRemote`, and old retry tests after equivalent retry coverage exists in `internal/runtime/channelplane`.

- [ ] **Step 5: Update message tests and benchmarks**

Replace `fakeChannelCluster`, `fakeMetaRefresher`, and `fakeRemoteAppender` uses in send-path tests with `fakeChannelAppender` unless a test is explicitly about read/fetch behavior. Update `BenchmarkSend` so durable benchmark cases measure message business overhead plus fake appender overhead, not route refresh.

- [ ] **Step 6: Run message package tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -count=1
GOWORK=off go test ./internal/usecase/message -run '^$' -bench BenchmarkSend -benchtime=1s -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/usecase/message internal/app/committed_events_test.go
git rm -f internal/usecase/message/retry.go internal/usecase/message/retry_test.go
git commit -m "refactor: send durable messages through channel plane appender"
```

---

### Task 4: Wire App Composition And Local Owner Append

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/channelcluster.go`
- Test: `internal/app/channelcluster_test.go`
- Test: `internal/app/build_test.go`
- Test: `internal/app/lifecycle_test.go`
- Test: `internal/app/observability_test.go`

- [ ] **Step 1: Write failing app wiring tests**

Add tests for the new local append and lifecycle boundary:

```go
func TestAppChannelClusterAppendLocalBatchDoesNotForward(t *testing.T) {
    remote := &recordingRemoteAppender{}
    cluster := &appChannelCluster{service: notLeaderChannelService{}, remoteAppender: remote, localNodeID: 1}

    _, err := cluster.AppendLocalBatch(context.Background(), channel.AppendBatchRequest{ChannelID: channel.ChannelID{ID: "g1", Type: 2}})

    require.ErrorIs(t, err, channel.ErrNotLeader)
    require.Empty(t, remote.calls)
}
```

Add a lifecycle test that `channelplane` starts after `channelmeta` and before `gateway`.

- [ ] **Step 2: Run RED app tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestAppChannelClusterAppendLocalBatchDoesNotForward|Test.*ChannelPlane.*Lifecycle|Test.*ChannelPlane.*Build' -count=1
```

Expected: FAIL because app wiring and `AppendLocalBatch` do not exist.

- [ ] **Step 3: Split local owner append from legacy append**

In `internal/app/channelcluster.go`:

- Extract current local `service.AppendBatch` plus trace/metric recording into `AppendLocalBatch`.
- Ensure `AppendLocalBatch` never calls `forwardAppendBatchToLeader`.
- Keep legacy `AppendBatch` as a wrapper around local append plus current forwarding fallback until Task 5 removes it.
- Keep `ApplyRoutingMeta`, `EnsureLocalRuntime`, and `RemoveLocalRuntime` unchanged.

- [ ] **Step 4: Construct channel plane in `build.go`**

Create `app.channelPlane` after `channelMetaSync`, `app.channelLog`, and `app.nodeClient` are available. Plane options should receive:

- local node ID and logger
- route resolver adapter over `app.channelMetaSync`
- local owner adapter over `app.channelLog.AppendLocalBatch`
- temporary compatibility remote appender over `app.nodeClient.AppendBatchToLeader`
- default reactor options from `channelplane.DefaultOptions()` until config is added in Task 6

Pass `app.channelPlane` as `message.Options.ChannelAppender`. Do not pass `MetaRefresher` or `RemoteAppender` to `message.New`.

- [ ] **Step 5: Add channel plane lifecycle component**

Add app fields and lifecycle helpers for `channelPlane`. Start order should be:

```text
cluster -> managed_slots_ready -> channelmeta -> channelplane -> presence/... -> gateway
```

Stop order should reverse normally. On stop, call `channelPlane.Stop(ctx)` before shutting down `channelmeta` and channel log resources.

- [ ] **Step 6: Run app tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'Test(AppChannelCluster|Build|Lifecycle|Observability)' -count=1
GOWORK=off go test ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app
git commit -m "feat: wire channel plane into app composition"
```

---

### Task 5: Replace Remote Append RPC With Peer Reactor Batches

**Files:**
- Create: `internal/runtime/channelplane/peer_reactor.go`
- Create: `internal/runtime/channelplane/peer_rpc.go`
- Test: `internal/runtime/channelplane/peer_reactor_test.go`
- Create: `internal/access/node/channel_plane_rpc.go`
- Create: `internal/access/node/channel_plane_codec.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/client.go`
- Delete or retire: `internal/access/node/channel_append_rpc.go`
- Delete or rewrite: `internal/access/node/channel_append_rpc_test.go`
- Test: `internal/access/node/channel_plane_rpc_test.go`
- Modify: `internal/app/build.go`
- Test: `internal/app/multinode_integration_test.go`
- Test: `internal/app/send_stress_integration_test.go`

- [ ] **Step 1: Write failing peer reactor and RPC tests**

Add tests that lock down batching and result alignment:

```go
func TestPeerReactorBatchesMultipleChannelsForSameNode(t *testing.T) { /* two channel envelopes -> one AppendBatches RPC */ }
func TestPeerReactorSplitsResultsBackToOwningReactors(t *testing.T) { /* aligned completion callbacks */ }
func TestPeerReactorReturnsTypedBackpressure(t *testing.T) { /* full peer queue -> ErrPeerBackpressured */ }
func TestAppendBatchesRPCValidatesRouteEpochBeforeAppend(t *testing.T) { /* stale route -> stale_route */ }
func TestAppendBatchesRPCDoesNotRefreshOrRedirect(t *testing.T) { /* adapter returns typed hint only */ }
```

- [ ] **Step 2: Run RED peer/RPC tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane ./internal/access/node -run 'TestPeerReactor|TestAppendBatchesRPC' -count=1
```

Expected: FAIL because peer reactor and RPC do not exist.

- [ ] **Step 3: Implement `AppendBatches` DTOs and codec**

Use the neutral `internal/runtime/channelplane/peer_rpc.go` DTOs at the node adapter boundary:

```go
type AppendBatchesRequest struct { Batches []AppendBatchEnvelope }
type AppendBatchEnvelope struct { RouteEpoch RouteEpoch; Request channel.AppendBatchRequest }
type AppendBatchesResponse struct { Results []AppendBatchRemoteResult }
```

Keep transport conversion in `internal/access/node`; do not import access packages from `channelplane`.

- [ ] **Step 4: Implement node adapter behavior**

`handleChannelPlaneAppendBatchesRPC` must:

- validate local channel owner state and route epoch
- call the local owner append path only when this node is leader
- return typed statuses: `ok`, `not_leader`, `stale_route`, `lease_expired`, `write_fenced`, `not_ready`, `backpressure`, `invalid`
- avoid multi-step metadata refresh or second-hop redirects

- [ ] **Step 5: Implement peer reactor batching**

Peer reactor must aggregate remote append effects by target node/lane and flush on max wait, max records, or max bytes. It must split responses back to owning channel reactors without completing same-channel requests out of order.

- [ ] **Step 6: Remove compatibility remote path and legacy RPC**

After `PeerReactor` is wired into `channelplane` and all durable send callers use it:

- remove the temporary compatibility remote-owner adapter
- remove `Client.AppendBatchToLeader` if no callers remain
- delete or retire `channel_append_rpc.go`
- convert or delete legacy forwarding tests
- convert legacy `appChannelCluster.AppendBatch` to local-only or delete it if no callers require the old interface

- [ ] **Step 7: Run peer, node, and multi-node tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane ./internal/access/node -count=1
GOWORK=off go test -tags=integration ./internal/app -run 'Test.*Follower.*Send|Test.*Stale.*Route|TestSendStressThreeNode' -count=1 -v
```

Expected: PASS. If the integration regex matches no current tests, add the missing focused integration tests before proceeding.

- [ ] **Step 8: Commit**

```bash
git add internal/runtime/channelplane internal/access/node internal/app
git commit -m "feat: batch remote channel appends through peer reactors"
```

---

### Task 6: Add Config, FLOW Docs, And Final Verification

**Files:**
- Modify: `internal/app/config.go`
- Test: `internal/app/config_test.go`
- Modify: `cmd/wukongim/config.go`
- Test: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`
- Modify: `internal/FLOW.md`
- Modify: `internal/runtime/channelmeta/FLOW.md`
- Create: `internal/runtime/channelplane/FLOW.md`
- Modify: `pkg/channel/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `pkg/slot/FLOW.md`
- Optional modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Optional modify: `docs/development/CODE_QUALITY.md`

- [ ] **Step 1: Write failing config tests**

Add tests for defaults and parsing:

```go
func TestConfigChannelPlaneDefaults(t *testing.T) {
    cfg := validConfig()
    require.NoError(t, cfg.ApplyDefaultsAndValidate())
    require.GreaterOrEqual(t, cfg.ChannelPlane.ReactorCount, 4)
    require.Greater(t, cfg.ChannelPlane.PeerLaneCount, 0)
    require.Positive(t, cfg.ChannelPlane.PeerBatchMaxWait)
}
```

In `cmd/wukongim/config_test.go`, parse `WK_CHANNEL_PLANE_REACTOR_COUNT`, `WK_CHANNEL_PLANE_PEER_LANE_COUNT`, and `WK_CHANNEL_PLANE_PEER_BATCH_MAX_WAIT` from config/env.

- [ ] **Step 2: Add `ChannelPlaneConfig` and loader wiring**

Add root-level `Config.ChannelPlane ChannelPlaneConfig` with English comments for every field. Defaults:

- `ReactorCount`: `max(4, runtime.GOMAXPROCS(0))`
- `PeerLaneCount`: `max(1, min(8, runtime.GOMAXPROCS(0)))`
- `PeerBatchMaxWait`: start with `500 * time.Microsecond`

Reject negative values; zero means default. Update `wukongim.conf.example` with `WK_` keys.

- [ ] **Step 3: Wire config into channel plane options**

In `internal/app/build.go`, pass `cfg.ChannelPlane` values into `channelplane.Options`. Keep same-channel inflight fixed at `1`; do not expose it as a config field in this phase.

- [ ] **Step 4: Update FLOW docs**

Update docs to state:

- durable send write routing is owned by `internal/runtime/channelplane`
- `message.App` does not know slot leader, channel leader, remote append, or route refresh
- `appChannelCluster` is local owner / channel execution facade, not a router
- node append RPC is a typed batch adapter only; it does not refresh or redirect
- single-node deployment remains a single-node cluster

Create `internal/runtime/channelplane/FLOW.md` with package responsibilities, dependency rules, reactor flow, backpressure model, and test commands.

- [ ] **Step 5: Run focused verification**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane ./internal/usecase/message ./internal/app ./internal/access/node -count=1
GOWORK=off go test ./pkg/channel ./pkg/slot/... ./pkg/cluster ./internal/runtime/channelmeta -run '^$' -count=1
GOWORK=off go test ./... -run '^$' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run integration/performance verification**

Run only when the focused unit suite passes:

```bash
GOWORK=off go test -tags=integration ./internal/app -run 'Test.*Follower.*Send|Test.*Stale.*Route|Test.*Peer.*Backpressure|TestSendStressThreeNode' -count=1 -v
```

Expected: PASS. If these named tests do not exist yet, add focused tests for follower-gateway send, stale route retry, peer backpressure mapping, and send-stress throughput before declaring done.

- [ ] **Step 7: Commit**

```bash
git add internal/app cmd/wukongim wukongim.conf.example internal/FLOW.md internal/runtime/channelmeta/FLOW.md internal/runtime/channelplane/FLOW.md pkg/channel/FLOW.md pkg/cluster/FLOW.md pkg/slot/FLOW.md docs/development/PROJECT_KNOWLEDGE.md docs/development/CODE_QUALITY.md
git commit -m "docs: document channel plane runtime configuration"
```

---

## Final Acceptance Checklist

- [ ] `message.App` has no durable-send dependency on `MetaRefresher`, `RemoteAppender`, or channel leader routing.
- [ ] `internal/runtime/channelplane` owns route refresh, retry, same-channel ordering, local append, remote append, and typed backpressure.
- [ ] `appChannelCluster.AppendLocalBatch` never forwards; legacy forwarding path is removed after `PeerReactor` cutover.
- [ ] Node append RPC handles `AppendBatches` only and never performs multi-step refresh or redirects.
- [ ] Same-channel append effects are serialized in Phase 1.
- [ ] `RouteGeneration` is authoritative and validated by both local cache and remote owners.
- [ ] `wukongim.conf.example` includes all new `WK_CHANNEL_PLANE_*` keys.
- [ ] FLOW docs match the new boundaries.
- [ ] Focused unit tests, compile sweep, and required integration tests pass.
