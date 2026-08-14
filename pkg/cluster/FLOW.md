---
scope: subtree
summary: Composes Controller state, Slot Multi-Raft metadata, typed node RPC, routing, and replicated Channel runtimes behind Node.
---

# Cluster Runtime Flow

## Responsibility

`pkg/cluster` is the reusable cluster composition root. `Node` owns lifecycle,
readiness, public facade delegation, route publication, and bounded snapshots;
focused subpackages own Controller adaptation, immutable routing, typed node
RPC, Slot reconciliation/proposal, Channel hosting, and observation loops.

Every deployment follows these cluster semantics, including a single-node
cluster. There is no standalone data or control path.

## Boundaries

- `control` adapts Controller state, writes, Raft transport, and snapshots;
  `routing` publishes hash-Slot authority; `slots` and `propose` own Slot
  Multi-Raft lifecycle and proposals; `channels` hosts Channel runtimes; `net`
  transports typed node RPC; `observe` runs low-frequency reporting.
- `Node` delegates validated intents. Manager business policy, drain safety,
  access DTOs, and response shaping remain in `internal`.
- Typed RPC routes opaque upper-layer DTOs by registered service. Cluster may
  fence maintenance and ownership but must not absorb delivery or Manager
  business logic.
- Controller, Slot, Channel, transport, and storage implementations remain
  behind stable public facades and neutral errors.

## Main Flows

1. Lifecycle wiring starts transport and Controller, installs each valid control
   snapshot into discovery/routes, reconciles Slots and Channel resources, and
   exposes readiness; Stop reverses that ownership after rejecting foreground
   work and invalidating readiness.
2. Slot proposals and metadata facades resolve one immutable route snapshot,
   group Channel- or UID-owned work by physical Slot, execute locally or
   forward, and require the receiver to recheck actual leadership.
3. Channel append resolves or creates Slot-owned runtime metadata, applies it
   monotonically to the selected runtime, and appends locally or forwards to
   the exact leader while background control/task convergence stays bounded.

## Invariants and Failure Semantics

- Route authority is `(HashSlot, SlotID, LeaderNodeID, LeaderTerm,
  ConfigEpoch, RouteRevision)` from one immutable publication. Local
  `AuthorityEpoch` is diagnostic only and never a distributed fence.
- Desired or preferred ownership never substitutes for an observed leader.
  Missing, stale, incomplete, duplicate, or mismatched authority evidence
  fails readiness or the foreground operation closed.
- The data-plane lease expires when the node cannot publish healthy readiness;
  local Channel leaders then reject new writes without discarding admitted work.
- Slot and Channel metadata are authoritative at their current owners. Caches,
  hints, compatibility codecs, and local replicas cannot roll back a newer
  generation or make migration decisions from stale state.
- Lifecycle, fanout, workers, retries, scans, repairs, retention, tasks,
  diagnostics, and observations are bounded and low-cardinality.
- Maintenance closes business admission before storage replacement and keeps
  only explicitly allowed restore RPC available. Backup and restore retain
  cluster routing and exact authority fences.

## Read First

- [Public API](api.go)
- [Node ownership](node.go)
- [Lifecycle](node_lifecycle.go)
- [Routing publication](routing/router.go)
- [Channel hosting](channels/service.go)

## Update Triggers

Update this file when `Node` lifecycle or readiness changes, subtree ownership
moves, route identity or publication changes, Slot or Channel authority flow
changes, a typed RPC gains special policy, or maintenance/backup semantics
change.
