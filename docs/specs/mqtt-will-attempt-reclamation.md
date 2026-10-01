# Bounded Will attempt reclamation

Source revision: `3f8f0960af900492032b09feeaaf9bcfc73c4449`.
The operator requested continued development after Started recovery. This slice
addresses retained journal capacity with the unchanged approved process seams:
independent Paho MQTT 5, public HTTP provisioning, loopback temporary gofail
controls and exact harness-owned process crashes. No test reads journal/Slot files.

## Failure inventory before implementation

1. A terminal Will's cleanup fails; its retained attempt permanently consumes one
   of the 1,024 node-wide records and eventually prevents unrelated publication.
2. Reclamation deletes the current Started reservation, losing safe recovery.
3. An absent row, expired grant, read error or unknown claim becomes deletion
   authority; a delayed proposal may still commit against that reservation.
4. A newer execution is inferred from a different key or inconsistent equal
   generation rather than a valid exact authoritative Will row.
5. Cleanup failure or cancellation makes a sweep claim success or retry effects.
6. Full-journal discovery retains unbounded bodies/maps, adds a worker, monopolizes
   the journal lock during remote reads, or overlaps Stop/restore generations.
7. A test observes another node's spare capacity instead of exercising the full
   executor journal, silently resubscribes, or treats a closed receiver as quiet.

## Decision and bounds

Reclaim only when reservation encounters capacity pressure. The existing Will
execution cohort performs one bounded reclamation page and yields; a later
ordinary turn retries reservation through the normal execution claim path.
No second inline reservation/publication attempt is permitted by cleanup.

The journal supplies at most 16 checksummed body-free captured attempts per page,
using a fair volatile filename cursor over at most 1,024 records. Pages overlap
by advancing one start position, so every record becomes first within one wrap
even if the page deadline stops later authority reads. Infrastructure
owns inventory and exact deletion; a usecase owns the decision. Fresh foreground
Slot reads must return one valid exact Will row. A strictly higher execution
generation fences the old attempt; an identical executor in Published or Rejected
state also permits deletion. Absent, malformed, inconsistent, older, current
nonterminal and unknown rows retain their records. Lease time grants nothing.
Remote reads run outside journal locks. A single nonwaiting reclamation admission
and a 750 ms page deadline bound pressure work within the existing five-second
Will turn. Failed deletion remains uncertain and never grants publication proof.

## Process acceptance

In both 256-Slot topologies, temporarily reduce the journal admission cap to two:
hold one exact worker after Started, publish another lifetime's Will while its
terminal cleanup fails, then submit a third Will through the same ClientID Slot.
Require actual capacity refusal on that executor followed by exactly one third
publication after cleanup becomes reachable. Crash/restart the captured executor
and recover the first Will once, proving its current reservation was retained.
The persistent recipient resumes without SUBSCRIBE and observes a finite quiet
window after all ACKs. Emit bounded JSON after process/client cleanup.

The lower cap and fault controls exist only in temporary instrumented binaries;
ordinary journal capacity stays 1,024. This does not qualify production-cap stress,
missing-row cleanup, delayed unknown-claim races, arbitrary restore/delete,
partitions or already-admitted unknown-effect terminal recovery.
