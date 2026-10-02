# MQTT Session deadline reconciliation

This step turns authoritative deadline candidates into atomic Session/Will
decisions. It does not publish Wills or equate elapsed leases with remote owner
isolation. Durable publication still requires execution fencing, current
authorization and retained idempotency proof across uncertain append results.

## Failure inventory before implementation

1. A stale timer closes or updates a successor. Candidates carry the complete
   original owner; reread current Slot authority and reject changed ownership.
2. A renewed live lease is closed from a stale scan. Read the current row before
   isolation; a not-yet-due active row needs no work. If renewal races admitted
   isolation, the existing disconnect path drains and rereads that exact owner.
3. An unreachable owner is declared disconnected from time alone. Require the
   exact quiescence port, keep failures visible, and preserve the original lease
   boundary when no later renewal was committed.
4. A delayed sweep restarts the offline lifetime or Will Delay. Expired active
   sessions first use the existing durable abnormal disconnect at the recorded
   lease boundary. Each call commits at most one lifecycle transition.
5. Will Delay ends before Session expiry but the Will stays Waiting forever. Read
   the referenced Will with its current Session from one authority snapshot, then
   use WillDue to detach Ready work atomically. Do not end a still-live offline
   lifetime or drop counters, allocators or other delivery responsibility.
6. Session expiry ends a lifetime without releasing its due Will, or normal
   disconnect resurrects a cancelled Will. Apply the existing atomic lifecycle
   event, preserve binding and detached obligations, and never mutate a terminal
   Will directly. Expiry without a Will uses the same Session-end transition.
7. Missing, foreign, malformed or mismatched Will/Session snapshot data becomes
   absence or successful cleanup. Validate the complete reference and owner;
   authority changes/conflicts remain explicit, without an unbounded retry loop.
8. A reconnect between read and commit is overwritten, or a malformed/uncertain
   commit result is reported as applied. Retain full owner/revision CAS and the
   existing committed receipt checks. Retry rereads authoritative state.
9. Clock regression, overflow or caller cancellation causes premature expiry,
   partial updates or a silent timing fallback. Fail before the dependent effect.
10. Scheduling introduces one worker/timer per session or an unbounded scan. This
    usecase handles one supplied identity with bounded reads and one mutation.
    App scheduling must page both Session deadlines and Waiting Will deadlines;
    detached Ready/executing work belongs to the future publication worker.

## Reconciliation contract

`ReconcileDeadline` consumes a complete observed owner. Missing or replaced
Sessions return `ErrFenced`; a current non-due or already-ended row is a no-op.
Active expiry delegates exact isolated abnormal disconnect. Offline expiry or
Will Delay is handled from current coherent authority and one conditional
lifecycle command. Cancellation, corruption, authority movement and uncertain
commit replies are errors, not evidence that the obligation vanished.

A timer's stored row is a candidate, never mutation authority. No background
worker, unbounded retry, listener admission or Will publication is introduced by
this method. The full product remains disabled pending all approved capabilities.

## Frozen context

Source `4c329e595`; SHA-256 digests:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `06980b5f94923a380f1fa5044659157e67148252460797133d2c7c03e429efda`
- `internal/runtime/mqttsession/FLOW.md`: `01488eb8b82a05cb8a03a39c33290f8429cc5a3ddaf2169772021aba5d7f95fc`
- `pkg/db/meta/FLOW.md`: `e05d11e22fc0c4afa0898a9e6498b45b9cc7f594fd4b8529587056cd784ecbb7`
- `internal/app/FLOW.md`: `7fa6de76a3b2ac3501d212d467e022789d09400c3ba059791a5ea83c111a8889`
