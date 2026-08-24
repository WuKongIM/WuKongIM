---
scope: package
summary: Owns Channel-keyed reactor state, event scheduling, replication progress, lifecycle transitions, and fenced worker completion.
---

# Channel Reactor Flow

## Responsibility

`pkg/channel/reactor` owns every loaded Channel runtime. A stable hash assigns a
Channel key to one node-local reactor, and that reactor goroutine is the sole
writer of its machine, append, replication, retention, and lifecycle state.

Blocking store, transport, metadata-resolution, and close work leaves through
typed bounded workers and returns as `EventWorkerResult`.

## Boundaries

- Reactor partitioning is node-local execution ownership, not a cluster hash
  Slot or persisted routing decision.
- Pure Channel invariants live in `pkg/channel/machine`; public blocking calls
  are assembled by `pkg/channel/service`.
- The reactor decides when work is valid and required. Workers perform I/O but
  never mutate runtime state directly.
- Observers and benchmark events read bounded state through the mailbox and do
  not bypass the single-writer rule.

## Main Flows

1. Priority mailboxes admit control, append, replication, worker-completion,
   and maintenance events under fairness and due-work budgets; appends then
   validate metadata/capacity, flush to workers, and apply fenced completions.
   Exact durable-quorum proposals flush immediately from the reactor; the
   MessageDB coordinator below this seam remains the physical group-commit
   batching owner.
2. Leaders apply `AckOffset`, serve cached or stored pulls, and advance quorum;
   followers run one continuous pull/apply chain, return progress, checkpoint
   idle HW, and activate from hints only after authoritative bounded loading.
   Repeated caught-up anti-entropy probes widen deterministically to one minute;
   only discovered missed progress resets the initial short cadence.
3. The lifecycle controller checkpoints and stops caught-up replicas, performs
   a final mailbox-ordered recheck, and evicts or shuts down runtime/store
   ownership only after every fence and pending-work guard passes.

## Invariants and Failure Semantics

- Each asynchronous completion must match Channel key, generation, epoch,
  leader epoch, and operation ID. Stale results cannot advance or evict a newer
  incarnation.
- Quorum install remains pending work and keeps append admission closed until
  recovery plus the current-authority barrier succeeds. Each accepted append
  completes directly from its exact quorum-commit receipt, without hot-path
  PullHint or AckOffset signals.
- One loaded runtime has one lifecycle controller. Hot follower replication
  remains separate but exposes pending-work evidence to lifecycle guards.
- Cold activation is not loaded state and does not consume `MaxChannels` until
  metadata and store loading both succeed.
- A hint is never authority proof. Newer hints coalesce behind one fixed lease;
  only authoritative, active, structurally valid local-replica metadata applies.
- Mailbox turns, append batches, caches, pending work, workers, checkpoints,
  retries, activation, and observer payloads remain bounded.
- Shutdown closes admission, fences late delivery, detaches every published
  store once, drains worker-owned closes, and never closes the shared store
  factory or database.
- Committed lookup is read-only and returns a row only when its positive
  sequence is covered by current HW.

## Read First

- [Reactor state](reactor.go)
- [Event domains](event.go)
- [Main loop](reactor_loop.go)
- [Lifecycle controller](lifecycle_controller.go)
- [Worker completion routing](worker_completion.go)

## Update Triggers

Update this file when event domains or priority change, runtime ownership
changes, a worker task or fence is added, replication progress changes,
activation or lifecycle stages change, or shutdown/store ownership changes.
