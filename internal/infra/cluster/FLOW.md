---
scope: package
summary: Adapts internal ports to cluster, channel, metadata, node-RPC, and operations runtimes without owning business policy.
---

# Cluster Infrastructure Flow

## Responsibility

`internal/infra/cluster` is the translation boundary between entry-agnostic
internal ports and concrete cluster runtimes. It maps DTOs, clones mutable
payloads, chooses local versus typed node-RPC execution, preserves aligned batch
results, and translates infrastructure failures into the error families owned
by the calling usecase or runtime.

Adapters cover Channel reads/appends, Slot metadata, UID presence/membership,
management, plugins, diagnostics, and bounded operations observations.

## Boundaries

- Business validation, retry policy, pagination semantics, and HTTP response
  shaping remain in `internal/usecase` or `internal/access`.
- Raft, Channel runtime, routing, storage, and control-plane mechanics remain in
  `pkg/cluster`, `pkg/channel`, `pkg/controller`, and `pkg/slot`.
- Local-versus-remote adapters select the owner node and transport typed DTOs;
  they do not reinterpret remote state or bypass its authority.
- Infrastructure adapters must not construct new business workflows or expose
  concrete cluster types through internal ports.

## Main Flows

1. Data-plane adapters map append, metadata, and membership DTOs to their
   resolved Channel or physical-Slot authority, preserve payload ownership and
   aligned results, then translate typed failures back to the calling runtime.
   Committed-message scans preserve the usecase-resolved bounds, raw scan
   order, command flags, and owned payloads; `message.PageReader` selects pages.
   Channel append clones payload bytes once and explicitly transfers that
   immutable ownership to the Channel runtime. Mutable recipient metadata is
   reread from the Slot leader per routed batch; only person metadata is cached.
   First person SENDs commit coalesced Channel directory tasks/runtime metadata;
   the asynchronous projector later ensures UID memberships and publishes ready.
2. Presence reconstruction coalesces target groups into one bounded read per
   active owner instead of repeating unavailable-owner timeouts per Hash Slot. It validates each
   owner boot identity, and rechecks current membership and Slot authority before
   publishing a complete result. Unknown or unavailable proof remains explicit.
3. Presence and recipient adapters resolve exact fenced targets, group work by
   owner, and choose the local authority or one typed RPC envelope per owner.
   UID-list endpoint lookup resolves at most 256 inputs per page, groups by the
   full fenced target, and reuses the target-batch path with at most four remote
   leaders in flight. Duplicate UID multiplicity is retained; failed or foreign
   endpoint evidence fails the whole UID-list lookup.
3. Management and operations adapters receive policy-validated requests,
   select node-local or peer execution, and return bounded, redacted read
   models with partial evidence explicit. Revision-fenced management writes may
   read the Controller-visible snapshot separately from the Node-applied read
   model so runtime reconciliation cannot stall their CAS convergence.

- Conversation list and legacy-sync previews use explicit persisted-head and
  persisted-message ports. Byte-limited scans preserve continuation; every item
  failure propagates. Sync metadata preparation uses one bounded UID membership
  batch and existing Slot-grouped channel metadata reads, without the SEND cache.
  Sparse found memberships are identity-validated and expanded to aligned results.
  Personal mutations and history retain committed reads.

## Invariants and Failure Semantics

- Route, leader, term, epoch, revision, lease and optional prepared-append route fences must be forwarded
  exactly; preferred or cached ownership must never replace observed authority.
- Missing leaders, stale routes, unavailable placement, and write fences fail
  closed as typed retryable errors, including a stopped or unreachable append
  authority transport. Context cancellation and deadlines remain
  unchanged.
- CMD discovery may precede the first command log. Its reader treats only the
  routed typed Channel-not-found result as empty, matching the bind tail read.
  CMD reads batch up to 32 directory channels through authoritative source
  metadata and committed Channel reads, preserving alignment and per-channel
  pagination. An ambiguous batch-level absence cannot clear other logs.
  A confirmed terminal source produces an aligned `ErrChannelDisbanded` item
  without reading its command log. The CMD usecase decides to skip that item
  during global sync; direct source reads retain the error.
- Batch adapters preserve cardinality and order. Missing, duplicate,
  contradictory, or unrepresentable evidence is an error, not fabricated
  success.
- A generic append failure may still resolve through durable idempotency lookup
  and therefore emits no premature adapter terminal error; final item logging
  and recovered/unresolved accounting belong to channelappend.
- Retry proof uses routed original committed content, retaining HW/retention
  fences while avoiding history-edit overlays. MQTT compares exact body and
  semantic metadata; only its ingress clock may differ. Proof bytes and read
  budgets include metadata; append mappings preserve independent ownership.
- Keyed Wills select a required server-domain lookup capability; absence cannot
  fall back to client numbers. Original committed proof remains mandatory.
- MQTTWillReceipts separately reads retained proof through fresh Slot metadata and the routed recovered Channel port. It uses SEND person normalization and verifies the complete content hash/HW; unavailable authority stays an error and absence grants no redispatch. Fresh Slot target absence has a distinct usecase error, permitting a separate exact sealed non-dispatch query before first SEND, never retry by itself.
- MQTT source protection maps exact source identity through fresh Slot runtime
  metadata to routed Channel admission, using the app's message-ID allocator.
  Confirmed runtime absence uses the existing bounded Channel initializer, then
  rereads fresh Slot fences. No business metadata creation, policy retry or authority fallback is allowed.
- Person-directory batching shares duplicate Channel results, detaches canceled
  waiters without canceling accepted work. Admission proves a durable task, not
  committed UID membership; MQTT future-source admission needs a separate barrier.
- Mutable request and response payloads crossing runtime ownership boundaries
  are cloned unless the contract explicitly transfers ownership.
- Node lifecycle, Slot movement, retention, and Controller changes are executed
  only after the usecase's safety gates; this package never mutates assignments
  or durable state as a shortcut.
- Fanout and diagnostic work must remain concurrency-, deadline-, and result-
  bounded and must not place user, Channel, Slot, or node identities in metric
  labels.

- Message-update storage maps a narrow usecase port to authoritative Slot operations. Read adapters preserve edit version/time and byte-limited continuation after content growth; committed history and persisted previews retain separate base-read semantics.

The append adapter promotes storage non-submission evidence only with `errors.Is`, never string matching. The closed result remains distinct from a durability receipt and can enter ordinary positive idempotency recovery.

## Read First
- [Append adapter](channel_append.go)
- [Metadata adapter](channel_metadata.go)
- [Presence adapter](presence.go)
- [Management adapters](management.go)
- [Operations projection](opsobserve.go)

## Update Triggers

Update this file when the package gains or removes an adapter family, changes
authority or error mapping, changes local/remote routing ownership, alters
batch alignment or payload ownership, or changes a safety boundary for a
durable management operation.
