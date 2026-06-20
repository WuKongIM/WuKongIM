# Presence Authority Raft-Term Fencing Design

## Background

The three-node `wkcli sim` smoke can stop during the first gateway activation:

```text
gateway auth failed failure=activation_context_deadline
target: uid=wkcli-sim-u-000001 hash_slot=19 slot_id=1 leader=2 route_revision=16 authority_epoch=4
last_error=internalv2/runtime/presence: not leader
```

The failure happens before any messages are sent. Node 1 resolves the UID route
to Slot 1 leader node 2, but node 2 rejects the presence authority request as
`not leader`.

This became visible after controller task execution was added because startup
now has more control snapshots and task-result writes. That makes the timing
between foreground routing updates and authority worker reconciliation easier to
hit. The deeper issue already existed: `AuthorityEpoch` is generated from each
node's local observation order, but presence RPC treats it as a cross-node
fencing token.

## Problem

`AuthorityEpoch` is not a distributed fact. Two nodes can legitimately observe
the same Slot leader identity through different local event sequences:

```text
node1 route target: hash_slot=19 slot=1 leader=2 authority_epoch=4
node2 directory:     hash_slot=19 slot=1 leader=2 authority_epoch=1
```

Both agree that node 2 is the Slot leader. The request should be accepted, but
the directory rejects it because the local epoch differs from the caller's
epoch.

This means `AuthorityEpoch` is too strong for remote RPC validation. Raising
activation timeouts only makes the failure slower when the epochs never
converge.

## Goals

- Make presence authority ownership follow actual Slot Raft leadership.
- Use a fencing value that is globally meaningful across nodes.
- Preserve correct rejection of stale requests after Slot leadership changes.
- Avoid coupling gateway activation correctness to best-effort route-authority
  event delivery.
- Keep controller tasks as desired-state and workflow state; they must not
  become the source of actual data-plane authority.

## Non-Goals

- Do not implement slot leader transfer in this design.
- Do not make `PreferredLeader` authoritative for user presence.
- Do not introduce a new global presence Raft group.
- Do not make gateway activation bypass cluster routing.

## Chosen Approach

Use the actual Slot Raft leader term as the cross-node authority fence.

The presence authority target should be derived from:

```text
HashSlot
SlotID
LeaderNodeID
SlotLeaderTerm
ConfigEpoch
RouteRevision
```

The hard authority identity is:

```text
HashSlot + SlotID + LeaderNodeID + SlotLeaderTerm + ConfigEpoch
```

`RouteRevision` remains diagnostic and ordering context. A route-revision-only
update for the same Slot leader term must preserve presence routes.

`AuthorityEpoch` should be removed from hard validation. It can be kept
temporarily as a deprecated diagnostic field while callers and DTOs migrate.

## Why Raft Term

Raft leadership is the real source of data-plane authority. `PreferredLeader` is
only controller intent. A requested target leader can be behind or unavailable,
and Raft may elect a different legal leader. Presence must follow the actual
leader:

```text
Controller desired state: slot 1 preferred_leader=2
Slot Raft actual state:  slot 1 leader=3 term=12
Presence authority:      slot 1 leader=3 term=12
```

If leadership later changes:

```text
old authority: slot=1 leader=3 term=12
new authority: slot=1 leader=2 term=13
```

Old requests are rejected by term mismatch, and clients retry by resolving a
fresh route.

## Component Changes

### Slot Multi-Raft Status

`pkg/slot/multiraft.Status` already exposes `Term`. The clusterv2 Slot leader
observer should carry both leader and term into routing:

```text
SlotStatus{
  SlotID
  Leader
  Term
}
```

Unknown leader observations should continue to avoid clearing the last known
non-zero leader. A non-zero leader with a newer term must update the route
table.

### Routing Table

`pkg/clusterv2/routing.Table` should maintain leader terms:

```text
SlotLeaders[slotID]     = leaderNodeID
SlotLeaderTerms[slotID] = raftTerm
SlotConfigEpochs[slotID] = configEpoch
```

`RouteKey`, `RouteKeys`, and `RouteHashSlot` should return:

```text
Route{
  HashSlot
  SlotID
  Leader
  LeaderTerm
  ConfigEpoch
  PreferredLeader
  Peers
  Revision
}
```

### Public Route Authority Event

`clusterv2.RouteAuthority` should carry:

```text
HashSlot
SlotID
LeaderNodeID
LeaderTerm
ConfigEpoch
RouteRevision
```

`AuthorityEpoch` remains only during migration if needed, and must not be used
for cross-node correctness.

