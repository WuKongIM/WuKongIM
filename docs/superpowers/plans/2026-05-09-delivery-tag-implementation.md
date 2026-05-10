# Delivery Tag Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Add leader-authoritative delivery tags for all channel delivery paths so every node reuses only its local subscriber partition, invalidates stale caches safely, and hands ACK/retry ownership to the target partition node.

**Architecture:** Keep `internal/app/deliveryrouting.go` as the orchestration point, but stop treating the subscriber list as a per-message full-channel scan. A new `internal/runtime/deliverytag` cache owns tag incarnations, tag versions, TTL, and stale-safe invalidation, while `internal/usecase/delivery` turns channel-specific subscriber sources into leader-built or leader-fetched tag partitions. The authoritative subscriber store keeps a durable mutation version on the channel record so tag invalidation is fenced by storage state, not by cache-local heuristics.

**Tech Stack:** Go, `testing` + `testify`, current `pkg/slot` Raft/meta stack, `pkg/cluster`, `internal/access/node`, `internal/runtime/delivery`, `internal/usecase/delivery`.

---

## References And Constraints

- Spec: `docs/raw/delivery-tag-design.md`.
- Risk background: `docs/raw/100k-channel-support-risk-analysis.md`.
- Project rules: `docs/development/PROJECT_KNOWLEDGE.md`.
- Old behavioral reference: `learn_project/WuKongIM/internal/channel/handler/event_distribute.go`.
- Follow `AGENTS.md`: keep the layering boundaries, do not add a new all-purpose `service` layer, and add concise English comments on key exported structs and methods.
- Delivery tags are leader-authoritative for all channels, not only 10k/100k channels.
- Ordinary member changes keep `tagKey` stable and advance `tagVersion`.
- Non-leader caches must contain only the local partition, never the full subscriber set.
- Older requests must never evict a newer local tag ref.
- `SubscriberMutationVersion` must be durable and authoritative; the cache only consumes it.
- `PartitionTopologyVersion` must compare the full `{HashSlotTableVersion, SlotAuthorityRefs[]}` shape, not a single max version.
- Cmd/derived delivery tags must also carry `SourceChannelKey` and `SourceSubscriberMutationVersion`, and they must become stale when the source channel advances.
- Leader incarnation changes, leader restart, or cold cache rebuild may mint a new `tagKey`; only ordinary member mutations must keep the current `tagKey` stable.

## File Map

### Slot metadata and command encoding
- Modify: `pkg/slot/meta/catalog.go`
- Modify: `pkg/slot/meta/channel.go`
- Modify: `pkg/slot/meta/codec.go`
- Modify: `pkg/slot/meta/batch.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/proxy/store.go`
- Modify tests in `pkg/slot/meta/channel_test.go`, `pkg/slot/meta/codec_test.go`, `pkg/slot/fsm/state_machine_test.go`, `pkg/slot/fsm/subscriber_command_limits_test.go`, and any `pkg/slot/proxy/*_test.go` coverage that asserts subscriber reads/writes.

### Delivery tag runtime and source adapters
- Create: `internal/runtime/deliverytag/types.go`
- Create: `internal/runtime/deliverytag/cache.go`
- Create: `internal/runtime/deliverytag/manager.go`
- Create: `internal/runtime/deliverytag/topology.go`
- Create: `internal/runtime/deliverytag/manager_test.go`
- Modify: `internal/usecase/delivery/subscriber.go`
- Create: `internal/usecase/delivery/source.go`
- Modify: `internal/usecase/delivery/subscriber_test.go`
- Modify: `internal/usecase/channel/app.go`

