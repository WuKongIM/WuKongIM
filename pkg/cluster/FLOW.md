---
scope: subtree
summary: Composes Controller state, Slot Multi-Raft metadata, typed node RPC, routing, and replicated Channel runtimes behind Node.
---

# Cluster Runtime Flow

## Responsibility
`Node` composes cluster runtimes and owns lifecycle, readiness and bounded snapshots.

## Boundaries

- `control` adapts Controller state, writes, Raft transport, and snapshots;
  `routing` publishes hash-Slot authority; `slots` and `propose` own Slot
  Multi-Raft lifecycle and proposals; `channels` hosts Channel runtimes; `net`
  transports typed node RPC; `observe` runs low-frequency reporting.
- `Node` delegates validated intents; Manager policy, drain safety, DTOs and response shaping stay in `internal`.
- Typed RPC routes opaque DTOs by registered service; cluster may fence maintenance/ownership but does not absorb delivery or Manager logic.
- Controller, Slot, Channel, transport and storage stay behind public facades and neutral errors.

## Main Flows

1. Node construction records format identity only for fresh directories; startup rechecks
   supported markers before writable runtimes. Lifecycle starts transport and Controller, installs control routes, reconciles
   Slots/Channels and exposes readiness. Stop rejects work and reverses ownership.
2. Slot proposals and metadata facades resolve one immutable route snapshot,
   group Channel- or UID-owned work by physical Slot, execute locally or
   forward, and recheck leadership. Person-directory prepare joins UID membership/runtime metadata before publishing directory-ready.
3. Channel append resolves or creates Slot-owned runtime metadata, applies it
   monotonically to the selected runtime, and appends locally or forwards to
   the exact leader while background control/task convergence stays bounded.
   Repair probes activate cold replicas through authoritative metadata and the
   native reactor before inspecting progress. Native follower proofs read exact
   durable state and recheck metadata/runtime authority, rejecting future durable
   epochs or fences; diagnostic probes stay read-only. A dead
   Leader can preempt an unpromoted replacement through the existing guarded
   abort, then elect from the next authoritative scan; promoted tasks are protected.
   Replacement catch-up stays runnable while its valid target is lagging.
   Failover proof renewal re-probes the surviving target under the current fence;
   it never falls back to draining the unavailable source.
4. Conversation and history reads batch Slot routes and group by exact Leader,
   preserving alignment and item errors. Conversation codec 10 carries UID-owned
   badge floors, excluded internal-position counts, and optional set-unread
   boundaries; leader reads add retention and the latest own send. Cold quorum Leaders (even HW=LEO=0)
   recover first; loaded Leaders still installing authority cannot serve HW.
   Indexed committed reads use a distinct RPC kind, retaining HW, retention,
   and authority fences; older nodes reject it instead of serving a range.
5. `LocalControlSnapshot` exposes the latest fully Node-applied control state;
   revision-fenced management adapters may use `LocalControllerSnapshot` to read
   Controller-visible state without waiting for runtime task reconciliation.
   Repair also reads this fresh health snapshot so a stalled task cannot hide failures.
6. Controller-backed management mutations, including Slot leader-transfer task
   creation, preserve semantic CAS errors and task identity through typed RPC;
   task-result RPC carries executor progress and terminal observations.

## Invariants and Failure Semantics

- `pkg/dataformat` owns immutable format/creator metadata; missing markers stay unregistered.
  Corrupt or unsupported markers fail before storage opens.
- Offline generation seals reject incomplete imports and mismatched bootstrap
  configuration before native startup.
- Event sequence reads route to the Slot leader and include durable projections.
- Route authority is `(HashSlot, SlotID, LeaderNodeID, LeaderTerm,
  ConfigEpoch, RouteRevision)` from one immutable publication. Local
  `AuthorityEpoch` is diagnostic only and never a distributed fence.
- Desired or preferred ownership never substitutes for an observed leader.
  Missing, stale, incomplete, duplicate, or mismatched authority evidence
  fails readiness or the foreground operation closed.
- Slot Raft defaults to a 50 ms local tick, two-tick heartbeat, and 40-tick
  election floor. Heartbeats run every 100 ms and elections start after at
  least two seconds, while proposal replication remains event-driven.
- The data-plane lease expires when the node cannot publish healthy readiness;
  local Channel leaders then reject new writes without discarding admitted work.
- Slot and Channel metadata are authoritative at their current owners. Caches,
  hints, compatibility codecs, and local replicas cannot roll back a newer
  generation or make migration decisions from stale state.
- Runtime-meta creation uses one supervised owner per logical Slot: duplicate
  identities coalesce, unique work is bounded and canonical-sorted, placement
  comes from one current revision, and uncertain proposals retry only rows an
  authoritative reread proves missing.
- UID-owned membership fanout and person-directory batches have fixed
  concurrency. Directory-ready can never hide missing UID membership or
  missing append runtime metadata.
- Lifecycle, fanout, retries, scans, repairs, tasks and diagnostics stay bounded.
  Repair scans rotate Slots with row cursors under tick/task budgets. Slot
  leadership loss drops its cursor; newly unavailable nodes restart owned Slot
  scans, while unchanged health retains progress. Migration ticks have a five-second
  deadline and up to eight durable steps per automatic failover. Task cursors
  advance one candidate at a time, rechecking Slot ownership; a timed-out task
  cannot skip other unstarted candidates in a larger configured tick budget.
  Fresh durable progress permits continuation; stalled work yields immediately.
- Maintenance closes business admission before storage replacement and keeps
  only explicitly allowed restore RPC available. Backup and restore retain
  cluster routing and exact authority fences.

## Read First

- [Public API](api.go), [Node ownership](node.go), [Lifecycle](node_lifecycle.go)
- [Routing publication](routing/router.go), [Channel hosting](channels/service.go)

## Update Triggers

Update when lifecycle, readiness, ownership, route/authority publication,
typed RPC policy, or maintenance/backup semantics change.
