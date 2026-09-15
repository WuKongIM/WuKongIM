---
scope: package
summary: Schedules bounded message-edit notification repair and retention cleanup through injected source and dispatcher ports.
---

# Message Update Repair Flow

## Responsibility

One supervised loop drains committed-edit hints and discovers durable work on
owned hash slots. It delegates notification and retention policy to the message
usecase. Ready dispatch and durable repair each overlap at most four independent
identities in joined lanes. The supervising loop runs these turns serially.

## Boundaries

No HTTP, gateway frames, concrete cluster construction or message-body storage
belongs here. The composition root starts, stops and joins this worker.

## Main Flows

1. Accept body-free committed identities into a 1,024-entry coalescing ready queue.
   A wake drains at most eight visits of four subscriber pages under a one-second
   deadline. Join each group of at most four lanes before selecting more work.
2. Rotate bounded owned-slot scans under a four-second turn deadline.
3. Prioritize active slots; dispatch at most 32 subscriber pages per turn and
   16 pages per target before yielding. Progress remains durable outside the loop.
4. Scan bounded body-free retention-index candidates and delegate guarded cleanup.
5. Cancel/join before shutdown or restore; restart with empty process cursors.

## Invariants and Failure Semantics

- At most four slot selections per tick and eight pending/cleanup candidates per
  selection. Dispatch creates up to four temporary managed lanes;
  there is no goroutine per queued channel or member. One worker never dispatches
  the same identity concurrently. Stop/restore joins all active lanes.
- A dispatch error caused by the shared one-second wave deadline retains one
  ready retry per queued identity/version, subject to queue capacity. Capture
  expiration at call return;
  a dependency deadline while the wave is live does not qualify. Partial-page
  continuations preserve the spent retry, and newer commits retain precedence.
- Queue overflow, dependency failures, repeated wave expiration and restart fall
  back to the durable scan. Shutdown does not enqueue a budget retry.
- Failed work stays durable and retries on later passes. Stale-version tasks
  cannot clear newer notification work.
- Error counts are reported at most once per minute without message contents
  or identities in labels.

The selector always reserves background Slot capacity even when only two Slots
are owned, so a hot Slot cannot permanently hide cold notification/cleanup work.

## Read First

- [Worker](worker.go)
- [Committed-edit queue](ready.go)

## Update Triggers

Update when scan budgets, fairness, retry, observation or lifecycle changes.
