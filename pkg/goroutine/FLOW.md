---
scope: package
summary: Tracks first-party goroutine ownership, fixed task labels, pool pressure, panics, health, metrics, and bounded shutdown evidence.
---

# Goroutine Ownership Flow

## Responsibility

This package is the process-wide ownership registry for first-party goroutines
reachable from `cmd/wukongim`. Business modules still own cancellation, queue
closure, dependency order, and restart policy.

## Boundaries

- Standalone tools, `pkg/client`, tests, generated code, and third-party
  goroutines are unmanaged; snapshots report process, managed, and non-negative
  unmanaged totals.
- Task IDs come from a fixed low-cardinality catalog. Node, Slot, Channel, UID,
  connection, request, error, and function values never become labels.
- `Default()` is the shared always-on registry used before and during app composition.

## Main Flows

1. `SafeGo` validates the task, increments lifecycle state, applies pprof
   labels, executes, records bounded panic evidence, applies recover/repanic
   policy, and decrements active state.
2. Audited pool adapters register scrape-time worker, busy, capacity, queue,
   rejection, and ownership snapshots; health is derived per pool before totals.
3. Group waits and baseline fences provide bounded live-task evidence during
   shutdown without canceling work.

## Invariants and Failure Semantics

- Burst tasks recover according to catalog policy; critical permanent loops
  repanic. Critical panics and over-declared fixed counts are unhealthy.
- Pool rejection or full bounded queues are critical. At least 80% worker use
  with queueing becomes warning only after ten seconds; relief or a monitoring
  gap resets continuity.
- Retired-pool rejection totals remain monotonic. Inflight, capacity, queue,
  and rejected work are distinct from worker goroutine counts.
- Required tasks are unhealthy below expected count after readiness. Optional
  tasks become required only after first start.
- Only compile-time expected cohorts use `fixed`; configuration-sized or
  per-runtime cohorts use `dynamic`.
- Terminal prepare, Gateway SEND drain, UID membership fanout, and Channel
  quorum pools use fixed task identities; run, user, Channel, Slot, peer,
  authority, and capability values remain ordinary data.
- Permission and runtime-metadata batch fanout use distinct fixed Slot task
  identities and bounded worker cohorts.
- Manager Prometheus queries use one fixed burst identity for the bounded
  per-request fanout cohort.

## Read First

- [Registry](registry.go)
- [Task catalog](catalog.go)

## Update Triggers

Update this file when task catalog, panic policy, ownership, pool accounting,
health thresholds, metrics, fences, or shutdown evidence changes.
