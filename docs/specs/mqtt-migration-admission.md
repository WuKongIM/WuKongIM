# MQTT replay coverage at Channel migration cutover

Native log catch-up cannot prove replay content coverage. Planned leader transfer
and replica replacement require the target's current replica-local readiness at
final catch-up and again immediately before changing membership/leadership. New
leader and replacement verification require coverage before clearing the fence.
Missing, malformed, uncovered or recovery-pending evidence yields without writing
a blocked/terminal task, so bounded background recovery can make progress.

Automatic failover must select and recover a surviving native leader even if its
shared content lags. Its pre-commit phases retain native quorum rules; readiness
is required before the new leader's write fence is cleared. No dead-source drain
or MQTT planning against a dead leader is introduced.

Readiness is request-local, tied to the probed HW and current task fence. It is
never cached in a task or used as source-release/consumer-GC proof. Old peers and
unsupported stores omit evidence and cannot silently pass cutover; current native
channels return explicit covered/no-anchor evidence. Existing guarded Slot writes
and five-second tick bounds remain in force. No new table or encoding is needed.

## Failure inventory before code

1. Native HW alone admits an uncovered learner, an old peer with no evidence, or
   a result associated with a different HW/anchor/fence.
2. A valid catch-up receipt is reused after a later replica restart/content loss
   at the separate promote/commit phase, or incomplete native recovery clears a fence.
3. A temporarily lagging replay target is permanently blocked instead of remaining
   runnable; repeated waits churn Slot metadata or monopolize executor turns.
4. Failover waits for content before a surviving leader exists to coordinate its
   recovery, or skips readiness when opening that new leader for writes.
5. Native channels with explicit no-anchor coverage regress; real Slot-backed
   replacement bypasses the new gate or cannot finish after content recovery.

Tests precede code: a phase/admission matrix, renewed ready evidence after a wait,
existing native phase order regression, and real three-node replacement/cutover.
Full product listener, automatic source release and process/load acceptance remain
separate requirements of the approved design.

## Frozen context

Source `66285fd62`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/cluster/FLOW.md`: `2997d1cf2f76d0c58806249e13e0e03656f1af949c9b2701491e4393b651cd29`
- `pkg/channel/FLOW.md`: `5057c1b934002fd832c7e0ecaca047c1e086712f577d87bfb506b0423b069fc3`

## Three-node regression discovered before the fix

After replacement clears its fence, native recovery installs another barrier.
The runtime reports HW 7 while the local and follower durable checkpoints remain
at 6. The pinned reader correctly rejects 7; reading at 6 succeeds. Active leader
probes must checkpoint their recovered, captured HW before reading coverage and
request bounded native committed-tail propagation using exact installed authority
(no caller-supplied HW). Stable task fences must permit that repair, while changed
fences and pending recovery reject it. Failed checkpoint/refresh must not return a
receipt. Followers never advance their own HW from the probe's metadata.

The next real run passed ProbeTarget but stalled in DrainLeader: the source
runtime still had the cleared replacement fence instead of the new transfer
fence. Graceful drain must apply current metadata and probe recovered source
progress before draining. Native asynchronous HW lag also remains runnable at
both pre-fence and final catch-up; resumed proof, not permanent blocking, decides
progress. Failover still avoids all source operations.
