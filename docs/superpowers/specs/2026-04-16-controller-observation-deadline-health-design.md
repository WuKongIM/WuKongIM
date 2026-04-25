# Controller Observation and Deadline-Driven Health Design

## Summary

WuKongIM's controller currently treats high-frequency observation data as Raft
commands. Every controller observation loop can:

- propose `NodeHeartbeat`
- propose per-slot `RuntimeView` updates
- propose `EvaluateTimeouts`

This keeps all controller inputs fully replicated, but it pushes fast-changing
observation traffic onto the same Raft log that should primarily carry control
plane decisions such as assignment changes, repair tasks, rebalance tasks, and
operator actions.

In a steady three-node cluster with no user traffic, this design still produces
continuous controller `CommittedEntries`, synchronous Pebble writes, and CPU /
GC pressure. The main issue is not the existence of controller consensus; it is
that observation and time progression are modeled as consensus writes.

This design keeps controller decisions and node health state authoritative under
Raft while moving raw observation into a leader-local cache. Node liveness is
advanced by a leader-local deadline scheduler and only emits Raft proposals when
status crosses an edge such as `Alive -> Suspect` or `Suspect -> Dead`.

## Goals

- Remove `NodeHeartbeat` and `RuntimeView` from the controller Raft hot path.
- Remove fixed-period `EvaluateTimeouts` proposals.
- Keep `Alive / Suspect / Dead / Draining` as controller-authoritative,
  durable, replicated state.
- Preserve existing repair / rebalance semantics built on controller state.
- Keep controller failover safe and predictable.
- Make idle clusters nearly quiet at the controller Raft layer.
- Reduce empty-work CPU, allocation, GC, and Pebble sync write load.

## Non-Goals

- No rewrite of controller repair / rebalance policy.
- No rewrite of slot `multiraft` semantics.
- No switch to fully derived node health state in v1 of this redesign.
- No attempt to make follower controllers maintain identical raw observation
  caches.
- No change to operator-facing node states or draining semantics.

## Current Problems

### 1. Observation is modeled as consensus traffic

`slotAgent.HeartbeatOnce()` reports node heartbeat and per-slot runtime views on
an observation loop. The controller leader currently turns those reports into
`CommandKindNodeHeartbeat` proposals, which are then replicated and committed.

This means ephemeral values such as:

- `LastHeartbeatAt`
- `LastReportAt`
- `LeaderID`
- `HasQuorum`
- `CurrentPeers`

are continuously written through controller Raft even when they do not change
controller intent.

### 2. Time progression is modeled as periodic consensus traffic

`controllerTickOnce()` currently proposes `CommandKindEvaluateTimeouts` on every
controller observation tick. The state machine then scans all nodes and writes
updated node records even when no node crosses a liveness boundary.

This makes "time passing" look like a replicated mutation.

### 3. Control-plane writes are dominated by non-decision traffic

Controller Raft should primarily protect:

- assignments
- reconcile tasks
- migration state
- operator actions
- durable node health transitions

Instead, it is also carrying high-frequency observation and periodic timeout
reevaluation.

## Decision

The controller will split control-plane inputs into three classes:

### 1. Replicated control decisions

These continue to go through controller Raft:

- assignment updates
- reconcile task updates
- migration commands
- operator actions
- node health edge transitions

### 2. Leader-local observation

These no longer go through controller Raft:

- raw node heartbeat reports
- raw per-slot runtime views
- report timestamps

The controller leader will store them in a local in-memory `observationCache`.

### 3. Leader-local time-driven scheduling

Timeout reevaluation is no longer a fixed-period Raft proposal. The controller
leader will maintain node health deadlines locally and only propose a replicated
update when a node actually changes health state.

## Architecture

### A. `observationCache`

Add a leader-local observation cache owned by the controller host.

### Responsibilities

- store the freshest observation for each node
- store the freshest observation for each slot runtime view
- provide a consistent snapshot for planner / reconciler reads on the leader
- perform local de-duplication of unchanged observation payloads

### Suggested structure

- `nodes[nodeID] -> observedNode`
  - `lastSeen`
  - `addr`
  - `capacityWeight`
  - optional local bookkeeping fields
