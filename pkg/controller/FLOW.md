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
- Transport can deliver Raft, state-sync and control/task RPC before startup
  completes. Runtime resource pointers publish and snapshot under the existing
  state lock; queue waits and FSM snapshots run after that lock is released.
  All facade Raft reads snapshot the service through the same short lock.

## Main Flows

1. Raft Ready persists HardState, entries, and snapshots before message send and
   FIFO apply. Already-waiting proposals and peer Steps use separate bounded
   Ready batches; kinds never mix, preserving exact local future association.
   Charge original peer bytes before Raft can rewrite shared entry headers.
   The scheduler batches commands, applies FSM semantics, saves one
   final state file, publishes it, then persists the applied boundary.
2. Startup restores the latest snapshot when materialized state is absent or older, replays the
   committed suffix, and automatic/manual compaction snapshots only applied
   materialized state before trimming covered WAL history.
3. Planner, lifecycle, and task APIs propose versioned fenced commands through
   Raft, while non-voter mirrors refresh the complete state file and watchers
   publish latest-state wakeups.
4. Rare `EnsurePresenceOwnerSet`, `ClaimPresenceGeneration` and
   `GetPresenceGeneration` calls use committed quorum/apply barriers. Claims CAS
   against the preceding Hash Slot generation and return their exact FSM apply
   receipt through the Raft proposal future. `G` and owner epoch `M` use committed
   Raft indexes; boot IDs are equality identities only. Controller validates desired
   mapping, while the Node facade separately checks actual Slot leadership/term.
   No UID, Session, or per-SEND record enters this section.

## Invariants and Failure Semantics

- The Raft WAL plus applied-boundary metadata is authoritative. The JSON file
  is its materialized state and is saved before publication and applied-index
  advancement.
- New WAL segments have versioned, independently checksummed headers; prefix
  compaction deletes only fully covered segments after persisting any legacy
  successor anchor and syncs each prefix deletion.
  Startup can anchor a pruned legacy first header only after strict read-only
  validation of its node identity, every remaining CRC, snapshot checksum,
  matching durable metadata, and complete committed suffix. A checksummed, atomic
  `legacy-anchors.json` persists that proof before append, allowing ordinary crash recovery
  when WAL later advances beyond metadata. It rewrites no WAL.
  All other checksum failures remain fatal. Incomplete newest physical tails
  alone may be truncated and synced; legacy recovery probes never truncate.
- Offline bootstrap inspection reads bounded WAL and snapshot artifacts without
  truncation or startup repair; even an incomplete newest tail fails verification.
- Raft storage readers capture one immutable last-successful durable cut and never
  acquire the writer's I/O lock. Entry headers and all ConfState slices are private;
  payloads and cloned snapshots stay immutable, with detached public results.
  Successful persistence publishes the cut; failed writes and empty/no-op paths
  do not. Applied-only publication reuses the suffix. ReadIndex still waits for
  fresh quorum and the successful FSM/durable-applied watermark.
- `Revision` versions logical cluster state; `AppliedRaftIndex` versions Raft
  materialization. Empty probes and health reports may advance applied state
  without advancing logical revision.
- Presence control reads use leader-only quorum ReadIndex, then wait for the
  actual FSM save and durable applied marker; RawNode Advance is not that proof.
  The current term must commit before issuing reads. At most 64 protocol tokens
  exist, including canceled issued tokens until quorum retirement or term reset;
  tombstones retain no caller context. Peer-forwarded read requests/responses
  are unsupported. Known leadership loss retires reads before any blocking
  Ready work. Stop and apply failure close pending reads, including waits
  inside apply-queue admission or configuration application. ProbePropose keeps
  its distinct write-path probing contract.
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
- Startup publishes the Raft service under the runtime mutex; inbound Step
  captures it under that mutex and releases the lock before Raft execution.
- A capability-proven, bounded Presence owner vector includes non-data ingress
  nodes and retains leaving/down obligations. Unknown schema, command or
  business-protocol support prevents activation. Once active, new/readdressed
  member endpoints must prove those capabilities before their membership CAS;
  a private bounded probe client does not publish an unaccepted endpoint to
  discovery. Removing an old owner without a separate drain proof remains
  not-ready. Health does not discharge ownership.
- Presence format 2 binds capability responses to the full semantic membership
  cut, including removed tombstones, endpoints, join states, roles and voters.
  Unrelated logical revisions do not invalidate this cut. Only first activation
  from absent Presence may use its zero M/stamp basis; normal lifecycle forbids
  endpoint/NodeID reuse and requires external drain before removal. This does
  not prove arbitrary pre-activation raw-mutation ABA or a legacy owner's drain.
- After activation, semantic membership mutations advance a durable roster
  high water, including ABA. A stamp newer than M closes old serving gates.
  Ensure binds observed M and stamp; even a restored identical vector requires
  a new M after such change. Claim also retains its exact per-Hash-Slot
  predecessor and mapping fence. Older format capability, missing stamp and
  snapshot high-water rollback fail closed; there is no automatic downgrade.
- With an unchanged activated roster stamp and exact fresh capabilities, Ensure
  returns M from its quorum read cut without a noop proposal. Concurrent later
  M may supersede that read result; it never rewrites the later vector and cannot
  authorize a subsequent Claim or owner Begin using the superseded M.
- Optional Presence state participates in clone, normalization, checksums,
  snapshots and mirror sync. Existing-state snapshot install rejects known G/M
  rollback. Ordinary backup restore replaces data while preserving Controller
  history. Replacing all Controller history with an old disk image cannot prove
  monotonicity and is not an identity-preserving generation recovery protocol.

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
