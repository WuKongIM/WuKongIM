# Standards review

The independent Standards reviewer inspected tracked and untracked changes
against source revision `1aff256b2bd9bc71a69cf5e365553357a69c3bc9`, applicable
AGENTS and FLOW documents. No tests were executed by that reviewer.

Two actionable issues were found and repaired:

- The escrow writer could produce a valid 1,024-node row larger than the former
  128 KiB decoder bound. Encoding/decoding now share a checked 256 KiB bound.
- Periodic cancellation could wait on foreground Channel locks and perform an
  uninterruptible synchronous commit on the health owner. It now skips busy
  append/checkpoint/budget locks and submits through the existing Coordinator.
  Canonical locks and registry pins transfer to its once-only finalizer;
  timeout preserves the charged proof. There is no synchronous fallback,
  additional worker or additional queue.

The reviewer verified both repairs and reported no remaining actionable issue
or additional code smell. The cancellation plan shares existing exact
original/nonce checks. Same-source ownership prevents duplicate admission and
overlapping GC/replacement refunds; publication reconciles only exact durable
proof. Shutdown joins the coordinator before registry drain.

The final candidate's process/race outcomes are reported separately by the
parent; review completion itself is not runtime acceptance.