### Presence RouteTarget

`internalv2/runtime/presence.RouteTarget` should add:

```text
LeaderTerm uint64
ConfigEpoch uint64
```

The hard validation becomes:

```text
target.LeaderNodeID == localNodeID
slot exists for target.HashSlot
slot.target.HashSlot == target.HashSlot
slot.target.SlotID == target.SlotID
slot.target.LeaderNodeID == target.LeaderNodeID
slot.target.LeaderTerm == target.LeaderTerm
slot.target.ConfigEpoch == target.ConfigEpoch
```

`RouteRevision` should not reject requests when the authority identity is the
same. The directory may advance its stored route revision when it observes a
newer revision.

### Presence Reconcile

The presence worker should not depend on best-effort events as the only source
of truth. Events should become triggers for a pull-based reconcile:

```text
for each hashSlot in current route snapshot:
  route = RouteHashSlot(hashSlot)
  if route.Leader == localNodeID:
    Directory.BecomeAuthority(targetFromRoute(route))
  else:
    Directory.LoseAuthority(hashSlot)
```

Run this reconcile:

- after startup route readiness;
- when `WatchRouteAuthorities` emits an event;
- on a low-frequency periodic timer;
- after local Slot leader observation changes.

This makes dropped route-authority events recoverable.

### Conversation Authority

Conversation authority uses the same route target pattern. If it treats
`AuthorityEpoch` as a remote hard fence, migrate it to the same
`LeaderTerm + ConfigEpoch` model so presence and conversation do not diverge.

### Apply Snapshot And Tasks

Task execution must not delay authority reconciliation. The control snapshot
apply flow should make routing and authority publication visible before task
executors do blocking work.

Target flow:

```text
apply control snapshot
update router
publish or trigger route-authority reconcile
reconcile local Slot assignments
schedule task executors asynchronously
update readiness snapshot
```

Bootstrap and leader-transfer task executors should report progress through the
controller task writer, but their waits and retries should run outside the
foreground snapshot apply path.

### Readiness

Gateway start and `/readyz` should require at least one successful authority
reconcile pass after route readiness:

```text
cluster routes ready
Slot leaders observed with terms
presence authority reconcile completed
conversation authority reconcile completed
gateway start
readyz=true
```

This keeps the smoke helper from starting client connections while the node has
only installed routes but has not installed local authority state.

## Compatibility And Migration

1. Add `LeaderTerm` and `ConfigEpoch` fields while retaining
   `AuthorityEpoch`.
2. Populate the new fields through Slot status, routing, route authority events,
   and presence/conversation targets.
3. Change validation to prefer `LeaderTerm + ConfigEpoch`. During migration,
   reject if the new fields are present and mismatch; old zero-value targets may
   use the old behavior only inside tests or single-process adapters.
4. Remove remote dependence on `AuthorityEpoch`.
5. Update docs and tests to describe `AuthorityEpoch` as deprecated diagnostic
   state, or remove it after all callers migrate.

## Testing Plan

- Unit: routing table preserves and returns Slot leader terms.
- Unit: route-authority events include `LeaderTerm` and `ConfigEpoch`.
- Unit: presence directory accepts different local `AuthorityEpoch` values when
  `LeaderTerm + ConfigEpoch` match.
- Unit: presence directory rejects an old leader term after Slot leadership
  changes.
- Unit: route-revision-only updates preserve active presence routes.
- Unit: dropped route-authority event is repaired by pull-based reconcile.
- Integration: controller task progress churn does not cause activation
  `not leader`.
- E2E: three-node `wkcli sim` with 1000 users and 1000 groups reaches send
  phase and does not stop during initial activation.
- E2E: during slot leader transfer, activation retries may happen, but clients
  eventually resolve the actual Raft leader and continue.

## Risks

- If Slot status term is stale on one node, a request may still retry until that
  node observes the fresh term. This is acceptable and bounded by normal Raft
  observation.
- Async task executors need careful idempotency because apply no longer blocks
  on them. Existing task IDs and attempt/config fences should be used for this.
- Conversation authority must be migrated in the same series to avoid retaining
  the same latent bug in message post-commit paths.

## Success Criteria

- Presence authority validation no longer depends on a caller-provided local
  observation epoch.
- Actual Slot Raft leader and term are the source of authority.
- Controller task churn cannot make route targets permanently disagree with
  target-node Directory state.
- The reported smoke command can run past activation and into message sending
  without `activation_not_leader` or `activation_context_deadline`.