### Node RPC and delivery routing
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/client.go`
- Create: `internal/access/node/delivery_tag_codec.go`
- Create: `internal/access/node/delivery_tag_rpc.go`
- Modify: `internal/access/node/delivery_push_codec.go`
- Modify: `internal/access/node/delivery_push_rpc.go`
- Modify tests in `internal/access/node/delivery_push_rpc_test.go` and new `internal/access/node/delivery_tag_rpc_test.go`.

### App wiring and integration
- Modify: `internal/app/build.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/presenceauthority.go`
- Modify tests in `internal/app/deliveryrouting_test.go`, `internal/app/channel_management_integration_test.go`, `internal/app/multinode_integration_test.go`, and `internal/app/deliveryrouting_benchmark_test.go` if tag fanout needs a benchmark guardrail.
- Update: `docs/development/PROJECT_KNOWLEDGE.md` only if a new durable invariant appears during implementation.

---

## Task 1: Make Subscriber Mutation Version Durable In Slot Meta

**Files:**
- Modify: `pkg/slot/meta/catalog.go`
- Modify: `pkg/slot/meta/channel.go`
- Modify: `pkg/slot/meta/codec.go`
- Modify: `pkg/slot/meta/batch.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/proxy/store.go`
- Test: `pkg/slot/meta/channel_test.go`
- Test: `pkg/slot/meta/codec_test.go`
- Test: `pkg/slot/fsm/state_machine_test.go`
- Test: `pkg/slot/fsm/subscriber_command_limits_test.go`

- [x] **Step 1: Write the failing tests**

Add tests that prove:

1. `metadb.Channel` can round-trip a durable `SubscriberMutationVersion`.
2. Subscriber add/remove/reset paths leave the version monotonic.
3. Chunked logical subscriber mutations only expose one version fence to callers.
4. Existing subscriber command size limits still reject oversize UID sets.

Example shape:

```go
func TestChannelSubscriberMutationVersionRoundTrip(t *testing.T) { /* ... */ }
func TestSubscriberMutationVersionAdvancesMonotonically(t *testing.T) { /* ... */ }
```

- [x] **Step 2: Run the failing tests**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm -run 'Test.*SubscriberMutationVersion|Test.*SubscriberCommandLimits' -count=1`

Expected: FAIL because the channel record does not yet carry the durable version and the FSM/store paths do not fence it.

- [x] **Step 3: Implement the minimal metadata change**

Add `SubscriberMutationVersion` to the channel record, encode/decode it in the channel value codec, and make the slot FSM/store write paths preserve it on ordinary channel updates while advancing it only when the subscriber membership mutation is durably applied.

- [x] **Step 4: Run the tests again**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm -run 'Test.*SubscriberMutationVersion|Test.*SubscriberCommandLimits' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit (not run in local main resume)**

```bash
git add pkg/slot/meta pkg/slot/fsm pkg/slot/proxy
git commit -m "feat: add durable subscriber mutation version"
```

---

## Task 2: Build The Delivery Tag Runtime Cache

**Files:**
- Create: `internal/runtime/deliverytag/types.go`
- Create: `internal/runtime/deliverytag/cache.go`
- Create: `internal/runtime/deliverytag/manager.go`
- Create: `internal/runtime/deliverytag/topology.go`
- Test: `internal/runtime/deliverytag/manager_test.go`

- [x] **Step 1: Write the failing tests**

Add tests that prove:

1. A leader-built tag keeps the same `tagKey` across ordinary Add/Remove mutations and only increments `tagVersion`.
2. A follower caches only the local node partition, not the whole channel membership.
3. A stale request with an older `tagVersion` never evicts a newer local cache entry.
4. A `PartitionTopologyVersion` mismatch forces refetch/rebuild.
5. TTL cleanup removes cold tags and stale channel refs.
6. A cmd/derived tag becomes stale when the source channel's `SubscriberMutationVersion` advances, even if the derived tag key itself does not change.
7. A leader incarnation change or cold rebuild can mint a new `tagKey` and must not collide with an older follower cache entry.

Example shape:

```go
func TestTagVersionIncrementsWithoutChangingKey(t *testing.T) { /* ... */ }
func TestStaleRequestDoesNotEvictNewerLocalTag(t *testing.T) { /* ... */ }
func TestLeaderIncarnationCanMintFreshTagKey(t *testing.T) { /* ... */ }
```

