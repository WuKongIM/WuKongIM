---
scope: package
summary: Materializes durable person-directory tasks through bounded shared preparation and recovery workers.
---

# Person Directory Flow

## Responsibility

The projector creates both UID memberships for a durable person Channel task,
then marks the exact source generation Ready. It owns shared work and completion
publication; the source adapter owns Slot authority and persistence.

## Boundaries

`TaskSource` supplies current-leader discovery, generation validation and durable
completion. `MembershipWriter` owns routed UID membership writes. The projector
owns only bounded scheduling and result publication; entry protocols and message
append policy remain outside this package.

## Main Flows

- `Prepare` admits bounded direct requests and waits for their aligned results.
  Canceling a waiter does not cancel accepted shared durable work.
- One scanner owns round-robin discovery cursors and at most one reserved page.
  The page shares the 512-task budget with queued and executing work. Storage
  reads run outside the scheduler so they cannot hold completed caller results.
- The scheduler alone owns known/inflight tasks and waiters. It deduplicates
  discovery against direct admissions, queues batches to eight fixed workers,
  and publishes results independently of scans. At most one pending batch waits
  for the existing worker queue, charged to inflight. Queue space directly
  resumes dispatch; completion handling and Stop stay available throughout.
- Workers take only already-queued FIFO batches within the original 64-task
  page. An unfitting head stays owned for the next worker; no fill timer or
  cluster operation runs under queue ownership.
- Workers recheck source leadership and generation, create UID memberships,
  and complete only tasks with all memberships durable. With a writer that
  reports physical Slot groups as they finish, workers keep the original queued
  task groups intact. A group may commit Ready and publish once its membership
  results are terminal. For a mixed worker cohort, at most two joined Ready
  workers consume the original groups while membership callbacks keep making
  progress. The writer supplies the same shared Slot proposal context to all
  calls. The original worker retains its task credits through the joins.
  Failed durable tasks remain discoverable at the original repair cadence.
- Stop cancels the generation and joins the scanner, scheduler and all workers.
  A caller timeout leaves the same cleanup ownership available to a later Stop.

## Invariants and Failure Semantics

No scan or callback alone proves Ready. Membership and source completion remain
real durable operations. Page size, eight workers, 512-task/waiter bounds,
four-second attempts, authority fences and metadata formats are unchanged.

## Read First

- [Scheduler and worker ownership](projector.go)
- [Direct preparation](prepare.go)
- [Public scheduling regression](scheduling_public_integration_test.go)

## Update Triggers

Update when scheduling ownership, discovery, admission bounds, completion
publication or Stop semantics change.
