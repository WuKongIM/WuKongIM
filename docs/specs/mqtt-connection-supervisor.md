# MQTT connection lifecycle supervisor

Gateway close callbacks may run synchronously inside a packet handler. They must
not wait for quiescence of that handler's own operation. One node-owned supervisor
therefore retains bounded per-owner registration, schedules renewal and accepts
nonblocking disconnect intent; a fixed worker pool performs usecase calls.

## Failure inventory before implementation

1. Register uncommitted/pending/stale owners or exceed retained capacity. Require
   an installed live local lease, exact complete identity and bounded registration.
2. Reserve a new queue item for every ping/close or lose a normal disconnect when
   the queue fills. Each registered owner has one indexed scheduling entry and
   one immutable first disconnect intent; repeated notifications coalesce.
3. Close waits on its own PUBLISH, runs business cleanup under a lock, or creates
   a goroutine/timer per connection. Acceptance only fences/schedules; fixed
   workers run outside locks and one heap/timer schedules all owners.
4. Renewal succeeds without a newer installed lease, extends a late/expired owner,
   or competes with its disconnect. Read the actual Owners lease/revision; allow
   one job per owner and prioritize accepted disconnect intent on completion.
5. Network failure, panic or late nil result erases queued intent or returns false
   completion. Retain exact intent, retry on a bounded delay, check call deadlines,
   and require physical/local quiescence as well as lifecycle completion.
6. Normal DISCONNECT waits in a queue beyond lease expiry and becomes abnormal,
   or its offline clock restarts on every retry. Carry the trusted local monotonic
   observation in DisconnectCommand; validate it and preserve it on retries.
7. A renewal starves other owners, scan cost grows with all connections, or slow
   calls block the whole node. Use indexed O(log N) scheduling and a fixed bounded
   pool, with bounded queued jobs and per-call timeouts. No full-registry polling.
8. Stop admits new work, forgets accepted normal intent, returns before workers
   exit, or restarts an old owner boot. Stop fences all owner admission, schedules
   remaining registered owners as abnormal, preserves earlier intent, and drains/join workers.
   A timed-out Stop leaves the same run/dependencies alive for an exact retry;
   this supervisor and owner registry are one lifetime and never restart.
9. Stored ended/replaced rows or an uncertain publish barrier are treated as
   isolation proof. Even stale lifecycle completion requires Owners.Quiesce;
   unresolved-effect barriers remain retained and prevent successful shutdown.
10. External diagnostics expose identities/errors or callbacks throw arbitrary
    text. Use fixed task labels, constant-time aggregate counters and redacted
    callback errors. Clocks, expiring request contexts and startup cancellation
    must not silently change the node-owned worker lifetime.

Default workers: 16 (maximum 128). Queued jobs: one worker cohort. Tracked owners:
bounded by the configured capacity (default Owners capacity). Renew halfway
through the installed remaining lease; retry failures after 250ms, bounded by
lease expiry for active owners. Each call has a one-second budget by default;
cleanup and renewal are shared fairly by due time. Production load/pressure
validation and complete MQTT entry composition remain required.

## Frozen context

Source `f9a926c81`; SHA-256 digests:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/runtime/mqttsession/FLOW.md`: `2c855481f134b349a2e458db5507d4139b0549ad54e9368f918e9ec90a1c9270`
- `internal/usecase/mqttsession/FLOW.md`: `a0b7c20fa60f7c56154ecf436d8ec8865a2d18677f2d9eea142699d174489182`
- `internal/access/mqtt/FLOW.md`: `814ebcfb77565d1a30f178d205eb5ac446f08db9b057959770ec331cce566266`
- `internal/app/FLOW.md`: `7fa6de76a3b2ac3501d212d467e022789d09400c3ba059791a5ea83c111a8889`
- `pkg/gateway/FLOW.md`: `3e2270d966269b2835a32c54e001467aba014cc5554e4db4f253e11878348dc7`
- `pkg/goroutine/FLOW.md`: `e80ee0697416c327910cd6898ccbe5db7f40d2f9cbd647c435a1c3ff7e2b9181`
- `pkg/workqueue/FLOW.md`: `6720c38d539f939cab8d64daecc539484fe2023aa3e5a5260d52f18260a6d5cd`

## Shutdown ownership

Stop makes one O(N) heap pass after fencing admission, promoting waiting live
connections so a failed cleanup retry cannot hide them behind future renewals.
Steady-state scheduling never scans the registry. Queued/running work converts
on its own completion; normal intent accepted earlier is preserved. App must
keep dependencies available after a timed-out Stop and separately call Owners
cleanup for reservations never registered here. This supervisor has no restart
path; restore constructs a fresh owner registry with a different boot identity.

A nil lifecycle callback after its deadline is failure. Unknown publish barriers
are never cleared here. This supplies scheduling, not unavailable-owner recovery
proof, Session source protection, delivery restoration or complete MQTT admission.

## Next entry wiring

The gateway PacketHandler remains to be implemented. Acquire/authenticate through
Session usecases, register the returned owner before returning an accepted
handshake, and retain an owner operation across CONNACK enqueue until open or
rollback. A failed registration must perform bounded acquisition cleanup.
Rollback and close callbacks must release their handshake scope and enqueue exact
abnormal intent without synchronously joining their own packet execution. Normal
DISCONNECT captures its trusted observation before fencing and queues its first
intent; gateway notifications cannot overwrite it. Keep Alive remains transport
read-idle policy, independent of Session execution-lease renewal. Full product
composition still needs subscription, delivery, recovery and capability gates.