- [x] **Step 2: Run the failing tests**

Run: `go test ./internal/runtime/deliverytag -run 'Test.*Tag|Test.*Topology' -count=1`

Expected: FAIL because the package does not exist yet.

- [x] **Step 3: Implement the runtime cache**

Add a small node-local manager that:

1. Tracks `tagKey -> tag body`.
2. Tracks `channelKey -> current ref`.
3. Stores only the local partition on follower nodes.
4. Compares the full partition topology reference before reuse.
5. Uses stale-safe replacement rules so older lookups never overwrite newer refs.
6. Carries `SourceChannelKey` and `SourceSubscriberMutationVersion` for cmd/derived tags and marks them stale when the source version advances.
7. Allows a new leader incarnation or cold rebuild to mint a fresh `tagKey` instead of reusing an old cache key.

- [x] **Step 4: Run the tests again**

Run: `go test ./internal/runtime/deliverytag -run 'Test.*Tag|Test.*Topology' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit (not run in local main resume)**

```bash
git add internal/runtime/deliverytag
git commit -m "feat: add delivery tag runtime cache"
```

---

## Task 3: Teach Delivery Subscribers About Tag Sources And Special Channel Types

**Files:**
- Modify: `internal/usecase/delivery/subscriber.go`
- Create: `internal/usecase/delivery/source.go`
- Modify: `internal/usecase/delivery/subscriber_test.go`
- Modify: `internal/usecase/channel/app.go`
- Modify: `internal/usecase/user/legacy.go` if the special legacy lists need to participate in tag source resolution

- [x] **Step 1: Write the failing tests**

Add tests that prove the subscriber source can resolve:

1. Person channels from derived IDs.
2. Agent channels from derived IDs.
3. Ordinary group/community/topic/data/live/agent-group channels from paged subscriber storage.
4. Info channels with temporary overlays merged in.
5. Temp channels without turning one-shot request UIDs into reusable channel-level tag state.
6. Visitors and customer-service channels via their special subscriber source.
7. cmd/derived channels by resolving the source channel and reusing its subscriber source.
8. cmd/derived tags become stale when the source channel's subscriber mutation version advances.

Example shape:

```go
func TestSubscriberSourceResolvesPersonChannel(t *testing.T) { /* ... */ }
func TestSubscriberSourceRejectsReusableTempRequestSnapshot(t *testing.T) { /* ... */ }
```

- [x] **Step 2: Run the failing tests**

Run: `go test ./internal/usecase/delivery -run 'Test.*Subscriber|Test.*Snapshot' -count=1`

Expected: FAIL until the source abstraction and special cases are wired.

- [x] **Step 3: Implement the source abstraction**

Split the resolver into:

1. A general paging interface for store-backed subscribers.
2. Small channel-type-specific adapters for derived, temporary, overlay, and legacy channels, including explicit person and agent ID-derived resolvers.
3. A path that can hand the delivery tag manager a source without exposing access-layer details.

- [x] **Step 4: Run the tests again**

Run: `go test ./internal/usecase/delivery -run 'Test.*Subscriber|Test.*Snapshot' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit (not run in local main resume)**

```bash
git add internal/usecase/delivery internal/usecase/channel internal/usecase/user
git commit -m "feat: add delivery subscriber sources"
```

---

## Task 4: Add Leader-Only Delivery Tag RPCs And Finish The Wire Contract

