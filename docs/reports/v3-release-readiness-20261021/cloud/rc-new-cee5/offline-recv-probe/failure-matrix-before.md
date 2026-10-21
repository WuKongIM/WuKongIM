# Frozen scenarios before the probe code

Read-only product behavior probe on exact cee5. No product behavior is being implemented; this is not a tests-first claim. Eight schedules use the existing Engine, SessionPool.drain, engineFakeFactory and engineFakeClient frames queue, with one sender, one recipient, one deterministic sampled message, real configured 10-second deadline, fake clock, and per-read channel permits. No sleeps or network.

1. RECV queued at +9s and processed at +9s: delivered=1, loss=0 after +10s.
2. RECV queued at +9s, held in the actual fake-client frames queue until +11s: verify queue depth=1 at expiry; expired=1/loss=1, then received/ACK=1 with no reversal.
3. Actual RECV ingress only at +11s: queue depth=0 at expiry; same post-processing loss, distinct ingress timeline.
4. No RECV ingress: expired=1, received=0.
5. At exactly +10s, release and join the completed session read iteration before Engine.Advance: delivered=1, loss=0.
6. At exactly +10s, finish Engine.Advance before allowing the read: loss=1, then received=1, no reversal.
7. RECV processed before positive ACK: sampled state stays open without a deadline until ACK; matching ACK closes delivered once.
8. ACK remains queued/unprocessed: advancing to +10s causes no accepted-message loss; inspect before teardown.

Invariant failures: unexpected send count/attempt/retry; no exact sampled correlation; ACK identity mismatch; a read bypasses its permit; different fabricated payload/identity; wall-clock sleep; local queue occupancy inconsistent with schedule; missing redacted sample evidence for an expired case; any source/config threshold modification outside test fixture; any session/read goroutine still owned after Engine.Stop. Each is a hard probe failure. Boundary schedules use explicit ordered barriers, not racing goroutines.

Record exact ACK observation, fake-client RECV enqueue, verifier-processing completion, expiry advance, queue occupancy and aggregate/verifier evidence per schedule. This proves or refutes a mechanism only. It cannot recover missing historical cee5 sample identity or establish historical queue delay. No loss gate change, eventual-delivery waiver, cloud retry, or production execution is permitted.
