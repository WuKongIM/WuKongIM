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

Major adapters cover Channel append and committed reads, Slot-owned metadata,
UID-owned presence and membership, management operations, plugins, diagnostics,
and bounded operations observations.

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
   First person SENDs prepare coalesced UID membership/runtime metadata and
   publish directory-ready only after every prepare proposal joins.
2. Presence and recipient adapters resolve exact fenced targets, group work by
   owner, and choose the local authority or one typed RPC envelope per owner.
3. Management and operations adapters receive policy-validated requests,
   select node-local or peer execution, and return bounded, redacted read
   models with partial evidence explicit.

## Invariants and Failure Semantics

- Route, leader, term, epoch, revision, and lease fences must be forwarded
  exactly; preferred or cached ownership must never replace observed authority.
- Missing leaders, stale routes, unavailable placement, and write fences fail
  closed as typed retryable errors. Context cancellation and deadlines remain
  unchanged.
- Batch adapters preserve cardinality and order. Missing, duplicate,
  contradictory, or unrepresentable evidence is an error, not fabricated
  success.
- A generic append failure may still resolve through durable idempotency lookup
  and therefore emits no premature adapter terminal error; final item logging
  and recovered/unresolved accounting belong to channelappend.
- Person-directory batching shares duplicate Channel results, detaches canceled
  waiters without canceling accepted work, and never publishes ready after a
  membership or runtime-metadata prepare failure.
- Mutable request and response payloads crossing runtime ownership boundaries
  are cloned unless the contract explicitly transfers ownership.
- Node lifecycle, Slot movement, retention, and Controller changes are executed
  only after the usecase's safety gates; this package never mutates assignments
  or durable state as a shortcut.
- Fanout and diagnostic work must remain concurrency-, deadline-, and result-
  bounded and must not place user, Channel, Slot, or node identities in metric
  labels.

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