**Files:**
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/client.go`
- Create: `internal/access/node/delivery_tag_codec.go`
- Create: `internal/access/node/delivery_tag_rpc.go`
- Modify: `internal/access/node/delivery_push_codec.go`
- Modify: `internal/access/node/delivery_push_rpc.go`
- Test: `internal/access/node/delivery_tag_rpc_test.go`
- Test: `internal/access/node/delivery_push_rpc_test.go`

- [x] **Step 1: Write the failing tests**

Add tests that prove:

1. Only the channel leader can create/update a tag.
2. A follower can only fetch its local partition for a `tagKey`.
3. The response carries `tagKey + tagVersion` as the public fence.
4. Mismatched or stale responses return a retryable status instead of overwriting newer cache state.
5. The delivery-push response still reports exact `retryable`/`dropped` routes, while `accepted` can be a count on the batched path.
6. Topology snapshot acquisition fails closed with retryable when a self-consistent `PartitionTopologyVersion` cannot be read in one shot.
7. A leader incarnation change or cold rebuild may return a fresh `tagKey` and the client must treat the old key as stale rather than force reuse.

Example shape:

```go
func TestDeliveryTagRPCRejectsNonLeaderUpdates(t *testing.T) { /* ... */ }
func TestDeliveryPushBatchCanReturnAcceptedCount(t *testing.T) { /* ... */ }
```

- [x] **Step 2: Run the failing tests**

Run: `go test ./internal/access/node -run 'Test.*DeliveryTag|Test.*DeliveryPush' -count=1`

Expected: FAIL because the new RPC/service IDs and codecs do not exist yet.

- [x] **Step 3: Implement the RPCs and codec**

Add a dedicated tag RPC service ID, register the handler in `options.go`, add client helpers, and encode/decode only the data needed for leader fetch/update and local-partition cache refresh. Keep the legacy delivery-push codec compatible while the new batched path uses `accepted count + retryable/dropped precise routes`.
Use explicit retry/result names in the RPC contract: `ok`, `not_leader`, `tag_not_current`, `tag_version_mismatch`, and `retryable` for stale or non-authoritative responses.

- [x] **Step 4: Run the tests again**

Run: `go test ./internal/access/node -run 'Test.*DeliveryTag|Test.*DeliveryPush' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit (not run in local main resume)**

```bash
git add internal/access/node
git commit -m "feat: add delivery tag rpc contract"
```

---

## Task 5: Rewire Delivery Routing To Consume Tags And Hand Off Ownership

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/presenceauthority.go`
- Modify: `internal/runtime/delivery/manager.go` only if the resolver contract needs a small hook
- Test: `internal/app/deliveryrouting_test.go`
- Test: `internal/app/multinode_integration_test.go`
- Test: `internal/app/deliveryrouting_benchmark_test.go`

- [x] **Step 1: Write the failing tests**

Add tests that prove:

1. A message resolves through a tag partition instead of scanning the full subscriber set every time.
2. The leader sends only `tagKey + tagVersion` plus the target partition work.
3. The target partition node owns ACK/retry for its local routes.
4. Remote pushes still chunk large route lists and keep exact `retryable`/`dropped` feedback.
5. A stale tag response forces a refetch instead of poisoning the new cache.
6. Partition handoff work is idempotent on `(channelID, channelType, messageID, targetNodeID, tagKey, tagVersion)` so retries and committed replay do not duplicate delivery ownership.

Example shape:

```go
func TestDeliveryRoutingUsesTagPartition(t *testing.T) { /* ... */ }
func TestDeliveryRoutingRejectsStaleTagResponse(t *testing.T) { /* ... */ }
```

- [x] **Step 2: Run the failing tests**

Run: `go test ./internal/app -run 'Test.*DeliveryRouting|Test.*Tag' -count=1`

Expected: FAIL until the app wires the tag manager and the resolver uses it.

- [x] **Step 3: Wire the manager into app startup**

Construct the tag manager in `internal/app/build.go`, inject it into the delivery subscriber path, and thread the authoritative topology reader through `presenceauthority.go` or a small helper so the manager can compare the full partition version without guessing locally.

- [x] **Step 4: Update the delivery router**

Change `internal/app/deliveryrouting.go` so it resolves a tag once, fans out partitions by target node, and preserves the existing `delivery_push` route feedback contract on the remote hop. Keep the large-route chunking guard in place, and dedupe partition handoff work by `(channelID, channelType, messageID, targetNodeID, tagKey, tagVersion)` so leader retries and committed replay do not double-own ACK/retry state.

- [x] **Step 5: Run the tests again**

Run: `go test ./internal/app -run 'Test.*DeliveryRouting|Test.*Tag' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit (not run in local main resume)**

