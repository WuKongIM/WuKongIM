# MQTT connection delivery scheduling

Status: bounded runtime implemented and tested; the product listener remains unavailable.
This is the bounded execution part of the approved [IM design](mqtt-im-access.md),
not a substitute for source discovery, offline accounting or process acceptance.

## Contract and test seam

Use the already approved public runtime/usecase and app integration seams.
The runtime accepts one body-free task bound to one activated exact Owner.
The composition root supplies the task; only usecases authorize, account, recover,
send and end a Session. Runtime never imports entry or usecase packages.

- One indexed record per registered Owner, including queued/executing work;
  repeated registration cannot replace the task or reopen its send-order cursor.
- A fixed worker cohort executes at most one bounded turn per Owner at a time.
  Progress rejoins the tail of due work. Idle work has a bounded polling interval;
  an exact-owner wake coalesces, including while a turn is running. No body queue,
  per-session worker/timer, global connection scan or new durable table is added.
- Errors and panics retain the task with bounded retry delay. Wakes cannot defeat
  error backoff. Diagnostics contain aggregate counters, never callback values.
- A terminal task reports Done only after its usecase no longer needs this
  connection's volatile continuation. Mere fencing is not terminal: Sender may
  still need to finish exact-owner End after releasing its operation scope.
- Stop cancels turn contexts and closes registration/wake admission. It joins the
  scheduler and cohort before releasing records. A timeout retains the same run;
  a later Stop joins it. Stop is terminal and supplies no owner isolation proof.
  App must fence entry/owner admission before stopping delivery, and keep Session,
  Connections, Owners and cluster dependencies alive until all delivery calls join.

## Failure inventory (before implementation)

1. A hot connection monopolizes a worker; a second due connection never runs.
2. Duplicate wakes or registration create concurrent turns/reset recovery order.
3. Wake during a turn is lost when the turn reports idle; an idle task never
   rediscovers new work if a wake is lost.
4. More Owners than capacity or more calls than workers accumulate retained work.
5. Error/panic spins immediately, leaks sensitive diagnostics or retires required
   cleanup. Wake flooding bypasses retry delay.
6. A turn ignores cancellation: Stop falsely reports completion, drops its record,
   admits replacement work, or closes dependencies before the turn exits.
7. Startup cancellation poisons runtime lifetime; stop-before-start or repeated
   Start/Stop overlaps cohorts. Queued tasks start business effects after Stop.
8. A stale/foreign/unactivated owner registers work; a fenced task loses its
   retained terminal cleanup instead of letting the usecase resolve authority.
9. Scheduled Sender turns change original content/order, duplicate a new exchange
   or skip an ACK gap. Extend the existing three-node native Slot integration
   with the actual scheduler and Sender, using its known prepared source and a
   controlled sink. This does not establish automatic source discovery.

Integration tests use real Owners and the actual bounded worker queue, through
public methods. Controlled task callbacks represent the injected execution seam;
barriers make concurrency failures observable. Real elapsed-time checks stay in
the integration tier. App/Sender composition and source scheduling are separate
remaining work and must have their own real-authority evidence before admission.

## Validation

- Source revision `ab6625539699138d03bba1081b4fa73b19e15eeb`; applicable
  AGENTS/FLOW digests frozen in `/tmp/mqtt-delivery-scheduling-source.json` before
  edits. No schema/RPC/configuration change or new persistent table.
- Initial RED: missing runtime interface, `/tmp/mqtt-delivery-scheduler-red.log`.
  Backoff regression RED: a wake retried in 27.917 microseconds against a 100ms
  floor, `/tmp/mqtt-delivery-backoff-behavior-red.log`. The indexed entry now
  retains a failure floor; panic and expired-context completion also retry.
- `GOWORK=off go test -race -p 2 ./internal/runtime/mqttsession ./pkg/goroutine
  -count=1`: passed (1.941s / 2.315s),
  `/tmp/mqtt-delivery-scheduler-unit-race.log`.
- `GOWORK=off go test -race -tags=integration -p 2
  ./internal/runtime/mqttsession -count=1 -timeout=3m`: passed (3.486s),
  `/tmp/mqtt-delivery-scheduler-integration-race.log`. Covers fair progress, idle
  polling, 8 concurrent wake producers, exact activation, duplicate registration,
  bounded workers/capacity, backoff, panic, late result, fenced cleanup and joined
  stop with a deliberately slow callback. No per-owner worker is introduced.
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/app
  -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1 -timeout=4m
  -v`: passed (22.012s), `/tmp/mqtt-delivery-scheduler-cluster-race.log`.
  Three nodes / 256 hash Slots run actual Sender turns on two delivery workers
  with original content, native window commits and preserved ACK gaps. The
  scenario also retains replay recovery/rejoin/retirement and disk-reopen proofs.
  Its source is already prepared and sink controlled; no automatic source
  discovery, socket scheduling or product process acceptance is claimed.

Bounded per-connection source discovery/accounting is now implemented by the
[delivery coordinator](mqtt-delivery-coordinator.md). Remaining work includes
one stream registered after accepted CONNECT, entry wake/close hooks, offline
maintenance, and complete product lifecycle/restore admission. Successful runtime
Stop cannot stand in for those usecase and cluster guarantees.
