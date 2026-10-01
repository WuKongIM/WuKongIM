# MQTT Started Will recovery

Source revision: `14386ca24a4eb00e7a4d4ebf5899023736e2c70a`.
The operator approved crash-window acceptance and safe recovery after the
pre-merge review found Started work with no completion path. Reuse the approved
independent MQTT 5, public HTTP/metrics, exact owned-process restart and temporary
gofail process controls. No E2E test reads MQTT tables or invokes internal code.

## Failure inventory before tests and implementation

1. Crash after committed Started but before publication permanently loses a Will.
2. A committed publication with lost observation is repeated, changes identity,
   reruns hooks or gets rejected after permission changes instead of completing.
3. A still-admitted append outlives the executor lease and caller cancellation.
   Neither elapsed time nor absent receipt permits another publication attempt.
4. A negative proof describes another key, generation, node or boot, or a missing,
   corrupt, unsupported or oversized local record is interpreted as not published.
5. Publication races a negative-proof request. Exactly one durable transition may
   win: publication admission or sealed non-dispatch; the loser cannot dispatch.
6. An uncertain seal, prepare, claim or Started CAS response grants publication,
   overwrites the old execution identity or discards its recovery evidence.
7. Permission is reused from setup. Recovery needs current ordinary policy and
   the frozen prepared body, original server key and immutable target.
8. A foreign boot is trusted without its owning node's exclusive generation lock
   or valid retirement fact. Unreachable peers never become proof providers.
9. Recovery resets durable preparation or invents phases for legacy Executing
   records. Legacy or already-admitted work stays positive-receipt-only.
10. Disk pressure, unbounded attempts, stale cleanup, Stop/restore or concurrent
    generations bypass admission, erase another attempt or permit a late writer.
11. A test silently resubscribes, retries ambiguous CONNECT, changes receipt
    deadlines, accepts a closed receiver as a quiet window or reports before cleanup.

## Recovery decision

Keep the existing Slot row, execution generation and monotonic dispatch phases.
Before committing Started, persist an exact body-free local attempt record.
Publication must durably change that record from reserved to admitted before
calling the shared message path. A negative-proof operation atomically seals a
reserved record, preventing even a paused old executor from admitting it later.
An admitted record grants no negative proof. Missing records grant none either.

For another boot, the owning node must first prove that generation retired; its
exclusive lock plus a reserved record proves this attempt never admitted effects.
Recovery uses one exact-node RPC, then a fresh exact Will CAS. It never follows a
successor execution. A definite successor claim can resume the same frozen Started
body; unknown claims cannot publish. Positive receipt recovery takes precedence.

The store has a fixed node-wide record cap and bounded records/point reads,
requires the existing generation lock, and owns no worker or per-Will goroutine.
Capacity or uncertain persistence fails closed. Terminal completion removes only
the captured attempt; unproved legacy/remote effects remain pending.

## Process acceptance

Run independent single-node and three-node clusters with 256 hash Slots:

- Interrupt after Started, abruptly kill the exact executor, restart it, reconnect
  the persistent recipient once without SUBSCRIBE and require one original Will.
- Interrupt completion observation after a real publication, restart its executor
  and require the original unfinished QoS 1 exchange/identity, then no new Will.
- Delay a genuinely admitted append beyond two ten-second execution grants while
  independent CONNECT succeeds. Require no publication attempt beyond the first;
  after its original effect completes, receive one message and remain quiet.

Emit bounded JSON after all cleanup. Instrumented builds are acceptance/diagnostic
artifacts only; ordinary binaries retain no active gofail controls. These cases
do not qualify network partitions, arbitrary restore rollback, lost attempt files,
whole-Channel deletion or safe redispatch of already-admitted unknown effects.

## Exact bounds and remaining obligations

RPC 105 has closed version-1 request/reply envelopes with operation and complete
attempt echo. Identity encoding version 1 is capped at 2,304 bytes; checksummed
local records at 2,342 bytes and 1,024 attempts per node. Open removes at most
eight interrupted temporary files under the existing exclusive generation lock.
Known successor/terminal decisions clean only captured prior records. Failed
successor claims retain their new reservation even on definite conflict: an equal
local tuple could belong to an earlier unknown claim that applies late. Unknown
CAS and cleanup replies likewise conservatively retain files and may exhaust that fixed cap.
There is no age-based or automatic orphan journal sweep in this slice.

Fresh Slot target absence remains a distinct query error; only an independently
sealed attempt permits ordinary authorization/first-Channel creation. Other query
errors remain pending. A positive receipt is completed without reauthorization.
If current policy denies a sealed Started Will, retain it pending: the unchanged
Slot schema does not admit Started -> Rejected. A crash after durable Admitted
but before SEND is also conservatively positive-only, even if no effect occurred.
Legacy/lost-journal and already-admitted unknown-effect terminal recovery, journal
orphan reclamation, arbitrary restore/delete and aggregate shared-storage admission
remain separate obligations. This is not complete MQTT acceptance.

The Started and post-publication cuts hold the exact admitted worker in a
60-second temporary gofail sleep until SIGKILL. HTTP count remains observable;
disabling a term does not release an in-flight sleep. This prevents a fast worker
from finishing positive recovery while the recipient proves its begun exchange.
The sleep is never present in an ordinary product binary.
