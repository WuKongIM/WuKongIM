# Quorum continuation protocol foundation

The durable round now advances from dispatcher completions, one hedge timer and
caller cancellation. Starting an independent round does not wait for durability or
allocate a goroutine that waits for that proposal. The existing synchronous
`runDurableRound` wrapper uses this same state machine and still waits for its
terminal result. This is the protocol foundation for the bounded worker repair,
**not yet an activated nonblocking log/worker pipeline or a throughput fix**.

Source before this change: `1da17c983`. The preceding
[physical membership evidence](2026-09-28-send-ban-physical-commits.md) showed all
128 quorum workers occupied while hundreds of accepted jobs had not begun, even
though physical commit group limits were unset and post-collection queues were
almost empty. The intended next step is to transfer completion ownership while
retaining original admission capacity; changing physical batch caps is unsupported.

## Implemented boundary

`quorum_round_state.go` owns the protocol progression. It validates one proof per
distinct voter, requires local durability plus write quorum, and preserves the
preferred follower, delayed hedge, fallback, deferred trailing replica and owned
late-completion behavior. Existing synchronous users share the implementation.

Each round has a private mutex for protocol state; it never holds that mutex across
dependency submission or the terminal callback. A dispatch count includes every
planned local/replica action until that submission returns. Startup and terminal
fences therefore prevent inline completion, cancellation or concurrent callbacks
from publishing a terminal result before all planned writes have transferred to
their existing bounded owners. Duplicate and late callbacks cannot add votes or
publish a second result. Terminal publication stops hedge/cancellation wakeups.
Admitted writes keep `context.WithoutCancel`; caller cancellation reports unknown
and arranges the remaining replicas without waiting for their late replies.

No new public option, worker count, queue capacity, retry, durability relaxation or
permission-read behavior was introduced. Admission bounds remain the caller's
responsibility, as before. The current worker continues using the synchronous
wrapper; no claim is made that it releases its execution position earlier yet.
There is no user-visible behavior change requiring another Changelog entry for
this internal preparatory step. Applicable FLOW contracts remain accurate.

## Validation

[Failure contracts](assets/send-ban-quorum-continuation-20260928/contracts.md) and
new tests preceded implementation. RED failed because `startDurableRound` did not
exist. New coverage starts eight independent rounds before any result, prevents
follower/duplicate proof from replacing local durability, fences inline local
conflict until initial follower submission, handles cancel plus late replies, and
rejects invalid/canceled input without transferring callback ownership.

- New continuation plus existing round tests passed on the host.
- Linux arm64 race checks of those tests passed20 repetitions.
- The full replication default package and its integration tier passed.
- The real-process `TestUserAndChannelSendBan` passed for single-node and three-node
  clusters, including independent-ban combinations and committed-history controls.
- `TestSendBanGatewayDistributions` passed all four one/many UID×Channel cases.

The prebuilt E2E harness was reused. Original JSON reports have empty embedded
source fields; they were not edited afterward. Exact source hashes, base revision,
overlay map, command and tested binary/harness SHA-256 are retained beside them as
external identity evidence. Both reports say `performance_qualified=false`.
Final source-only edits after the race run added English field comments; the final
binary build and real-process tests include those comments. No workload remains
running in the Docker container.

## Next integration, not yet implemented

1. Add an optional log submission contract that transfers one terminal callback
   while holding the exact Channel authority/sequence ownership until its outcome.
   Durable retries, unresolved pending proposals, conflicts and reconciliation must
   preserve existing behavior. Test inline rejection/result and panic ownership
   before implementing that new contract.
2. Adapt quorum workers to retain their original combined queued/executing record
   budget through deferred outcome and result publication. Preserve payload/byte
   ownership, per-task cancellation, callback-before-return fences, observations,
   and close/drain. Do not multiply capacity by treating function return as finish.
3. Rebase diagnostic phase hooks onto the new state machine; old overlays replacing
   the whole `quorum_round.go` would silently restore the old implementation.
4. Run the original5000/4500/cap32 failure loop after actual activation, then clean
   uninterrupted30-minute R2 and all three fresh complete R6 comparisons.

The independent zero-hold permission-admission failure and physical cause of
natural I/O stalls remain unresolved. Original pressure qualification is still
incomplete. Evidence is indexed by the
[manifest](assets/send-ban-quorum-continuation-20260928/manifest.json).