```bash
git add internal/app
git commit -m "feat: route delivery through delivery tags"
```

---

## Task 6: Finish Channel Management Invalidation And End-To-End Regression Coverage

**Files:**
- Modify: `internal/usecase/channel/app.go`
- Modify: `internal/app/channel_management_integration_test.go`
- Modify: `internal/app/user_management_integration_test.go` if legacy channel or system-UID flows share the same invalidation path
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a new permanent rule is learned from the implementation

- [x] **Step 1: Write the failing tests**

Add tests that prove:

1. Add/Remove/Reset/RemoveAll only advance one logical subscriber mutation fence for the whole operation.
2. The leader marks the tag dirty or invalidates it before/alongside the durable mutation path.
3. A later delivery request must refetch after the durable mutation version advances.
4. Legacy HTTP channel-management endpoints still work through the new path.
5. Dirty lifecycle is explicit: mark dirty before the write, clear or refresh the ref after the durable version succeeds, and never rely on TTL alone.

Example shape:

```go
func TestChannelManagementInvalidatesDeliveryTagOncePerLogicalMutation(t *testing.T) { /* ... */ }
func TestChannelManagementLegacyEndpointsStillWork(t *testing.T) { /* ... */ }
```

- [x] **Step 2: Run the failing tests**

Run: `go test ./internal/app -run 'Test.*ChannelManagement|Test.*Legacy.*Management' -count=1`

Expected: FAIL until the mutation fence and invalidation path are wired.

- [x] **Step 3: Implement the mutation fence and invalidation path**

Make the channel management usecase treat chunked writes as one logical mutation, advance the durable subscriber mutation fence once, mark the leader-side tag dirty before the write, and clear or refresh the ref only after the durable mutation succeeds; never rely on TTL alone to repair staleness.

- [x] **Step 4: Run the tests again**

Run: `go test ./internal/app -run 'Test.*ChannelManagement|Test.*Legacy.*Management' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit (not run in local main resume)**

```bash
git add internal/usecase/channel internal/app docs/development/PROJECT_KNOWLEDGE.md
git commit -m "feat: invalidate delivery tags on channel mutations"
```

---

## Final Verification

- Run the targeted package suites that changed: `go test ./pkg/slot/meta ./pkg/slot/fsm ./internal/runtime/deliverytag ./internal/usecase/delivery ./internal/access/node ./internal/app -count=1`
- If the tag RPC or app wiring depends on multi-node behavior, run the smallest relevant integration subset with `-tags=integration` instead of the whole repository.
- Verify `git diff --check` passes before claiming completion.
---

## Resume Status - 2026-05-10

- Resumed on the local `main` checkout per operator instruction; the old `.worktrees/delivery-tag-implementation` directory is absent.
- The original Task 1-4 reference commits are on branch `delivery-tag-implementation`: `80f05cb4`, `59000c96`, `6fbdcfcc`, `e4fd0edc`, `0d513160`.
- Task 5 and Task 6 implementation is present in the local `main` working tree rather than in a separate worktree commit; commit steps are intentionally left unchecked because no commit was created in this resume.
- Integrity verification passed with `go test ./pkg/slot/meta ./pkg/slot/fsm ./internal/runtime/deliverytag ./internal/usecase/delivery ./internal/access/node ./internal/usecase/channel ./internal/app -count=1` and `git diff --check`.
- Task-specific verification also passed for the Task 1-5 targeted `-run` commands; Task 6's documented app regex matched no unit tests because its endpoint regression tests are integration-tagged, so the smallest relevant subset was verified with `go test -tags=integration ./internal/app -run 'TestAppChannelManagementLegacyEndpointsPersistThroughStore|TestAppLegacyUserManagementEndpointsPersistAndQueryRuntime' -count=1`.
