---
scope: package
summary: Schedules bounded message-edit notification repair and retention cleanup through injected source and dispatcher ports.
---

# Message Update Repair Flow

## Responsibility

One supervised loop discovers durable work on owned hash slots and delegates
notification and retention policy to the message usecase.

## Boundaries

No HTTP, gateway frames, concrete cluster construction or message-body storage
belongs here. The composition root starts, stops and joins this worker.

## Main Flows

1. Rotate bounded owned-slot scans under a four-second turn deadline.
2. Prioritize active slots; dispatch at most 32 subscriber pages per turn and
   16 pages per target before yielding. Progress remains durable outside the loop.
3. Scan bounded body-free retention-index candidates and delegate guarded cleanup.
4. Cancel/join before shutdown or restore; restart with empty process cursors.

## Invariants and Failure Semantics

- At most four slot selections per tick and eight pending/cleanup candidates per
  selection. No per-channel or per-member goroutine is created.
- Failed work stays durable and retries on later passes. Stale-version tasks
  cannot clear newer notification work.
- Error counts are reported at most once per minute without message contents
  or identities in labels.

The selector always reserves background Slot capacity even when only two Slots
are owned, so a hot Slot cannot permanently hide cold notification/cleanup work.

## Read First

- [Worker](worker.go)

## Update Triggers

Update when scan budgets, fairness, retry, observation or lifecycle changes.