- `slots[slotID] -> observedRuntimeView`
  - `leaderID`
  - `hasQuorum`
  - `currentPeers`
  - `observedConfigEpoch`
  - `lastReportAt`

This cache is not replicated and is not persisted.

### B. `nodeHealthScheduler`

Add a leader-local scheduler responsible for translating raw node observation
into durable health transitions.

### Responsibilities

- maintain `suspectAt` and `deadAt` deadlines per node
- wake up only when the nearest deadline expires
- re-check current observation on wake-up
- propose replicated status changes only when a node crosses a state edge

### Deadline model

For each observed node:

- `suspectAt = lastSeen + suspectTimeout`
- `deadAt = lastSeen + deadTimeout`

When a new heartbeat arrives, the scheduler refreshes deadlines for that node.
When a deadline fires, the scheduler validates that the deadline is still
current before acting.

### Generation-based invalidation

Each scheduled deadline should carry a node-local generation counter. When
observation refreshes a node, the generation increments. Expired timers with an
older generation are ignored.

This avoids stale wake-ups after fresh observation has already arrived.

### C. Controller command model

### Remove from normal write path

The following command patterns should stop being part of the normal controller
Raft path:

- `CommandKindNodeHeartbeat`
- `CommandKindEvaluateTimeouts`

`CommandKindNodeHeartbeat` may remain temporarily for compatibility or tests,
but the production path should no longer use it for steady-state observation.

### Add explicit status delta command

Introduce a command dedicated to durable node health transitions, for example:

- `CommandKindNodeStatusUpdate`

Payload shape:

- one or more node status transitions in a single batch
- each transition includes:
  - `nodeID`
  - `newStatus`
  - optional expected prior status
  - a causality timestamp such as `observedAt` or `evaluatedAt`

This command represents a controller decision, not a request to scan the whole
node table.

### D. Planner input model

The planner should consume a combined view:

- durable controller state from `controllerMeta`
  - nodes
  - assignments
  - tasks
  - migrations
- leader-local observation snapshot from `observationCache`
  - runtime views
  - fresh liveness evidence

This preserves the current controller-authoritative model while ensuring the
planner sees fresh runtime information without turning that information into
Raft log entries.

## Data Flow

### 1. Node / slot observation report

1. `slotAgent.HeartbeatOnce()` sends:
   - one node report
   - zero or more slot runtime view reports
2. controller follower redirects to leader as today
3. controller leader accepts the report
4. leader updates `observationCache`
5. if the report updates node liveness evidence, leader refreshes that node's
   deadlines in `nodeHealthScheduler`
6. no Raft proposal is emitted at this point

### 2. Health edge transition

1. a node's local deadline expires on the leader
2. `nodeHealthScheduler` re-checks current observation for the node
3. if no status edge is crossed, nothing is proposed
4. if status changes, leader proposes `NodeStatusUpdate`
5. controller Raft commits the update
6. state machine persists the node's new durable health state
7. planner and reconciler now observe the updated durable status

### 3. Assignment / task decision

1. controller leader builds planner input from:
   - durable controller state
   - observation snapshot
2. planner determines whether repair / rebalance / bootstrap is needed
3. if a decision exists, leader proposes the corresponding assignment / task
   update through controller Raft
4. state machine persists the durable decision

## Leader Change Semantics

### Warmup period

A newly elected controller leader starts with an empty or incomplete
`observationCache`. It must not immediately treat missing observation as node
failure.

During a leader warmup period:

- the leader accepts fresh observation and builds cache state
- node health scheduling is paused or conservative
- planner does not issue repair / rebalance decisions that depend on missing
  fresh observation

Suggested warmup rule:

- warmup lasts at least `controllerObservationInterval * 2..3`
- or until the leader receives at least one fresh observation round from the
  cluster nodes it expects to hear from

The system should prefer delayed repair over false failure detection right after
leader election.

### Post-warmup recovery

After warmup, the leader reconstructs deadline scheduling from observed
`lastSeen` values and resumes normal edge-driven health transitions.

## Query Semantics

### Observed runtime views

APIs that currently expose controller runtime views should no longer read them
from `controllerMeta`.

