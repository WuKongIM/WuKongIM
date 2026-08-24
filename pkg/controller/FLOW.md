---
scope: subtree
summary: Implements the canonical Controller Raft runtime, durable cluster state, mirror synchronization, planning, and fenced task transitions.
---

# Controller Flow

## Responsibility

`pkg/controller` is the reusable Controller engine. Controller voters replicate
commands through Raft and materialize committed state into
`cluster-state.json`; non-voters remain mirrors and refresh the complete state
file from voters.

The root package is the public facade. `state`, `statefile`, `command`, `fsm`,
`planner`, `sync`, `raft`, and `server` are focused engine components and must
not depend on `pkg/cluster`.

## Boundaries

- `pkg/cluster/control` adapts this engine into the production cluster. This
  subtree does not own product startup, Manager safety policy, or Slot/Channel
  execution.
- Controller state holds durable, bounded control intent and progress only;
  high-frequency runtime observations, raw credentials, repository chunks,
  Channel identities, and local staging paths stay outside it.
- Observers run after authoritative transitions and cannot influence Raft,
  persistence, applied boundaries, or task semantics.
- Watch events are wakeups. Consumers needing exact current state read
  `LocalState`.

## Main Flows

1. Raft Ready persists HardState, entries, and snapshots before message send and
   FIFO apply; the scheduler batches commands, applies FSM semantics, saves one
   final state file, publishes it, then persists the applied boundary.
2. Startup restores materialized state or the latest snapshot, replays the
   committed suffix, and automatic/manual compaction snapshots only applied
   materialized state before trimming covered WAL history.
3. Planner, lifecycle, and task APIs propose versioned fenced commands through
   Raft, while non-voter mirrors refresh the complete state file and watchers
   publish latest-state wakeups.

## Invariants and Failure Semantics

- The Raft WAL plus applied-boundary metadata is authoritative. The JSON file
  is its materialized state and is saved before publication and applied-index
  advancement.
- Startup may repair only an incomplete physical record at the newest WAL
  segment tail by truncating to the last complete record and syncing it before
  append. Checksum mismatches, incomplete older segments, and a newest segment
  without any complete record fail closed.
- `Revision` versions logical cluster state; `AppliedRaftIndex` versions Raft
  materialization. Empty probes and health reports may advance applied state
  without advancing logical revision.
- State normalization, validation, checksums, clone, snapshot, and sync must
  cover every durable section, including bounded operations MCP and scheduled
  backup state.
- Task progress and completion are fenced by task identity, kind, Slot, epoch,
  attempt, phase, participant, and observed Raft proof as applicable. Durable
  assignments change only at the final proven commit phase.
- Node removal is only a tombstone primitive; higher layers prove drain safety.
  A changed removal is revision-fenced, while an existing tombstone is
  idempotent.
- Controller voter promotion separates live learner/voter preparation from the
  final revision- and voter-proof-fenced durable state command.
- Watch buffers retain the newest visible state under pressure; bounded
  observers and diagnostics must never stall apply.

## Read First

- [Runtime facade](runtime.go)
- [Durable state model](state/types.go)
- [Raft run loop](raft/service_run.go)
- [FSM command dispatch](fsm/mutations.go)
- [Mirror sync contracts](sync/contracts.go)

## Update Triggers

Update this file when durable Controller state changes, Raft/apply ordering
changes, a command or task fence changes, voter versus mirror ownership changes,
snapshot/compaction semantics change, or a new observer enters the apply path.