New semantics:

- leader answers from local `observationCache`
- follower redirects to leader or returns `not leader`

This matches the fact that runtime views are now leader-local observations, not
replicated metadata.

### Durable node status

Durable node status remains readable from controller metadata and remains
consistent across controller failover.

## State Machine Changes

The controller state machine should become responsible only for applying
controller decisions, not for recomputing time-based health transitions.

### Apply node health transition

When `NodeStatusUpdate` is committed, the state machine:

- loads the durable node record
- validates any optional expected prior status
- applies the new durable status
- persists the updated node record

### Remove full-table timeout scan

`applyTimeoutEvaluation()` should be removed from the steady-state path.
The state machine should not periodically scan every node based on a leader-
provided `Now` value.

## Correctness Constraints

- only the controller leader accepts observation into the authoritative cache
- only replicated status edge transitions change durable node health state
- repair / rebalance decisions must use durable node health state, not raw
  missing observation alone
- `Draining` remains operator-controlled and must not be overridden by fresh
  observation
- a stale local timer must never emit a proposal after fresher observation has
  already advanced the node generation

## Performance Expectations

This design should remove three major sources of idle control-plane churn:

- per-observation controller Raft proposals
- periodic `EvaluateTimeouts` proposals
- repeated durable writes for unchanged runtime view and timeout state

After rollout, idle controller Raft traffic should mostly consist of:

- leader election / membership mechanics
- real node health edge transitions
- real assignment / task / migration decisions

## Testing Strategy

### Unit tests

#### `observationCache`

- newest observation wins
- stale observation does not overwrite newer state
- unchanged runtime view can be de-duplicated

#### `nodeHealthScheduler`

- heartbeat refresh pushes deadlines forward
- stale generation wake-up is ignored
- no proposal is emitted when no state edge is crossed
- `Alive -> Suspect -> Dead` edges emit exactly one proposal each
- fresh heartbeat before deadline prevents status transition

#### Controller service / host tests

- follower redirects observation reports
- leader accepts observation without emitting Raft proposals
- leader change clears or invalidates prior local scheduling state
- warmup suppresses repair / rebalance decisions until observation is fresh

#### Planner / cluster integration tests

- planner still repairs when a node is durably marked dead
- planner still skips degraded no-quorum slots
- runtime-view-dependent decisions still work using observation snapshots
- `Draining` semantics remain unchanged

#### Performance validation

On the existing three-node docker-compose cluster:

- compare idle controller `CommittedEntries` before and after change
- compare controller CPU usage before and after change
- compare GC / allocation profile before and after change
- verify that a stopped node still transitions to `Suspect` / `Dead` and
  triggers repair after the configured timeout

## Rollout Plan

### Phase 1

- introduce `observationCache`
- move node heartbeat and runtime view handling off the controller Raft path
- switch runtime view reads to leader-local observation

### Phase 2

- introduce `nodeHealthScheduler`
- replace `EvaluateTimeouts` with edge-driven `NodeStatusUpdate`
- remove full-table timeout scanning from the steady-state path

### Phase 3

- add leader warmup logic
- verify no false repair / rebalance on controller leader change

### Phase 4

- tune observation cadence separately from health timeout logic if needed

## Risks and Mitigations

### Risk: leader-local observation creates transient blind spots after failover

Mitigation:

- explicit warmup window
- fail-closed planner behavior during warmup
- deadline reconstruction from fresh observation only

### Risk: planner behavior changes when runtime view is no longer durable

Mitigation:

- keep durable assignment / task state unchanged
- preserve runtime-view shape in planner inputs
- add regression tests covering repair, rebalance, and degraded cases

### Risk: stale timers emit wrong node state updates

Mitigation:

- generation-based timer invalidation
- optional expected prior status in `NodeStatusUpdate`

## Recommendation

Implement this design in the following priority order:

1. remove observation from the controller Raft hot path
2. add deadline-driven health scheduling
3. remove fixed-period timeout proposals
4. validate idle-cluster performance and failure recovery behavior

This keeps the current controller-authoritative control model intact while
moving observation and time progression out of the consensus write path that is
currently dominating idle controller cost.
